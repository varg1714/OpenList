package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestParseStorageMappings(t *testing.T) {
	mapping, err := parseStorageMappings([]string{"javdb:Actor A=12", "fc2:个人收藏=7"})
	if err != nil {
		t.Fatalf("parse storage mappings: %v", err)
	}
	if mapping["javdb:Actor A"] != 12 || mapping["fc2:个人收藏"] != 7 {
		t.Fatalf("mapping = %#v", mapping)
	}
	if _, err := parseStorageMappings([]string{"invalid"}); err == nil {
		t.Fatal("invalid mapping unexpectedly parsed")
	}
	if _, err := parseStorageMappings([]string{"javdb:Actor A=0"}); err == nil {
		t.Fatal("zero storage ID unexpectedly parsed")
	}
}

func TestMigrateMediaCommandDryRunDoesNotWriteNormalizedDataOrJournal(t *testing.T) {
	dbPath, dataDir := seedCommandDatabase(t)
	journalPath := filepath.Join(dataDir, "journal.json")
	var stdout, stderr bytes.Buffer
	command := NewCommand(&stdout, &stderr)
	command.SetArgs([]string{"--db", dbPath, "--data-dir", dataDir, "--journal", journalPath, "--dry-run"})
	if err := command.Execute(); err != nil {
		t.Fatalf("dry-run command: %v, stderr=%s", err, stderr.String())
	}
	var report migrationReportOutput
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode dry-run report: %v; output=%s", err, stdout.String())
	}
	if report.WorksCreated != 0 || report.ArtifactsMoved != 0 || report.ArtifactsDeleted != 0 || report.ArtifactDirectoriesRemoved != 0 {
		t.Fatalf("dry-run report = %+v", report)
	}
	if _, err := os.Stat(journalPath); !os.IsNotExist(err) {
		t.Fatalf("dry-run journal stat error = %v", err)
	}
	assertCommandWorkCount(t, dbPath, 0)
	database, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("reopen dry-run database: %v", err)
	}
	if database.Migrator().HasTable(&model.FilmWork{}) {
		t.Fatal("dry run created normalized film_works table")
	}
}

func TestMigrateMediaCommandDryRunRejectsMissingDatabaseWithoutCreatingIt(t *testing.T) {
	directory := t.TempDir()
	dbPath := filepath.Join(directory, "missing.db")
	before, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read directory before dry-run: %v", err)
	}
	command := NewCommand(&bytes.Buffer{}, &bytes.Buffer{})
	command.SetArgs([]string{"--db", dbPath, "--data-dir", filepath.Join(directory, "data"), "--dry-run"})
	if err := command.Execute(); err == nil {
		t.Fatal("missing dry-run database unexpectedly accepted")
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatalf("missing dry-run database was created: %v", err)
	}
	after, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read directory after dry-run: %v", err)
	}
	if !reflect.DeepEqual(directoryEntryNames(before), directoryEntryNames(after)) {
		t.Fatalf("dry-run directory entries changed: before=%v after=%v", directoryEntryNames(before), directoryEntryNames(after))
	}
}

func TestMigrateMediaCommandDryRunLeavesDatabaseBytesAndSidecarsUnchanged(t *testing.T) {
	directory := t.TempDir()
	dbPath := filepath.Join(directory, "openlist.db")
	database, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open fixture database: %v", err)
	}
	if err := database.AutoMigrate(&model.Storage{}, &model.Film{}, &model.MagnetCache{}); err != nil {
		t.Fatalf("migrate fixture database: %v", err)
	}
	if err := database.Create(&model.Storage{ID: 1, Driver: "Javdb", MountPath: "/javdb"}).Error; err != nil {
		t.Fatalf("seed fixture storage: %v", err)
	}
	if err := database.Create(&model.Film{Source: "javdb", Actor: "Actor A", Name: "ABP-123 title.mp4", Url: "https://javdb.test/v/abp-123"}).Error; err != nil {
		t.Fatalf("seed fixture film: %v", err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatalf("get fixture database handle: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close fixture database: %v", err)
	}
	before := snapshotDirectoryFiles(t, directory)

	command := NewCommand(&bytes.Buffer{}, &bytes.Buffer{})
	command.SetArgs([]string{"--db", dbPath, "--data-dir", filepath.Join(directory, "data"), "--dry-run"})
	if err := command.Execute(); err != nil {
		t.Fatalf("read-only dry-run: %v", err)
	}
	after := snapshotDirectoryFiles(t, directory)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("dry-run changed database directory: before=%v after=%v", before, after)
	}
}

