package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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
	if report.WorksCreated != 0 || report.ArtifactsCopied != 0 {
		t.Fatalf("dry-run report = %+v", report)
	}
	if _, err := os.Stat(journalPath); !os.IsNotExist(err) {
		t.Fatalf("dry-run journal stat error = %v", err)
	}
	assertCommandWorkCount(t, dbPath, 0)
}

func TestMigrateMediaCommandApplyPrintsStructuredReport(t *testing.T) {
	dbPath, dataDir := seedCommandDatabase(t)
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
	if err := database.AutoMigrate(&model.Storage{}, &model.Film{}); err != nil {
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
	if err := database.AutoMigrate(&model.Storage{}, &model.Film{}); err != nil {
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
