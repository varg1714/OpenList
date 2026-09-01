package emby_wrapper_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/fs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/http_range"
)

func TestEndToEndThroughFS(t *testing.T) {
	_ = setup(t)
	// 通过 fs 重命名设置 actor（等价于 UI 操作）
	if err := fs.Rename(context.Background(), "/ew/Movies", `{"actors":"演员A"}`); err != nil {
		t.Fatalf("rename via fs: %+v", err)
	}
	// fs 列表应包含虚拟 nfo
	objs, err := fs.List(context.Background(), "/ew/Movies", &fs.ListArgs{})
	if err != nil {
		t.Fatalf("fs list: %+v", err)
	}
	var found bool
	for _, o := range objs {
		if o.GetName() == "AAA.nfo" {
			found = true
		}
	}
	if !found {
		t.Fatal("virtual nfo must appear in fs list")
	}
	// fs 链接（strm generateStrm 同款调用链：Link -> 读取内容）
	link, _, err := fs.Link(context.Background(), "/ew/Movies/AAA.nfo", model.LinkArgs{})
	if err != nil {
		t.Fatalf("fs link: %+v", err)
	}
	if link.RangeReader == nil {
		t.Fatal("nfo link must have a range reader")
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
	got := string(body)
	if !strings.Contains(got, "演员A") || !strings.Contains(got, "AAA") {
		t.Errorf("nfo content mismatch: %s", got)
	}
}

// TestEndToEndTVShowThroughFS：TV 模式经 fs 层全链路（等价于 strm 落盘路径：虚拟名落盘、播放还原真实文件）。
func TestEndToEndTVShowThroughFS(t *testing.T) {
	_ = setup(t)
	if err := writeDirOrdered(t, "/Movies/2024年"); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := writeEpisodeFile(t, "/Movies/2024年/A1.mp4", "a1"); err != nil {
		t.Fatalf("write A1: %v", err)
	}
	if err := fs.Rename(context.Background(), "/ew/Movies", `{"tv_show":true,"tv_show_name":"测试剧"}`); err != nil {
		t.Fatalf("rename via fs: %+v", err)
	}
	objs, err := fs.List(context.Background(), "/ew/Movies", &fs.ListArgs{})
	if err != nil {
		t.Fatalf("fs list: %+v", err)
	}
	episodeFound, tvshowFound, seasonFound := false, false, false
	for _, o := range objs {
		switch o.GetName() {
		case "AAA-S01E01.mkv":
			episodeFound = true
		case "tvshow.nfo":
			tvshowFound = true
		case "2024年":
			seasonFound = true
		}
	}
	if !episodeFound || !tvshowFound || !seasonFound {
		t.Fatalf("fs list must contain episode/tvshow.nfo/season folder, got %v", names(objs))
	}
	// 季文件夹：虚拟剧集 + season.nfo
	objs, err = fs.List(context.Background(), "/ew/Movies/2024年", &fs.ListArgs{})
	if err != nil {
		t.Fatalf("fs list season: %+v", err)
	}
	if !containsName(objs, "A1-S02E01.mp4") || !containsName(objs, "season.nfo") {
		t.Fatalf("season folder listing mismatch, got %v", names(objs))
	}
	// 播放链路：季内虚拟剧集路径 → 还原真实文件内容
	link, _, err := fs.Link(context.Background(), "/ew/Movies/2024年/A1-S02E01.mp4", model.LinkArgs{})
	if err != nil {
		t.Fatalf("fs link episode: %+v", err)
	}
	if link.RangeReader == nil {
		t.Fatal("episode link must have a range reader")
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
	if string(body) != "a1" {
		t.Errorf("episode must play the real file content, got %q", string(body))
	}
}

// containsName 判断列表是否包含指定名称。
func containsName(objs []model.Obj, name string) bool {
	for _, o := range objs {
		if o.GetName() == name {
			return true
		}
	}
	return false
}
