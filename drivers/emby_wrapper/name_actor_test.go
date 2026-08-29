package emby_wrapper_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/http_range"
)

func readNFOLink(t *testing.T, d interface {
	Get(context.Context, string) (model.Obj, error)
	Link(context.Context, model.Obj, model.LinkArgs) (*model.Link, error)
}, path string) string {
	t.Helper()
	obj, err := d.Get(context.Background(), path)
	if err != nil {
		t.Fatalf("get nfo: %+v", err)
	}
	link, err := d.Link(context.Background(), obj, model.LinkArgs{})
	if err != nil {
		t.Fatalf("link: %+v", err)
	}
	rc, err := link.RangeReader.RangeRead(context.Background(), http_range.Range{Length: -1})
	if err != nil {
		t.Fatalf("range read: %+v", err)
	}
	defer rc.Close()
	body, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read: %+v", err)
	}
	return string(body)
}

// TestNameAsActorListAndContent：开启 use_name_as_actor 后，
// 直接子文件夹中的影片生成 nfo，actor = 子文件夹名，内容可经 Link 读取。
func TestNameAsActorListAndContent(t *testing.T) {
	d := setup(t)
	// /Movies 开启，直接子文件夹 /Movies/A1 放影片
	if err := writeDownstreamDir(t, "/Movies/A1"); err != nil {
		t.Fatalf("mkdir A1: %v", err)
	}
	if err := writeDownstreamFile(t, "/Movies/A1/BBB.mp4", "x"); err != nil {
		t.Fatalf("write movie: %v", err)
	}
	if err := d.Rename(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, `{"use_name_as_actor":true}`); err != nil {
		t.Fatalf("enable: %+v", err)
	}
	objs, err := d.List(context.Background(), &model.Object{Name: "A1", Path: "/Movies/A1", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list A1: %+v", err)
	}
	if got := names(objs); len(got) != 2 {
		t.Fatalf("expected [BBB.mp4 BBB.nfo], got %v", got)
	}
	got := readNFOLink(t, d, "/Movies/A1/BBB.nfo")
	if !strings.Contains(got, "BBB") {
		t.Errorf("nfo must contain title BBB, got %s", got)
	}
	if !strings.Contains(got, "<name>A1</name>") {
		t.Errorf("nfo must contain actor A1, got %s", got)
	}
}

// TestNameAsActorSubtree：孙级目录继承最近开启者的直接子文件夹名。
func TestNameAsActorSubtree(t *testing.T) {
	d := setup(t)
	if err := writeDownstreamDir(t, "/Movies/A1/A11"); err != nil {
		t.Fatalf("mkdir A11: %v", err)
	}
	if err := writeDownstreamFile(t, "/Movies/A1/A11/CCC.mkv", "x"); err != nil {
		t.Fatalf("write movie: %v", err)
	}
	if err := d.Rename(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, `{"use_name_as_actor":true}`); err != nil {
		t.Fatalf("enable: %+v", err)
	}
	objs, err := d.List(context.Background(), &model.Object{Name: "A11", Path: "/Movies/A1/A11", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list A11: %+v", err)
	}
	if got := names(objs); len(got) != 2 {
		t.Fatalf("expected [CCC.mkv CCC.nfo], got %v", got)
	}
	got := readNFOLink(t, d, "/Movies/A1/A11/CCC.nfo")
	if !strings.Contains(got, "<name>A1</name>") {
		t.Errorf("subtree nfo must contain actor A1, got %s", got)
	}
}

// TestNameAsActorNotOnEnablerItself：开启者自身目录不生成 nfo。
func TestNameAsActorNotOnEnablerItself(t *testing.T) {
	d := setup(t)
	if err := d.Rename(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, `{"use_name_as_actor":true}`); err != nil {
		t.Fatalf("enable: %+v", err)
	}
	objs, err := d.List(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list Movies: %+v", err)
	}
	if got := names(objs); len(got) != 1 || got[0] != "AAA.mkv" {
		t.Errorf("enabler itself must have no nfo, got %v", got)
	}
}

// TestNameAsActorManualActorsWin：手动 actors 覆盖名称即 actor。
func TestNameAsActorManualActorsWin(t *testing.T) {
	d := setup(t)
	if err := writeDownstreamDir(t, "/Movies/A1"); err != nil {
		t.Fatalf("mkdir A1: %v", err)
	}
	if err := writeDownstreamFile(t, "/Movies/A1/BBB.mp4", "x"); err != nil {
		t.Fatalf("write movie: %v", err)
	}
	if err := d.Rename(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, `{"use_name_as_actor":true}`); err != nil {
		t.Fatalf("enable: %+v", err)
	}
	if err := d.Rename(context.Background(), &model.Object{Name: "A1", Path: "/Movies/A1", IsFolder: true}, `{"actors":"手动演员"}`); err != nil {
		t.Fatalf("manual on A1: %+v", err)
	}
	got := readNFOLink(t, d, "/Movies/A1/BBB.nfo")
	if !strings.Contains(got, "<name>手动演员</name>") {
		t.Errorf("manual actors must win, got %s", got)
	}
}
