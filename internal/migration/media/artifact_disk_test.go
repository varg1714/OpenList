package media

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

func TestMigrateLegacyMediaUsesActualArtifactNamesWhenDatabaseNameIsTooLong(t *testing.T) {
	// Given
	database := newMigrationTestDB(t)
	dataDir := t.TempDir()
	createStorages(t, database, model.Storage{ID: 15, Driver: "Javdb", MountPath: "/javdb"})
	databaseName := "SSNI-772 " + strings.Repeat("x", 300) + ".mp4"
	film := model.Film{
		Source: "javdb",
		Actor:  "miru",
		Name:   databaseName,
		Url:    "https://javdb.test/v/ssni-772",
	}
	if err := database.Create(&film).Error; err != nil {
		t.Fatalf("seed film: %v", err)
	}
	actualName := "SSNI-772 truncated.title"
	legacyRoot := filepath.Join(dataDir, "emby", "javdb", "miru", actualName)
	writeArtifactFixture(t, legacyRoot, map[string]string{
		actualName + ".jpg":  "legacy poster",
		actualName + ".nfo":  "legacy nfo",
		actualName + ".strm": "legacy stream",
	})

	// When
	report, err := MigrateLegacyMediaWithOptions(context.Background(), database, MigrationOptions{
		Mode: MigrationApply, DataDir: dataDir, JournalPath: filepath.Join(dataDir, "migration-journal.json"),
	})

	// Then
	if err != nil {
		t.Fatalf("migrate artifacts for overlong database name: %v", err)
	}
	if report.ArtifactsMoved != 2 || report.ArtifactDirectoriesRemoved != 1 {
		t.Fatalf("artifact report = %+v", report)
	}
	targetRoot := filepath.Join(dataDir, "emby", "javdb", "miru", "SSNI-772")
	assertArtifactContent(t, filepath.Join(targetRoot, "SSNI-772.jpg"), "legacy poster")
	assertArtifactContent(t, filepath.Join(targetRoot, "SSNI-772.nfo"), "legacy nfo")
	if _, err := os.Stat(legacyRoot); !os.IsNotExist(err) {
		t.Fatalf("legacy root remains after migration: %v", err)
	}
}

func TestMigrateLegacyMediaSkipsImpossibleBackgroundNameForMaximumLengthDirectory(t *testing.T) {
	// Given
	database := newMigrationTestDB(t)
	dataDir := t.TempDir()
	createStorages(t, database, model.Storage{ID: 15, Driver: "Javdb", MountPath: "/javdb"})
	film := model.Film{
		Source: "javdb",
		Actor:  "miru",
		Name:   "SSNI-772 " + strings.Repeat("x", 300) + ".mp4",
		Url:    "https://javdb.test/v/ssni-772",
	}
	if err := database.Create(&film).Error; err != nil {
		t.Fatalf("seed film: %v", err)
	}
	actualName := "SSNI-772 " + strings.Repeat("x", 242)
	legacyRoot := filepath.Join(dataDir, "emby", "javdb", "miru", actualName)
	writeArtifactFixture(t, legacyRoot, map[string]string{
		actualName + ".jpg": "legacy poster",
		actualName + ".nfo": "legacy nfo",
	})

	// When
	report, err := MigrateLegacyMediaWithOptions(context.Background(), database, MigrationOptions{
		Mode: MigrationApply, DataDir: dataDir, JournalPath: filepath.Join(dataDir, "migration-journal.json"),
	})

	// Then
	if err != nil {
		t.Fatalf("migrate maximum-length artifact names: %v", err)
	}
	if report.ArtifactsMoved != 2 || report.ArtifactDirectoriesRemoved != 1 {
		t.Fatalf("artifact report = %+v", report)
	}
	targetRoot := filepath.Join(dataDir, "emby", "javdb", "miru", "SSNI-772")
	assertArtifactContent(t, filepath.Join(targetRoot, "SSNI-772.jpg"), "legacy poster")
	assertArtifactContent(t, filepath.Join(targetRoot, "SSNI-772.nfo"), "legacy nfo")
}

