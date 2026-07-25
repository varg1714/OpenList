package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/cmd/flags"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

func TestMigrateLegacyMediaMovesArtifactsAndRemovesEmptyLegacyDirectory(t *testing.T) {
	database := newMigrationTestDB(t)
	dataDir := t.TempDir()
	previousDataDir := flags.DataDir
	flags.DataDir = dataDir
	t.Cleanup(func() { flags.DataDir = previousDataDir })
	createStorages(t, database, model.Storage{ID: 1, Driver: "Javdb", MountPath: "/javdb"})
	film := model.Film{Source: "javdb", Actor: "Actor A", Name: "ABP-123 title.mp4", Url: "https://javdb.test/v/abp-123"}
	if err := database.Create(&film).Error; err != nil {
		t.Fatalf("seed film: %v", err)
	}

	legacyRoot := filepath.Join(dataDir, "emby", "javdb", "Actor A", "ABP-123 title")
	writeArtifactFixture(t, legacyRoot, map[string]string{
		"ABP-123 title.jpg":            "legacy poster",
		"ABP-123 title-background.jpg": "legacy background",
		"ABP-123 title.nfo":            "legacy nfo",
		"fanart1.jpg":                  "legacy fanart",
		"ABP-123 title.1.srt":          "legacy subtitle",
	})

	journalPath := filepath.Join(dataDir, "migration-journal.json")
	report, err := MigrateLegacyMediaWithOptions(context.Background(), database, MigrationOptions{
		Mode: MigrationApply, DataDir: dataDir, JournalPath: journalPath,
	})
	if err != nil {
		t.Fatalf("migrate artifacts: %v", err)
	}
	if report.ArtifactMovesPlanned != 5 || report.ArtifactsMoved != 5 || report.ArtifactDirectoriesPlanned != 1 || report.ArtifactDirectoriesRemoved != 1 {
		t.Fatalf("artifact report = %+v", report)
	}
	targetRoot := filepath.Join(dataDir, "emby", "javdb", "Actor A", "ABP-123")
	for name, want := range map[string]string{
		"ABP-123.jpg":            "legacy poster",
		"ABP-123-background.jpg": "legacy background",
		"ABP-123.nfo":            "legacy nfo",
		"fanart1.jpg":            "legacy fanart",
		"ABP-123.1.srt":          "legacy subtitle",
	} {
		assertArtifactContent(t, filepath.Join(targetRoot, name), want)
	}
	if _, err := os.Stat(legacyRoot); !os.IsNotExist(err) {
		t.Fatalf("legacy root remains after move: %v", err)
	}
}

func TestMigrateLegacyMediaUsesOnlyFirstPartWorkArtifacts(t *testing.T) {
	database := newMigrationTestDB(t)
	dataDir := t.TempDir()
	previousDataDir := flags.DataDir
	flags.DataDir = dataDir
	t.Cleanup(func() { flags.DataDir = previousDataDir })
	createStorages(t, database, model.Storage{ID: 18, Driver: "FC2", MountPath: "/fc2"})
	films := []model.Film{
		{Source: "fc2", Actor: "个人收藏", Name: "FC2-PPV-1042868-cd1.mp4", Url: "https://adult.contents.fc2.com/article/1042868/"},
		{Source: "fc2", Actor: "个人收藏", Name: "FC2-PPV-1042868-cd2.mp4", Url: "https://adult.contents.fc2.com/article/1042868/"},
	}
	if err := database.Create(&films).Error; err != nil {
		t.Fatalf("seed multipart films: %v", err)
	}
	legacyRoot := filepath.Join(dataDir, "emby", "fc2", "个人收藏", "FC2-PPV-1042868")
	writeArtifactFixture(t, legacyRoot, map[string]string{
		"FC2-PPV-1042868-cd1.jpg":            "first part poster",
		"FC2-PPV-1042868-cd2.jpg":            "second part poster",
		"FC2-PPV-1042868-cd1-background.jpg": "first part background",
		"FC2-PPV-1042868-cd2-background.jpg": "second part background",
		"FC2-PPV-1042868-cd1.nfo":            "first part nfo",
		"FC2-PPV-1042868-cd2.nfo":            "second part nfo",
		"FC2-PPV-1042868-cd2.1.srt":          "second part subtitle",
	})

	report, err := MigrateLegacyMediaWithOptions(context.Background(), database, MigrationOptions{
		Mode: MigrationApply, DataDir: dataDir, JournalPath: filepath.Join(dataDir, "journal.json"),
	})
	if err != nil {
		t.Fatalf("migrate multipart work artifacts: %v", err)
	}
	if report.ArtifactMovesPlanned != 3 || report.ArtifactDeletesPlanned != 3 || report.ArtifactsMoved != 3 || report.ArtifactsDeleted != 3 || report.ArtifactsExisting != 1 {
		t.Fatalf("multipart work artifact report = %+v", report)
	}
	targetRoot := filepath.Join(dataDir, "emby", "fc2", "个人收藏", "FC2-PPV-1042868")
	assertArtifactContent(t, filepath.Join(targetRoot, "FC2-PPV-1042868.jpg"), "first part poster")
	assertArtifactContent(t, filepath.Join(targetRoot, "FC2-PPV-1042868-background.jpg"), "first part background")
	assertArtifactContent(t, filepath.Join(targetRoot, "FC2-PPV-1042868.nfo"), "first part nfo")
	assertArtifactContent(t, filepath.Join(targetRoot, "FC2-PPV-1042868-cd2.1.srt"), "second part subtitle")
	for _, redundant := range []string{
		"FC2-PPV-1042868-cd1.jpg", "FC2-PPV-1042868-cd1-background.jpg", "FC2-PPV-1042868-cd1.nfo",
		"FC2-PPV-1042868-cd2.jpg", "FC2-PPV-1042868-cd2-background.jpg", "FC2-PPV-1042868-cd2.nfo",
	} {
		if _, err := os.Lstat(filepath.Join(legacyRoot, redundant)); !os.IsNotExist(err) {
			t.Fatalf("redundant artifact %s remains: %v", redundant, err)
		}
	}
}

