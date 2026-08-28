package emby_wrapper

import (
	"testing"

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

func TestUpsertAndGetEmbyDirSetting(t *testing.T) {
	if err := UpsertEmbyDirSetting(1, "/Movies", "三上悠亚,深田咏美"); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	item, err := GetEmbyDirSetting(1, "/Movies")
	if err != nil || item == nil {
		t.Fatalf("get: %v %v", item, err)
	}
	if item.Actors != "三上悠亚,深田咏美" {
		t.Errorf("unexpected actors %q", item.Actors)
	}
	// 不同 storage 隔离
	other, err := GetEmbyDirSetting(2, "/Movies")
	if err != nil || other != nil {
		t.Errorf("other storage must have no setting, got %v %v", other, err)
	}
}

func TestUpsertEmptyClearsEmbyDirSetting(t *testing.T) {
	if err := UpsertEmbyDirSetting(1, "/Movies", "三上悠亚"); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := UpsertEmbyDirSetting(1, "/Movies", "  "); err != nil {
		t.Fatalf("clear: %v", err)
	}
	item, err := GetEmbyDirSetting(1, "/Movies")
	if err != nil || item != nil {
		t.Errorf("setting must be deleted, got %v %v", item, err)
	}
}

func TestListEmbyDirSettings(t *testing.T) {
	if err := UpsertEmbyDirSetting(1, "/a", "A"); err != nil {
		t.Fatalf("upsert a: %v", err)
	}
	if err := UpsertEmbyDirSetting(1, "/b", "B"); err != nil {
		t.Fatalf("upsert b: %v", err)
	}
	m, err := ListEmbyDirSettings(1)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if m["/a"] != "A" || m["/b"] != "B" {
		t.Errorf("unexpected map: %v", m)
	}
}

var _ = model.EmbyDirSetting{} // 防止未使用导入
