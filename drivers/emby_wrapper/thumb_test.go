package emby_wrapper_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/drivers/emby_wrapper"
	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
)

// setupWithThumb 与 setup 相同，但 local 存储开启 thumbnail（视频对象带 Thumb URL，
// 模拟 115 等网盘驱动的预览图能力）。
func setupWithThumb(t *testing.T) *emby_wrapper.EmbyWrapper {
	t.Helper()
	// 测试环境未跑配置 hooks，SlicesMap 为空导致 local 的 GetFileType 判定 UNKNOWN、
	// 不给视频生成 thumb；补视频类型表（生产环境由配置加载填充）
	conf.SlicesMap[conf.VideoTypes] = []string{"mp4", "mkv"}
	tmp := t.TempDir()
	localRoot = tmp
	_ = os.MkdirAll(filepath.Join(tmp, "Movies"), 0o755)
	_ = os.WriteFile(filepath.Join(tmp, "Movies", "AAA.mkv"), []byte("x"), 0o644)
	localID, err := op.CreateStorage(context.Background(), model.Storage{
		Driver:    "Local",
		MountPath: "/local",
		Addition:  fmt.Sprintf(`{"root_folder_path":%q,"thumbnail":true}`, tmp),
	})
	if err != nil {
		t.Fatalf("create local storage: %+v", err)
	}
	wrapperID, err := op.CreateStorage(context.Background(), model.Storage{
		Driver:    "EmbyWrapper",
		MountPath: "/ew",
		Addition:  `{"remote_path":"/local"}`,
	})
	if err != nil {
		t.Fatalf("create emby wrapper storage: %+v", err)
	}
	t.Cleanup(func() {
		_ = op.DeleteStorageById(context.Background(), localID)
		_ = op.DeleteStorageById(context.Background(), wrapperID)
	})
	d, err := op.GetStorageByMountPath("/ew")
	if err != nil {
		t.Fatalf("get emby wrapper storage: %+v", err)
	}
	return d.(*emby_wrapper.EmbyWrapper)
}