func TestMigrateLegacyMediaPrefersCanonicalArtifactDirectoryAcrossMultipartSources(t *testing.T) {
	database := newMigrationTestDB(t)
	dataDir := t.TempDir()
	previousDataDir := flags.DataDir
	flags.DataDir = dataDir
	t.Cleanup(func() { flags.DataDir = previousDataDir })
	createStorages(t, database, model.Storage{ID: 18, Driver: "FC2", MountPath: "/fc2"})
	films := []model.Film{
		{Source: "fc2", Actor: "个人收藏", Name: "FC2-PPV-2382903-cd1.mp4", Url: "FC2-PPV-2382903"},
		{Source: "fc2", Actor: "个人收藏", Name: "FC2-PPV-2382903 历史标题-cd2.mp4", Url: "FC2-PPV-2382903"},
	}
	if err := database.Create(&films).Error; err != nil {
		t.Fatalf("seed duplicate part films: %v", err)
	}
	legacyParent := filepath.Join(dataDir, "emby", "fc2", "个人收藏")
	alternateRoot := filepath.Join(legacyParent, "FC2-PPV-2382903 历史标题")
	canonicalRoot := filepath.Join(legacyParent, "FC2-PPV-2382903")
	writeArtifactFixture(t, alternateRoot, map[string]string{
		"FC2-PPV-2382903 历史标题-cd2.nfo":   "alternate nfo",
		"FC2-PPV-2382903 历史标题-cd2.1.srt": "alternate subtitle",
	})
	writeArtifactFixture(t, canonicalRoot, map[string]string{"FC2-PPV-2382903-cd1.nfo": "canonical nfo"})

	report, err := MigrateLegacyMediaWithOptions(context.Background(), database, MigrationOptions{
		Mode: MigrationApply, DataDir: dataDir, JournalPath: filepath.Join(dataDir, "journal.json"),
	})
	if err != nil {
		t.Fatalf("migrate duplicate part artifacts: %v", err)
	}
	if report.ArtifactMovesPlanned != 2 || report.ArtifactDeletesPlanned != 1 || report.ArtifactDirectoriesPlanned != 1 ||
		report.ArtifactsMoved != 2 || report.ArtifactsDeleted != 1 || report.ArtifactDirectoriesRemoved != 1 {
		t.Fatalf("multipart source artifact report = %+v", report)
	}
	target := filepath.Join(dataDir, "emby", "fc2", "个人收藏", "FC2-PPV-2382903", "FC2-PPV-2382903.nfo")
	assertArtifactContent(t, target, "canonical nfo")
	assertArtifactContent(t, filepath.Join(filepath.Dir(target), "FC2-PPV-2382903-cd2.1.srt"), "alternate subtitle")
	if _, err := os.Stat(alternateRoot); !os.IsNotExist(err) {
		t.Fatalf("alternate duplicate root remains: %v", err)
	}
}

func TestMigrateLegacyMediaArtifactRerunIsIdempotent(t *testing.T) {
	database := newMigrationTestDB(t)
	dataDir := t.TempDir()
	previousDataDir := flags.DataDir
	flags.DataDir = dataDir
	t.Cleanup(func() { flags.DataDir = previousDataDir })
	createStorages(t, database, model.Storage{ID: 1, Driver: "Javdb", MountPath: "/javdb"})
	if err := database.Create(&model.Film{Source: "javdb", Actor: "Actor A", Name: "ABP-123 title.mp4", Url: "https://javdb.test/v/abp-123"}).Error; err != nil {
		t.Fatalf("seed film: %v", err)
	}
	writeArtifactFixture(t, filepath.Join(dataDir, "emby", "javdb", "Actor A", "ABP-123 title"), map[string]string{"poster.jpg": "poster"})
	options := MigrationOptions{Mode: MigrationApply, DataDir: dataDir, JournalPath: filepath.Join(dataDir, "journal.json")}
	first, err := MigrateLegacyMediaWithOptions(context.Background(), database, options)
	if err != nil {
		t.Fatalf("first migration: %v", err)
	}
	second, err := MigrateLegacyMediaWithOptions(context.Background(), database, options)
	if err != nil {
		t.Fatalf("second migration: %v", err)
	}
	if first.ArtifactsMoved != 1 || second.ArtifactsMoved != 0 || second.ArtifactsExisting != 1 {
		t.Fatalf("artifact rerun reports = first %+v, second %+v", first, second)
	}
}

func TestMigrateLegacyMediaRefusesConflictingArtifactTarget(t *testing.T) {
	database := newMigrationTestDB(t)
	dataDir := t.TempDir()
	previousDataDir := flags.DataDir
	flags.DataDir = dataDir
	t.Cleanup(func() { flags.DataDir = previousDataDir })
	createStorages(t, database, model.Storage{ID: 1, Driver: "Javdb", MountPath: "/javdb"})
	if err := database.Create(&model.Film{Source: "javdb", Actor: "Actor A", Name: "ABP-123 title.mp4", Url: "https://javdb.test/v/abp-123"}).Error; err != nil {
		t.Fatalf("seed film: %v", err)
	}
	writeArtifactFixture(t, filepath.Join(dataDir, "emby", "javdb", "Actor A", "ABP-123 title"), map[string]string{"poster.jpg": "legacy poster"})
	targetRoot := filepath.Join(dataDir, "emby", "javdb", "1", "Actor A", "ABP-123")
	writeArtifactFixture(t, targetRoot, map[string]string{"poster.jpg": "different target"})

	_, err := MigrateLegacyMediaWithOptions(context.Background(), database, MigrationOptions{
		Mode: MigrationApply, DataDir: dataDir, JournalPath: filepath.Join(dataDir, "journal.json"),
	})
	if !errors.Is(err, ErrArtifactCollision) {
		t.Fatalf("artifact error = %v, want ErrArtifactCollision", err)
	}
	assertArtifactContent(t, filepath.Join(targetRoot, "poster.jpg"), "different target")
	assertCount(t, database, &model.FilmWork{}, 0)
	assertCount(t, database, &model.FilmFile{}, 0)
	assertCount(t, database, &model.SourceMagnet{}, 0)
}

