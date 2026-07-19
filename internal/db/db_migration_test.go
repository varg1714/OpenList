package db_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/cmd/flags"
	appdb "github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestInitDoesNotMigrateLegacyMediaIdentityCollision(t *testing.T) {
	database, err := gorm.Open(sqlite.Open("file:startup-media-collision?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open startup database: %v", err)
	}
	if err := database.AutoMigrate(&model.Storage{}, &model.Film{}); err != nil {
		t.Fatalf("migrate startup fixture: %v", err)
	}
	if err := database.Create(&model.Storage{ID: 1, Driver: "Javdb", MountPath: "/javdb"}).Error; err != nil {
		t.Fatalf("seed storage: %v", err)
	}
	films := []model.Film{
		{Source: "javdb", Actor: "Actor A", Name: "ABP-123 first.mp4", Url: "https://javdb.test/v/one"},
		{Source: "JAVDB", Actor: "Actor B", Name: "abp-123 second.mp4", Url: "https://javdb.test/v/two"},
	}
	if err := database.Create(&films).Error; err != nil {
		t.Fatalf("seed films: %v", err)
	}

	err = appdb.Init(database)
	if err != nil {
		t.Fatalf("db.Init error = %v, want schema-only initialization", err)
	}

	var works int64
	if countErr := database.Model(&model.FilmWork{}).Count(&works).Error; countErr != nil {
		t.Fatalf("count works after refused startup: %v", countErr)
	}
	if works != 0 {
		t.Fatalf("normal startup migrated %d legacy works", works)
	}
}

func TestInitDoesNotMigrateLegacyArtifactCollision(t *testing.T) {
	database, err := gorm.Open(sqlite.Open("file:startup-media-artifact-collision?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open startup database: %v", err)
	}
	if err := database.AutoMigrate(&model.Storage{}, &model.Film{}); err != nil {
		t.Fatalf("migrate startup fixture: %v", err)
	}
	dataDir := t.TempDir()
	previousDataDir := flags.DataDir
	flags.DataDir = dataDir
	t.Cleanup(func() { flags.DataDir = previousDataDir })
	if err := database.Create(&model.Storage{ID: 1, Driver: "Javdb", MountPath: "/javdb"}).Error; err != nil {
		t.Fatalf("seed storage: %v", err)
	}
	film := model.Film{Source: "javdb", Actor: "Actor A", Name: "ABP-123 title.mp4", Url: "https://javdb.test/v/abp-123"}
	if err := database.Create(&film).Error; err != nil {
		t.Fatalf("seed film: %v", err)
	}
	legacyRoot := filepath.Join(dataDir, "emby", "javdb", "Actor A", "ABP-123 title")
	targetRoot := filepath.Join(dataDir, "emby", "javdb", "1", "Actor A", "ABP-123")
	if err := os.MkdirAll(legacyRoot, 0o755); err != nil {
		t.Fatalf("create legacy artifact root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacyRoot, "poster.jpg"), []byte("legacy"), 0o644); err != nil {
		t.Fatalf("write legacy artifact: %v", err)
	}
	if err := os.MkdirAll(targetRoot, 0o755); err != nil {
		t.Fatalf("create normalized artifact root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(targetRoot, "poster.jpg"), []byte("different"), 0o644); err != nil {
		t.Fatalf("write conflicting artifact: %v", err)
	}

	err = appdb.Init(database)
	if err != nil {
		t.Fatalf("db.Init error = %v, want schema-only initialization", err)
	}
	var works int64
	if err := database.Model(&model.FilmWork{}).Count(&works).Error; err != nil {
		t.Fatalf("count startup works: %v", err)
	}
	if works != 0 {
		t.Fatalf("normal startup migrated %d legacy works", works)
	}
	content, err := os.ReadFile(filepath.Join(targetRoot, "poster.jpg"))
	if err != nil || string(content) != "different" {
		t.Fatalf("normal startup changed target artifact: content=%q error=%v", content, err)
	}
}
