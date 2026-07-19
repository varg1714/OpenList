package media

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/cmd/flags"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

func TestMigrateLegacyMediaCopiesArtifactsAndRetainsLegacyFiles(t *testing.T) {
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
	if report.ArtifactsCopied != 5 || report.ArtifactsPlanned != 5 {
		t.Fatalf("artifact report = %+v", report)
	}
	targetRoot := filepath.Join(dataDir, "emby", "javdb", "1", "Actor A", "ABP-123")
	for name, want := range map[string]string{
		"ABP-123.jpg":            "legacy poster",
		"ABP-123-background.jpg": "legacy background",
		"ABP-123.nfo":            "legacy nfo",
		"fanart1.jpg":            "legacy fanart",
		"ABP-123.1.srt":          "legacy subtitle",
	} {
		assertArtifactContent(t, filepath.Join(targetRoot, name), want)
	}
	assertArtifactContent(t, filepath.Join(legacyRoot, "ABP-123 title.jpg"), "legacy poster")
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
	if first.ArtifactsCopied != 1 || second.ArtifactsCopied != 0 || second.ArtifactsExisting != 1 {
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
	assertCount(t, database, &model.CloudFileCache{}, 0)
}

func TestMigrateLegacyMediaRefusesPlannedArtifactTargetCollisionBeforeDatabaseWrites(t *testing.T) {
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
	if !errors.Is(err, ErrArtifactCollision) {
		t.Fatalf("planned collision error = %v, want ErrArtifactCollision", err)
	}
	assertCount(t, database, &model.FilmWork{}, 0)
	assertCount(t, database, &model.FilmFile{}, 0)
	assertCount(t, database, &model.SourceMagnet{}, 0)
	assertCount(t, database, &model.CloudFileCache{}, 0)
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
	if report.ArtifactsPlanned != 1 {
		t.Fatalf("dry-run report = %+v", report)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "emby", "javdb", "1", "Actor A", "ABP-123", "poster.jpg")); !os.IsNotExist(err) {
		t.Fatalf("dry run created target, stat error = %v", err)
	}
	if _, err := os.Stat(journalPath); !os.IsNotExist(err) {
		t.Fatalf("dry run wrote journal, stat error = %v", err)
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