func TestMigrateLegacyMediaRejectsAmbiguousPlainSourcesBeforeArtifactPlanning(t *testing.T) {
	database := newMigrationTestDB(t)
	dataDir := t.TempDir()
	previousDataDir := flags.DataDir
	flags.DataDir = dataDir
	t.Cleanup(func() { flags.DataDir = previousDataDir })
	createStorages(t, database, model.Storage{ID: 1, Driver: "Javdb", MountPath: "/javdb"})
	films := []model.Film{
		{Source: "javdb", Actor: "Actor A", Name: "ABP-123 first.mp4", Url: "https://javdb.test/v/abp-123"},
		{Source: "javdb", Actor: "Actor A", Name: "ABP-123 second.mp4", Url: "https://javdb.test/v/abp-123"},
	}
	if err := database.Create(&films).Error; err != nil {
		t.Fatalf("seed duplicate identity films: %v", err)
	}
	writeArtifactFixture(t, filepath.Join(dataDir, "emby", "javdb", "Actor A", "ABP-123 first"), map[string]string{"poster.jpg": "first"})
	writeArtifactFixture(t, filepath.Join(dataDir, "emby", "javdb", "Actor A", "ABP-123 second"), map[string]string{"poster.jpg": "second"})

	_, err := MigrateLegacyMediaWithOptions(context.Background(), database, MigrationOptions{Mode: MigrationApply, DataDir: dataDir})
	if !errors.Is(err, ErrIdentityCollision) {
		t.Fatalf("ambiguous plain source error = %v, want ErrIdentityCollision", err)
	}
	assertCount(t, database, &model.FilmWork{}, 0)
	assertCount(t, database, &model.FilmFile{}, 0)
	assertCount(t, database, &model.SourceMagnet{}, 0)
	assertArtifactContent(t, filepath.Join(dataDir, "emby", "javdb", "Actor A", "ABP-123 first", "poster.jpg"), "first")
	assertArtifactContent(t, filepath.Join(dataDir, "emby", "javdb", "Actor A", "ABP-123 second", "poster.jpg"), "second")
}

func TestMigrateLegacyMediaRejectsMatchingArtifactSymlinkBeforeDatabaseWrites(t *testing.T) {
	database := newMigrationTestDB(t)
	dataDir := t.TempDir()
	previousDataDir := flags.DataDir
	flags.DataDir = dataDir
	t.Cleanup(func() { flags.DataDir = previousDataDir })
	createStorages(t, database, model.Storage{ID: 1, Driver: "Javdb", MountPath: "/javdb"})
	if err := database.Create(&model.Film{Source: "javdb", Actor: "Actor A", Name: "ABP-123 title.mp4", Url: "https://javdb.test/v/abp-123"}).Error; err != nil {
		t.Fatalf("seed film: %v", err)
	}
	legacyRoot := filepath.Join(dataDir, "emby", "javdb", "Actor A", "ABP-123 title")
	if err := os.MkdirAll(legacyRoot, 0o755); err != nil {
		t.Fatalf("create legacy root: %v", err)
	}
	external := filepath.Join(dataDir, "outside.jpg")
	if err := os.WriteFile(external, []byte("outside"), 0o644); err != nil {
		t.Fatalf("write external artifact: %v", err)
	}
	if err := os.Symlink(external, filepath.Join(legacyRoot, "fanart1.jpg")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	_, err := MigrateLegacyMediaWithOptions(context.Background(), database, MigrationOptions{Mode: MigrationApply, DataDir: dataDir})
	var safetyErr *ArtifactSafetyError
	if !errors.As(err, &safetyErr) || !errors.Is(err, ErrArtifactSafety) {
		t.Fatalf("symlink migration error = %v, want typed artifact safety error", err)
	}
	assertCount(t, database, &model.FilmWork{}, 0)
	assertCount(t, database, &model.FilmFile{}, 0)
}

func TestMigrateLegacyMediaMovesInternalBackgroundSymlink(t *testing.T) {
	database := newMigrationTestDB(t)
	dataDir := t.TempDir()
	previousDataDir := flags.DataDir
	flags.DataDir = dataDir
	t.Cleanup(func() { flags.DataDir = previousDataDir })
	createStorages(t, database, model.Storage{ID: 1, Driver: "Javdb", MountPath: "/javdb"})
	film := model.Film{Source: "javdb", Actor: "斋藤亚美丽", Name: "ABW-016 xx.mp4", Url: "https://javdb.test/v/abw-016"}
	if err := database.Create(&film).Error; err != nil {
		t.Fatalf("seed film: %v", err)
	}
	legacyRoot := filepath.Join(dataDir, "emby", "javdb", film.Actor, "ABW-016 xx")
	if err := os.MkdirAll(legacyRoot, 0o755); err != nil {
		t.Fatalf("create legacy root: %v", err)
	}
	posterName := "ABW-016 xx.jpg"
	if err := os.WriteFile(filepath.Join(legacyRoot, posterName), []byte("legacy poster"), 0o644); err != nil {
		t.Fatalf("write legacy poster: %v", err)
	}
	background := filepath.Join(legacyRoot, "ABW-016 xx-background.jpg")
	if err := os.Symlink(posterName, background); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	expectedSource, err := filepath.EvalSymlinks(filepath.Join(legacyRoot, posterName))
	if err != nil {
		t.Fatalf("resolve expected poster path: %v", err)
	}
	if resolved, err := safeArtifactSourcePath(legacyRoot, filepath.Base(background)); err != nil || resolved != expectedSource {
		t.Fatalf("resolve internal background symlink: path=%q err=%v", resolved, err)
	}

	report, err := MigrateLegacyMediaWithOptions(context.Background(), database, MigrationOptions{Mode: MigrationApply, DataDir: dataDir})
	if err != nil {
		t.Fatalf("migrate internal background symlink: %v", err)
	}
	if report.ArtifactMovesPlanned != 2 || report.ArtifactsMoved != 2 {
		t.Fatalf("symlink move report = %+v", report)
	}
	target := filepath.Join(dataDir, "emby", "javdb", film.Actor, "ABW-016", "ABW-016-background.jpg")
	assertArtifactContent(t, target, "legacy poster")
	if info, err := os.Lstat(target); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("target background is not a symlink: info=%v err=%v", info, err)
	}
	if _, err := os.Stat(legacyRoot); !os.IsNotExist(err) {
		t.Fatalf("legacy symlink root remains: %v", err)
	}
}

