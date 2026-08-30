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
	if err := UpsertEmbyDirSetting(1, "/Movies", "三上悠亚,深田咏美", "", nil, nil); err != nil {
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
	if err := UpsertEmbyDirSetting(1, "/Movies", "三上悠亚", "", nil, nil); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := UpsertEmbyDirSetting(1, "/Movies", "  ", "", nil, nil); err != nil {
		t.Fatalf("clear: %v", err)
	}
	item, err := GetEmbyDirSetting(1, "/Movies")
	if err != nil || item != nil {
		t.Errorf("setting must be deleted, got %v %v", item, err)
	}
}

func TestListEmbyDirSettings(t *testing.T) {
	if err := UpsertEmbyDirSetting(1, "/a", "A", "", nil, nil); err != nil {
		t.Fatalf("upsert a: %v", err)
	}
	if err := UpsertEmbyDirSetting(1, "/b", "B", "", nil, nil); err != nil {
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
	if err := UpsertEmbyDirSetting(1, "/A", "X", "", boolPtr(true), nil); err != nil {
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
	if err := UpsertEmbyDirSetting(1, "/A", "", "", nil, nil); err != nil {
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
	if err := UpsertEmbyDirSetting(1, "/A", "", "", boolPtr(true), nil); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// 不带 use 字段（nil）写 actors，use 保持
	if err := UpsertEmbyDirSetting(1, "/A", "Y", "", nil, nil); err != nil {
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
	if err := UpsertEmbyDirSetting(1, "/A", "", "", boolPtr(true), nil); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	f := false
	if err := UpsertEmbyDirSetting(1, "/A", "", "", &f, nil); err != nil {
		t.Fatalf("disable: %v", err)
	}
	item, err := GetEmbyDirSetting(1, "/A")
	if err != nil || item != nil {
		t.Errorf("row must be deleted, got %v %v", item, err)
	}
}

func TestUpsertPlotAndAppendMergeSemantics(t *testing.T) {
	// plot 单独配置：行保留
	if err := UpsertEmbyDirSetting(1, "/A", "", "P", nil, nil); err != nil {
		t.Fatalf("upsert plot: %v", err)
	}
	item, err := GetEmbyDirSetting(1, "/A")
	if err != nil || item == nil {
		t.Fatalf("get: %v %v", item, err)
	}
	if item.Plot != "P" {
		t.Errorf("expected plot=P, got %q", item.Plot)
	}
	// 整表单写入 actors+plot（模拟 UI 预填提交）：两者都保留
	if err := UpsertEmbyDirSetting(1, "/A", "X", "P", nil, nil); err != nil {
		t.Fatalf("full form write: %v", err)
	}
	item, err = GetEmbyDirSetting(1, "/A")
	if err != nil || item == nil {
		t.Fatalf("get: %v %v", item, err)
	}
	if item.Plot != "P" || item.Actors != "X" {
		t.Errorf("expected plot=P actors=X, got %+v", item)
	}
	// 整表单清 plot（actors 保留）：plot 空、actors 保留
	if err := UpsertEmbyDirSetting(1, "/A", "X", "", nil, nil); err != nil {
		t.Fatalf("clear plot: %v", err)
	}
	item, err = GetEmbyDirSetting(1, "/A")
	if err != nil || item == nil {
		t.Fatalf("get: %v %v", item, err)
	}
	if item.Plot != "" || item.Actors != "X" {
		t.Errorf("expected plot cleared actors kept, got %+v", item)
	}
}

func TestUpsertAppendThreeState(t *testing.T) {
	// 开启 append（仅 append 配置）：行保留
	tf := true
	if err := UpsertEmbyDirSetting(1, "/A", "", "", nil, &tf); err != nil {
		t.Fatalf("enable append: %v", err)
	}
	item, err := GetEmbyDirSetting(1, "/A")
	if err != nil || item == nil {
		t.Fatalf("get: %v %v", item, err)
	}
	if item.AppendFileNameToPlot == nil || !*item.AppendFileNameToPlot {
		t.Errorf("expected append enabled, got %+v", item.AppendFileNameToPlot)
	}
	// 关闭 append（false = 清除，与 use_name_as_actor 一致）：无其他配置则删行
	ff := false
	if err := UpsertEmbyDirSetting(1, "/A", "", "", nil, &ff); err != nil {
		t.Fatalf("disable append: %v", err)
	}
	item, err = GetEmbyDirSetting(1, "/A")
	if err != nil || item != nil {
		t.Errorf("expected row deleted after disable, got %v %v", item, err)
	}
	// 重新开启 + 其他字段：关闭只清 append，其他保留
	if err := UpsertEmbyDirSetting(1, "/A", "X", "P", nil, &tf); err != nil {
		t.Fatalf("enable with actors+plot: %v", err)
	}
	if err := UpsertEmbyDirSetting(1, "/A", "X", "P", nil, &ff); err != nil {
		t.Fatalf("disable append only: %v", err)
	}
	item, err = GetEmbyDirSetting(1, "/A")
	if err != nil || item == nil {
		t.Fatalf("row must survive with actors+plot, got %v %v", item, err)
	}
	if item.AppendFileNameToPlot != nil {
		t.Errorf("append must be cleared, got %+v", item.AppendFileNameToPlot)
	}
	if item.Actors != "X" || item.Plot != "P" {
		t.Errorf("actors/plot must survive append disable, got %+v", item)
	}
}

var _ = model.EmbyDirSetting{} // 防止未使用导入
