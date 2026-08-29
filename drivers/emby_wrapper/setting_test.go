package emby_wrapper

import (
	"testing"
)

func newTestWrapper() *EmbyWrapper {
	return &EmbyWrapper{}
}

func TestResolveSettingInheritsAncestor(t *testing.T) {
	d := newTestWrapper()
	d.ID = 1
	if err := UpsertEmbyDirSetting(d.ID, "/Movies", "三上悠亚", nil); err != nil {
		t.Fatalf("set actors: %+v", err)
	}
	// 子目录没有自身设置，应继承 /Movies 的设置
	item, err := d.resolveSetting("/Movies/Sub")
	if err != nil || item == nil {
		t.Fatalf("expected inherited setting, got %v %v", item, err)
	}
	if item.Actors != "三上悠亚" {
		t.Errorf("expected inherited actors, got %q", item.Actors)
	}
	// 自身设置覆盖祖先
	if err := UpsertEmbyDirSetting(d.ID, "/Movies/Sub", "深田咏美", nil); err != nil {
		t.Fatalf("set sub actors: %+v", err)
	}
	item, err = d.resolveSetting("/Movies/Sub")
	if err != nil || item == nil {
		t.Fatalf("expected own setting, got %v %v", item, err)
	}
	if item.Actors != "深田咏美" {
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
	d.ID = 1
	// A 开启 use_name_as_actor
	if err := UpsertEmbyDirSetting(d.ID, "/A", "", boolPtr(true)); err != nil {
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
	d.ID = 1
	if err := UpsertEmbyDirSetting(d.ID, "/A", "", boolPtr(true)); err != nil {
		t.Fatalf("enable on A: %v", err)
	}
	if err := UpsertEmbyDirSetting(d.ID, "/A/A1", "手动演员", nil); err != nil {
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
	d.ID = 1
	if err := UpsertEmbyDirSetting(d.ID, "/A", "", boolPtr(true)); err != nil {
		t.Fatalf("enable on A: %v", err)
	}
	if err := UpsertEmbyDirSetting(d.ID, "/A/A1", "", boolPtr(true)); err != nil {
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