func TestMigrateLegacyMediaAcceptsAlreadyCanonicalInternalBackgroundSymlink(t *testing.T) {
	for _, mode := range []MigrationMode{MigrationDryRun, MigrationApply} {
		t.Run(string(mode), func(t *testing.T) {
			database := newMigrationTestDB(t)
			dataDir := t.TempDir()
			createStorages(t, database, model.Storage{ID: 18, Driver: "FC2", MountPath: "/fc2"})
			film := model.Film{Source: "fc2", Actor: "个人收藏", Name: "FC2-PPV-2350291.mp4", Url: "FC2-PPV-2350291"}
			if err := database.Create(&film).Error; err != nil {
				t.Fatalf("seed film: %v", err)
			}
			root := filepath.Join(dataDir, "emby", "fc2", film.Actor, "FC2-PPV-2350291")
			if err := os.MkdirAll(root, 0o755); err != nil {
				t.Fatalf("create canonical root: %v", err)
			}
			posterName := "FC2-PPV-2350291.jpg"
			backgroundName := "FC2-PPV-2350291-background.jpg"
			if err := os.WriteFile(filepath.Join(root, posterName), []byte("canonical poster"), 0o644); err != nil {
				t.Fatalf("write canonical poster: %v", err)
			}
			backgroundPath := filepath.Join(root, backgroundName)
			if err := os.Symlink(posterName, backgroundPath); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}

			report, err := MigrateLegacyMediaWithOptions(context.Background(), database, MigrationOptions{Mode: mode, DataDir: dataDir})
			if err != nil {
				t.Fatalf("%s canonical internal symlink: %v", mode, err)
			}
			if report.ArtifactsExisting != 2 || report.ArtifactsPlanned != 0 {
				t.Fatalf("%s report = %+v", mode, report)
			}
			assertArtifactContent(t, filepath.Join(root, posterName), "canonical poster")
			assertArtifactContent(t, backgroundPath, "canonical poster")
			linkTarget, err := os.Readlink(backgroundPath)
			if err != nil {
				t.Fatalf("read canonical background symlink: %v", err)
			}
			if linkTarget != posterName {
				t.Fatalf("canonical background link = %q, want %q", linkTarget, posterName)
			}
		})
	}
}

func TestMigrateLegacyMediaRejectsNonRegularMatchingArtifactBeforeDatabaseWrites(t *testing.T) {
	database := newMigrationTestDB(t)
	dataDir := t.TempDir()
	previousDataDir := flags.DataDir
	flags.DataDir = dataDir
	t.Cleanup(func() { flags.DataDir = previousDataDir })
	createStorages(t, database, model.Storage{ID: 1, Driver: "Javdb", MountPath: "/javdb"})
	if err := database.Create(&model.Film{Source: "javdb", Actor: "Actor A", Name: "ABP-123 title.mp4", Url: "https://javdb.test/v/abp-123"}).Error; err != nil {
		t.Fatalf("seed film: %v", err)
	}
	legacyRoot := filepath.Join(dataDir, "emby", "javdb", "Actor A", "ABP-123 title")
	if err := os.MkdirAll(filepath.Join(legacyRoot, "fanart2.jpg"), 0o755); err != nil {
		t.Fatalf("create directory artifact: %v", err)
	}

	_, err := MigrateLegacyMediaWithOptions(context.Background(), database, MigrationOptions{Mode: MigrationApply, DataDir: dataDir})
	var artifactErr *ArtifactMigrationError
	if !errors.As(err, &artifactErr) || !errors.Is(err, ErrArtifactMigration) {
		t.Fatalf("directory migration error = %v, want typed artifact migration error", err)
	}
	assertCount(t, database, &model.FilmWork{}, 0)
	assertCount(t, database, &model.FilmFile{}, 0)
}

func TestMigrateLegacyMediaDryRunDoesNotCopyOrWriteJournal(t *testing.T) {
	database := newMigrationTestDB(t)
	dataDir := t.TempDir()
	previousDataDir := flags.DataDir
	flags.DataDir = dataDir
	t.Cleanup(func() { flags.DataDir = previousDataDir })
	createStorages(t, database, model.Storage{ID: 1, Driver: "Javdb", MountPath: "/javdb"})
	if err := database.Create(&model.Film{Source: "javdb", Actor: "Actor A", Name: "ABP-123 title.mp4", Url: "https://javdb.test/v/abp-123"}).Error; err != nil {
		t.Fatalf("seed film: %v", err)
	}
	writeArtifactFixture(t, filepath.Join(dataDir, "emby", "javdb", "Actor A", "ABP-123 title"), map[string]string{"poster.jpg": "legacy poster"})
	journalPath := filepath.Join(dataDir, "journal.json")
	report, err := MigrateLegacyMediaWithOptions(context.Background(), database, MigrationOptions{
		Mode: MigrationDryRun, DataDir: dataDir, JournalPath: journalPath,
	})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if report.ArtifactsPlanned != 2 || report.ArtifactMovesPlanned != 1 || report.ArtifactDirectoriesPlanned != 1 || report.ArtifactsMoved != 0 || report.ArtifactDirectoriesRemoved != 0 {
		t.Fatalf("dry-run report = %+v", report)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "emby", "javdb", "Actor A", "ABP-123", "poster.jpg")); !os.IsNotExist(err) {
		t.Fatalf("dry run created target, stat error = %v", err)
	}
	assertArtifactContent(t, filepath.Join(dataDir, "emby", "javdb", "Actor A", "ABP-123 title", "poster.jpg"), "legacy poster")
	if _, err := os.Stat(journalPath); !os.IsNotExist(err) {
		t.Fatalf("dry run wrote journal, stat error = %v", err)
	}
}