func TestMigrateLegacyMediaMovesCodePrefixedArtifactDirectoryWhenDatabaseTitleDiffers(t *testing.T) {
	// Given
	database := newMigrationTestDB(t)
	dataDir := t.TempDir()
	createStorages(t, database, model.Storage{ID: 15, Driver: "Javdb", MountPath: "/javdb"})
	film := model.Film{
		Source: "javdb",
		Actor:  "miru",
		Name:   "SSNI-772 translated-title.mp4",
		Url:    "https://javdb.test/v/ssni-772",
	}
	if err := database.Create(&film).Error; err != nil {
		t.Fatalf("seed film: %v", err)
	}
	legacyRoot := filepath.Join(dataDir, "emby", "javdb", "miru", "SSNI-772 original-title")
	writeArtifactFixture(t, legacyRoot, map[string]string{
		"SSNI-772 original-title.nfo": "legacy nfo",
	})

	// When
	report, err := MigrateLegacyMediaWithOptions(context.Background(), database, MigrationOptions{
		Mode: MigrationApply, DataDir: dataDir, JournalPath: filepath.Join(dataDir, "migration-journal.json"),
	})

	// Then
	if err != nil {
		t.Fatalf("migrate code-prefixed directory: %v", err)
	}
	if report.ArtifactMovesPlanned != 1 || report.ArtifactsMoved != 1 || report.ArtifactDirectoriesRemoved != 1 {
		t.Fatalf("artifact report = %+v", report)
	}
	assertArtifactContent(t, filepath.Join(dataDir, "emby", "javdb", "miru", "SSNI-772", "SSNI-772.nfo"), "legacy nfo")
	if _, err := os.Stat(legacyRoot); !os.IsNotExist(err) {
		t.Fatalf("legacy root remains after move: %v", err)
	}
}

func TestMigrateLegacyMediaRemovesLegacyDirectoryContainingOnlyFinderMetadataAfterMove(t *testing.T) {
	// Given
	database := newMigrationTestDB(t)
	dataDir := t.TempDir()
	createStorages(t, database, model.Storage{ID: 15, Driver: "Javdb", MountPath: "/javdb"})
	film := model.Film{
		Source: "javdb",
		Actor:  "miru",
		Name:   "SSNI-795 title.mp4",
		Url:    "https://javdb.test/v/ssni-795",
	}
	if err := database.Create(&film).Error; err != nil {
		t.Fatalf("seed film: %v", err)
	}
	legacyRoot := filepath.Join(dataDir, "emby", "javdb", "miru", "SSNI-795 title")
	writeArtifactFixture(t, legacyRoot, map[string]string{
		".DS_Store":          "finder metadata",
		"SSNI-795 title.nfo": "legacy nfo",
	})

	// When
	report, err := MigrateLegacyMediaWithOptions(context.Background(), database, MigrationOptions{
		Mode: MigrationApply, DataDir: dataDir, JournalPath: filepath.Join(dataDir, "migration-journal.json"),
	})

	// Then
	if err != nil {
		t.Fatalf("migrate directory with Finder metadata: %v", err)
	}
	if report.ArtifactsMoved != 1 || report.ArtifactDirectoriesRemoved != 1 {
		t.Fatalf("artifact report = %+v", report)
	}
	if _, err := os.Stat(legacyRoot); !os.IsNotExist(err) {
		t.Fatalf("legacy root remains after move: %v", err)
	}
}

func TestMigrateLegacyMediaReplansArtifactsAfterCompletedJournal(t *testing.T) {
	// Given
	database := newMigrationTestDB(t)
	dataDir := t.TempDir()
	createStorages(t, database, model.Storage{ID: 15, Driver: "Javdb", MountPath: "/javdb"})
	film := model.Film{
		Source: "javdb",
		Actor:  "miru",
		Name:   "SSNI-772 translated-title.mp4",
		Url:    "https://javdb.test/v/ssni-772",
	}
	if err := database.Create(&film).Error; err != nil {
		t.Fatalf("seed film: %v", err)
	}
	knownRoot := filepath.Join(dataDir, "emby", "javdb", "miru", "SSNI-772 translated-title")
	writeArtifactFixture(t, knownRoot, map[string]string{"poster.jpg": "legacy poster"})
	options := MigrationOptions{
		Mode: MigrationApply, DataDir: dataDir, JournalPath: filepath.Join(dataDir, "migration-journal.json"),
	}
	if _, err := MigrateLegacyMediaWithOptions(context.Background(), database, options); err != nil {
		t.Fatalf("initial migration: %v", err)
	}
	alternateRoot := filepath.Join(dataDir, "emby", "javdb", "miru", "SSNI-772 original-title")
	writeArtifactFixture(t, alternateRoot, map[string]string{
		"SSNI-772 original-title.1.srt": "legacy subtitle",
	})

	// When
	report, err := MigrateLegacyMediaWithOptions(context.Background(), database, options)

	// Then
	if err != nil {
		t.Fatalf("rerun migration after completed journal: %v", err)
	}
	if report.ArtifactsMoved != 1 || report.ArtifactDirectoriesRemoved != 1 {
		t.Fatalf("rerun artifact report = %+v", report)
	}
	assertArtifactContent(t, filepath.Join(dataDir, "emby", "javdb", "miru", "SSNI-772", "SSNI-772.1.srt"), "legacy subtitle")
	if _, err := os.Stat(alternateRoot); !os.IsNotExist(err) {
		t.Fatalf("alternate legacy root remains after rerun: %v", err)
	}
}

