package emby_wrapper

import (
	stdpath "path"
	"strings"
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
		{"AAA.mkv", 1, 1, "S01E01.mkv"},
		{"B.mp4", 2, 5, "S02E05.mp4"},
		{"C", 12, 3, "S12E03"},
		{"D.mkv", 1, 100, "S01E100.mkv"},
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
	// Ctime 为零时回退修改时间（名称顺序与之相反，可判别是回退而非名称比较）
	if !byCreateTimeName(obj("B.mp4", time.Time{}, base), obj("A.mp4", time.Time{}, base.Add(time.Hour))) {
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
	idx.addEpisode(real, "/R/A.mp4", "/R", "A-S01E01.mp4")
	e, ok := idx.entry("/R/A-S01E01.mp4")
	if !ok || e.real != real || e.name != "A-S01E01.mp4" || e.path != "/R/A.mp4" {
		t.Errorf("entry must resolve canonical virtual path, got %+v %v", e, ok)
	}
	if _, ok := idx.entry("/r/a-s01e01.mp4"); !ok {
		t.Errorf("entry must be case-insensitive")
	}
	if _, ok := idx.entry("/R/A.mp4"); ok {
		t.Errorf("original real path must not be an entry key")
	}
	if got := strings.TrimSuffix(real.GetName(), stdpath.Ext(real.GetName())); got != "A" {
		t.Errorf("title must be original base name, got %q", got)
	}
	if got := idx.nfoBases["/r/a-s01e01"]; got != "A-S01E01.mp4" {
		t.Errorf("nfo base must map virtual nfo path to virtual name, got %q", got)
	}
	if got, ok := idx.episodeName(real); !ok || got != "A-S01E01.mp4" {
		t.Errorf("episodeName by real path must work, got %q %v", got, ok)
	}
}

// TestTVIndexAddEpisodeCollision：虚拟路径冲突时真实同名条目优先；已有真实同名条目不被覆盖。
func TestTVIndexAddEpisodeCollision(t *testing.T) {
	idx := newTVIndexForTest("/R")
	gen := &model.Object{Name: "A1.mp4", Path: "/R/2024年/A1.mp4", Modified: time.Now()}
	realFile := &model.Object{Name: "S01E01.mp4", Path: "/R/2024年/S01E01.mp4", Modified: time.Now()}
	// 先生成名、后真实同名：真实同名条目占用虚拟路径
	idx.addEpisode(gen, "/R/2024年/A1.mp4", "/R/S01", "S01E01.mp4")
	idx.addEpisode(realFile, "/R/2024年/S01E01.mp4", "/R/S01", "S01E01.mp4")
	e, _ := idx.entry("/R/S01/S01E01.mp4")
	if e.real != realFile {
		t.Errorf("real-named entry must win the virtual path, got %+v", e)
	}
	// 先真实同名、后生成名：不被覆盖
	idx2 := newTVIndexForTest("/R")
	idx2.addEpisode(realFile, "/R/2024年/S01E01.mp4", "/R/S01", "S01E01.mp4")
	idx2.addEpisode(gen, "/R/2024年/A1.mp4", "/R/S01", "S01E01.mp4")
	e, _ = idx2.entry("/R/S01/S01E01.mp4")
	if e.real != realFile {
		t.Errorf("existing real-named entry must not be overwritten, got %+v", e)
	}
}

// newTVIndexForTest 构造空索引（测试辅助，buildTVIndex 是驱动方法）。
func newTVIndexForTest(root string) *tvIndex {
	return &tvIndex{
		root:        root,
		byVirtual:   map[string]tvEntry{},
		nfoBases:    map[string]string{},
		byReal:      map[string]string{},
		seasonNo:    map[string]int{},
		seasonAlias: map[string]string{},
	}
}