func TestMigrateLegacyMediaRemovesNonEmptyLegacyDirectory(t *testing.T) {
	database := newMigrationTestDB(t)
	dataDir := t.TempDir()
	createStorages(t, database, model.Storage{ID: 1, Driver: "Javdb", MountPath: "/javdb"})
	if err := database.Create(&model.Film{Source: "javdb", Actor: "Actor A", Name: "ABP-123 title.mp4", Url: "https://javdb.test/v/abp-123"}).Error; err != nil {
		t.Fatalf("seed film: %v", err)
	}
	legacyRoot := filepath.Join(dataDir, "emby", "javdb", "Actor A", "ABP-123 title")
	writeArtifactFixture(t, legacyRoot, map[string]string{"poster.jpg": "poster", "keep.txt": "operator file"})

	report, err := MigrateLegacyMediaWithOptions(context.Background(), database, MigrationOptions{Mode: MigrationApply, DataDir: dataDir})
	if err != nil {
		t.Fatalf("migrate with nonempty legacy root: %v", err)
	}
	if report.ArtifactDirectoriesPlanned != 1 || report.ArtifactDirectoriesRemoved != 1 {
		t.Fatalf("nonempty directory report = %+v", report)
	}
	if _, err := os.Stat(legacyRoot); !os.IsNotExist(err) {
		t.Fatalf("nonempty legacy root remains: %v", err)
	}
	assertArtifactContent(t, filepath.Join(dataDir, "emby", "javdb", "Actor A", "ABP-123", "poster.jpg"), "poster")
}

func TestMigrateLegacyMediaDryRunPlansUnverifiedLegacyDirectoryRemoval(t *testing.T) {
	database := newMigrationTestDB(t)
	dataDir := t.TempDir()
	createStorages(t, database, model.Storage{ID: 18, Driver: "FC2", MountPath: "/fc2"})
	films := []model.Film{
		{Source: "fc2", Actor: "Collection", Name: "FC2-PPV-100 title-cd1.mp4", Url: "FC2-PPV-100"},
		{Source: "fc2", Actor: "Collection", Name: "FC2-PPV-100 title-cd2.mp4", Url: "FC2-PPV-100"},
	}
	if err := database.Create(&films).Error; err != nil {
		t.Fatalf("seed multipart films: %v", err)
	}
	legacyRoot := filepath.Join(dataDir, "emby", "fc2", "Collection", "FC2-PPV-100 title")
	writeArtifactFixture(t, legacyRoot, map[string]string{"FC2-PPV-100 title-cd2.nfo": "unverified cd2 nfo"})

	report, err := MigrateLegacyMediaWithOptions(context.Background(), database, MigrationOptions{Mode: MigrationDryRun, DataDir: dataDir})
	if err != nil {
		t.Fatalf("dry-run unverified cleanup: %v", err)
	}
	if report.ArtifactDeletesPlanned != 0 || report.ArtifactDirectoriesPlanned != 1 {
		t.Fatalf("unverified cleanup report = %+v", report)
	}
	assertArtifactContent(t, filepath.Join(legacyRoot, "FC2-PPV-100 title-cd2.nfo"), "unverified cd2 nfo")
}

func TestMigrateLegacyMediaRejectsVersionOneJournalBeforeDatabaseWrites(t *testing.T) {
	database := newMigrationTestDB(t)
	dataDir := t.TempDir()
	journalPath := filepath.Join(dataDir, "journal.json")
	createStorages(t, database, model.Storage{ID: 1, Driver: "Javdb", MountPath: "/javdb"})
	if err := database.Create(&model.Film{Source: "javdb", Actor: "Actor A", Name: "ABP-123 title.mp4", Url: "https://javdb.test/v/abp-123"}).Error; err != nil {
		t.Fatalf("seed film: %v", err)
	}
	if err := os.WriteFile(journalPath, []byte(`{"version":1,"entries":[]}`), 0o644); err != nil {
		t.Fatalf("seed v1 journal: %v", err)
	}

	_, err := MigrateLegacyMediaWithOptions(context.Background(), database, MigrationOptions{
		Mode: MigrationApply, DataDir: dataDir, JournalPath: journalPath,
	})
	if !errors.Is(err, ErrArtifactJournalVersion) {
		t.Fatalf("v1 journal error = %v, want ErrArtifactJournalVersion", err)
	}
	assertCount(t, database, &model.FilmWork{}, 0)
}

func TestMigrateLegacyMediaRecoversRenameCompletedBeforeJournalUpdate(t *testing.T) {
	database := newMigrationTestDB(t)
	dataDir := t.TempDir()
	journalPath := filepath.Join(dataDir, "journal.json")
	createStorages(t, database, model.Storage{ID: 1, Driver: "Javdb", MountPath: "/javdb"})
	if err := database.Create(&model.Film{Source: "javdb", Actor: "Actor A", Name: "ABP-123 title.mp4", Url: "https://javdb.test/v/abp-123"}).Error; err != nil {
		t.Fatalf("seed film: %v", err)
	}
	legacyRoot := filepath.Join(dataDir, "emby", "javdb", "Actor A", "ABP-123 title")
	writeArtifactFixture(t, legacyRoot, map[string]string{"poster.jpg": "poster"})
	options := MigrationOptions{Mode: MigrationApply, DataDir: dataDir, JournalPath: journalPath}
	normalized, err := options.normalized()
	if err != nil {
		t.Fatalf("normalize options: %v", err)
	}
	databasePlan, err := buildMigrationPlan(database, normalized)
	if err != nil {
		t.Fatalf("build database plan: %v", err)
	}
	artifacts, err := collectArtifactPlan(databasePlan, normalized.DataDir)
	if err != nil {
		t.Fatalf("build artifact plan: %v", err)
	}
	artifacts.ID = artifactIdentityPlanID(databasePlan)
	var move artifactOperation
	for _, operation := range artifacts.Operations {
		if operation.Kind == artifactMove {
			move = operation
			break
		}
	}
	if move.ID == "" {
		t.Fatal("artifact plan has no move operation")
	}
	if err := os.MkdirAll(filepath.Dir(move.TargetPath), 0o755); err != nil {
		t.Fatalf("create target root: %v", err)
	}
	if err := os.Rename(move.SourcePath, move.TargetPath); err != nil {
		t.Fatalf("simulate completed rename: %v", err)
	}
	journal := artifactJournal{Version: artifactJournalVersion, PlanID: artifacts.ID, Operations: artifacts.Operations}
	if err := writeArtifactJournal(journalPath, dataDir, journal); err != nil {
		t.Fatalf("write interrupted journal: %v", err)
	}

	report, err := MigrateLegacyMediaWithOptions(context.Background(), database, options)
	if err != nil {
		t.Fatalf("recover interrupted rename: %v", err)
	}
	if report.ArtifactsMoved != 0 || report.ArtifactsExisting != 1 || report.ArtifactDirectoriesRemoved != 1 {
		t.Fatalf("recovery report = %+v", report)
	}
	assertArtifactContent(t, move.TargetPath, "poster")
	if _, err := os.Stat(legacyRoot); !os.IsNotExist(err) {
		t.Fatalf("legacy root remains after recovery: %v", err)
	}
}

