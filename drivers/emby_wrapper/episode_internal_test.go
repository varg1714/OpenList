package emby_wrapper

import (
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

func TestIsNumberedEpisode(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"AAA.mkv", false},
		{"A-S01E01.mp4", true},
		{"A.S01E01.mp4", true},
		{"Show.s01.e02.mkv", true},
		{"Show.s01_e02.mkv", true},
		{"Show.1x02.mkv", true},
		{"S01E01.mkv", true},
		{"A-1080p.mp4", false},
		{"A-B.mp4", false},
	}
	for _, c := range cases {
		if got := isNumberedEpisode(c.name); got != c.want {
			t.Errorf("isNumberedEpisode(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestEpisodeVirtualName(t *testing.T) {
	cases := []struct {
		fileName string
		seasonNo int
		epNo     int
		want     string
	}{
		{"AAA.mkv", 1, 1, "AAA-S01E01.mkv"},
		{"B.mp4", 2, 5, "B-S02E05.mp4"},
		{"C", 12, 3, "C-S12E03"},
		{"D.mkv", 1, 100, "D-S01E100.mkv"},
	}
	for _, c := range cases {
		if got := episodeVirtualName(c.fileName, c.seasonNo, c.epNo); got != c.want {
			t.Errorf("episodeVirtualName(%q, %d, %d) = %q, want %q", c.fileName, c.seasonNo, c.epNo, got, c.want)
		}
	}
}

func TestByCreateTimeName(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	obj := func(name string, ctime, mtime time.Time) model.Obj {
		return &model.Object{Name: name, Path: "/dir/" + name, Modified: mtime, Ctime: ctime}
	}
	// 创建时间优先
	if !byCreateTimeName(obj("A.mp4", base, base), obj("B.mp4", base.Add(time.Hour), base.Add(2*time.Hour))) {
		t.Error("older ctime must sort first")
	}
	// Ctime 为零时回退修改时间
	if !byCreateTimeName(obj("A.mp4", time.Time{}, base), obj("B.mp4", time.Time{}, base.Add(time.Hour))) {
		t.Error("zero ctime must fall back to mtime")
	}
	// 时间相同按名称升序
	if !byCreateTimeName(obj("A.mp4", base, base), obj("B.mp4", base, base)) {
		t.Error("same time must tie-break by name asc")
	}
	if byCreateTimeName(obj("B.mp4", base, base), obj("A.mp4", base, base)) {
		t.Error("name tie-break must be asc")
	}
}

func TestTVIndexAddAndResolve(t *testing.T) {
	idx := newTVIndexForTest("/R")
	real := &model.Object{Name: "A.mp4", Path: "/R/A.mp4", Modified: time.Now()}
	idx.addEpisode(real, "/R/A.mp4", "A-S01E01.mp4")
	if got := idx.resolve("A-S01E01.mp4"); got != real {
		t.Errorf("resolve must return the real object, got %v", got)
	}
	if got := idx.resolve("a-s01e01.mp4"); got != real {
		t.Errorf("resolve must be case-insensitive, got %v", got)
	}
	if got := idx.resolve("A.mp4"); got != nil {
		t.Errorf("original name must not resolve, got %v", got)
	}
	if got := idx.titles["a-s01e01.mp4"]; got != "A" {
		t.Errorf("title must be original base name, got %q", got)
	}
	if got := idx.nfoBases["a-s01e01"]; got != "A-S01E01.mp4" {
		t.Errorf("nfo base must map to virtual name, got %q", got)
	}
	if got, ok := idx.episodeName(real); !ok || got != "A-S01E01.mp4" {
		t.Errorf("episodeName by real path must work, got %q %v", got, ok)
	}
}

// newTVIndexForTest 构造空索引（测试辅助，Task 4 的 buildTVIndex 是驱动方法）。
func newTVIndexForTest(root string) *tvIndex {
	return &tvIndex{
		root:      root,
		byVirtual: map[string]model.Obj{},
		titles:    map[string]string{},
		names:     map[string]string{},
		nfoBases:  map[string]string{},
		byReal:    map[string]string{},
		seasonNo:  map[string]int{},
	}
}
