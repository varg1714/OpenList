package bilibili

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestMain 为快照相关测试提供 sqlite db（pornhub 驱动同款设施）。
// 不影响既有纯 mock 测试——db 包全局在此初始化后空闲可用。
func TestMain(m *testing.M) {
	dataDir, err := os.MkdirTemp("", "bilibili-test-")
	if err != nil {
		panic(err)
	}
	conf.Conf = conf.DefaultConfig(dataDir)
	database, err := gorm.Open(sqlite.Open(filepath.Join(dataDir, "bilibili-test.db")), &gorm.Config{})
	if err != nil {
		panic(err)
	}
	if err := db.Init(database); err != nil {
		panic(err)
	}
	code := m.Run()
	if sqlDB, sqlErr := database.DB(); sqlErr == nil {
		_ = sqlDB.Close()
	}
	_ = os.RemoveAll(dataDir)
	os.Exit(code)
}