func TestMigrateMediaCommandApplyPrintsStructuredReport(t *testing.T) {
	dbPath, dataDir := seedCommandDatabase(t)
	legacyRoot := filepath.Join(dataDir, "emby", "javdb", "Actor A", "ABP-123 title")
	if err := os.MkdirAll(legacyRoot, 0o755); err != nil {
		t.Fatalf("create command artifact root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacyRoot, "poster.jpg"), []byte("poster"), 0o644); err != nil {
		t.Fatalf("write command artifact: %v", err)
	}
	var stdout, stderr bytes.Buffer
	command := NewCommand(&stdout, &stderr)
	command.SetArgs([]string{"--db", dbPath, "--data-dir", dataDir, "--apply"})
	if err := command.Execute(); err != nil {
		t.Fatalf("apply command: %v, stderr=%s", err, stderr.String())
	}
	var report migrationReportOutput
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode apply report: %v; output=%s", err, stdout.String())
	}
	if report.WorksCreated != 1 || report.FilesCreated != 1 {
		t.Fatalf("apply report = %+v", report)
	}
	if report.ArtifactMovesPlanned != 1 || report.ArtifactDirectoriesPlanned != 1 || report.ArtifactsMoved != 1 || report.ArtifactDirectoriesRemoved != 1 {
		t.Fatalf("artifact operation report = %+v", report)
	}
	assertCommandWorkCount(t, dbPath, 1)
}

func TestMigrateMediaCommandApplyKeepsExistingArtifactAndDeletesLaterConflict(t *testing.T) {
	dbPath, dataDir := seedCommandDatabase(t)
	legacyRoot := filepath.Join(dataDir, "emby", "javdb", "Actor A", "ABP-123 title")
	targetRoot := filepath.Join(dataDir, "emby", "javdb", "Actor A", "ABP-123")
	for path, content := range map[string]string{
		filepath.Join(legacyRoot, "poster.jpg"): "legacy",
		filepath.Join(targetRoot, "poster.jpg"): "different",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create artifact directory: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write artifact fixture: %v", err)
		}
	}
	var stdout, stderr bytes.Buffer
	command := NewCommand(&stdout, &stderr)
	command.SetArgs([]string{"--db", dbPath, "--data-dir", dataDir, "--apply"})
	if err := command.Execute(); err != nil {
		t.Fatalf("apply with differing artifacts: %v, stderr=%s", err, stderr.String())
	}
	var report migrationReportOutput
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode conflict cleanup report: %v; output=%s", err, stdout.String())
	}
	if report.ArtifactDeletesPlanned != 1 || report.ArtifactsDeleted != 1 || report.ArtifactsExisting != 1 {
		t.Fatalf("conflict cleanup report = %+v", report)
	}
	content, err := os.ReadFile(filepath.Join(targetRoot, "poster.jpg"))
	if err != nil || string(content) != "different" {
		t.Fatalf("retained artifact = %q, err=%v", content, err)
	}
	if _, err := os.Stat(legacyRoot); !os.IsNotExist(err) {
		t.Fatalf("later artifact root remains: %v", err)
	}
	assertCommandWorkCount(t, dbPath, 1)
}

