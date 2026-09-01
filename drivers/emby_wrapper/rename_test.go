package emby_wrapper_test

import (
	"context"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

func TestMkdirConfigHasActors(t *testing.T) {
	d := setup(t)
	items := d.MkdirConfig()
	if len(items) != 7 {
		t.Fatalf("expected 7 mkdir config items, got %d", len(items))
	}
	if items[0].Name != "actors" {
		t.Errorf("expected actors field, got %q", items[0].Name)
	}
	if items[1].Name != "use_name_as_actor" || items[1].Type != "bool" {
		t.Errorf("expected bool use_name_as_actor field, got %+v", items[1])
	}
	if items[2].Name != "plot" {
		t.Errorf("expected plot field, got %q", items[2].Name)
	}
	if items[3].Name != "append_file_name_to_plot" || items[3].Type != "bool" {
		t.Errorf("expected bool append_file_name_to_plot field, got %+v", items[3])
	}
	if items[4].Name != "tv_show" || items[4].Type != "bool" {
		t.Errorf("expected bool tv_show field, got %+v", items[4])
	}
	if items[5].Name != "tv_show_name" || items[5].Type != "string" {
		t.Errorf("expected string tv_show_name field, got %+v", items[5])
	}
	if items[6].Name != "tv_show_subfolders" || items[6].Type != "bool" {
		t.Errorf("expected bool tv_show_subfolders field, got %+v", items[6])
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
	if err := d.Rename(context.Background(), movies, `{"actors":"演员A,演员B"}`); err != nil {
		t.Fatalf("rename folder: %+v", err)
	}
	item, err := getSettingForTest(d, "/Movies")
	if err != nil || item == nil {
		t.Fatalf("expected setting, got %v %v", item, err)
	}
	if item.Actors != "演员A,演员B" {
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
