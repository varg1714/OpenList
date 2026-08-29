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

func boolPtr(b bool) *bool { return &b }

func TestUpsertAndGetEmbyDirSetting(t *testing.T) {
	if err := UpsertEmbyDirSetting(1, "/Movies", "三上悠亚,深田咏美", nil); err != nil {
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
	if err := UpsertEmbyDirSetting(1, "/Movies", "三上悠亚", nil); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := UpsertEmbyDirSetting(1, "/Movies", "  ", nil); err != nil {
		t.Fatalf("clear: %v", err)
	}
	item, err := GetEmbyDirSetting(1, "/Movies")
	if err != nil || item != nil {
		t.Errorf("setting must be deleted, got %v %v", item, err)
	}
}

func TestListEmbyDirSettings(t *testing.T) {
	if err := UpsertEmbyDirSetting(1, "/a", "A", nil); err != nil {
		t.Fatalf("upsert a: %v", err)
	}
	if err := UpsertEmbyDirSetting(1, "/b", "B", nil); err != nil {
		t.Fatalf("upsert b: %v", err)
	}
	m, err := ListEmbyDirSettings(1)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if m["/a"].Actors != "A" || m["/b"].Actors != "B" {
		t.Errorf("unexpected map: %v", m)
	}
}

func TestUpsertUseNameAsActorKeepsActors(t *testing.T) {
	if err := UpsertEmbyDirSetting(1, "/A", "X", boolPtr(true)); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	item, err := GetEmbyDirSetting(1, "/A")
	if err != nil || item == nil {
		t.Fatalf("get: %v %v", item, err)
	}
	if !item.UseNameAsActor || item.Actors != "X" {
		t.Errorf("expected use=true actors=X, got %+v", item)
	}
	// actors 清空不影响 use
	if err := UpsertEmbyDirSetting(1, "/A", "", nil); err != nil {
		t.Fatalf("clear actors: %v", err)
	}
	item, err = GetEmbyDirSetting(1, "/A")
	if err != nil || item == nil {
		t.Fatalf("get after clear actors: %v %v", item, err)
	}
	if !item.UseNameAsActor || item.Actors != "" {
		t.Errorf("use must survive actors clear, got %+v", item)
	}
}

func TestUpsertActorsKeepsUseNameAsActor(t *testing.T) {
	if err := UpsertEmbyDirSetting(1, "/A", "", boolPtr(true)); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// 不带 use 字段（nil）写 actors，use 保持
	if err := UpsertEmbyDirSetting(1, "/A", "Y", nil); err != nil {
		t.Fatalf("set actors: %v", err)
	}
	item, err := GetEmbyDirSetting(1, "/A")
	if err != nil || item == nil {
		t.Fatalf("get: %v %v", item, err)
	}
	if !item.UseNameAsActor || item.Actors != "Y" {
		t.Errorf("use must survive actors write, got %+v", item)
	}
}

func TestUpsertDisableUseNameAsActorDeletesRow(t *testing.T) {
	if err := UpsertEmbyDirSetting(1, "/A", "", boolPtr(true)); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	f := false
	if err := UpsertEmbyDirSetting(1, "/A", "", &f); err != nil {
		t.Fatalf("disable: %v", err)
	}
	item, err := GetEmbyDirSetting(1, "/A")
	if err != nil || item != nil {
		t.Errorf("row must be deleted, got %v %v", item, err)
	}
}

var _ = model.EmbyDirSetting{} // 防止未使用导入
