package virtual_file

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/cmd/flags"
	"github.com/go-resty/resty/v2"
)

func TestCacheFanartUsesFanartOneNameAndExactContent(t *testing.T) {
	dataDir := setFanartTestDataDir(t)
	wantContent := []byte{0x00, 0xff, 'j', 'p', 'e', 'g'}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("Referer"); got != "https://example.test/film" {
			t.Errorf("Referer = %q, want film URL", got)
		}
		_, _ = writer.Write(wantContent)
	}))
	t.Cleanup(server.Close)

	result, err := CacheFanart(context.Background(), FanartCacheRequest{
		Source:   "javdb",
		Dir:      "actor",
		FilmName: "ABC-123.jpg",
		Index:    1,
		URL:      server.URL,
		Headers:  map[string]string{"Referer": "https://example.test/film"},
		Client:   resty.New(),
		MaxBytes: int64(len(wantContent)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Existing {
		t.Fatal("Existing = true, want false")
	}

	wantPath := filepath.Join(dataDir, "emby", "javdb", "actor", "ABC-123", "fanart1.jpg")
	content, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != string(wantContent) {
		t.Fatalf("content = %v, want %v", content, wantContent)
	}
}

func TestFanartPathRejectsInvalidIndex(t *testing.T) {
	setFanartTestDataDir(t)
	for _, index := range []int{-1, 0} {
		if _, err := FanartPath("javdb", "actor", "ABC-123", index); err == nil {
			t.Errorf("FanartPath index %d returned nil error", index)
		}
	}
}

func TestFanartPathCanonicalizesLongMultibyteFilmName(t *testing.T) {
	dataDir := setFanartTestDataDir(t)
	longName := strings.Repeat("界", 100) + ".jpg"

	path, err := FanartPath("javdb", "个人收藏", longName, 2)
	if err != nil {
		t.Fatal(err)
	}
	wantDirectory := strings.Repeat("界", 70)
	want := filepath.Join(dataDir, "emby", "javdb", "个人收藏", wantDirectory, "fanart2.jpg")
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	if len(filepath.Base(filepath.Dir(path))) > 255 {
		t.Fatalf("film directory byte length = %d, want <= 255", len(filepath.Base(filepath.Dir(path))))
	}
}

func TestFanartPathRejectsUnsafeComponents(t *testing.T) {
	setFanartTestDataDir(t)
	tests := []struct {
		name     string
		source   string
		dir      string
		filmName string
	}{
		{name: "empty source", source: "", dir: "actor", filmName: "ABC-123"},
		{name: "source traversal", source: "../javdb", dir: "actor", filmName: "ABC-123"},
		{name: "absolute source", source: string(filepath.Separator) + "javdb", dir: "actor", filmName: "ABC-123"},
		{name: "empty dir", source: "javdb", dir: "", filmName: "ABC-123"},
		{name: "dir traversal", source: "javdb", dir: "..", filmName: "ABC-123"},
		{name: "dir separator", source: "javdb", dir: "actor/other", filmName: "ABC-123"},
		{name: "empty film name", source: "javdb", dir: "actor", filmName: ""},
		{name: "film traversal", source: "javdb", dir: "actor", filmName: "../ABC-123"},
		{name: "film backslash", source: "fc2", dir: "个人收藏", filmName: `folder\ABC-123`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := FanartPath(test.source, test.dir, test.filmName, 1); err == nil {
				t.Fatal("FanartPath returned nil error")
			}
		})
	}
}

func TestCacheFanartRejectsSymlinkedActorDirectory(t *testing.T) {
	dataDir := setFanartTestDataDir(t)
	outside := t.TempDir()
	sourceDirectory := filepath.Join(dataDir, "emby", "javdb")
	if err := os.MkdirAll(sourceDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(sourceDirectory, "actor")); err != nil {
		t.Fatal(err)
	}

	_, err := CacheFanart(context.Background(), FanartCacheRequest{
		Source: "javdb", Dir: "actor", FilmName: "ABC-123", Index: 1,
		URL: "https://example.test/fanart.jpg", Client: resty.New(),
	})
	if err == nil {
		t.Fatal("CacheFanart returned nil error")
	}
	if _, statErr := os.Lstat(filepath.Join(outside, "ABC-123")); !os.IsNotExist(statErr) {
		t.Fatalf("film directory created outside cache root: %v", statErr)
	}
}

func TestCacheFanartRecoversExistingFileWithoutRequest(t *testing.T) {
	setFanartTestDataDir(t)
	path, err := FanartPath("fc2", "个人收藏", "FC2-PPV-123.jpg", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	wantContent := []byte("existing fanart")
	if err := os.WriteFile(path, wantContent, 0o644); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = io.WriteString(writer, "replacement")
	}))
	t.Cleanup(server.Close)

	result, err := CacheFanart(context.Background(), FanartCacheRequest{
		Source: "fc2", Dir: "个人收藏", FilmName: "FC2-PPV-123.jpg", Index: 1,
		URL: server.URL, Client: resty.New(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Existing {
		t.Fatal("Existing = false, want true")
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("requests = %d, want 0", got)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != string(wantContent) {
		t.Fatalf("content = %q, want %q", content, wantContent)
	}
}

func setFanartTestDataDir(t *testing.T) string {
	t.Helper()
	previous := flags.DataDir
	dataDir := t.TempDir()
	flags.DataDir = dataDir
	t.Cleanup(func() { flags.DataDir = previous })
	return dataDir
}