func TestArtifactJournalRejectsUnknownOperationState(t *testing.T) {
	dataDir := t.TempDir()
	operation := artifactOperation{Kind: artifactRemoveDir, SourcePath: filepath.Join(dataDir, "legacy"), State: "future-state"}
	operation.ID = stableHash(string(operation.Kind), operation.SourcePath, operation.TargetPath, operation.VerifyPath, operation.LinkTarget, operation.SHA256)
	err := preflightJournalOperations(&artifactPlan{ID: "plan", Operations: []artifactOperation{operation}}, dataDir)
	if !errors.Is(err, ErrArtifactMigration) {
		t.Fatalf("unknown journal state error = %v, want ErrArtifactMigration", err)
	}
}

func TestLoadArtifactJournalAppliesProgressAndIgnoresTruncatedFinalLine(t *testing.T) {
	dataDir := t.TempDir()
	journalPath := filepath.Join(dataDir, "journal.json")
	operation := artifactOperation{Kind: artifactRemoveDir, SourcePath: filepath.Join(dataDir, "legacy"), State: "pending"}
	operation.ID = stableHash(string(operation.Kind), operation.SourcePath, operation.TargetPath, operation.VerifyPath, operation.LinkTarget, operation.SHA256)
	journal := artifactJournal{Version: artifactJournalVersion, PlanID: "plan-id", Operations: []artifactOperation{operation}}
	if err := writeArtifactJournal(journalPath, dataDir, journal); err != nil {
		t.Fatalf("write immutable journal: %v", err)
	}
	progress := fmt.Sprintf("{\"plan_id\":\"plan-id\",\"operation_id\":%q,\"state\":\"verified\"}\n{\"plan_id\":", operation.ID)
	if err := os.WriteFile(journalPath+".progress", []byte(progress), 0o644); err != nil {
		t.Fatalf("write progress fixture: %v", err)
	}

	loaded, exists, err := loadArtifactJournal(journalPath, dataDir)
	if err != nil {
		t.Fatalf("load journal with progress: %v", err)
	}
	if !exists || len(loaded.Operations) != 1 || loaded.Operations[0].State != "verified" {
		t.Fatalf("loaded journal = %+v", loaded)
	}
	progressLog := artifactProgressLog{path: journalPath + ".progress", dataDir: dataDir, planID: journal.PlanID}
	if err := progressLog.record(&loaded.Operations[0], "done"); err != nil {
		t.Fatalf("append after truncated progress tail: %v", err)
	}
	reloaded, exists, err := loadArtifactJournal(journalPath, dataDir)
	if err != nil {
		t.Fatalf("reload journal after progress append: %v", err)
	}
	if !exists || reloaded.Operations[0].State != "done" {
		t.Fatalf("reloaded journal = %+v", reloaded)
	}
}

func TestArtifactProgressRecordUsesBoundedMemoryForCompleteLog(t *testing.T) {
	dataDir := t.TempDir()
	progressPath := filepath.Join(dataDir, "journal.json.progress")
	content := bytes.Repeat([]byte("{}\n"), 2<<20)
	if err := os.WriteFile(progressPath, content, 0o644); err != nil {
		t.Fatalf("write complete progress fixture: %v", err)
	}
	operation := artifactOperation{ID: "operation-id", State: "pending"}
	progress := artifactProgressLog{path: progressPath, dataDir: dataDir, planID: "plan-id"}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	if err := progress.record(&operation, "done"); err != nil {
		t.Fatalf("record progress event: %v", err)
	}
	runtime.ReadMemStats(&after)
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated >= uint64(len(content)/2) {
		t.Fatalf("progress append allocated %d bytes for %d-byte complete log", allocated, len(content))
	}
}

func TestLoadArtifactJournalRejectsInvalidCompleteProgressEvents(t *testing.T) {
	for _, test := range []struct {
		name     string
		progress func(operationID string) string
	}{
		{name: "malformed", progress: func(string) string { return "not-json\n" }},
		{name: "empty-line", progress: func(string) string { return "\n" }},
		{name: "unknown-operation", progress: func(string) string {
			return "{\"plan_id\":\"plan-id\",\"operation_id\":\"missing\",\"state\":\"done\"}\n"
		}},
		{name: "unknown-state", progress: func(operationID string) string {
			return fmt.Sprintf("{\"plan_id\":\"plan-id\",\"operation_id\":%q,\"state\":\"future\"}\n", operationID)
		}},
		{name: "plan-mismatch", progress: func(operationID string) string {
			return fmt.Sprintf("{\"plan_id\":\"other-plan\",\"operation_id\":%q,\"state\":\"done\"}\n", operationID)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			dataDir := t.TempDir()
			journalPath := filepath.Join(dataDir, "journal.json")
			operation := artifactOperation{Kind: artifactRemoveDir, SourcePath: filepath.Join(dataDir, "legacy"), State: "pending"}
			operation.ID = stableHash(string(operation.Kind), operation.SourcePath, operation.TargetPath, operation.VerifyPath, operation.LinkTarget, operation.SHA256)
			journal := artifactJournal{Version: artifactJournalVersion, PlanID: "plan-id", Operations: []artifactOperation{operation}}
			if err := writeArtifactJournal(journalPath, dataDir, journal); err != nil {
				t.Fatalf("write immutable journal: %v", err)
			}
			if err := os.WriteFile(journalPath+".progress", []byte(test.progress(operation.ID)), 0o644); err != nil {
				t.Fatalf("write progress fixture: %v", err)
			}
			if _, _, err := loadArtifactJournal(journalPath, dataDir); !errors.Is(err, ErrArtifactMigration) {
				t.Fatalf("invalid progress error = %v, want ErrArtifactMigration", err)
			}
		})
	}
}

