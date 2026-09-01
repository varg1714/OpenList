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
	if err := UpsertEmbyDirSetting(1, "/Movies", "演员A,演员B", "", "", nil, nil, nil, nil); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	item, err := GetEmbyDirSetting(1, "/Movies")
	if err != nil || item == nil {
		t.Fatalf("get: %v %v", item, err)
	}
	if item.Actors != "演员A,演员B" {
		t.Errorf("unexpected actors %q", item.Actors)
	}
	// 不同 storage 隔离
	other, err := GetEmbyDirSetting(2, "/Movies")
	if err != nil || other != nil {
		t.Errorf("other storage must have no setting, got %v %v", other, err)
	}
}

func TestUpsertEmptyClearsEmbyDirSetting(t *testing.T) {
	if err := UpsertEmbyDirSetting(1, "/Movies", "演员A", "", "", nil, nil, nil, nil); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := UpsertEmbyDirSetting(1, "/Movies", "  ", "", "", nil, nil, nil, nil); err != nil {
		t.Fatalf("clear: %v", err)
	}
	item, err := GetEmbyDirSetting(1, "/Movies")
	if err != nil || item != nil {
		t.Errorf("setting must be deleted, got %v %v", item, err)
	}
}

func TestListEmbyDirSettings(t *testing.T) {
	if err := UpsertEmbyDirSetting(1, "/a", "A", "", "", nil, nil, nil, nil); err != nil {
		t.Fatalf("upsert a: %v", err)
	}
	if err := UpsertEmbyDirSetting(1, "/b", "B", "", "", nil, nil, nil, nil); err != nil {
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
	if err := UpsertEmbyDirSetting(1, "/A", "X", "", "", boolPtr(true), nil, nil, nil); err != nil {
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
	if err := UpsertEmbyDirSetting(1, "/A", "", "", "", nil, nil, nil, nil); err != nil {
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
	if err := UpsertEmbyDirSetting(1, "/A", "", "", "", boolPtr(true), nil, nil, nil); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// 不带 use 字段（nil）写 actors，use 保持
	if err := UpsertEmbyDirSetting(1, "/A", "Y", "", "", nil, nil, nil, nil); err != nil {
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
	if err := UpsertEmbyDirSetting(1, "/A", "", "", "", boolPtr(true), nil, nil, nil); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	f := false
	if err := UpsertEmbyDirSetting(1, "/A", "", "", "", &f, nil, nil, nil); err != nil {
		t.Fatalf("disable: %v", err)
	}
	item, err := GetEmbyDirSetting(1, "/A")
	if err != nil || item != nil {
		t.Errorf("row must be deleted, got %v %v", item, err)
	}
}

func TestUpsertPlotAndAppendMergeSemantics(t *testing.T) {
	// plot 单独配置：行保留
	if err := UpsertEmbyDirSetting(1, "/A", "", "P", "", nil, nil, nil, nil); err != nil {
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
	if err := UpsertEmbyDirSetting(1, "/A", "X", "P", "", nil, nil, nil, nil); err != nil {
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
	if err := UpsertEmbyDirSetting(1, "/A", "X", "", "", nil, nil, nil, nil); err != nil {
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
	if err := UpsertEmbyDirSetting(1, "/A", "", "", "", nil, &tf, nil, nil); err != nil {
		t.Fatalf("enable append: %v", err)
	}
	item, err := GetEmbyDirSetting(1, "/A")
	if err != nil || item == nil {
		t.Fatalf("get: %v %v", item, err)
	}
	if item.AppendFileNameToPlot == nil || !*item.AppendFileNameToPlot {
		t.Errorf("expected append enabled, got %+v", item.AppendFileNameToPlot)
	}
	// 显式关闭 append（仅 append=false，无其他字段）：删行，恢复上层继承
	ff := false
	if err := UpsertEmbyDirSetting(1, "/A", "", "", "", nil, &ff, nil, nil); err != nil {
		t.Fatalf("disable append: %v", err)
	}
	item, err = GetEmbyDirSetting(1, "/A")
	if err != nil || item != nil {
		t.Errorf("expected row deleted when only append=false, got %v %v", item, err)
	}
	// 显式关闭 append + actors/plot 非空：行保留且 append=false（阻断上层继承）
	if err := UpsertEmbyDirSetting(1, "/A", "X", "P", "", nil, &tf, nil, nil); err != nil {
		t.Fatalf("enable with actors+plot: %v", err)
	}
	if err := UpsertEmbyDirSetting(1, "/A", "X", "P", "", nil, &ff, nil, nil); err != nil {
		t.Fatalf("disable append only: %v", err)
	}
	item, err = GetEmbyDirSetting(1, "/A")
	if err != nil || item == nil {
		t.Fatalf("row must survive with actors+plot, got %v %v", item, err)
	}
	if item.AppendFileNameToPlot == nil || *item.AppendFileNameToPlot {
		t.Errorf("append must be explicitly false, got %+v", item.AppendFileNameToPlot)
	}
	if item.Actors != "X" || item.Plot != "P" {
		t.Errorf("actors/plot must survive append disable, got %+v", item)
	}
	// 全部字段清空：删行
	if err := UpsertEmbyDirSetting(1, "/A", "", "", "", nil, nil, nil, nil); err != nil {
		t.Fatalf("clear all: %v", err)
	}
	item, err = GetEmbyDirSetting(1, "/A")
	if err != nil || item != nil {
		t.Errorf("expected row deleted, got %v %v", item, err)
	}
}

// TestUpsertTVShowMergeSemantics：tv_show/tv_show_name 独立合并；显式关闭保行；全清删行。
func TestUpsertTVShowMergeSemantics(t *testing.T) {
	tv, ff := true, false
	// 标记电视剧 + 剧名 + actors 并存
	if err := UpsertEmbyDirSetting(99, "/TV", "演员X", "", "剧名X", nil, nil, &tv, nil); err != nil {
		t.Fatalf("mark tv show: %v", err)
	}
	item, err := GetEmbyDirSetting(99, "/TV")
	if err != nil || item == nil {
		t.Fatalf("get: %v %v", item, err)
	}
	if !item.TvShow || item.TvShowName != "剧名X" || item.Actors != "演员X" {
		t.Errorf("expected tv_show=true name=剧名X actors=演员X, got %+v", item)
	}
	// 写 actors 不影响 tv 字段（重新提供剧名以保留之）
	if err := UpsertEmbyDirSetting(99, "/TV", "演员Y", "", "剧名X", nil, nil, nil, nil); err != nil {
		t.Fatalf("set actors: %v", err)
	}
	item, err = GetEmbyDirSetting(99, "/TV")
	if err != nil || item == nil {
		t.Fatalf("get: %v %v", item, err)
	}
	if !item.TvShow || item.TvShowName != "剧名X" || item.Actors != "演员Y" {
		t.Errorf("tv fields must survive actors write, got %+v", item)
	}
	// 显式关闭 tv_show（actors/剧名仍在）：行保留，tv_show=false（阻断语义与 append 一致）
	if err := UpsertEmbyDirSetting(99, "/TV", "演员Y", "", "剧名X", nil, nil, &ff, nil); err != nil {
		t.Fatalf("disable tv show: %v", err)
	}
	item, err = GetEmbyDirSetting(99, "/TV")
	if err != nil || item == nil {
		t.Fatalf("row must survive explicit disable, got %v %v", item, err)
	}
	if item.TvShow {
		t.Errorf("expected tv_show disabled, got %+v", item)
	}
	// 全部清空：删行
	if err := UpsertEmbyDirSetting(99, "/TV", "", "", "", nil, nil, nil, nil); err != nil {
		t.Fatalf("clear all: %v", err)
	}
	item, err = GetEmbyDirSetting(99, "/TV")
	if err != nil || item != nil {
		t.Errorf("expected row deleted, got %v %v", item, err)
	}
}

// TestUpsertTVShowNameClear：清剧名不影响 tv_show 标记。
func TestUpsertTVShowNameClear(t *testing.T) {
	tv := true
	if err := UpsertEmbyDirSetting(100, "/TV2", "", "", "剧名", nil, nil, &tv, nil); err != nil {
		t.Fatalf("mark: %v", err)
	}
	if err := UpsertEmbyDirSetting(100, "/TV2", "", "", "", nil, nil, nil, nil); err != nil {
		t.Fatalf("clear name: %v", err)
	}
	item, err := GetEmbyDirSetting(100, "/TV2")
	if err != nil || item == nil {
		t.Fatalf("get: %v %v", item, err)
	}
	if item.TvShowName != "" || !item.TvShow {
		t.Errorf("name cleared but tv_show kept, got %+v", item)
	}
}

// TestUpsertTVShowSubfoldersMergeSemantics：tv_show_subfolders 独立合并；仅开选项保行；全清删行。
func TestUpsertTVShowSubfoldersMergeSemantics(t *testing.T) {
	tf, ff := true, false
	// 仅开选项：行保留
	if err := UpsertEmbyDirSetting(101, "/TS", "", "", "", nil, nil, nil, &tf); err != nil {
		t.Fatalf("enable subfolders: %v", err)
	}
	item, err := GetEmbyDirSetting(101, "/TS")
	if err != nil || item == nil {
		t.Fatalf("row must survive with only tv_show_subfolders, got %v %v", item, err)
	}
	if !item.TvShowSubfolders {
		t.Errorf("expected tv_show_subfolders=true, got %+v", item)
	}
	// 写 actors 不影响选项
	if err := UpsertEmbyDirSetting(101, "/TS", "演员X", "", "", nil, nil, nil, nil); err != nil {
		t.Fatalf("set actors: %v", err)
	}
	item, err = GetEmbyDirSetting(101, "/TS")
	if err != nil || item == nil {
		t.Fatalf("get: %v %v", item, err)
	}
	if !item.TvShowSubfolders || item.Actors != "演员X" {
		t.Errorf("option must survive actors write, got %+v", item)
	}
	// 显式关闭（无其他字段）：删行，恢复上层语义
	if err := UpsertEmbyDirSetting(101, "/TS", "", "", "", nil, nil, nil, &ff); err != nil {
		t.Fatalf("disable: %v", err)
	}
	item, err = GetEmbyDirSetting(101, "/TS")
	if err != nil || item != nil {
		t.Errorf("expected row deleted, got %v %v", item, err)
	}
}

var _ = model.EmbyDirSetting{} // 防止未使用导入
