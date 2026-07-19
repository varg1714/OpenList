package virtual_file

import (
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
	wantRoot := filepath.Join(flags.DataDir, "emby", "javdb", "12", "Actor A", "ABP-123")
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
}