func TestExecuteSymlinkMoveRecoversExpectedOrphanTemporaryLink(t *testing.T) {
	dataDir := t.TempDir()
	sourceRoot := filepath.Join(dataDir, "legacy")
	targetRoot := filepath.Join(dataDir, "target")
	if err := os.MkdirAll(sourceRoot, 0o755); err != nil {
		t.Fatalf("create source root: %v", err)
	}
	if err := os.MkdirAll(targetRoot, 0o755); err != nil {
		t.Fatalf("create target root: %v", err)
	}
	targetPoster := filepath.Join(targetRoot, "CODE.jpg")
	if err := os.WriteFile(targetPoster, []byte("poster"), 0o644); err != nil {
		t.Fatalf("write target poster: %v", err)
	}
	sourceBackground := filepath.Join(sourceRoot, "CODE title-background.jpg")
	if err := os.Symlink("CODE title.jpg", sourceBackground); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "CODE title.jpg"), []byte("poster"), 0o644); err != nil {
		t.Fatalf("write source poster: %v", err)
	}
	targetBackground := filepath.Join(targetRoot, "CODE-background.jpg")
	relativeTarget, err := filepath.Rel(filepath.Dir(targetBackground), targetPoster)
	if err != nil {
		t.Fatalf("resolve relative target: %v", err)
	}
	temporary := targetBackground + ".media-migration-link"
	if err := os.Symlink(relativeTarget, temporary); err != nil {
		t.Fatalf("create interrupted temporary symlink: %v", err)
	}
	hash, err := fileHash(targetPoster)
	if err != nil {
		t.Fatalf("hash target poster: %v", err)
	}
	operation := artifactOperation{Kind: artifactSymlink, SourcePath: sourceBackground, TargetPath: targetBackground, LinkTarget: targetPoster, SHA256: hash, State: "pending"}
	operation.ID = stableHash(string(operation.Kind), operation.SourcePath, operation.TargetPath, operation.VerifyPath, operation.LinkTarget, operation.SHA256)
	progress := &artifactProgressLog{path: filepath.Join(dataDir, "journal.json.progress"), dataDir: dataDir, planID: "plan-id"}

	changed, err := executeSymlinkMove(&operation, progress)
	if err != nil {
		t.Fatalf("recover orphan temporary symlink: %v", err)
	}
	if !changed {
		t.Fatal("recovered symlink move was not reported as changed")
	}
	if _, err := os.Lstat(temporary); !os.IsNotExist(err) {
		t.Fatalf("temporary symlink remains: %v", err)
	}
	if target, err := os.Readlink(targetBackground); err != nil || target != relativeTarget {
		t.Fatalf("target symlink = %q, err=%v; want %q", target, err, relativeTarget)
	}
	if _, err := os.Lstat(sourceBackground); !os.IsNotExist(err) {
		t.Fatalf("source symlink remains: %v", err)
	}
}

func TestRemoveExpectedTemporarySymlinkRejectsUnexpectedEntry(t *testing.T) {
	for _, test := range []struct {
		name   string
		create func(path string) error
	}{
		{name: "regular-file", create: func(path string) error { return os.WriteFile(path, []byte("keep"), 0o644) }},
		{name: "different-link", create: func(path string) error { return os.Symlink("different.jpg", path) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "target.media-migration-link")
			if err := test.create(path); err != nil {
				t.Skipf("create temporary entry: %v", err)
			}
			if err := removeExpectedTemporarySymlink(path, "expected.jpg"); !errors.Is(err, ErrArtifactMigration) {
				t.Fatalf("unexpected temporary entry error = %v, want ErrArtifactMigration", err)
			}
			if _, err := os.Lstat(path); err != nil {
				t.Fatalf("unexpected temporary entry was removed: %v", err)
			}
		})
	}
}

