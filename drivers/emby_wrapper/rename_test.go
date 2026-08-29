package emby_wrapper_test

import (
	"context"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

func TestMkdirConfigHasActors(t *testing.T) {
	d := setup(t)
	items := d.MkdirConfig()
	if len(items) != 2 {
		t.Fatalf("expected 2 mkdir config items, got %d", len(items))
	}
	if items[0].Name != "actors" {
		t.Errorf("expected actors field, got %q", items[0].Name)
	}
	if items[1].Name != "use_name_as_actor" || items[1].Type != "bool" {
		t.Errorf("expected bool use_name_as_actor field, got %+v", items[1])
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

func TestRenameEnableAndDisableUseNameAsActor(t *testing.T) {
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
	if err := d.Rename(context.Background(), movies, `{"use_name_as_actor":true}`); err != nil {
		t.Fatalf("enable: %+v", err)
	}
	item, err := getSettingForTest(d, "/Movies")
	if err != nil || item == nil || !item.UseNameAsActor {
		t.Fatalf("expected use_name_as_actor enabled, got %v %v", item, err)
	}
	if err := d.Rename(context.Background(), movies, `{"use_name_as_actor":false}`); err != nil {
		t.Fatalf("disable: %+v", err)
	}
	item, err = getSettingForTest(d, "/Movies")
	if err != nil || item != nil {
		t.Errorf("expected row deleted after disable, got %v %v", item, err)
	}
}

func TestRenameWithoutUseFieldKeepsIt(t *testing.T) {
	d := setup(t)
	movies := &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}
	if err := d.Rename(context.Background(), movies, `{"use_name_as_actor":true}`); err != nil {
		t.Fatalf("enable: %+v", err)
	}
	// 只改 actors，不带 use 字段：use 必须保持
	if err := d.Rename(context.Background(), movies, `{"actors":"A"}`); err != nil {
		t.Fatalf("set actors: %+v", err)
	}
	item, err := getSettingForTest(d, "/Movies")
	if err != nil || item == nil {
		t.Fatalf("get: %v %v", item, err)
	}
	if !item.UseNameAsActor || item.Actors != "A" {
		t.Errorf("use must survive actors-only rename, got %+v", item)
	}
}
