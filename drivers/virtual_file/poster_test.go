package virtual_file

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/cmd/flags"
	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/go-resty/resty/v2"
)

func TestPosterPathsUseCanonicalCacheLayout(t *testing.T) {
	dataDir := t.TempDir()
	previous := flags.DataDir
	flags.DataDir = dataDir
	t.Cleanup(func() { flags.DataDir = previous })

	paths, err := PosterPaths("javdb", "actor", "MIDV-169.mp4")
	if err != nil {
		t.Fatal(err)
	}
	wantDirectory := filepath.Join(dataDir, "emby", "javdb", "actor", "MIDV-169")
	if paths.Poster != filepath.Join(wantDirectory, "poster.jpg") {
		t.Fatalf("poster = %q, want canonical path", paths.Poster)
	}
	if paths.LegacyPoster != filepath.Join(wantDirectory, "MIDV-169.jpg") {
		t.Fatalf("legacy poster = %q, want legacy path", paths.LegacyPoster)
	}
	if paths.Background != filepath.Join(wantDirectory, "MIDV-169-background.jpg") {
		t.Fatalf("background = %q, want canonical path", paths.Background)
	}
}

func TestReplacePosterAtomicallyOverwritesAndRemovesBackgroundSymlink(t *testing.T) {
	paths := setupPosterReplacement(t)
	legacyPoster := paths.LegacyPoster
	if err := os.Symlink(filepath.Base(legacyPoster), paths.Background); err != nil {
		t.Fatal(err)
	}

	result, err := ReplacePoster("javdb", "actor", "MIDV-169.mp4", []byte("new poster"))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Published {
		t.Fatal("Published = false, want true")
	}
	assertFileContent(t, paths.Poster, "new poster")
	if _, err := os.Lstat(legacyPoster); !os.IsNotExist(err) {
		t.Fatalf("legacy poster still exists: %v", err)
	}
	if _, err := os.Lstat(paths.Background); !os.IsNotExist(err) {
		t.Fatalf("background symlink still exists: %v", err)
	}
	assertNoPosterTemps(t, filepath.Dir(paths.Poster))
}

func TestReplacePosterPreservesRegularBackground(t *testing.T) {
	paths := setupPosterReplacement(t)
	legacyPoster := paths.LegacyPoster
	if err := os.WriteFile(paths.Background, []byte("regular background"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := ReplacePoster("javdb", "actor", "MIDV-169.mp4", []byte("new poster"))
	if err != nil || !result.Published {
		t.Fatalf("result = %+v, error = %v", result, err)
	}
	assertFileContent(t, paths.Background, "regular background")
	if _, err := os.Lstat(legacyPoster); !os.IsNotExist(err) {
		t.Fatalf("legacy poster still exists: %v", err)
	}
}

func TestReplacePosterRequiresExistingPoster(t *testing.T) {
	dataDir := t.TempDir()
	previous := flags.DataDir
	flags.DataDir = dataDir
	t.Cleanup(func() { flags.DataDir = previous })

	paths, err := PosterPaths("javdb", "actor", "MIDV-169.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.Poster), 0o755); err != nil {
		t.Fatal(err)
	}

	result, err := ReplacePoster("javdb", "actor", "MIDV-169.mp4", []byte("new poster"))
	if err == nil || result.Published {
		t.Fatalf("result = %+v, error = %v, want missing-poster failure", result, err)
	}
	if _, err := os.Lstat(paths.Poster); !os.IsNotExist(err) {
		t.Fatalf("poster created instead of replaced: %v", err)
	}
	assertNoPosterTemps(t, filepath.Dir(paths.Poster))
}

func TestReplacePosterReportsPostPublishSymlinkRemovalFailure(t *testing.T) {
	paths := setupPosterReplacement(t)
	legacyPoster := paths.LegacyPoster
	if err := os.Symlink(filepath.Base(legacyPoster), paths.Background); err != nil {
		t.Fatal(err)
	}
	previousRemove := removePosterBackgroundSymlink
	removePosterBackgroundSymlink = func(string) error { return errors.New("remove denied") }
	t.Cleanup(func() { removePosterBackgroundSymlink = previousRemove })

	result, err := ReplacePoster("javdb", "actor", "MIDV-169.mp4", []byte("published poster"))
	if err == nil {
		t.Fatal("error = nil, want background removal failure")
	}
	if !result.Published {
		t.Fatal("Published = false after successful rename")
	}
	assertFileContent(t, paths.Poster, "published poster")
	assertFileContent(t, legacyPoster, "old poster")
	if info, statErr := os.Lstat(paths.Background); statErr != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("background symlink not retained after removal failure: %v", statErr)
	}
}

func TestReplacePosterRejectsSymlinkedParentWithoutMutation(t *testing.T) {
	dataDir := t.TempDir()
	outside := t.TempDir()
	previous := flags.DataDir
	flags.DataDir = dataDir
	t.Cleanup(func() { flags.DataDir = previous })
	sourceDirectory := filepath.Join(dataDir, "emby", "javdb")
	if err := os.MkdirAll(sourceDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(sourceDirectory, "actor")); err != nil {
		t.Fatal(err)
	}

	result, err := ReplacePoster("javdb", "actor", "MIDV-169.mp4", []byte("new poster"))
	if err == nil || result.Published {
		t.Fatalf("result = %+v, error = %v, want pre-publish failure", result, err)
	}
	entries, readErr := os.ReadDir(outside)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("outside directory mutated: %v", entries)
	}
}

func TestSynImageAndNfoDoesNotRecreateLegacyArtworkWhenPosterExists(t *testing.T) {
	dataDir := t.TempDir()
	previous := flags.DataDir
	previousClient := base.RestyClient
	flags.DataDir = dataDir
	base.RestyClient = resty.New()
	t.Cleanup(func() {
		flags.DataDir = previous
		base.RestyClient = previousClient
	})

	paths, err := PosterPaths("javdb", "actor", "MIDV-169.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.Poster), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Poster, []byte("DMM poster"), 0o644); err != nil {
		t.Fatal(err)
	}

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		_, _ = response.Write([]byte("legacy poster"))
	}))
	defer server.Close()

	SynImageAndNfo("javdb", "actor", []model.EmbyFileObj{{
		ObjThumb: model.ObjThumb{
			Object:    model.Object{Name: "MIDV-169.mp4"},
			Thumbnail: model.Thumbnail{Thumbnail: server.URL + "/cover.jpg"},
		},
	}})

	if requests.Load() != 0 {
		t.Fatalf("legacy cover requests = %d, want 0", requests.Load())
	}
	assertFileContent(t, paths.Poster, "DMM poster")
	if _, err := os.Lstat(paths.LegacyPoster); !os.IsNotExist(err) {
		t.Fatalf("legacy poster recreated: %v", err)
	}
	if _, err := os.Lstat(paths.Background); !os.IsNotExist(err) {
		t.Fatalf("background symlink recreated: %v", err)
	}
}

func setupPosterReplacement(t *testing.T) PosterPathSet {
	t.Helper()
	dataDir := t.TempDir()
	previous := flags.DataDir
	flags.DataDir = dataDir
	t.Cleanup(func() { flags.DataDir = previous })
	paths, err := PosterPaths("javdb", "actor", "MIDV-169.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.Poster), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.LegacyPoster, []byte("old poster"), 0o644); err != nil {
		t.Fatal(err)
	}
	return paths
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != want {
		t.Fatalf("content at %s = %q, want %q", path, content, want)
	}
}

func assertNoPosterTemps(t *testing.T, directory string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(directory, ".dmm-poster-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary files remain: %v", matches)
	}
}