func TestMigrateLegacyMediaRemovesCodePrefixedDirectoryWithoutDatabaseWork(t *testing.T) {
	// Given
	database := newMigrationTestDB(t)
	dataDir := t.TempDir()
	createStorages(t, database, model.Storage{ID: 15, Driver: "Javdb", MountPath: "/javdb"})
	film := model.Film{
		Source: "javdb",
		Actor:  "miru",
		Name:   "SSNI-772 translated-title.mp4",
		Url:    "https://javdb.test/v/ssni-772",
	}
	if err := database.Create(&film).Error; err != nil {
		t.Fatalf("seed film: %v", err)
	}
	orphanRoot := filepath.Join(dataDir, "emby", "javdb", "miru", "SSNI-773 orphan-title")
	orphanNFO := filepath.Join(orphanRoot, "SSNI-773 orphan-title.nfo")
	writeArtifactFixture(t, orphanRoot, map[string]string{filepath.Base(orphanNFO): "orphan nfo"})

	// When
	report, err := MigrateLegacyMediaWithOptions(context.Background(), database, MigrationOptions{
		Mode: MigrationApply, DataDir: dataDir, JournalPath: filepath.Join(dataDir, "migration-journal.json"),
	})

	// Then
	if err != nil {
		t.Fatalf("migrate with unmatched directory: %v", err)
	}
	if report.ArtifactMovesPlanned != 0 || report.ArtifactsMoved != 0 || report.ArtifactDirectoriesPlanned != 1 || report.ArtifactDirectoriesRemoved != 1 {
		t.Fatalf("orphan artifact report = %+v", report)
	}
	if _, err := os.Stat(orphanRoot); !os.IsNotExist(err) {
		t.Fatalf("orphan artifact root remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "emby", "javdb", "miru", "SSNI-773")); !os.IsNotExist(err) {
		t.Fatalf("orphan code directory was created: %v", err)
	}
}

func TestMigrateLegacyMediaRefusesDifferingNFOInCodePrefixedDirectory(t *testing.T) {
	// Given
	database := newMigrationTestDB(t)
	dataDir := t.TempDir()
	createStorages(t, database, model.Storage{ID: 15, Driver: "Javdb", MountPath: "/javdb"})
	film := model.Film{
		Source: "javdb",
		Actor:  "miru",
		Name:   "SSNI-772 translated-title.mp4",
		Url:    "https://javdb.test/v/ssni-772",
	}
	if err := database.Create(&film).Error; err != nil {
		t.Fatalf("seed film: %v", err)
	}
	legacyRoot := filepath.Join(dataDir, "emby", "javdb", "miru", "SSNI-772 original-title")
	targetRoot := filepath.Join(dataDir, "emby", "javdb", "miru", "SSNI-772")
	writeArtifactFixture(t, legacyRoot, map[string]string{"SSNI-772 original-title.nfo": "legacy nfo"})
	writeArtifactFixture(t, targetRoot, map[string]string{"SSNI-772.nfo": "new nfo"})

	// When
	_, err := MigrateLegacyMediaWithOptions(context.Background(), database, MigrationOptions{
		Mode: MigrationApply, DataDir: dataDir, JournalPath: filepath.Join(dataDir, "migration-journal.json"),
	})

	// Then
	if !errors.Is(err, ErrArtifactCollision) {
		t.Fatalf("migration error = %v, want ErrArtifactCollision", err)
	}
	assertArtifactContent(t, filepath.Join(legacyRoot, "SSNI-772 original-title.nfo"), "legacy nfo")
	assertArtifactContent(t, filepath.Join(targetRoot, "SSNI-772.nfo"), "new nfo")
}
