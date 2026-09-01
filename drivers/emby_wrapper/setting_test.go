package emby_wrapper

import (
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

// wrapperTestSeq 为包内测试分配互不冲突的 storageID（测试间共享内存库，硬编码 ID 会互相干扰）。
var wrapperTestSeq uint

func newTestWrapper() *EmbyWrapper {
	wrapperTestSeq++
	return &EmbyWrapper{Storage: model.Storage{ID: uint(wrapperTestSeq)}}
}

func TestResolveSettingInheritsAncestor(t *testing.T) {
	d := newTestWrapper()
	if err := UpsertEmbyDirSetting(d.ID, "/Movies", "演员A", "", "", nil, nil, nil, nil); err != nil {
		t.Fatalf("set actors: %+v", err)
	}
	// 子目录没有自身设置，应继承 /Movies 的设置
	item, err := d.resolveSetting("/Movies/Sub")
	if err != nil || item == nil {
		t.Fatalf("expected inherited setting, got %v %v", item, err)
	}
	if item.Actors != "演员A" {
		t.Errorf("expected inherited actors, got %q", item.Actors)
	}
	// 自身设置覆盖祖先
	if err := UpsertEmbyDirSetting(d.ID, "/Movies/Sub", "演员B", "", "", nil, nil, nil, nil); err != nil {
		t.Fatalf("set sub actors: %+v", err)
	}
	item, err = d.resolveSetting("/Movies/Sub")
	if err != nil || item == nil {
		t.Fatalf("expected own setting, got %v %v", item, err)
	}
	if item.Actors != "演员B" {
		t.Errorf("expected own actors, got %q", item.Actors)
	}
	// 无任何设置
	item, err = d.resolveSetting("/Other")
	if err != nil || item != nil {
		t.Errorf("expected no setting for /Other, got %v %v", item, err)
	}
}

func TestResolveSettingUseNameAsActor(t *testing.T) {
	d := newTestWrapper()
	// A 开启 use_name_as_actor
	if err := UpsertEmbyDirSetting(d.ID, "/A", "", "", "", boolPtr(true), nil, nil, nil); err != nil {
		t.Fatalf("enable on A: %v", err)
	}
	// 直接子文件夹：actor = 自身名
	item, err := d.resolveSetting("/A/A1")
	if err != nil || item == nil {
		t.Fatalf("expected A1 name-as-actor, got %v %v", item, err)
	}
	if item.Actors != "A1" {
		t.Errorf("expected actors=A1, got %q", item.Actors)
	}
	// 孙级继承：actor 同为 A1
	item, err = d.resolveSetting("/A/A1/A11")
	if err != nil || item == nil {
		t.Fatalf("expected A11 inherited actor, got %v %v", item, err)
	}
	if item.Actors != "A1" {
		t.Errorf("expected inherited actors=A1, got %q", item.Actors)
	}
	// 更深的层级同样继承 A1
	item, err = d.resolveSetting("/A/A1/A11/A111")
	if err != nil || item == nil {
		t.Fatalf("expected deep inherited actor, got %v %v", item, err)
	}
	if item.Actors != "A1" {
		t.Errorf("expected deep actors=A1, got %q", item.Actors)
	}
	// 其他直接子文件夹：自身名
	item, err = d.resolveSetting("/A/A2")
	if err != nil || item == nil {
		t.Fatalf("expected A2 name-as-actor, got %v %v", item, err)
	}
	if item.Actors != "A2" {
		t.Errorf("expected actors=A2, got %q", item.Actors)
	}
	// 开启者自身不获得 actor
	item, err = d.resolveSetting("/A")
	if err != nil || item != nil {
		t.Errorf("enabler itself must have no actor, got %v %v", item, err)
	}
}

func TestResolveSettingManualActorsWin(t *testing.T) {
	d := newTestWrapper()
	if err := UpsertEmbyDirSetting(d.ID, "/A", "", "", "", boolPtr(true), nil, nil, nil); err != nil {
		t.Fatalf("enable on A: %v", err)
	}
	if err := UpsertEmbyDirSetting(d.ID, "/A/A1", "手动演员", "", "", nil, nil, nil, nil); err != nil {
		t.Fatalf("manual on A1: %v", err)
	}
	item, err := d.resolveSetting("/A/A1")
	if err != nil || item == nil {
		t.Fatalf("expected manual actors, got %v %v", item, err)
	}
	if item.Actors != "手动演员" {
		t.Errorf("manual actors must win, got %q", item.Actors)
	}
	// 手动设置继承给子树
	item, err = d.resolveSetting("/A/A1/A11")
	if err != nil || item == nil {
		t.Fatalf("expected inherited manual actors, got %v %v", item, err)
	}
	if item.Actors != "手动演员" {
		t.Errorf("manual actors must inherit, got %q", item.Actors)
	}
	// 未被手动覆盖的兄弟分支仍用名称
	item, err = d.resolveSetting("/A/A2")
	if err != nil || item == nil {
		t.Fatalf("expected A2 name-as-actor, got %v %v", item, err)
	}
	if item.Actors != "A2" {
		t.Errorf("expected actors=A2, got %q", item.Actors)
	}
}

func TestResolveSettingNearestEnablerWins(t *testing.T) {
	d := newTestWrapper()
	if err := UpsertEmbyDirSetting(d.ID, "/A", "", "", "", boolPtr(true), nil, nil, nil); err != nil {
		t.Fatalf("enable on A: %v", err)
	}
	if err := UpsertEmbyDirSetting(d.ID, "/A/A1", "", "", "", boolPtr(true), nil, nil, nil); err != nil {
		t.Fatalf("enable on A1: %v", err)
	}
	// A 与 A1 都开启：A11 使用最近的开启者 A1 -> actor = A11
	item, err := d.resolveSetting("/A/A1/A11")
	if err != nil || item == nil {
		t.Fatalf("expected nearest enabler actor, got %v %v", item, err)
	}
	if item.Actors != "A11" {
		t.Errorf("expected actors=A11 (nearest enabler), got %q", item.Actors)
	}
	// A1 自身：A1 开启但自身不获得 -> 继续向上命中 A -> actor = A1
	item, err = d.resolveSetting("/A/A1")
	if err != nil || item == nil {
		t.Fatalf("expected A1 from outer enabler, got %v %v", item, err)
	}
	if item.Actors != "A1" {
		t.Errorf("expected actors=A1 (outer enabler), got %q", item.Actors)
	}
}

// TestResolveSettingDistancePriorityMixed 钉住距离优先语义（用户确认）：
// 祖先手动 actors 与近处 use_name_as_actor 并存时，近处设置优先。
func TestResolveSettingDistancePriorityMixed(t *testing.T) {
	d := newTestWrapper()
	// 祖先手动 actors + 近处 use_name_as_actor
	if err := UpsertEmbyDirSetting(d.ID, "/A", "X", "", "", nil, nil, nil, nil); err != nil {
		t.Fatalf("manual on A: %v", err)
	}
	if err := UpsertEmbyDirSetting(d.ID, "/A/A1", "", "", "", boolPtr(true), nil, nil, nil); err != nil {
		t.Fatalf("enable on A1: %v", err)
	}
	// 近处 use 生效：A11 用 A1 的开关 -> actor = A11
	item, err := d.resolveSetting("/A/A1/A11")
	if err != nil || item == nil {
		t.Fatalf("expected nearest use actor, got %v %v", item, err)
	}
	if item.Actors != "A11" {
		t.Errorf("distance priority: expected actors=A11, got %q", item.Actors)
	}
	// A1 自身不因自己的开关获得 actor -> 回退祖先手动 X
	item, err = d.resolveSetting("/A/A1")
	if err != nil || item == nil {
		t.Fatalf("expected ancestor manual actors, got %v %v", item, err)
	}
	if item.Actors != "X" {
		t.Errorf("enabler itself must fall back to ancestor manual, got %q", item.Actors)
	}
	// 未被 use 覆盖的兄弟分支继承祖先手动
	item, err = d.resolveSetting("/A/A2")
	if err != nil || item == nil {
		t.Fatalf("expected ancestor manual actors, got %v %v", item, err)
	}
	if item.Actors != "X" {
		t.Errorf("sibling must inherit ancestor manual, got %q", item.Actors)
	}
}

// TestResolveSettingPlotDimension：plot 独立继承（分维度）。
func TestResolveSettingPlotDimension(t *testing.T) {
	d := newTestWrapper()
	// /A 设置 plot=P；/A/A1 只设置 actors=Y
	if err := UpsertEmbyDirSetting(d.ID, "/A", "", "P", "", nil, nil, nil, nil); err != nil {
		t.Fatalf("set plot on A: %v", err)
	}
	if err := UpsertEmbyDirSetting(d.ID, "/A/A1", "Y", "", "", nil, nil, nil, nil); err != nil {
		t.Fatalf("set actors on A1: %v", err)
	}
	// A1 影片：actor=Y（近处），plot=P（独立继承）
	item, err := d.resolveSetting("/A/A1")
	if err != nil || item == nil {
		t.Fatalf("expected setting, got %v %v", item, err)
	}
	if item.Actors != "Y" || item.Plot != "P" {
		t.Errorf("expected actors=Y plot=P, got %+v", item)
	}
	// A2（无自身设置）：actor 空、plot=P
	item, err = d.resolveSetting("/A/A2")
	if err != nil || item == nil {
		t.Fatalf("expected setting, got %v %v", item, err)
	}
	if item.Actors != "" || item.Plot != "P" {
		t.Errorf("expected actors empty plot=P, got %+v", item)
	}
	// 无任何配置的目录：nil
	item, err = d.resolveSetting("/Other")
	if err != nil || item != nil {
		t.Errorf("expected nil for /Other, got %v %v", item, err)
	}
}

// TestResolveSettingAppendDimension：append 独立继承；显式 false 阻断上层，全清删行后恢复继承。
func TestResolveSettingAppendDimension(t *testing.T) {
	d := newTestWrapper()
	tf := true
	// /A 开 append + plot=P
	if err := UpsertEmbyDirSetting(d.ID, "/A", "", "P", "", nil, &tf, nil, nil); err != nil {
		t.Fatalf("config A: %v", err)
	}
	item, err := d.resolveSetting("/A/A1")
	if err != nil || item == nil {
		t.Fatalf("expected inherited append, got %v %v", item, err)
	}
	if item.AppendFileNameToPlot == nil || !*item.AppendFileNameToPlot || item.Plot != "P" {
		t.Errorf("expected append=true plot=P inherited, got %+v", item)
	}
	// A1 设 actors=Y + 显式关闭 append：行保留，阻断继承
	ff := false
	if err := UpsertEmbyDirSetting(d.ID, "/A/A1", "Y", "", "", nil, &ff, nil, nil); err != nil {
		t.Fatalf("disable append on A1: %v", err)
	}
	item, err = d.resolveSetting("/A/A1")
	if err != nil || item == nil {
		t.Fatalf("expected setting, got %v %v", item, err)
	}
	if item.Actors != "Y" {
		t.Errorf("expected actors=Y, got %q", item.Actors)
	}
	if item.AppendFileNameToPlot == nil || *item.AppendFileNameToPlot {
		t.Errorf("explicit false must block inheritance, got %+v", item.AppendFileNameToPlot)
	}
	// plot 仍独立继承 P
	if item.Plot != "P" {
		t.Errorf("plot must still inherit P, got %q", item.Plot)
	}
	// 全清 A1 行（删行）：恢复继承 A 的 append=true
	if err := UpsertEmbyDirSetting(d.ID, "/A/A1", "", "", "", nil, nil, nil, nil); err != nil {
		t.Fatalf("clear A1: %v", err)
	}
	item, err = d.resolveSetting("/A/A1")
	if err != nil || item == nil {
		t.Fatalf("expected setting, got %v %v", item, err)
	}
	if item.AppendFileNameToPlot == nil || !*item.AppendFileNameToPlot {
		t.Errorf("append must re-inherit after row deletion, got %+v", item.AppendFileNameToPlot)
	}
}

// TestResolveSettingPlotWithNameAsActor：use 合成 actor 与 plot 独立并存。
func TestResolveSettingPlotWithNameAsActor(t *testing.T) {
	d := newTestWrapper()
	if err := UpsertEmbyDirSetting(d.ID, "/A", "", "P", "", boolPtr(true), nil, nil, nil); err != nil {
		t.Fatalf("config A: %v", err)
	}
	item, err := d.resolveSetting("/A/A1")
	if err != nil || item == nil {
		t.Fatalf("expected setting, got %v %v", item, err)
	}
	if item.Actors != "A1" || item.Plot != "P" {
		t.Errorf("expected actors=A1 plot=P, got %+v", item)
	}
}
