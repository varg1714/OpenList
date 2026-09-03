package emby_wrapper_test

import (
	"context"
	"io"
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
	"github.com/OpenListTeam/OpenList/v4/pkg/http_range"
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
	if got := sortedNames(objs); !reflect.DeepEqual(got, []string{"S01E01.mkv", "S01E01.nfo", "S02", "S03", "tvshow.nfo"}) {
		t.Errorf("root listing mismatch, got %v", got)
	}
	// 季 2（2024年）：A1 先建 → E01；含 season.nfo
	objs, err = d.List(context.Background(), &model.Object{Name: "S02", Path: "/Movies/S02", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list 2024年: %+v", err)
	}
	if got := sortedNames(objs); !reflect.DeepEqual(got, []string{"S02E01.mp4", "S02E01.nfo", "S02E02.mp4", "S02E02.nfo", "season.nfo"}) {
		t.Errorf("season 2 listing mismatch, got %v", got)
	}
	// 季 3（2025年）
	objs, err = d.List(context.Background(), &model.Object{Name: "S03", Path: "/Movies/S03", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list 2025年: %+v", err)
	}
	if got := sortedNames(objs); !reflect.DeepEqual(got, []string{"S03E01.mp4", "S03E01.nfo", "season.nfo"}) {
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
	objs, err := d.List(context.Background(), &model.Object{Name: "S01", Path: "/Movies/S01", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list: %+v", err)
	}
	if got := sortedNames(objs); !reflect.DeepEqual(got, []string{"S01E01.mp4", "S01E01.nfo", "season.nfo"}) {
		t.Errorf("no-root-videos season must start at 1, got %v", got)
	}
}

// TestTVShowNestedFolderInSeason：季内嵌套子文件夹的文件提取（扁平化）到季目录——
// 不再保留嵌套文件夹，全部条目按创建时间统一编号后展示于季别名目录下。
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
	// C 先建 → 季 2 的 E01；D 后建 → E02；两者都提取到 S02 目录下（专题 不再展示）
	objs, err := d.List(context.Background(), &model.Object{Name: "S02", Path: "/Movies/S02", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list S02: %+v", err)
	}
	if got := sortedNames(objs); !reflect.DeepEqual(got, []string{"S02E01.mp4", "S02E01.nfo", "S02E02.mp4", "S02E02.nfo", "season.nfo"}) {
		t.Errorf("nested files must be flattened into the season, got %v", got)
	}
	// 扁平化后季名（title）仍用季文件夹本身的名字（2024年），而非子文件夹名
	if got := readNFOLink(t, d, "/Movies/S02/season.nfo"); !strings.Contains(got, "<title><![CDATA[2024年]]></title>") {
		t.Errorf("season title must be the season folder name despite flattening, got %s", got)
	}
}

// TestTVShowSeasonNFOContent：season.nfo 含分配的季号与原季文件夹名（Emby 季显示名）。
func TestTVShowSeasonNFOContent(t *testing.T) {
	d := setup(t)
	if err := writeDirOrdered(t, "/Movies/2024年"); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := writeEpisodeFile(t, "/Movies/2024年/A1.mp4", "a1"); err != nil {
		t.Fatalf("write A1: %v", err)
	}
	markTVShow(t, d, `{"tv_show":true}`)
	got := readNFOLink(t, d, "/Movies/S02/season.nfo")
	if !strings.Contains(got, "<seasonnumber>2</seasonnumber>") {
		t.Errorf("season.nfo must carry assigned season number, got %s", got)
	}
	if !strings.Contains(got, "<title><![CDATA[2024年]]></title>") {
		t.Errorf("season.nfo must carry original folder name as title, got %s", got)
	}
}

// TestTVShowNFOsContent：剧集 nfo（episodedetails、title=原名、actors、plot=影片名）与 tvshow.nfo（剧名+简介）。
func TestTVShowNFOsContent(t *testing.T) {
	d := setup(t)
	if err := writeDirOrdered(t, "/Movies/2024年"); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := writeEpisodeFile(t, "/Movies/2024年/A1.mp4", "a1"); err != nil {
		t.Fatalf("write A1: %v", err)
	}
	markTVShow(t, d, `{"tv_show":true,"tv_show_name":"测试剧","plot":"剧集介绍","actors":"演员A"}`)
	ep := readNFOLink(t, d, "/Movies/S02/S02E01.nfo")
	if !strings.Contains(ep, "<episodedetails>") {
		t.Errorf("episode nfo must use episodedetails root, got %s", ep)
	}
	if !strings.Contains(ep, "<![CDATA[A1]]>") {
		t.Errorf("episode nfo title must be original base name, got %s", ep)
	}
	if !strings.Contains(ep, "<plot><![CDATA[A1]]></plot>") {
		t.Errorf("episode nfo plot must carry movie name, got %s", ep)
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
	objs, err := d.List(context.Background(), &model.Object{Name: "S02", Path: "/Movies/S02", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list: %+v", err)
	}
	if got := sortedNames(objs); !reflect.DeepEqual(got, []string{"S02E01.mp4", "S02E01.nfo", "Show.S01E03.mkv", "Show.S01E03.nfo", "season.nfo"}) {
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
	// 真实季目录下的同名 season.nfo 优先于虚拟生成（别名路径请求应读到真实内容）
	if err := writeDownstreamFile(t, "/Movies/2024年/season.nfo", "real-content"); err != nil {
		t.Fatalf("write real season.nfo: %v", err)
	}
	markTVShow(t, d, `{"tv_show":true}`)
	if got := readNFOLink(t, d, "/Movies/S02/season.nfo"); got != "real-content" {
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
	if got := readNFOLink(t, d, "/Movies/S02/S02E01.mp4"); got != "a1" {
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
	if got := sortedNames(objs); !reflect.DeepEqual(got, []string{"S01E01.mp4", "S01E01.nfo", "tvshow.nfo"}) {
		t.Errorf("nested tv show must be independent, got %v", got)
	}
	// 父剧的季 2：X 不参与编号
	objs, err = d.List(context.Background(), &model.Object{Name: "S02", Path: "/Movies/S02", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list 2024年: %+v", err)
	}
	if got := sortedNames(objs); !reflect.DeepEqual(got, []string{"S02E01.mp4", "S02E01.nfo", "season.nfo", "内嵌剧"}) {
		t.Errorf("nested tv must be skipped by parent season, got %v", got)
	}
	// 通过季别名导航进嵌套 TV（Emby 实际扫描路径）：独立成剧，文件可 Get/Link
	objs, err = d.List(context.Background(), &model.Object{Name: "内嵌剧", Path: "/Movies/S02/内嵌剧", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list nested tv via alias: %+v", err)
	}
	if got := sortedNames(objs); !reflect.DeepEqual(got, []string{"S01E01.mp4", "S01E01.nfo", "tvshow.nfo"}) {
		t.Errorf("nested tv via alias must be independent, got %v", got)
	}
	if got := readNFOLink(t, d, "/Movies/S02/内嵌剧/S01E01.mp4"); got != "x" {
		t.Errorf("nested episode via alias must link real file, got %q", got)
	}
	if got := readNFOLink(t, d, "/Movies/S02/内嵌剧/tvshow.nfo"); !strings.Contains(got, "内嵌剧名") {
		t.Errorf("nested tvshow.nfo via alias must carry show name, got %s", got)
	}
}

// TestTVShowDuplicateRealNamesInSeason：扁平化后不同嵌套子目录的同名文件
// （非视频、已编号视频）不丢弃，后者映射消解名（原名-2.扩展名）保持可见。
func TestTVShowDuplicateRealNamesInSeason(t *testing.T) {
	d := setup(t)
	if err := writeDirOrdered(t, "/Movies/2024年"); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := writeDirOrdered(t, "/Movies/2024年/正片"); err != nil {
		t.Fatalf("mkdir 正片: %v", err)
	}
	if err := writeEpisodeFile(t, "/Movies/2024年/正片/Show.S01E03.mkv", "ep-a"); err != nil {
		t.Fatalf("write ep-a: %v", err)
	}
	if err := writeDownstreamFile(t, "/Movies/2024年/正片/poster.jpg", "poster-a"); err != nil {
		t.Fatalf("write poster-a: %v", err)
	}
	if err := writeDirOrdered(t, "/Movies/2024年/花絮"); err != nil {
		t.Fatalf("mkdir 花絮: %v", err)
	}
	if err := writeEpisodeFile(t, "/Movies/2024年/花絮/Show.S01E03.mkv", "ep-b"); err != nil {
		t.Fatalf("write ep-b: %v", err)
	}
	if err := writeDownstreamFile(t, "/Movies/2024年/花絮/poster.jpg", "poster-b"); err != nil {
		t.Fatalf("write poster-b: %v", err)
	}
	markTVShow(t, d, `{"tv_show":true}`)
	objs, err := d.List(context.Background(), &model.Object{Name: "S02", Path: "/Movies/S02", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list S02: %+v", err)
	}
	if got := sortedNames(objs); !reflect.DeepEqual(got, []string{
		"Show.S01E03-2.mkv", "Show.S01E03-2.nfo", "Show.S01E03.mkv", "Show.S01E03.nfo",
		"poster-2.jpg", "poster.jpg", "season.nfo",
	}) {
		t.Errorf("duplicate real names must be disambiguated, not dropped, got %v", got)
	}
	// 消解名可 Get/Link：映射到后创建的文件
	if got := readNFOLink(t, d, "/Movies/S02/Show.S01E03-2.mkv"); got != "ep-b" {
		t.Errorf("disambiguated episode must link its own file, got %q", got)
	}
	if got := readNFOLink(t, d, "/Movies/S02/poster-2.jpg"); got != "poster-b" {
		t.Errorf("disambiguated non-video must link its own file, got %q", got)
	}
	// 首个（先创建）保持原名
	if got := readNFOLink(t, d, "/Movies/S02/Show.S01E03.mkv"); got != "ep-a" {
		t.Errorf("first episode must keep original name, got %q", got)
	}
	if got := readNFOLink(t, d, "/Movies/S02/poster.jpg"); got != "poster-a" {
		t.Errorf("first non-video must keep original name, got %q", got)
	}
}

// TestTVShowListIndexFailureNoPlainFallback：TV 树内索引构建失败（如 remote 上游
// 某目录 List 报错）时 List 返回错误，绝不降级为未映射的原始列表（否则上层/strm
// 会看到 2024年 等真实名并按错误结构更新落盘）。
func TestTVShowListIndexFailureNoPlainFallback(t *testing.T) {
	d := setup(t)
	if err := writeDirOrdered(t, "/Movies/2024年"); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := writeEpisodeFile(t, "/Movies/2024年/A1.mp4", "a1"); err != nil {
		t.Fatalf("write A1: %v", err)
	}
	if err := writeDirOrdered(t, "/Movies/2024年/专题"); err != nil {
		t.Fatalf("mkdir 专题: %v", err)
	}
	if err := writeEpisodeFile(t, "/Movies/2024年/专题/D.mp4", "d"); err != nil {
		t.Fatalf("write D: %v", err)
	}
	locked := filepath.Join(localRoot, "Movies", "2024年", "专题")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	markTVShow(t, d, `{"tv_show":true}`)
	// 索引遍历 专题 时 List 失败 → tvContext 报错；TV 树内必须返回错误而非降级原始列表
	if _, err := d.List(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, model.ListArgs{Refresh: true}); err == nil {
		t.Error("tv tree list must fail when index build fails (no plain fallback)")
	}
	_ = os.Chmod(locked, 0o755)
}

// TestGetVirtualEpisodeNotFound：无匹配的虚拟剧集路径保持 404。
func TestGetVirtualEpisodeNotFound(t *testing.T) {
	d := setup(t)
	markTVShow(t, d, `{"tv_show":true}`)
	if _, err := d.Get(context.Background(), "/Movies/S99E99.mp4"); err == nil {
		t.Fatal("unmapped virtual episode must not be served")
	}
}

// TestGetVirtualEpisodeWrongDirectory：虚拟名必须限定在所属目录内解析——
// 根目录文件 AAA.mkv 的虚拟名 S01E01.mkv 在其他季目录下请求必须 404。
func TestGetVirtualEpisodeWrongDirectory(t *testing.T) {
	d := setup(t)
	if err := writeDirOrdered(t, "/Movies/2024年"); err != nil {
		t.Fatalf("mkdir 2024年: %v", err)
	}
	if err := writeEpisodeFile(t, "/Movies/2024年/A1.mp4", "a1"); err != nil {
		t.Fatalf("write A1: %v", err)
	}
	markTVShow(t, d, `{"tv_show":true}`)
	// AAA.mkv 位于剧集根（季 1），其虚拟名在季 2 目录下不可解析
	if _, err := d.Get(context.Background(), "/Movies/S02/S01E01.mkv"); err == nil {
		t.Fatal("virtual episode under the wrong directory must not be served")
	}
	if _, err := d.Get(context.Background(), "/Movies/2024年/S01E01.nfo"); err == nil {
		t.Fatal("virtual episode nfo under the wrong directory must not be served")
	}
}

// TestTVShowDuplicateVirtualNameAcrossSeasons：不同季生成相同虚拟名（A1.mp4 →
// S02E01.mp4 与真实编号文件 S02E01.mp4）时，播放与 nfo 都必须限定在请求目录内。
func TestTVShowDuplicateVirtualNameAcrossSeasons(t *testing.T) {
	d := setup(t)
	if err := writeDirOrdered(t, "/Movies/2024年"); err != nil {
		t.Fatalf("mkdir 2024年: %v", err)
	}
	if err := writeEpisodeFile(t, "/Movies/2024年/A1.mp4", "a1"); err != nil {
		t.Fatalf("write A1: %v", err)
	}
	if err := writeDirOrdered(t, "/Movies/2025年"); err != nil {
		t.Fatalf("mkdir 2025年: %v", err)
	}
	// 真实编号文件保留原名，与季 2 的虚拟名相同（重名跨季冲突）
	if err := writeEpisodeFile(t, "/Movies/2025年/S02E01.mp4", "real"); err != nil {
		t.Fatalf("write numbered: %v", err)
	}
	markTVShow(t, d, `{"tv_show":true}`)
	// 季 2 的虚拟剧集播放它自己的文件，而非季 3 的同名真实文件
	if got := readNFOLink(t, d, "/Movies/S02/S02E01.mp4"); got != "a1" {
		t.Errorf("season 2 virtual episode must play its own file, got %q", got)
	}
	// 季 2 的剧集 nfo 标题来自 A1.mp4，而非季 3 的文件名
	if got := readNFOLink(t, d, "/Movies/S02/S02E01.nfo"); !strings.Contains(got, "<![CDATA[A1]]>") {
		t.Errorf("season 2 episode nfo title must come from A1.mp4, got %s", got)
	}
	// 季 3 的真实文件直接播放
	if got := readNFOLink(t, d, "/Movies/S03/S02E01.mp4"); got != "real" {
		t.Errorf("real numbered file must play directly, got %q", got)
	}
	// List 路径：withTVShowNFOs 为季 2 列表构建的剧集 nfo 内容，标题必须来自 A1.mp4
	objs, err := d.List(context.Background(), &model.Object{Name: "S02", Path: "/Movies/S02", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list 2024年: %+v", err)
	}
	var epNFO model.Obj
	for _, o := range objs {
		if o.GetName() == "S02E01.nfo" {
			epNFO = o
			break
		}
	}
	if epNFO == nil {
		t.Fatal("S02E01.nfo must appear in season 2 listing")
	}
	link, err := d.Link(context.Background(), epNFO, model.LinkArgs{})
	if err != nil {
		t.Fatalf("link listed nfo: %+v", err)
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
	if !strings.Contains(string(body), "<![CDATA[A1]]>") {
		t.Errorf("listed season 2 episode nfo title must come from A1.mp4, got %s", body)
	}
	if strings.Contains(string(body), "<![CDATA[A1-S02E01]]>") {
		t.Errorf("listed season 2 episode nfo title must not be the colliding name, got %s", body)
	}
}

// TestTVShowSubfoldersDynamicInheritance：父目录开选项后直接子文件夹自动成剧
// （剧集+tvshow.nfo，剧名回退文件夹名）；父目录自身列表正常；后新增子文件夹同样自动生效。
func TestTVShowSubfoldersDynamicInheritance(t *testing.T) {
	d := setup(t)
	if err := writeDirOrdered(t, "/Movies/演员"); err != nil {
		t.Fatalf("mkdir 演员: %v", err)
	}
	if err := writeEpisodeFile(t, "/Movies/演员/剧1/A1.mp4", "a1"); err != nil {
		t.Fatalf("write A1: %v", err)
	}
	if err := d.Rename(context.Background(), &model.Object{Name: "演员", Path: "/Movies/演员", IsFolder: true}, `{"tv_show_subfolders":true}`); err != nil {
		t.Fatalf("enable subfolders option: %+v", err)
	}
	// 剧1 自动成剧：第 1 季 + tvshow.nfo
	objs, err := d.List(context.Background(), &model.Object{Name: "剧1", Path: "/Movies/演员/剧1", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list 剧1: %+v", err)
	}
	if got := sortedNames(objs); !reflect.DeepEqual(got, []string{"S01E01.mp4", "S01E01.nfo", "tvshow.nfo"}) {
		t.Errorf("subfolder must auto-become a tv show, got %v", got)
	}
	show := readNFOLink(t, d, "/Movies/演员/剧1/tvshow.nfo")
	if !strings.Contains(show, "<![CDATA[剧1]]>") {
		t.Errorf("inherited show name must be folder name, got %s", show)
	}
	// 父目录自身：不是剧，列表正常
	objs, err = d.List(context.Background(), &model.Object{Name: "演员", Path: "/Movies/演员", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list 演员: %+v", err)
	}
	if got := sortedNames(objs); !reflect.DeepEqual(got, []string{"剧1"}) {
		t.Errorf("option holder itself must list normally, got %v", got)
	}
	// 动态性：后新增子文件夹自动生效
	if err := writeDirOrdered(t, "/Movies/演员/剧2"); err != nil {
		t.Fatalf("mkdir 剧2: %v", err)
	}
	if err := writeEpisodeFile(t, "/Movies/演员/剧2/B1.mp4", "b1"); err != nil {
		t.Fatalf("write B1: %v", err)
	}
	objs, err = d.List(context.Background(), &model.Object{Name: "剧2", Path: "/Movies/演员/剧2", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list 剧2: %+v", err)
	}
	if got := sortedNames(objs); !reflect.DeepEqual(got, []string{"S01E01.mp4", "S01E01.nfo", "tvshow.nfo"}) {
		t.Errorf("new subfolder must auto-become a show, got %v", got)
	}
}

// TestTVShowSubfoldersExcludedFromSeasons：父目录同时标记为电视剧时，
// 继承成剧的直接子文件夹从季分配中排除（各自独立成剧），父剧只保留自身文件与 tvshow.nfo。
func TestTVShowSubfoldersExcludedFromSeasons(t *testing.T) {
	d := setup(t)
	if err := writeDirOrdered(t, "/Movies/演员"); err != nil {
		t.Fatalf("mkdir 演员: %v", err)
	}
	if err := writeEpisodeFile(t, "/Movies/演员/剧1/A1.mp4", "a1"); err != nil {
		t.Fatalf("write A1: %v", err)
	}
	markTVShowAt(t, d, "/Movies/演员", `{"tv_show":true,"tv_show_subfolders":true}`)
	// 剧1 继承为剧 → 从 演员 的季分配排除 → 无季文件夹，只有 剧1 文件夹 + tvshow.nfo
	objs, err := d.List(context.Background(), &model.Object{Name: "演员", Path: "/Movies/演员", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list 演员: %+v", err)
	}
	if got := sortedNames(objs); !reflect.DeepEqual(got, []string{"tvshow.nfo", "剧1"}) {
		t.Errorf("inherited subfolders must be excluded from seasons, got %v", got)
	}
	// 剧1 自身独立成剧
	objs, err = d.List(context.Background(), &model.Object{Name: "剧1", Path: "/Movies/演员/剧1", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list 剧1: %+v", err)
	}
	if got := sortedNames(objs); !reflect.DeepEqual(got, []string{"S01E01.mp4", "S01E01.nfo", "tvshow.nfo"}) {
		t.Errorf("剧1 must be its own show, got %v", got)
	}
}

// TestFolderAdditionReflectsInheritedTVShow：List 的文件夹 addition 反映继承后的生效状态。
func TestFolderAdditionReflectsInheritedTVShow(t *testing.T) {
	d := setup(t)
	if err := writeDirOrdered(t, "/Movies/演员"); err != nil {
		t.Fatalf("mkdir 演员: %v", err)
	}
	if err := writeEpisodeFile(t, "/Movies/演员/剧1/A1.mp4", "a1"); err != nil {
		t.Fatalf("write A1: %v", err)
	}
	if err := d.Rename(context.Background(), &model.Object{Name: "演员", Path: "/Movies/演员", IsFolder: true}, `{"tv_show_subfolders":true}`); err != nil {
		t.Fatalf("enable option: %+v", err)
	}
	objs, err := d.List(context.Background(), &model.Object{Name: "演员", Path: "/Movies/演员", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list: %+v", err)
	}
	for _, o := range objs {
		if o.GetName() != "剧1" {
			continue
		}
		add, ok := o.(model.ObjAdditional)
		if !ok {
			t.Fatal("folder must expose additional")
		}
		fa, ok := add.GetAddition().(emby_wrapper.FolderAddition)
		if !ok {
			t.Fatalf("unexpected addition type %T", add.GetAddition())
		}
		if fa.TvShow == nil || !*fa.TvShow {
			t.Error("inherited tv_show must be reflected in addition")
		}
		return
	}
	t.Fatal("剧1 folder not found")
}
