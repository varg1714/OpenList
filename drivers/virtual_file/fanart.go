package virtual_file

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/OpenListTeam/OpenList/v4/cmd/flags"
	"github.com/go-resty/resty/v2"
)

type FanartCacheRequest struct {
	Source   string
	Dir      string
	FilmName string
	Index    int
	URL      string
	Headers  map[string]string
	Client   *resty.Client
	MaxBytes int64
}

func CacheFanart(ctx context.Context, request FanartCacheRequest) (HTTPFileCacheResult, error) {
	destination, err := FanartPath(request.Source, request.Dir, request.FilmName, request.Index)
	if err != nil {
		return HTTPFileCacheResult{}, err
	}
	if err := ensureFanartDirectories(request.Source, request.Dir, filepath.Base(filepath.Dir(destination))); err != nil {
		return HTTPFileCacheResult{}, err
	}
	return CacheHTTPFile(ctx, HTTPFileCacheRequest{
		URL:         request.URL,
		Destination: destination,
		Headers:     request.Headers,
		Client:      request.Client,
		MaxBytes:    request.MaxBytes,
	})
}

func FanartPath(source, dir, filmName string, index int) (string, error) {
	if index < 1 {
		return "", fmt.Errorf("invalid fanart index: %d", index)
	}
	if err := validateFanartPathComponent("source", source); err != nil {
		return "", err
	}
	if err := validateFanartPathComponent("directory", dir); err != nil {
		return "", err
	}
	if err := validateFanartPathComponent("film name", filmName); err != nil {
		return "", err
	}

	canonicalName := CutString(ClearFilmName(filmName))
	filmDirectory := GetRealName(AppendImageName(canonicalName))
	if err := validateFanartPathComponent("film directory", filmDirectory); err != nil {
		return "", err
	}

	root, err := filepath.Abs(filepath.Join(flags.DataDir, "emby", source))
	if err != nil {
		return "", err
	}
	destination := filepath.Join(root, dir, filmDirectory, fmt.Sprintf("fanart%d.jpg", index))
	if err := ensureFanartPathContained(root, destination); err != nil {
		return "", err
	}
	return destination, nil
}

func validateFanartPathComponent(label, component string) error {
	if component == "" || component == "." || component == ".." || filepath.IsAbs(component) || strings.ContainsAny(component, `/\`) {
		return fmt.Errorf("unsafe fanart %s: %q", label, component)
	}
	return nil
}

func ensureFanartPathContained(root, destination string) error {
	relative, err := filepath.Rel(root, destination)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return fmt.Errorf("fanart destination escapes cache root: %s", destination)
	}
	return nil
}

func ensureFanartDirectories(source, dir, filmDirectory string) error {
	dataRoot, err := filepath.Abs(flags.DataDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dataRoot, 0o755); err != nil {
		return err
	}
	if err := ensureFanartDirectory(dataRoot); err != nil {
		return err
	}
	for _, component := range []string{"emby", source, dir, filmDirectory} {
		dataRoot = filepath.Join(dataRoot, component)
		if err := ensureFanartDirectory(dataRoot); err != nil {
			return err
		}
	}
	return nil
}

func ensureFanartDirectory(directory string) error {
	info, err := os.Lstat(directory)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("unsafe fanart directory: %s", directory)
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	if err := os.Mkdir(directory, 0o755); err != nil {
		if !os.IsExist(err) {
			return err
		}
		return ensureFanartDirectory(directory)
	}
	return nil
}