// TestTVShowEpisodeThumb：上游对象带 thumb 时，虚拟剧集旁附加
// {虚拟名}-thumb.jpg 占位对象（内容在 Link 时惰性下载）；Get 可反查。
func TestTVShowEpisodeThumb(t *testing.T) {
	d := setupWithThumb(t)
	if err := writeDirOrdered(t, "/Movies/2024年"); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := writeEpisodeFile(t, "/Movies/2024年/A1.mp4", "a1"); err != nil {
		t.Fatalf("write A1: %v", err)
	}
	markTVShow(t, d, `{"tv_show":true}`)
	// 根视频（季 1）旁有 S01E01-thumb.jpg
	objs, err := d.List(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list root: %+v", err)
	}
	if got := sortedNames(objs); !reflect.DeepEqual(got, []string{"S01E01-thumb.jpg", "S01E01.mkv", "S01E01.nfo", "S02", "tvshow.nfo"}) {
		t.Errorf("root view must attach episode thumb, got %v", got)
	}
	// 季视图同样附加
	objs, err = d.List(context.Background(), &model.Object{Name: "S02", Path: "/Movies/S02", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list season: %+v", err)
	}
	if got := sortedNames(objs); !reflect.DeepEqual(got, []string{"S02E01-thumb.jpg", "S02E01.mp4", "S02E01.nfo", "season.nfo"}) {
		t.Errorf("season view must attach episode thumb, got %v", got)
	}
	// Get 反查占位对象
	obj, err := d.Get(context.Background(), "/Movies/S02/S02E01-thumb.jpg")
	if err != nil {
		t.Fatalf("get thumb: %+v", err)
	}
	if obj.GetName() != "S02E01-thumb.jpg" {
		t.Errorf("thumb object name mismatch, got %q", obj.GetName())
	}
	// 不存在的剧集 → 404
	if _, err := d.Get(context.Background(), "/Movies/S02/S99E99-thumb.jpg"); err == nil {
		t.Error("unmapped thumb must not be served")
	}
}

// TestTVShowShowImagesListing：fanart_count>0 且剧集带 thumb 时，剧根视图附加
// poster.jpg（第 1 个候选）+ fanart1..N.jpg（后续候选，按剧集编号顺序）；
// 季视图不附加；候选不足时不输出多余占位。
func TestTVShowShowImagesListing(t *testing.T) {
	d := setupWithThumb(t)
	d.FanartCount = 3
	if err := writeDirOrdered(t, "/Movies/2024年"); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := writeEpisodeFile(t, "/Movies/2024年/A1.mp4", "a1"); err != nil {
		t.Fatalf("write A1: %v", err)
	}
	if err := writeEpisodeFile(t, "/Movies/2024年/B1.mp4", "b1"); err != nil {
		t.Fatalf("write B1: %v", err)
	}
	markTVShow(t, d, `{"tv_show":true}`)
	// 剧根视图：候选 = 根视频 AAA.mkv（第 1 集）→ poster.jpg；季内 A1/B1 → fanart1/2.jpg
	objs, err := d.List(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list root: %+v", err)
	}
	got := sortedNames(objs)
	for _, want := range []string{"poster.jpg", "fanart1.jpg", "fanart2.jpg"} {
		if !containsStr(got, want) {
			t.Errorf("root view must attach %s, got %v", want, got)
		}
	}
	if containsStr(got, "fanart3.jpg") {
		t.Errorf("no extra image beyond candidates, got %v", got)
	}
	// 季视图不附加剧根封面
	objs, err = d.List(context.Background(), &model.Object{Name: "S02", Path: "/Movies/S02", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list season: %+v", err)
	}
	if got := sortedNames(objs); containsStr(got, "poster.jpg") || containsStr(got, "fanart1.jpg") {
		t.Errorf("season view must not attach show images, got %v", got)
	}
}

// TestTVShowShowImagesGet：poster/fanart 占位可经 Get 反查（编号 ↔ 候选序号一致）。
func TestTVShowShowImagesGet(t *testing.T) {
	d := setupWithThumb(t)
	d.FanartCount = 3
	if err := writeDirOrdered(t, "/Movies/2024年"); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := writeEpisodeFile(t, "/Movies/2024年/A1.mp4", "a1"); err != nil {
		t.Fatalf("write A1: %v", err)
	}
	markTVShow(t, d, `{"tv_show":true}`)
	for _, name := range []string{"poster.jpg", "fanart1.jpg"} {
		obj, err := d.Get(context.Background(), "/Movies/"+name)
		if err != nil {
			t.Fatalf("get %s: %+v", name, err)
		}
		if obj.GetName() != name {
			t.Errorf("image object name mismatch for %s, got %q", name, obj.GetName())
		}
	}
	// 候选只有 2 个（AAA + A1），fanart2 越界 → 404
	if _, err := d.Get(context.Background(), "/Movies/fanart2.jpg"); err == nil {
		t.Error("fanart beyond candidates must not be served")
	}
	// 非剧根目录（季视图）下不提供
	if _, err := d.Get(context.Background(), "/Movies/S02/poster.jpg"); err == nil {
		t.Error("poster inside season view must not be served")
	}
}

// TestTVShowShowImagesRealFileWins：真实同名 poster.jpg 存在时展示真实文件、
// 不附加虚拟占位（候选槽位直接跳过，不后移占用其它名字）。
func TestTVShowShowImagesRealFileWins(t *testing.T) {
	d := setupWithThumb(t)
	d.FanartCount = 3
	if err := writeDownstreamFile(t, "/Movies/poster.jpg", "real-poster"); err != nil {
		t.Fatalf("write real poster: %v", err)
	}
	markTVShow(t, d, `{"tv_show":true}`)
	objs, err := d.List(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list root: %+v", err)
	}
	if got := sortedNames(objs); !containsStr(got, "poster.jpg") || containsStr(got, "fanart1.jpg") {
		t.Errorf("real poster wins and sole candidate slot skipped (no fanart1), got %v", got)
	}
	if got := readNFOLink(t, d, "/Movies/poster.jpg"); got != "real-poster" {
		t.Errorf("real poster file must be served, got %q", got)
	}
}

// TestTVShowShowImagesDisabled：fanart_count=0（默认）不附加任何剧根封面。
func TestTVShowShowImagesDisabled(t *testing.T) {
	d := setupWithThumb(t)
	markTVShow(t, d, `{"tv_show":true}`)
	objs, err := d.List(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list root: %+v", err)
	}
	if got := sortedNames(objs); containsStr(got, "poster.jpg") || containsStr(got, "fanart1.jpg") {
		t.Errorf("fanart_count=0 must attach nothing, got %v", got)
	}
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// TestTVShowEpisodeThumbRealFileWins：真实目录存在同名 -thumb.jpg 时展示真实文件，
// 不附加虚拟对象。
func TestTVShowEpisodeThumbRealFileWins(t *testing.T) {
	d := setupWithThumb(t)
	if err := writeDirOrdered(t, "/Movies/2024年"); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := writeEpisodeFile(t, "/Movies/2024年/A1.mp4", "a1"); err != nil {
		t.Fatalf("write A1: %v", err)
	}
	if err := writeDownstreamFile(t, "/Movies/2024年/S02E01-thumb.jpg", "real-thumb"); err != nil {
		t.Fatalf("write real thumb: %v", err)
	}
	markTVShow(t, d, `{"tv_show":true}`)
	objs, err := d.List(context.Background(), &model.Object{Name: "S02", Path: "/Movies/S02", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list season: %+v", err)
	}
	// 真实同名 thumb 原样展示且不重复附加
	if got := sortedNames(objs); !reflect.DeepEqual(got, []string{"S02E01-thumb.jpg", "S02E01.mp4", "S02E01.nfo", "season.nfo"}) {
		t.Errorf("real thumb file must win without duplication, got %v", got)
	}
	if got := readNFOLink(t, d, "/Movies/S02/S02E01-thumb.jpg"); got != "real-thumb" {
		t.Errorf("real thumb file must be served, got %q", got)
	}
}
