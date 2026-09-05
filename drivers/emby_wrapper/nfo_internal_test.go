package emby_wrapper

import (
	"strings"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

func TestPlotFileNameStripsOnlyExtension(t *testing.T) {
	cases := map[string]string{
		"AAA.mkv":                 "AAA",
		"BBB.cd1.mkv":             "BBB.cd1",
		"CCC.CD2.mp4":             "CCC.CD2",
		"noext":                   "noext",
		"Example Movie Title.mp4": "Example Movie Title",
	}
	for in, want := range cases {
		if got := plotFileName(in); got != want {
			t.Errorf("plotFileName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildPlot(t *testing.T) {
	tf := true
	ff := false
	cases := []struct {
		name     string
		plot     string
		append   *bool
		fileName string
		want     string
	}{
		{name: "append off", plot: "P", append: nil, fileName: "AAA.mkv", want: "P"},
		{name: "append on with plot", plot: "P", append: &tf, fileName: "AAA.mkv", want: "P-AAA"},
		{name: "append on without plot", plot: "", append: &tf, fileName: "AAA.mkv", want: "AAA"},
		{name: "append on keeps cd part", plot: "P", append: &tf, fileName: "BBB.cd1.mkv", want: "P-BBB.cd1"},
		{name: "explicit false", plot: "P", append: &ff, fileName: "AAA.mkv", want: "P"},
	}
	for _, c := range cases {
		if got := buildPlot(c.plot, c.append, c.fileName); got != c.want {
			t.Errorf("%s: buildPlot(%q, %v, %q) = %q, want %q", c.name, c.plot, c.append, c.fileName, got, c.want)
		}
	}
}

// TestBuildEpisodeNFO：剧集 nfo 根元素 episodedetails，title=集名，保留 actors，无 plot 内容；
// aired 零值时不输出 <aired>。
func TestBuildEpisodeNFO(t *testing.T) {
	content, err := buildEpisodeNFO("A", time.Time{}, &model.EmbyDirSetting{Actors: "演员A"})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	got := string(content)
	if !strings.Contains(got, "<episodedetails>") {
		t.Errorf("missing episodedetails root, got %s", got)
	}
	if !strings.Contains(got, "<![CDATA[A]]>") {
		t.Errorf("missing title A, got %s", got)
	}
	if !strings.Contains(got, "<name>演员A</name>") {
		t.Errorf("missing actor, got %s", got)
	}
	if strings.Contains(got, "剧集介绍") {
		t.Errorf("episode nfo must not contain show plot, got %s", got)
	}
	if strings.Contains(got, "<aired>") {
		t.Errorf("zero aired must not render <aired>, got %s", got)
	}
}

// TestBuildEpisodeNFOAired：设置 aired 时剧集 nfo 写入 <aired>YYYY-MM-DD</aired>
// （Emby EpisodeNfoParser 识别为剧集首播日期）。
func TestBuildEpisodeNFOAired(t *testing.T) {
	aired := time.Date(2024, 3, 5, 22, 30, 0, 0, time.Local)
	content, err := buildEpisodeNFO("A", aired, &model.EmbyDirSetting{Actors: "演员A"})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	got := string(content)
	if !strings.Contains(got, "<aired>2024-03-05</aired>") {
		t.Errorf("episode nfo must carry aired date, got %s", got)
	}
	if !strings.Contains(got, "<![CDATA[A]]>") {
		t.Errorf("missing title A, got %s", got)
	}
}

// TestBuildTVShowNFO：剧集级 nfo 根元素 tvshow，title=剧名，plot=剧集介绍，保留 actors；
// 不含 aired（剧级首播日期 <premiered> 未实现，零值不输出）。
func TestBuildTVShowNFO(t *testing.T) {
	content, err := buildTVShowNFO("测试剧", "剧集介绍", &model.EmbyDirSetting{Actors: "演员A"})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	got := string(content)
	if !strings.Contains(got, "<tvshow>") {
		t.Errorf("missing tvshow root, got %s", got)
	}
	if !strings.Contains(got, "<![CDATA[测试剧]]>") {
		t.Errorf("missing show name, got %s", got)
	}
	if !strings.Contains(got, "<![CDATA[剧集介绍]]>") {
		t.Errorf("missing plot, got %s", got)
	}
	if !strings.Contains(got, "<name>演员A</name>") {
		t.Errorf("missing actor, got %s", got)
	}
}

// TestBuildSeasonNFO：季 nfo 根元素 season，含 seasonnumber 与 title（Emby 经 BaseNfoParser 的 title 识别季显示名；seasonname 是 Jellyfin 10.9+ 才支持的字段，Emby 不识别）。
func TestBuildSeasonNFO(t *testing.T) {
	got := string(buildSeasonNFO(2, "2024年"))
	if !strings.Contains(got, "<season>") {
		t.Errorf("missing season root, got %s", got)
	}
	if !strings.Contains(got, "<seasonnumber>2</seasonnumber>") {
		t.Errorf("missing seasonnumber 2, got %s", got)
	}
	if !strings.Contains(got, "<title><![CDATA[2024年]]></title>") {
		t.Errorf("missing season title, got %s", got)
	}
}
