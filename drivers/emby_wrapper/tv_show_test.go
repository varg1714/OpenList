package emby_wrapper_test

import (
	"context"
	"os"
	stdpath "path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/emby_wrapper"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

// writeEpisodeFile 写下游文件并等待 15ms：local 驱动取文件系统 birth time 作为
// CreateTime（os.Chtimes 无法控制），sleep 保证创建时间严格递增、排序确定性。
func writeEpisodeFile(t *testing.T, relPath, content string) error {
	t.Helper()
	if err := writeDownstreamFile(t, relPath, content); err != nil {
		return err
	}
	time.Sleep(15 * time.Millisecond)
	return nil
}

// writeDirOrdered 建下游目录并等待 15ms（季号按文件夹创建时间排序）。
func writeDirOrdered(t *testing.T, relPath string) error {
	t.Helper()
	if err := writeDownstreamDir(t, relPath); err != nil {
		return err
	}
	time.Sleep(15 * time.Millisecond)
	return nil
}

func sortedNames(objs []model.Obj) []string {
	got := names(objs)
	sort.Strings(got)
	return got
}

func markTVShow(t *testing.T, d *emby_wrapper.EmbyWrapper, payload string) {
	markTVShowAt(t, d, "/Movies", payload)
}

func markTVShowAt(t *testing.T, d *emby_wrapper.EmbyWrapper, dirPath, payload string) {
	t.Helper()
	if err := d.Rename(context.Background(), &model.Object{Name: stdpath.Base(dirPath), Path: dirPath, IsFolder: true}, payload); err != nil {
		t.Fatalf("mark tv show %s: %+v", dirPath, err)
	}
}

// TestTVShowSeasonsAndRootEpisodes：根目录直接文件 = 第 1 季；直接子文件夹按
// 创建时间分配连续季号（根有视频从 2 起）；季内文件按创建时间编号。
func TestTVShowSeasonsAndRootEpisodes(t *testing.T) {
	d := setup(t)
	// 根目录已有 AAA.mkv（setup 创建，最早）→ 季 1；子文件夹按创建时间：2024年 早 → 季 2，2025年 → 季 3
	if err := writeDirOrdered(t, "/Movies/2024年"); err != nil {
		t.Fatalf("mkdir 2024年: %v", err)
	}
	if err := writeDirOrdered(t, "/Movies/2025年"); err != nil {
		t.Fatalf("mkdir 2025年: %v", err)
	}
	if err := writeEpisodeFile(t, "/Movies/2024年/A1.mp4", "a1"); err != nil {
		t.Fatalf("write A1: %v", err)
	}
	if err := writeEpisodeFile(t, "/Movies/2024年/A2.mp4", "a2"); err != nil {
		t.Fatalf("write A2: %v", err)
	}
	if err := writeEpisodeFile(t, "/Movies/2025年/B1.mp4", "b1"); err != nil {
		t.Fatalf("write B1: %v", err)
	}
	markTVShow(t, d, `{"tv_show":true,"tv_show_name":"测试剧","plot":"剧集介绍","actors":"演员A"}`)
	// 根目录：AAA 季1 + 两个季文件夹 + tvshow.nfo
	objs, err := d.List(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list root: %+v", err)
	}
	if got := sortedNames(objs); !reflect.DeepEqual(got, []string{"2024年", "2025年", "AAA-S01E01.mkv", "AAA-S01E01.nfo", "tvshow.nfo"}) {
		t.Errorf("root listing mismatch, got %v", got)
	}
	// 季 2（2024年）：A1 先建 → E01；含 season.nfo
	objs, err = d.List(context.Background(), &model.Object{Name: "2024年", Path: "/Movies/2024年", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list 2024年: %+v", err)
	}
	if got := sortedNames(objs); !reflect.DeepEqual(got, []string{"A1-S02E01.mp4", "A1-S02E01.nfo", "A2-S02E02.mp4", "A2-S02E02.nfo", "season.nfo"}) {
		t.Errorf("season 2 listing mismatch, got %v", got)
	}
	// 季 3（2025年）
	objs, err = d.List(context.Background(), &model.Object{Name: "2025年", Path: "/Movies/2025年", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list 2025年: %+v", err)
	}
	if got := sortedNames(objs); !reflect.DeepEqual(got, []string{"B1-S03E01.mp4", "B1-S03E01.nfo", "season.nfo"}) {
		t.Errorf("season 3 listing mismatch, got %v", got)
	}
}

// TestTVShowNoRootVideos：根目录无直接视频时子文件夹从第 1 季起。
func TestTVShowNoRootVideos(t *testing.T) {
	d := setup(t)
	if err := os.Remove(filepath.Join(localRoot, "Movies", "AAA.mkv")); err != nil {
		t.Fatalf("remove AAA: %v", err)
	}
	if err := writeDirOrdered(t, "/Movies/2024年"); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := writeEpisodeFile(t, "/Movies/2024年/A1.mp4", "a1"); err != nil {
		t.Fatalf("write A1: %v", err)
	}
	markTVShow(t, d, `{"tv_show":true}`)
	objs, err := d.List(context.Background(), &model.Object{Name: "2024年", Path: "/Movies/2024年", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list: %+v", err)
	}
	if got := sortedNames(objs); !reflect.DeepEqual(got, []string{"A1-S01E01.mp4", "A1-S01E01.nfo", "season.nfo"}) {
		t.Errorf("no-root-videos season must start at 1, got %v", got)
	}
}

// TestTVShowNestedFolderInSeason：季内的嵌套子文件夹文件并入该季编号（原地展示）。
func TestTVShowNestedFolderInSeason(t *testing.T) {
	d := setup(t)
	if err := writeDirOrdered(t, "/Movies/2024年"); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := writeEpisodeFile(t, "/Movies/2024年/C.mp4", "c"); err != nil {
		t.Fatalf("write C: %v", err)
	}
	if err := writeDirOrdered(t, "/Movies/2024年/专题"); err != nil {
		t.Fatalf("mkdir 专题: %v", err)
	}
	if err := writeEpisodeFile(t, "/Movies/2024年/专题/D.mp4", "d"); err != nil {
		t.Fatalf("write D: %v", err)
	}
	markTVShow(t, d, `{"tv_show":true}`)
	// C 先建 → 季 2 的 E01；D 后建 → E02，显示在 专题 内
	objs, err := d.List(context.Background(), &model.Object{Name: "专题", Path: "/Movies/2024年/专题", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list 专题: %+v", err)
	}
	if got := sortedNames(objs); !reflect.DeepEqual(got, []string{"D-S02E02.mp4", "D-S02E02.nfo"}) {
		t.Errorf("nested folder episodes mismatch, got %v", got)
	}
	// 2024年 自身列表：C 在根、专题文件夹原样（无 season.nfo——只有直接子文件夹是季）
	objs, err = d.List(context.Background(), &model.Object{Name: "2024年", Path: "/Movies/2024年", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list 2024年: %+v", err)
	}
	if got := sortedNames(objs); !reflect.DeepEqual(got, []string{"C-S02E01.mp4", "C-S02E01.nfo", "season.nfo", "专题"}) {
		t.Errorf("season listing with nested dir mismatch, got %v", got)
	}
}

// TestTVShowSeasonNFOContent：season.nfo 含分配的季号与原文件夹名。
func TestTVShowSeasonNFOContent(t *testing.T) {
	d := setup(t)
	if err := writeDirOrdered(t, "/Movies/2024年"); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := writeEpisodeFile(t, "/Movies/2024年/A1.mp4", "a1"); err != nil {
		t.Fatalf("write A1: %v", err)
	}
	markTVShow(t, d, `{"tv_show":true}`)
	got := readNFOLink(t, d, "/Movies/2024年/season.nfo")
	if !strings.Contains(got, "<seasonnumber>2</seasonnumber>") {
		t.Errorf("season.nfo must carry assigned season number, got %s", got)
	}
	if !strings.Contains(got, "<seasonname><![CDATA[2024年]]></seasonname>") {
		t.Errorf("season.nfo must carry original folder name, got %s", got)
	}
}

// TestTVShowNFOsContent：剧集 nfo（episodedetails、title=原名、actors、无 plot）与 tvshow.nfo（剧名+简介）。
func TestTVShowNFOsContent(t *testing.T) {
	d := setup(t)
	if err := writeDirOrdered(t, "/Movies/2024年"); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := writeEpisodeFile(t, "/Movies/2024年/A1.mp4", "a1"); err != nil {
		t.Fatalf("write A1: %v", err)
	}
	markTVShow(t, d, `{"tv_show":true,"tv_show_name":"测试剧","plot":"剧集介绍","actors":"演员A"}`)
	ep := readNFOLink(t, d, "/Movies/2024年/A1-S02E01.nfo")
	if !strings.Contains(ep, "<episodedetails>") {
		t.Errorf("episode nfo must use episodedetails root, got %s", ep)
	}
	if !strings.Contains(ep, "<![CDATA[A1]]>") {
		t.Errorf("episode nfo title must be original base name, got %s", ep)
	}
	if !strings.Contains(ep, "<name>演员A</name>") {
		t.Errorf("episode nfo must keep actors, got %s", ep)
	}
	if strings.Contains(ep, "剧集介绍") {
		t.Errorf("episode nfo must not contain show plot, got %s", ep)
	}
	show := readNFOLink(t, d, "/Movies/tvshow.nfo")
	if !strings.Contains(show, "<tvshow>") || !strings.Contains(show, "<![CDATA[测试剧]]>") || !strings.Contains(show, "<![CDATA[剧集介绍]]>") {
		t.Errorf("tvshow.nfo mismatch, got %s", show)
	}
}

// TestTVShowNameFallbackFolder：未设置剧名时回退文件夹名。
func TestTVShowNameFallbackFolder(t *testing.T) {
	d := setup(t)
	markTVShow(t, d, `{"tv_show":true}`)
	show := readNFOLink(t, d, "/Movies/tvshow.nfo")
	if !strings.Contains(show, "<![CDATA[Movies]]>") {
		t.Errorf("show name must fall back to folder name, got %s", show)
	}
}

// TestTVShowSkipsNumbered：已含 SxxExx 的文件保持原名，不消耗序号。
func TestTVShowSkipsNumbered(t *testing.T) {
	d := setup(t)
	if err := writeDirOrdered(t, "/Movies/2024年"); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := writeEpisodeFile(t, "/Movies/2024年/A1.mp4", "a1"); err != nil {
		t.Fatalf("write A1: %v", err)
	}
	if err := writeEpisodeFile(t, "/Movies/2024年/Show.S01E03.mkv", "s"); err != nil {
		t.Fatalf("write numbered: %v", err)
	}
	markTVShow(t, d, `{"tv_show":true}`)
	objs, err := d.List(context.Background(), &model.Object{Name: "2024年", Path: "/Movies/2024年", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list: %+v", err)
	}
	if got := sortedNames(objs); !reflect.DeepEqual(got, []string{"A1-S02E01.mp4", "A1-S02E01.nfo", "Show.S01E03.mkv", "Show.S01E03.nfo", "season.nfo"}) {
		t.Errorf("numbered file must keep name without consuming index, got %v", got)
	}
}

// TestTVShowRealSeasonNFOFileWins：下游真实 season.nfo 优先于虚拟。
func TestTVShowRealSeasonNFOFileWins(t *testing.T) {
	d := setup(t)
	if err := writeDirOrdered(t, "/Movies/2024年"); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := writeEpisodeFile(t, "/Movies/2024年/A1.mp4", "a1"); err != nil {
		t.Fatalf("write A1: %v", err)
	}
	if err := writeDownstreamFile(t, "/Movies/2024年/season.nfo", "real-content"); err != nil {
		t.Fatalf("write real season.nfo: %v", err)
	}
	markTVShow(t, d, `{"tv_show":true}`)
	if got := readNFOLink(t, d, "/Movies/2024年/season.nfo"); got != "real-content" {
		t.Errorf("real season.nfo must win, got %q", got)
	}
}

// TestGetAndLinkSeasonEpisode：季内虚拟剧集路径 Get 反查真实文件，Link 还原真实内容。
func TestGetAndLinkSeasonEpisode(t *testing.T) {
	d := setup(t)
	if err := writeDirOrdered(t, "/Movies/2024年"); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := writeEpisodeFile(t, "/Movies/2024年/A1.mp4", "a1"); err != nil {
		t.Fatalf("write A1: %v", err)
	}
	markTVShow(t, d, `{"tv_show":true}`)
	if got := readNFOLink(t, d, "/Movies/2024年/A1-S02E01.mp4"); got != "a1" {
		t.Errorf("episode must play real file content, got %q", got)
	}
}

// TestTVShowNestedTVSkipped：嵌套标记为电视剧的子文件夹独立成剧，父剧索引跳过它。
func TestTVShowNestedTVSkipped(t *testing.T) {
	d := setup(t)
	if err := writeDirOrdered(t, "/Movies/2024年"); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := writeEpisodeFile(t, "/Movies/2024年/A1.mp4", "a1"); err != nil {
		t.Fatalf("write A1: %v", err)
	}
	if err := writeDirOrdered(t, "/Movies/2024年/内嵌剧"); err != nil {
		t.Fatalf("mkdir 内嵌剧: %v", err)
	}
	if err := writeEpisodeFile(t, "/Movies/2024年/内嵌剧/X.mp4", "x"); err != nil {
		t.Fatalf("write X: %v", err)
	}
	markTVShow(t, d, `{"tv_show":true}`)
	markTVShowAt(t, d, "/Movies/2024年/内嵌剧", `{"tv_show":true,"tv_show_name":"内嵌剧名"}`)
	// 内嵌剧自身：独立成剧，季 1
	objs, err := d.List(context.Background(), &model.Object{Name: "内嵌剧", Path: "/Movies/2024年/内嵌剧", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list 内嵌剧: %+v", err)
	}
	if got := sortedNames(objs); !reflect.DeepEqual(got, []string{"X-S01E01.mp4", "X-S01E01.nfo", "tvshow.nfo"}) {
		t.Errorf("nested tv show must be independent, got %v", got)
	}
	// 父剧的季 2：X 不参与编号
	objs, err = d.List(context.Background(), &model.Object{Name: "2024年", Path: "/Movies/2024年", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list 2024年: %+v", err)
	}
	if got := sortedNames(objs); !reflect.DeepEqual(got, []string{"A1-S02E01.mp4", "A1-S02E01.nfo", "season.nfo", "内嵌剧"}) {
		t.Errorf("nested tv must be skipped by parent season, got %v", got)
	}
}

// TestGetVirtualEpisodeNotFound：无匹配的虚拟剧集路径保持 404。
func TestGetVirtualEpisodeNotFound(t *testing.T) {
	d := setup(t)
	markTVShow(t, d, `{"tv_show":true}`)
	if _, err := d.Get(context.Background(), "/Movies/NOPE-S01E01.mp4"); err == nil {
		t.Fatal("unmapped virtual episode must not be served")
	}
}
