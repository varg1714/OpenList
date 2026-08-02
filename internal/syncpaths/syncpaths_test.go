package syncpaths

import (
	"slices"
	"testing"
)

func TestParseSyncPaths(t *testing.T) {
	cases := []struct {
		raw  string
		want []string
	}{
		{"", nil},
		{"   \n  ", nil},
		{"..", []string{"/"}},
		{"/", []string{"/"}},
		{"/sub", []string{"/sub"}},
		{"sub", []string{"/sub"}},
		{"/sub\n/sub2", []string{"/sub", "/sub2"}},
		{"/a,/b\n/c", []string{"/a", "/b", "/c"}},
		{"/a\n/a,/b", []string{"/a", "/b"}},
	}
	for _, c := range cases {
		if got := ParseSyncPaths(c.raw); !slices.Equal(got, c.want) {
			t.Errorf("ParseSyncPaths(%q) = %v, want %v", c.raw, got, c.want)
		}
	}
}

func TestWithinSyncPaths(t *testing.T) {
	entries := []string{"/movies", "/tv"}
	cases := []struct {
		path string
		want bool
	}{
		{"/movies", true},
		{"/movies/2024", true},
		{"/movies2", false},
		{"/tv/series/a", true},
		{"/tvx", false},
		{"/", false},
		{"/other", false},
	}
	for _, c := range cases {
		if got := WithinSyncPaths(c.path, entries); got != c.want {
			t.Errorf("WithinSyncPaths(%q) = %v, want %v", c.path, got, c.want)
		}
	}
	if !WithinSyncPaths("/a/b", []string{"/"}) {
		t.Errorf("root entry must match everything")
	}
}

func TestToRelEntries(t *testing.T) {
	cases := []struct {
		raw     string
		actual  string
		want    []string
		enabled bool
	}{
		{"", "/", nil, false},
		{"/sub", "/", []string{"/sub"}, true},
		{"/sub\n/missing", "/", []string{"/sub", "/missing"}, true},
		{"..", "/", []string{"/"}, true},
		{"/sub", "/other", nil, true},
		{"/local/sub", "/local", []string{"/sub"}, true},
	}
	for _, c := range cases {
		got, enabled := ToRelEntries(c.actual, c.raw)
		if enabled != c.enabled || !slices.Equal(got, c.want) {
			t.Errorf("ToRelEntries(%q, %q) = (%v, %v), want (%v, %v)", c.raw, c.actual, got, enabled, c.want, c.enabled)
		}
	}
}

func TestDirDepth(t *testing.T) {
	cases := map[string]int{"/": 0, "/a": 1, "/a/b": 2, "/a/b/c": 3}
	for path, want := range cases {
		if got := DirDepth(path); got != want {
			t.Errorf("DirDepth(%q) = %d, want %d", path, got, want)
		}
	}
}
