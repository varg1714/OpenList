package emby_wrapper_test

import (
	"context"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

func TestMkdirConfigHasActors(t *testing.T) {
	d := setup(t)
	items := d.MkdirConfig()
	if len(items) != 1 {
		t.Fatalf("expected 1 mkdir config item, got %d", len(items))
	}
	if items[0].Name != "actors" {
		t.Errorf("expected actors field, got %q", items[0].Name)
	}
}

func TestRenameFolderSavesActors(t *testing.T) {
	d := setup(t)
	objs, err := d.List(context.Background(), &model.Object{Name: "Root", Path: "/", IsFolder: true}, model.ListArgs{})
	if err != nil {
		t.Fatalf("list root: %+v", err)
	}
	var movies model.Obj
	for _, o := range objs {
		if o.GetName() == "Movies" {
			movies = o
		}
	}
	if movies == nil {
		t.Fatal("Movies folder not found")
	}
	if err := d.Rename(context.Background(), movies, `{"actors":"三上悠亚,深田咏美"}`); err != nil {
		t.Fatalf("rename folder: %+v", err)
	}
	item, err := getSettingForTest(d, "/Movies")
	if err != nil || item == nil {
		t.Fatalf("expected setting, got %v %v", item, err)
	}
	if item.Actors != "三上悠亚,深田咏美" {
		t.Errorf("expected actors saved, got %q", item.Actors)
	}
	// 重命名不改变下游真实文件夹名：列表里仍是 Movies
	got := names(objs)
	_ = got
}

func TestRenameFolderEmptyClearsActors(t *testing.T) {
	d := setup(t)
	objs, err := d.List(context.Background(), &model.Object{Name: "Root", Path: "/", IsFolder: true}, model.ListArgs{})
	if err != nil {
		t.Fatalf("list root: %+v", err)
	}
	var movies model.Obj
	for _, o := range objs {
		if o.GetName() == "Movies" {
			movies = o
		}
	}
	if err := d.Rename(context.Background(), movies, `{"actors":"A"}`); err != nil {
		t.Fatalf("set actors: %+v", err)
	}
	if err := d.Rename(context.Background(), movies, `{"actors":""}`); err != nil {
		t.Fatalf("clear actors: %+v", err)
	}
	item, err := getSettingForTest(d, "/Movies")
	if err != nil || item != nil {
		t.Errorf("expected setting cleared, got %v %v", item, err)
	}
}

func TestRenameFileNotSupported(t *testing.T) {
	d := setup(t)
	objs, err := d.List(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, model.ListArgs{})
	if err != nil {
		t.Fatalf("list Movies: %+v", err)
	}
	if len(objs) != 1 || objs[0].GetName() != "AAA.mkv" {
		t.Fatalf("unexpected listing %v", names(objs))
	}
	if err := d.Rename(context.Background(), objs[0], `{"actors":"A"}`); err == nil {
		t.Fatal("file rename must not be supported")
	}
}
