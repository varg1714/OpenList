package virtual_file

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/cmd/flags"
)

func legacyPosterSetup(t *testing.T) PosterPathSet {
	t.Helper()
	dataDir := t.TempDir()
	previous := flags.DataDir
	flags.DataDir = dataDir
	t.Cleanup(func() { flags.DataDir = previous })

	paths, err := PosterPaths("pornhub", "actor", "MIDV-169.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.Poster), 0o755); err != nil {
		t.Fatal(err)
	}
	return paths
}

func TestPromoteLegacyPosterRenamesWhenPosterAbsentAndLegacyRegular(t *testing.T) {
	paths := legacyPosterSetup(t)
	const originalContent = "legacy poster bytes"
	if err := os.WriteFile(paths.LegacyPoster, []byte(originalContent), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := PromoteLegacyPoster("pornhub", "actor", "MIDV-169.mp4"); err != nil {
		t.Fatal(err)
	}

	assertFileContent(t, paths.Poster, originalContent)
	if _, err := os.Lstat(paths.LegacyPoster); !os.IsNotExist(err) {
		t.Fatalf("legacy poster still exists after rename: %v", err)
	}
}

func TestPromoteLegacyPosterNoOpWhenPosterExists(t *testing.T) {
	paths := legacyPosterSetup(t)
	const legacyContent = "legacy poster bytes"
	const posterContent = "existing poster bytes"
	if err := os.WriteFile(paths.LegacyPoster, []byte(legacyContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Poster, []byte(posterContent), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := PromoteLegacyPoster("pornhub", "actor", "MIDV-169.mp4"); err != nil {
		t.Fatal(err)
	}

	assertFileContent(t, paths.Poster, posterContent)
	assertFileContent(t, paths.LegacyPoster, legacyContent)
}

func TestPromoteLegacyPosterNoOpWhenLegacyMissing(t *testing.T) {
	paths := legacyPosterSetup(t)

	if err := PromoteLegacyPoster("pornhub", "actor", "MIDV-169.mp4"); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Lstat(paths.Poster); !os.IsNotExist(err) {
		t.Fatalf("poster unexpectedly created: %v", err)
	}
}

func TestPromoteLegacyPosterErrorWhenLegacyNonRegular(t *testing.T) {
	paths := legacyPosterSetup(t)
	if err := os.Mkdir(paths.LegacyPoster, 0o755); err != nil {
		t.Fatal(err)
	}

	err := PromoteLegacyPoster("pornhub", "actor", "MIDV-169.mp4")
	if err == nil {
		t.Fatal("error = nil, want error for non-regular legacy poster")
	}

	if _, statErr := os.Lstat(paths.LegacyPoster); statErr != nil {
		t.Fatalf("legacy poster altered: %v", statErr)
	}
	if _, statErr := os.Lstat(paths.Poster); !os.IsNotExist(statErr) {
		t.Fatalf("poster unexpectedly created: %v", statErr)
	}
}

func TestPromoteLegacyPosterErrorWhenPosterNonRegular(t *testing.T) {
	paths := legacyPosterSetup(t)
	if err := os.WriteFile(paths.LegacyPoster, []byte("legacy content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(paths.Poster, 0o755); err != nil {
		t.Fatal(err)
	}

	err := PromoteLegacyPoster("pornhub", "actor", "MIDV-169.mp4")
	if err == nil {
		t.Fatal("error = nil, want error for non-regular poster destination")
	}

	assertFileContent(t, paths.LegacyPoster, "legacy content")
}
