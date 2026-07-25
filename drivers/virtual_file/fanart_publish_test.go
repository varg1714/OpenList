package virtual_file

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/cmd/flags"
)

func TestPublishFanartCreatesFanartAtomically(t *testing.T) {
	dataDir := setFanartTestDataDir(t)
	wantContent := []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 'J', 'F', 'I', 'F'}

	result, err := PublishFanart(FanartPublishRequest{
		Source:   "javdb",
		Dir:      "actor",
		FilmName: "ABC-123.jpg",
		Index:    1,
		Content:  wantContent,
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

func TestPublishFanartReportsExistingFile(t *testing.T) {
	setFanartTestDataDir(t)
	wantContent := []byte("existing fanart content")
	path, err := FanartPath("javdb", "actor", "ABC-123.jpg", 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, wantContent, 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := PublishFanart(FanartPublishRequest{
		Source:   "javdb",
		Dir:      "actor",
		FilmName: "ABC-123.jpg",
		Index:    2,
		Content:  []byte("new content"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Existing {
		t.Fatal("Existing = false, want true")
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != string(wantContent) {
		t.Fatalf("content = %q, want %q", content, wantContent)
	}
}

func TestPublishFanartMultipleIndices(t *testing.T) {
	dataDir := setFanartTestDataDir(t)
	contents := []string{"frame one", "frame two", "frame three"}
	for i, c := range contents {
		result, err := PublishFanart(FanartPublishRequest{
			Source:   "pornhub",
			Dir:      "star",
			FilmName: "ABC-123.jpg",
			Index:    i + 1,
			Content:  []byte(c),
		})
		if err != nil {
			t.Fatalf("index %d: %v", i+1, err)
		}
		if result.Existing {
			t.Fatalf("index %d: Existing = true, want false", i+1)
		}
	}

	for i, want := range contents {
		path := filepath.Join(dataDir, "emby", "pornhub", "star", "ABC-123", fmt.Sprintf("fanart%d.jpg", i+1))
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("index %d: %v", i+1, err)
		}
		if string(content) != want {
			t.Fatalf("index %d = %q, want %q", i+1, content, want)
		}
	}
}

func TestPublishFanartRejectsInvalidIndex(t *testing.T) {
	setFanartTestDataDir(t)
	for _, idx := range []int{-1, 0} {
		if _, err := PublishFanart(FanartPublishRequest{
			Source: "javdb", Dir: "actor", FilmName: "ABC-123", Index: idx, Content: []byte("x"),
		}); err == nil {
			t.Errorf("PublishFanart index %d returned nil error", idx)
		}
	}
}

func TestRemoveBackgroundDeletesRegularFile(t *testing.T) {
	paths := setupPosterReplacement(t)
	legacyPoster := paths.LegacyPoster
	if err := os.WriteFile(paths.Background, []byte("regular background"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := RemoveBackground("javdb", "actor", "MIDV-169.mp4"); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Lstat(paths.Background); !os.IsNotExist(err) {
		t.Fatalf("background still exists: %v", err)
	}
	if _, err := os.Lstat(legacyPoster); err != nil {
		t.Fatalf("legacy poster removed: %v", err)
	}
}

func TestRemoveBackgroundDeletesSymlink(t *testing.T) {
	paths := setupPosterReplacement(t)
	legacyPoster := paths.LegacyPoster
	if err := os.Symlink(filepath.Base(legacyPoster), paths.Background); err != nil {
		t.Fatal(err)
	}

	if err := RemoveBackground("javdb", "actor", "MIDV-169.mp4"); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Lstat(paths.Background); !os.IsNotExist(err) {
		t.Fatalf("background symlink still exists: %v", err)
	}
	content, err := os.ReadFile(legacyPoster)
	if err != nil {
		t.Fatalf("legacy poster lost: %v", err)
	}
	if string(content) != "old poster" {
		t.Fatalf("legacy poster content = %q, want old poster", content)
	}
}

func TestRemoveBackgroundTreatsMissingAsSuccess(t *testing.T) {
	paths := setupPosterReplacement(t)
	if err := os.Remove(paths.Background); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}

	if err := RemoveBackground("javdb", "actor", "MIDV-169.mp4"); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveBackgroundNeverRemovesPoster(t *testing.T) {
	paths := setupPosterReplacement(t)
	legacyPoster := paths.LegacyPoster
	if err := os.Symlink(filepath.Base(legacyPoster), paths.Background); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Poster, []byte("canonical poster"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := RemoveBackground("javdb", "actor", "MIDV-169.mp4"); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Lstat(paths.Background); !os.IsNotExist(err) {
		t.Fatalf("background not removed: %v", err)
	}
	assertFileContent(t, paths.Poster, "canonical poster")
	assertFileContent(t, legacyPoster, "old poster")
}

func TestRemoveBackgroundRejectsUnsafeComponents(t *testing.T) {
	dataDir := t.TempDir()
	previous := flags.DataDir
	flags.DataDir = dataDir
	t.Cleanup(func() { flags.DataDir = previous })

	for _, tc := range []struct {
		source, dir, name string
	}{
		{"", "actor", "film"},
		{"javdb", "", "film"},
		{"javdb", "..", "film"},
	} {
		if err := RemoveBackground(tc.source, tc.dir, tc.name); err == nil {
			t.Errorf("RemoveBackground(%q, %q, %q) returned nil error", tc.source, tc.dir, tc.name)
		}
	}
}
