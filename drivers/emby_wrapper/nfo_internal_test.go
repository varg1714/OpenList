package emby_wrapper

import (
	"strings"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

func TestPlotFileNameStripsOnlyExtension(t *testing.T) {
	cases := map[string]string{
		"AAA.mkv":              "AAA",
		"BBB.cd1.mkv":          "BBB.cd1",
		"CCC.CD2.mp4":          "CCC.CD2",
		"noext":                "noext",
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

// TestBuildEpisodeNFO：剧集 nfo 根元素 episodedetails，title=集名，保留 actors，无 plot 内容。
func TestBuildEpisodeNFO(t *testing.T) {
	content, err := buildEpisodeNFO("A", &model.EmbyDirSetting{Actors: "演员A"})
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
}

// TestBuildTVShowNFO：剧集级 nfo 根元素 tvshow，title=剧名，plot=剧集介绍，保留 actors。
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

// TestBuildSeasonNFO：季 nfo 根元素 season，含 seasonnumber 与 seasonname。
func TestBuildSeasonNFO(t *testing.T) {
	got := string(buildSeasonNFO(2, "2024年"))
	if !strings.Contains(got, "<season>") {
		t.Errorf("missing season root, got %s", got)
	}
	if !strings.Contains(got, "<seasonnumber>2</seasonnumber>") {
		t.Errorf("missing seasonnumber 2, got %s", got)
	}
	if !strings.Contains(got, "<seasonname><![CDATA[2024年]]></seasonname>") {
		t.Errorf("missing seasonname, got %s", got)
	}
}
