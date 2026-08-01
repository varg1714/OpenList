package cache

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

func TestGetCacheListNotFound(t *testing.T) {
	item, err := GetCacheList(99, "/nope")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if item != nil {
		t.Errorf("expected nil, got %+v", item)
	}
}

func TestUpsertCreateThenUpdate(t *testing.T) {
	if err := UpsertCacheList(1, "/dir", []model.CachedObj{{Name: "a"}}); err != nil {
		t.Fatalf("create: %v", err)
	}
	item, err := GetCacheList(1, "/dir")
	if err != nil || item == nil {
		t.Fatalf("get after create: %v %+v", err, item)
	}
	if len(item.Data) != 1 || item.Data[0].Name != "a" {
		t.Errorf("expected a, got %+v", item.Data)
	}
	first := item.UpdatedAt

	time.Sleep(2 * time.Millisecond)
	if err := UpsertCacheList(1, "/dir", []model.CachedObj{{Name: "b"}}); err != nil {
		t.Fatalf("update: %v", err)
	}
	item, err = GetCacheList(1, "/dir")
	if err != nil || item == nil {
		t.Fatalf("get after update: %v %+v", err, item)
	}
	if len(item.Data) != 1 || item.Data[0].Name != "b" {
		t.Errorf("expected b, got %+v", item.Data)
	}
	if !item.UpdatedAt.After(first) {
		t.Errorf("expected UpdatedAt refreshed")
	}
	var count int64
	if err := db.GetDb().Model(&model.CacheList{}).Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 row after upsert, got %d", count)
	}
}

func TestStorageIsolation(t *testing.T) {
	_ = UpsertCacheList(1, "/dir", []model.CachedObj{{Name: "1"}})
	_ = UpsertCacheList(2, "/dir", []model.CachedObj{{Name: "2"}})
	item, err := GetCacheList(1, "/dir")
	if err != nil || item == nil {
		t.Fatalf("storage1: %v %+v", err, item)
	}
	if len(item.Data) != 1 || item.Data[0].Name != "1" {
		t.Errorf("storage1 polluted: %+v", item.Data)
	}
}

func TestDeleteCacheList(t *testing.T) {
	_ = UpsertCacheList(3, "/dir", []model.CachedObj{{Name: "a"}})
	if err := DeleteCacheList(3, "/dir"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	item, err := GetCacheList(3, "/dir")
	if err != nil || item != nil {
		t.Errorf("expected nil after delete, got %v %v", item, err)
	}
}

func TestListCacheLists(t *testing.T) {
	_ = UpsertCacheList(4, "/a", []model.CachedObj{{Name: "1"}})
	_ = UpsertCacheList(4, "/b", []model.CachedObj{{Name: "2"}})
	_ = UpsertCacheList(5, "/c", []model.CachedObj{{Name: "3"}})
	rows, err := ListCacheLists(4)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("expected 2 rows for storage 4, got %d", len(rows))
	}
}
