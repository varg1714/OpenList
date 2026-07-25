package virtual_file

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// FanartPublishRequest groups the parameters for PublishFanart.
type FanartPublishRequest struct {
	Source   string
	Dir      string
	FilmName string
	Index    int
	Content  []byte
}

// FanartPublishResult reports whether a fanart destination already existed.
type FanartPublishResult struct {
	Existing bool
}

// PublishFanart writes fanart content to the canonical fanart path atomically.
// Uses temp-file + hard link, consistent with CacheHTTPFile.
// Returns Existing=true when the destination is already a regular file — the
// caller should treat that as a successful publish and advance progress.
func PublishFanart(req FanartPublishRequest) (FanartPublishResult, error) {
	destination, err := FanartPath(req.Source, req.Dir, req.FilmName, req.Index)
	if err != nil {
		return FanartPublishResult{}, err
	}
	if err := ensureFanartDirectories(req.Source, req.Dir, filepath.Base(filepath.Dir(destination))); err != nil {
		return FanartPublishResult{}, err
	}
	directory := filepath.Dir(destination)

	info, err := os.Lstat(destination)
	if err == nil {
		if info.Mode().IsRegular() {
			if err := syncFanartDirectory(directory); err != nil {
				return FanartPublishResult{}, err
			}
			return FanartPublishResult{Existing: true}, nil
		}
		return FanartPublishResult{}, fmt.Errorf("fanart destination is not a regular file: %s", destination)
	}
	if !os.IsNotExist(err) {
		return FanartPublishResult{}, err
	}

	temporary, err := os.CreateTemp(directory, ".fanart-publish-*")
	if err != nil {
		return FanartPublishResult{}, err
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()

	if _, err := temporary.Write(req.Content); err != nil {
		return FanartPublishResult{}, err
	}
	if err := temporary.Sync(); err != nil {
		return FanartPublishResult{}, err
	}
	if err := temporary.Chmod(0o644); err != nil {
		return FanartPublishResult{}, err
	}
	if err := temporary.Close(); err != nil {
		return FanartPublishResult{}, err
	}

	if err := os.Link(temporaryPath, destination); err != nil {
		if os.IsExist(err) {
			info, statErr := os.Lstat(destination)
			if statErr == nil && info.Mode().IsRegular() {
				if err := syncFanartDirectory(directory); err != nil {
					return FanartPublishResult{}, err
				}
				return FanartPublishResult{Existing: true}, nil
			}
			if statErr == nil {
				return FanartPublishResult{}, fmt.Errorf("fanart destination is not a regular file: %s", destination)
			}
		}
		return FanartPublishResult{}, err
	}
	if err := syncFanartDirectory(directory); err != nil {
		return FanartPublishResult{}, err
	}
	return FanartPublishResult{}, nil
}

func syncFanartDirectory(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	if err := directory.Sync(); err != nil {
		_ = directory.Close()
		return err
	}
	return directory.Close()
}

// RemoveBackground removes a film's background image (regular file or symlink).
// The path is resolved via PosterPaths. Poster and LegacyPoster are never touched.
// Missing background is treated as success (idempotent).
func RemoveBackground(source, dir, filmName string) error {
	paths, err := PosterPaths(source, dir, filmName)
	if err != nil {
		return err
	}

	info, err := os.Lstat(paths.Background)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return os.Remove(paths.Background)
	}
	return fmt.Errorf("background is neither a regular file nor a symlink: %s", paths.Background)
}
