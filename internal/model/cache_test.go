package model_test

import (
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func init() {
	dB, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		panic("failed to connect database")
	}
	conf.Conf = conf.DefaultConfig("data")
	db.Init(dB)
}

func TestCacheListModel(t *testing.T) {
	item := model.CacheList{
		StorageID: 1,
		DirPath:   "/dir",
		Data:      `[{"name":"a.txt"}]`,
		UpdatedAt: time.Now(),
	}
	if err := db.GetDb().Create(&item).Error; err != nil {
		t.Fatalf("failed to create: %+v", err)
	}
	var got model.CacheList
	if err := db.GetDb().Where("storage_id = ? AND dir_path = ?", 1, "/dir").First(&got).Error; err != nil {
		t.Fatalf("failed to find: %+v", err)
	}
	if got.Data != item.Data {
		t.Errorf("expected data %q, got %q", item.Data, got.Data)
	}
}