func TestArtifactPlanSymlinkDependsOnSelectedRegularDestination(t *testing.T) {
	database := newMigrationTestDB(t)
	dataDir := t.TempDir()
	createStorages(t, database, model.Storage{ID: 1, Driver: "Javdb", MountPath: "/javdb"})
	film := model.Film{Source: "javdb", Actor: "Actor A", Name: "ABP-123.mp4", Url: "https://javdb.test/v/abp-123"}
	if err := database.Create(&film).Error; err != nil {
		t.Fatalf("seed film: %v", err)
	}
	storageRoot := filepath.Join(dataDir, "emby", "javdb", "1", film.Actor, "ABP-123")
	if err := os.MkdirAll(storageRoot, 0o755); err != nil {
		t.Fatalf("create storage-scoped root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(storageRoot, "ABP-123.jpg"), []byte("poster"), 0o644); err != nil {
		t.Fatalf("write regular poster: %v", err)
	}
	if err := os.Symlink("ABP-123.jpg", filepath.Join(storageRoot, "poster.jpg")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	options, err := (MigrationOptions{Mode: MigrationDryRun, DataDir: dataDir}).normalized()
	if err != nil {
		t.Fatalf("normalize options: %v", err)
	}
	databasePlan, err := buildMigrationPlan(database, options)
	if err != nil {
		t.Fatalf("build database plan: %v", err)
	}
	artifacts, err := collectArtifactPlan(databasePlan, dataDir)
	if err != nil {
		t.Fatalf("build artifact plan: %v", err)
	}
	wantTarget := filepath.Join(dataDir, "emby", "javdb", film.Actor, "ABP-123", "ABP-123.jpg")
	for _, operation := range artifacts.Operations {
		if operation.Kind == artifactSymlink && filepath.Base(operation.TargetPath) == "poster.jpg" {
			if operation.LinkTarget != wantTarget || operation.LinkTarget == operation.TargetPath {
				t.Fatalf("poster symlink target = %q, want regular destination %q", operation.LinkTarget, wantTarget)
			}
			return
		}
	}
	t.Fatal("artifact plan has no poster symlink operation")
}

func TestPreflightRejectsInvalidSymlinkDependencies(t *testing.T) {
	for _, test := range []struct {
		name      string
		linkPaths func(targetA, targetB, missing string) (string, string)
		count     int
	}{
		{name: "self", count: 1, linkPaths: func(targetA, _, _ string) (string, string) { return targetA, "" }},
		{name: "unresolved", count: 1, linkPaths: func(_, _, missing string) (string, string) { return missing, "" }},
		{name: "cyclic", count: 2, linkPaths: func(targetA, targetB, _ string) (string, string) { return targetB, targetA }},
	} {
		t.Run(test.name, func(t *testing.T) {
			dataDir := t.TempDir()
			backing := filepath.Join(dataDir, "backing.jpg")
			if err := os.WriteFile(backing, []byte("poster"), 0o644); err != nil {
				t.Fatalf("write backing file: %v", err)
			}
			hash, err := fileHash(backing)
			if err != nil {
				t.Fatalf("hash backing file: %v", err)
			}
			targetA := filepath.Join(dataDir, "target-a.jpg")
			targetB := filepath.Join(dataDir, "target-b.jpg")
			linkA, linkB := test.linkPaths(targetA, targetB, filepath.Join(dataDir, "missing.jpg"))
			operations := make([]artifactOperation, 0, test.count)
			for index, values := range [][3]string{
				{filepath.Join(dataDir, "source-a.jpg"), targetA, linkA},
				{filepath.Join(dataDir, "source-b.jpg"), targetB, linkB},
			}[:test.count] {
				if err := os.Symlink(filepath.Base(backing), values[0]); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
				operation := artifactOperation{Kind: artifactSymlink, SourcePath: values[0], TargetPath: values[1], LinkTarget: values[2], SHA256: hash, State: "pending"}
				operation.ID = stableHash(string(operation.Kind), operation.SourcePath, operation.TargetPath, operation.VerifyPath, operation.LinkTarget, operation.SHA256)
				operations = append(operations, operation)
				_ = index
			}
			if err := preflightJournalOperations(&artifactPlan{ID: "plan", Operations: operations}, dataDir); !errors.Is(err, ErrArtifactMigration) {
				t.Fatalf("invalid symlink dependency error = %v, want ErrArtifactMigration", err)
			}
		})
	}
}

func TestMigrateLegacyMediaStorageScopedRootRemovesUnrecognizedCodeFiles(t *testing.T) {
	database := newMigrationTestDB(t)
	dataDir := t.TempDir()
	createStorages(t, database, model.Storage{ID: 1, Driver: "Javdb", MountPath: "/javdb"})
	film := model.Film{Source: "javdb", Actor: "Actor A", Name: "ABP-123.mp4", Url: "https://javdb.test/v/abp-123"}
	if err := database.Create(&film).Error; err != nil {
		t.Fatalf("seed film: %v", err)
	}
	storageRoot := filepath.Join(dataDir, "emby", "javdb", "1", film.Actor, "ABP-123")
	writeArtifactFixture(t, storageRoot, map[string]string{
		"ABP-123.1.srt":     "subtitle",
		"ABP-123.notes":     "operator notes",
		"ABP-123-cd-backup": "operator backup",
	})

	report, err := MigrateLegacyMediaWithOptions(context.Background(), database, MigrationOptions{Mode: MigrationApply, DataDir: dataDir})
	if err != nil {
		t.Fatalf("migrate storage-scoped artifacts: %v", err)
	}
	if report.ArtifactMovesPlanned != 1 || report.ArtifactsMoved != 1 || report.ArtifactDirectoriesPlanned != 1 || report.ArtifactDirectoriesRemoved != 1 {
		t.Fatalf("storage-scoped report = %+v", report)
	}
	targetRoot := filepath.Join(dataDir, "emby", "javdb", film.Actor, "ABP-123")
	assertArtifactContent(t, filepath.Join(targetRoot, "ABP-123.1.srt"), "subtitle")
	if _, err := os.Stat(storageRoot); !os.IsNotExist(err) {
		t.Fatalf("storage-scoped root remains: %v", err)
	}
}

func TestMigrateLegacyMediaUsesExplicitStorageMappingWhenSourceIsAmbiguous(t *testing.T) {
	database := newMigrationTestDB(t)
	createStorages(t, database,
		model.Storage{ID: 1, Driver: "Javdb", MountPath: "/javdb-one"},
		model.Storage{ID: 2, Driver: "Javdb", MountPath: "/javdb-two"},
	)
	if err := database.Create(&model.Film{Source: "javdb", Actor: "Actor A", Name: "ABP-123 title.mp4", Url: "https://javdb.test/v/abp-123"}).Error; err != nil {
		t.Fatalf("seed film: %v", err)
	}
	if _, err := MigrateLegacyMedia(context.Background(), database); !errors.Is(err, ErrUnresolvedIdentity) {
		t.Fatalf("ambiguous default error = %v, want ErrUnresolvedIdentity", err)
	}
	_, err := MigrateLegacyMediaWithOptions(context.Background(), database, MigrationOptions{
		Mode: MigrationDryRun, StorageMapping: map[string]uint{"javdb:Actor A": 2},
	})
	if err != nil {
		t.Fatalf("mapped dry run: %v", err)
	}
}

func writeArtifactFixture(t *testing.T, root string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("create artifact root: %v", err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write artifact %s: %v", name, err)
		}
	}
}

func assertArtifactContent(t *testing.T, path, want string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read artifact %s: %v", path, err)
	}
	if string(content) != want {
		t.Fatalf("artifact %s = %q, want %q", path, content, want)
	}
}
