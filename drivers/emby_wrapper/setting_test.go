package emby_wrapper

import (
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

func newTestWrapper() *EmbyWrapper {
	return &EmbyWrapper{}
}

func TestResolveSettingInheritsAncestor(t *testing.T) {
	d := newTestWrapper()
	d.ID = 1
	if err := UpsertEmbyDirSetting(d.ID, "/Movies", "三上悠亚"); err != nil {
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
	if err := UpsertEmbyDirSetting(d.ID, "/Movies/Sub", "深田咏美"); err != nil {
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

var _ = model.EmbyDirSetting{} // 保持 model 导入稳定
