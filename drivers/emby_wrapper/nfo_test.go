package emby_wrapper_test

import (
	"context"
	"strings"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

func TestListAddsVirtualNFO(t *testing.T) {
	d := setup(t)
	if err := d.Rename(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, `{"actors":"三上悠亚,深田咏美"}`); err != nil {
		t.Fatalf("set actors: %+v", err)
	}
	objs, err := d.List(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, model.ListArgs{})
	if err != nil {
		t.Fatalf("list Movies: %+v", err)
	}
	got := names(objs)
	if len(got) != 2 {
		t.Fatalf("expected [AAA.mkv AAA.nfo], got %v", got)
	}
	var nfo model.Obj
	for _, o := range objs {
		if o.GetName() == "AAA.nfo" {
			nfo = o
		}
	}
	if nfo == nil {
		t.Fatal("virtual AAA.nfo missing")
	}
	if nfo.IsDir() {
		t.Error("nfo must be a file")
	}
	if nfo.GetSize() == 0 {
		t.Error("nfo must have nonzero size")
	}
}

func TestListNoSettingNoNFO(t *testing.T) {
	d := setup(t)
	objs, err := d.List(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, model.ListArgs{})
	if err != nil {
		t.Fatalf("list Movies: %+v", err)
	}
	if got := names(objs); len(got) != 1 || got[0] != "AAA.mkv" {
		t.Errorf("expected only [AAA.mkv], got %v", got)
	}
}

func TestListOneNFOPerBaseName(t *testing.T) {
	d := setup(t)
	if err := d.Rename(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, `{"actors":"A"}`); err != nil {
		t.Fatalf("set actors: %+v", err)
	}
	// 追加 cd 分段与同名不同扩展名：应只生成 1 个 BBB.nfo
	if err := writeDownstreamFile(t, "/Movies/BBB.cd1.mkv", "x"); err != nil {
		t.Fatalf("write cd1: %v", err)
	}
	if err := writeDownstreamFile(t, "/Movies/BBB.cd2.mkv", "x"); err != nil {
		t.Fatalf("write cd2: %v", err)
	}
	if err := writeDownstreamFile(t, "/Movies/BBB.mp4", "x"); err != nil {
		t.Fatalf("write mp4: %v", err)
	}
	objs, err := d.List(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list: %+v", err)
	}
	got := names(objs)
	if len(got) != 6 {
		t.Fatalf("expected [AAA.mkv AAA.nfo BBB.cd1.mkv BBB.cd2.mkv BBB.mp4 BBB.nfo], got %v", got)
	}
	nfoCount := 0
	for _, o := range objs {
		if o.GetName() == "BBB.nfo" {
			nfoCount++
		}
	}
	if nfoCount != 1 {
		t.Errorf("expected exactly one BBB.nfo, got %d", nfoCount)
	}
}

func TestListSkipsRealNFO(t *testing.T) {
	d := setup(t)
	// 在下游放一个真实 AAA.nfo
	if err := writeDownstreamFile(t, "/Movies/AAA.nfo", "real"); err != nil {
		t.Fatalf("write real nfo: %v", err)
	}
	if err := d.Rename(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, `{"actors":"A"}`); err != nil {
		t.Fatalf("set actors: %+v", err)
	}
	objs, err := d.List(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list Movies: %+v", err)
	}
	got := names(objs)
	if len(got) != 2 {
		t.Fatalf("expected [AAA.mkv AAA.nfo], got %v", got)
	}
	for _, o := range objs {
		if o.GetName() == "AAA.nfo" && o.GetSize() != int64(len("real")) {
			t.Errorf("real nfo must win, got size %d", o.GetSize())
		}
	}
}

func TestListInheritedSettingAddsNFOInSubfolder(t *testing.T) {
	d := setup(t)
	if err := d.Rename(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, `{"actors":"三上悠亚"}`); err != nil {
		t.Fatalf("set actors: %+v", err)
	}
	// 子文件夹 + 影片
	if err := writeDownstreamDir(t, "/Movies/Sub"); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	if err := writeDownstreamFile(t, "/Movies/Sub/BBB.mp4", "x"); err != nil {
		t.Fatalf("write sub movie: %v", err)
	}
	objs, err := d.List(context.Background(), &model.Object{Name: "Sub", Path: "/Movies/Sub", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list Sub: %+v", err)
	}
	if got := names(objs); len(got) != 2 {
		t.Fatalf("expected [BBB.mp4 BBB.nfo], got %v", got)
	}
}

func TestNFONotGeneratedForNonMovieExt(t *testing.T) {
	d := setup(t)
	if err := writeDownstreamFile(t, "/Movies/note.txt", "x"); err != nil {
		t.Fatalf("write txt: %v", err)
	}
	if err := d.Rename(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, `{"actors":"A"}`); err != nil {
		t.Fatalf("set actors: %+v", err)
	}
	objs, err := d.List(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list Movies: %+v", err)
	}
	for _, o := range objs {
		if strings.HasSuffix(o.GetName(), ".nfo") && strings.HasPrefix(o.GetName(), "note") {
			t.Errorf("txt file must not get nfo, got %v", names(objs))
		}
	}
}

func TestRealNFOIgnoreCaseBlocksVirtual(t *testing.T) {
	d := setup(t)
	// 真实 nfo 小写 aaa.nfo + 影片大写 AAA.mkv：不应再生成虚拟 AAA.nfo
	if err := writeDownstreamFile(t, "/Movies/aaa.nfo", "real"); err != nil {
		t.Fatalf("write real nfo: %v", err)
	}
	if err := d.Rename(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, `{"actors":"A"}`); err != nil {
		t.Fatalf("set actors: %+v", err)
	}
	objs, err := d.List(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list Movies: %+v", err)
	}
	if got := names(objs); len(got) != 2 {
		t.Fatalf("expected [AAA.mkv aaa.nfo], got %v", got)
	}
}
