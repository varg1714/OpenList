package emby_wrapper_test

import (
	"context"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/drivers/emby_wrapper"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

func TestGetVirtualNFO(t *testing.T) {
	d := setup(t)
	if err := d.Rename(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, `{"actors":"三上悠亚"}`); err != nil {
		t.Fatalf("set actors: %+v", err)
	}
	obj, err := d.Get(context.Background(), "/Movies/AAA.nfo")
	if err != nil {
		t.Fatalf("get nfo: %+v", err)
	}
	if obj.GetName() != "AAA.nfo" || obj.IsDir() {
		t.Errorf("unexpected obj: %+v", obj)
	}
	if obj.GetSize() == 0 {
		t.Error("nfo must have content size")
	}
}

func TestGetRealNFOFileWins(t *testing.T) {
	d := setup(t)
	if err := writeDownstreamFile(t, "/Movies/AAA.nfo", "real-content"); err != nil {
		t.Fatalf("write real nfo: %v", err)
	}
	if err := d.Rename(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, `{"actors":"A"}`); err != nil {
		t.Fatalf("set actors: %+v", err)
	}
	obj, err := d.Get(context.Background(), "/Movies/AAA.nfo")
	if err != nil {
		t.Fatalf("get nfo: %+v", err)
	}
	if obj.GetSize() != int64(len("real-content")) {
		t.Errorf("real nfo must win, got size %d", obj.GetSize())
	}
}

func TestGetVirtualNFOInherited(t *testing.T) {
	d := setup(t)
	if err := d.Rename(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, `{"actors":"三上悠亚"}`); err != nil {
		t.Fatalf("set actors: %+v", err)
	}
	if err := writeDownstreamDir(t, "/Movies/Sub"); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	if err := writeDownstreamFile(t, "/Movies/Sub/BBB.mp4", "x"); err != nil {
		t.Fatalf("write sub movie: %v", err)
	}
	obj, err := d.Get(context.Background(), "/Movies/Sub/BBB.nfo")
	if err != nil {
		t.Fatalf("get inherited nfo: %+v", err)
	}
	if obj.GetName() != "BBB.nfo" {
		t.Errorf("unexpected obj: %v", obj.GetName())
	}
}

func TestGetNFOWithoutMatchingMovieNotFound(t *testing.T) {
	d := setup(t)
	if err := d.Rename(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, `{"actors":"A"}`); err != nil {
		t.Fatalf("set actors: %+v", err)
	}
	if _, err := d.Get(context.Background(), "/Movies/NotFound.nfo"); err == nil {
		t.Fatal("nfo without matching movie must not be served")
	}
}

func TestGetPlainFileForwardsDownstream(t *testing.T) {
	d := setup(t)
	obj, err := d.Get(context.Background(), "/Movies/AAA.mkv")
	if err != nil {
		t.Fatalf("get movie: %+v", err)
	}
	if obj.GetName() != "AAA.mkv" {
		t.Errorf("unexpected obj: %v", obj.GetName())
	}
}

func TestGetFolderExposesActorsAddition(t *testing.T) {
	d := setup(t)
	if err := d.Rename(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, `{"actors":"三上悠亚"}`); err != nil {
		t.Fatalf("set actors: %+v", err)
	}
	obj, err := d.Get(context.Background(), "/Movies")
	if err != nil {
		t.Fatalf("get folder: %+v", err)
	}
	add, ok := obj.(model.ObjAdditional)
	if !ok {
		t.Fatal("folder must expose additional")
	}
	fa, ok := add.GetAddition().(emby_wrapper.FolderAddition)
	if !ok {
		t.Fatalf("unexpected addition type %T", add.GetAddition())
	}
	if fa.Actors != "三上悠亚" {
		t.Errorf("expected actors %q, got %q", "三上悠亚", fa.Actors)
	}
}

func TestGetFolderExposesUseNameAsActor(t *testing.T) {
	d := setup(t)
	if err := d.Rename(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, `{"use_name_as_actor":true}`); err != nil {
		t.Fatalf("enable: %+v", err)
	}
	obj, err := d.Get(context.Background(), "/Movies")
	if err != nil {
		t.Fatalf("get folder: %+v", err)
	}
	add, ok := obj.(model.ObjAdditional)
	if !ok {
		t.Fatal("folder must expose additional")
	}
	fa, ok := add.GetAddition().(emby_wrapper.FolderAddition)
	if !ok {
		t.Fatalf("unexpected addition type %T", add.GetAddition())
	}
	if fa.UseNameAsActor == nil || !*fa.UseNameAsActor {
		t.Error("Get must expose use_name_as_actor=true")
	}
}
