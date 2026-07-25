package db

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestMain(m *testing.M) {
	dataDir, err := os.MkdirTemp("", "db-test-")
	if err != nil {
		panic(err)
	}
	conf.Conf = conf.DefaultConfig(dataDir)
	database, err := gorm.Open(sqlite.Open(filepath.Join(dataDir, "db-test.db")), &gorm.Config{})
	if err != nil {
		panic(err)
	}
	if err := Init(database); err != nil {
		panic(err)
	}

	code := m.Run()
	if sqlDB, sqlErr := database.DB(); sqlErr == nil {
		_ = sqlDB.Close()
	}
	_ = os.RemoveAll(dataDir)
	os.Exit(code)
}