func TestMigrateMediaCommandApplySupportsPrefixedSQLiteSchema(t *testing.T) {
	dbPath, dataDir := seedPrefixedCommandDatabase(t, "x_")
	var stdout, stderr bytes.Buffer
	command := NewCommand(&stdout, &stderr)
	command.SetArgs([]string{"--db", dbPath, "--data-dir", dataDir, "--table-prefix", "x_", "--apply"})
	if err := command.Execute(); err != nil {
		t.Fatalf("prefixed apply command: %v, stderr=%s", err, stderr.String())
	}
	var report migrationReportOutput
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode prefixed report: %v; output=%s", err, stdout.String())
	}
	if report.WorksCreated != 1 || report.FilesCreated != 1 {
		t.Fatalf("prefixed apply report = %+v", report)
	}
	database, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: "x_"}})
	if err != nil {
		t.Fatalf("reopen prefixed database: %v", err)
	}
	var count int64
	if err := database.Model(&model.FilmWork{}).Count(&count).Error; err != nil {
		t.Fatalf("count prefixed works: %v", err)
	}
	if count != 1 {
		t.Fatalf("prefixed work count = %d, want 1", count)
	}
}

func TestMigrateMediaCommandRejectsInvalidMapping(t *testing.T) {
	dbPath, dataDir := seedCommandDatabase(t)
	command := NewCommand(&bytes.Buffer{}, &bytes.Buffer{})
	command.SetArgs([]string{"--db", dbPath, "--data-dir", dataDir, "--apply", "--storage-map", "bad"})
	if err := command.Execute(); err == nil {
		t.Fatal("invalid mapping command unexpectedly succeeded")
	}
}

func seedCommandDatabase(t *testing.T) (string, string) {
	t.Helper()
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "openlist.db")
	database, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open command fixture: %v", err)
	}
	if err := database.AutoMigrate(&model.Storage{}, &model.Film{}, &model.MagnetCache{}); err != nil {
		t.Fatalf("migrate command fixture: %v", err)
	}
	if err := database.Create(&model.Storage{ID: 1, Driver: "Javdb", MountPath: "/javdb"}).Error; err != nil {
		t.Fatalf("seed command storage: %v", err)
	}
	if err := database.Create(&model.Film{Source: "javdb", Actor: "Actor A", Name: "ABP-123 title.mp4", Url: "https://javdb.test/v/abp-123"}).Error; err != nil {
		t.Fatalf("seed command film: %v", err)
	}
	return dbPath, dataDir
}

func seedPrefixedCommandDatabase(t *testing.T, prefix string) (string, string) {
	t.Helper()
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "openlist.db")
	database, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: prefix}})
	if err != nil {
		t.Fatalf("open prefixed command fixture: %v", err)
	}
	if err := database.AutoMigrate(&model.Storage{}, &model.Film{}, &model.MagnetCache{}); err != nil {
		t.Fatalf("migrate prefixed command fixture: %v", err)
	}
	if err := database.Create(&model.Storage{ID: 1, Driver: "Javdb", MountPath: "/javdb"}).Error; err != nil {
		t.Fatalf("seed prefixed command storage: %v", err)
	}
	if err := database.Create(&model.Film{Source: "javdb", Actor: "Actor A", Name: "ABP-123 title.mp4", Url: "https://javdb.test/v/abp-123"}).Error; err != nil {
		t.Fatalf("seed prefixed command film: %v", err)
	}
	return dbPath, dataDir
}

func assertCommandWorkCount(t *testing.T, dbPath string, want int64) {
	t.Helper()
	database, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("reopen command database: %v", err)
	}
	if !database.Migrator().HasTable(&model.FilmWork{}) {
		if want == 0 {
			return
		}
		t.Fatalf("film_works table is absent, want %d rows", want)
	}
	var count int64
	if err := database.Model(&model.FilmWork{}).Count(&count).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) && want == 0 {
			return
		}
		t.Fatalf("count command works: %v", err)
	}
	if count != want {
		t.Fatalf("command work count = %d, want %d", count, want)
	}
}

func directoryEntryNames(entries []os.DirEntry) []string {
	names := make([]string, len(entries))
	for index, entry := range entries {
		names[index] = entry.Name()
	}
	return names
}

func snapshotDirectoryFiles(t *testing.T, directory string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read snapshot directory: %v", err)
	}
	snapshot := make(map[string]string, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			snapshot[entry.Name()+"/"] = ""
			continue
		}
		content, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			t.Fatalf("read snapshot file %s: %v", entry.Name(), err)
		}
		snapshot[entry.Name()] = string(content)
	}
	return snapshot
}
