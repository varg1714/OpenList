package virtual_file

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/OpenListTeam/OpenList/v4/cmd/flags"
)

type PosterPathSet struct {
	Poster       string
	LegacyPoster string
	Background   string
}

type PosterPublishResult struct {
	Published bool
}

// PosterReplaceResult is kept for source compatibility.
// Deprecated: use PosterPublishResult.
type PosterReplaceResult = PosterPublishResult

var removePosterBackgroundSymlink = os.Remove

func PosterPaths(source, dir, filmName string) (PosterPathSet, error) {
	if err := validateFanartPathComponent("source", source); err != nil {
		return PosterPathSet{}, err
	}
	if err := validateFanartPathComponent("directory", dir); err != nil {
		return PosterPathSet{}, err
	}
	if err := validateFanartPathComponent("film name", filmName); err != nil {
		return PosterPathSet{}, err
	}

	canonicalName := CutString(ClearFilmName(filmName))
	if err := validateFanartPathComponent("canonical film name", canonicalName); err != nil {
		return PosterPathSet{}, err
	}
	posterName := AppendImageName(canonicalName)
	filmDirectory := GetRealName(posterName)
	if err := validateFanartPathComponent("film directory", filmDirectory); err != nil {
		return PosterPathSet{}, err
	}

	root, err := filepath.Abs(filepath.Join(flags.DataDir, "emby", source))
	if err != nil {
		return PosterPathSet{}, err
	}
	directory := filepath.Join(root, dir, filmDirectory)
	paths := PosterPathSet{
		Poster:       filepath.Join(directory, "poster.jpg"),
		LegacyPoster: filepath.Join(directory, posterName),
		Background:   filepath.Join(directory, strings.TrimSuffix(posterName, filepath.Ext(posterName))+"-background.jpg"),
	}
	if err := ensureFanartPathContained(root, paths.Poster); err != nil {
		return PosterPathSet{}, err
	}
	if err := ensureFanartPathContained(root, paths.Background); err != nil {
		return PosterPathSet{}, err
	}
	if err := ensureFanartPathContained(root, paths.LegacyPoster); err != nil {
		return PosterPathSet{}, err
	}
	return paths, nil
}

func PublishPoster(source, dir, filmName string, content []byte) (PosterPublishResult, error) {
	paths, err := PosterPaths(source, dir, filmName)
	if err != nil {
		return PosterPublishResult{}, err
	}
	filmDirectory := filepath.Base(filepath.Dir(paths.Poster))
	if err := ensureFanartDirectories(source, dir, filmDirectory); err != nil {
		return PosterPublishResult{}, err
	}
	if _, err := regularPosterExists(paths.LegacyPoster); err != nil {
		return PosterPublishResult{}, err
	}
	if _, err := regularPosterExists(paths.Poster); err != nil {
		return PosterPublishResult{}, err
	}

	temporary, err := os.CreateTemp(filepath.Dir(paths.Poster), ".dmm-poster-*")
	if err != nil {
		return PosterPublishResult{}, err
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()
	if _, err := temporary.Write(content); err != nil {
		return PosterPublishResult{}, err
	}
	if err := temporary.Sync(); err != nil {
		return PosterPublishResult{}, err
	}
	if err := temporary.Chmod(0o644); err != nil {
		return PosterPublishResult{}, err
	}
	if err := temporary.Close(); err != nil {
		return PosterPublishResult{}, err
	}
	if err := os.Rename(temporaryPath, paths.Poster); err != nil {
		return PosterPublishResult{}, err
	}

	result := PosterPublishResult{Published: true}
	backgroundInfo, err := os.Lstat(paths.Background)
	if err == nil && backgroundInfo.Mode()&os.ModeSymlink != 0 {
		if err := removePosterBackgroundSymlink(paths.Background); err != nil {
			return result, fmt.Errorf("remove poster background symlink after publish: %w", err)
		}
	} else if err != nil && !os.IsNotExist(err) {
		return result, fmt.Errorf("inspect poster background after publish: %w", err)
	}

	legacyInfo, err := os.Lstat(paths.LegacyPoster)
	if os.IsNotExist(err) {
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("inspect legacy poster after publish: %w", err)
	}
	if !legacyInfo.Mode().IsRegular() {
		return result, fmt.Errorf("legacy poster is not a regular file: %s", paths.LegacyPoster)
	}
	if err := os.Remove(paths.LegacyPoster); err != nil {
		return result, fmt.Errorf("remove legacy poster after publish: %w", err)
	}
	return result, nil
}

// PromoteLegacyPoster renames the legacy poster to poster.jpg when the canonical
// poster does not already exist. Missing legacy is idempotent success. Existing
// regular poster.jpg means the legacy file is left untouched.
func PromoteLegacyPoster(source, dir, filmName string) error {
	paths, err := PosterPaths(source, dir, filmName)
	if err != nil {
		return err
	}
	posterInfo, err := os.Lstat(paths.Poster)
	if err == nil {
		if !posterInfo.Mode().IsRegular() {
			return fmt.Errorf("poster destination is not a regular file: %s", paths.Poster)
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	legacyInfo, err := os.Lstat(paths.LegacyPoster)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !legacyInfo.Mode().IsRegular() {
		return fmt.Errorf("legacy poster is not a regular file: %s", paths.LegacyPoster)
	}
	return os.Rename(paths.LegacyPoster, paths.Poster)
}

// ReplacePoster publishes poster content using the legacy API name.
// Deprecated: use PublishPoster.
func ReplacePoster(source, dir, filmName string, content []byte) (PosterReplaceResult, error) {
	return PublishPoster(source, dir, filmName, content)
}

func regularPosterExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if err == nil {
		if !info.Mode().IsRegular() {
			return false, fmt.Errorf("poster destination is not a regular file: %s", path)
		}
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}
