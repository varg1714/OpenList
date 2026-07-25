package virtual_file

import (
	"image/color"
	"os"
	"path/filepath"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/cmd/flags"
)

func TestMediaPosterAndFanartPathsUseWorkPrimaryDirAndCode(t *testing.T) {
	previous := flags.DataDir
	flags.DataDir = t.TempDir()
	t.Cleanup(func() { flags.DataDir = previous })

	identity := MediaIdentity{StorageID: 12, Source: "javdb", PrimaryDir: "Actor A", Code: "ABP-123"}
	paths, err := ResolveMediaArtifactPaths(identity)
	if err != nil {
		t.Fatalf("resolve artifact paths: %v", err)
	}
	wantRoot := filepath.Join(flags.DataDir, "emby", "javdb", "Actor A", "ABP-123")
	if paths.Root != wantRoot || paths.Poster != filepath.Join(wantRoot, "poster.jpg") || paths.Background != filepath.Join(wantRoot, "ABP-123-background.jpg") {
		t.Fatalf("artifact paths = %+v, want root %q", paths, wantRoot)
	}
	fanart, err := MediaFanartPath(identity, 2)
	if err != nil {
		t.Fatalf("resolve fanart: %v", err)
	}
	if fanart != filepath.Join(wantRoot, "fanart2.jpg") {
		t.Fatalf("fanart = %q", fanart)
	}

	otherStorage, err := ResolveMediaArtifactPaths(MediaIdentity{StorageID: 99, Source: "javdb", PrimaryDir: "Actor A", Code: "ABP-123"})
	if err != nil {
		t.Fatalf("resolve artifact paths for other storage: %v", err)
	}
	if otherStorage.Root != paths.Root {
		t.Fatalf("storage-independent roots = %q and %q", paths.Root, otherStorage.Root)
	}
}

func TestPromoteLandscapeMediaFanartUsesIdentityPaths(t *testing.T) {
	setFanartTestDataDir(t)
	identity := MediaIdentity{StorageID: 12, Source: "javdb", PrimaryDir: "Actor A", Code: "ABP-123"}
	first, err := MediaFanartPath(identity, 1)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := MediaFanartPath(identity, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(first), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFanartJPEG(t, first, 400, 600, color.RGBA{R: 255, A: 255})
	writeFanartJPEG(t, candidate, 600, 400, color.RGBA{B: 255, A: 255})

	promoted, err := PromoteLandscapeMediaFanart(identity, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !promoted {
		t.Fatal("PromoteLandscapeMediaFanart promoted = false, want true")
	}
	assertFanartDimensions(t, first, 600, 400)
	assertFanartDimensions(t, candidate, 400, 600)
}

func TestRecoverMediaFanartSwapUsesIdentityPaths(t *testing.T) {
	setFanartTestDataDir(t)
	identity := MediaIdentity{StorageID: 12, Source: "javdb", PrimaryDir: "Actor A", Code: "ABP-123"}
	first, err := MediaFanartPath(identity, 1)
	if err != nil {
		t.Fatal(err)
	}
	second, err := MediaFanartPath(identity, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(first), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(first, []byte("portrait"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("landscape"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldTemp, newTemp := fanartSwapTempPaths(first, second)
	if err := os.Link(first, oldTemp); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(second, newTemp); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(newTemp, first); err != nil {
		t.Fatal(err)
	}

	if err := RecoverMediaFanartSwap(identity, 1, 2); err != nil {
		t.Fatal(err)
	}
	assertFanartContent(t, first, "landscape")
	assertFanartContent(t, second, "portrait")
}

func TestPublishMediaFanartUsesIdentityPath(t *testing.T) {
	setFanartTestDataDir(t)
	identity := MediaIdentity{StorageID: 12, Source: "pornhub", PrimaryDir: "Actor A", Code: "view-key"}
	path, err := MediaFanartPath(identity, 1)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := PublishMediaFanart(identity, 1, []byte("frame")); err != nil {
		t.Fatal(err)
	}
	assertFanartContent(t, path, "frame")
}

func TestRemoveMediaBackgroundUsesIdentityPath(t *testing.T) {
	setFanartTestDataDir(t)
	identity := MediaIdentity{StorageID: 12, Source: "pornhub", PrimaryDir: "Actor A", Code: "view-key"}
	paths, err := ResolveMediaArtifactPaths(identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.Root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Background, []byte("background"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := RemoveMediaBackground(identity); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(paths.Background); !os.IsNotExist(err) {
		t.Fatalf("background retained: %v", err)
	}
}

func TestPromoteLegacyMediaPosterUsesIdentityPath(t *testing.T) {
	setFanartTestDataDir(t)
	identity := MediaIdentity{StorageID: 12, Source: "pornhub", PrimaryDir: "Actor A", Code: "view-key"}
	paths, err := ResolveMediaArtifactPaths(identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.Root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.LegacyPoster, []byte("poster"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := PromoteLegacyMediaPoster(identity); err != nil {
		t.Fatal(err)
	}
	assertFanartContent(t, paths.Poster, "poster")
}
