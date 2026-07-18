package virtual_file

import (
	"context"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/OpenListTeam/OpenList/v4/cmd/flags"
	"github.com/go-resty/resty/v2"
)

var (
	fanartSwapMu sync.Mutex
	fanartRename = os.Rename
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

func SwapFanart(source, dir, filmName string, firstIndex, secondIndex int) error {
	if firstIndex == secondIndex {
		return nil
	}
	fanartSwapMu.Lock()
	defer fanartSwapMu.Unlock()
	first, err := FanartPath(source, dir, filmName, firstIndex)
	if err != nil {
		return err
	}
	second, err := FanartPath(source, dir, filmName, secondIndex)
	if err != nil {
		return err
	}
	if err := recoverFanartSwapPaths(first, second); err != nil {
		return err
	}
	if err := requireRegularFanart(first); err != nil {
		return err
	}
	if err := requireRegularFanart(second); err != nil {
		return err
	}

	return swapFanartPaths(first, second)
}

func PromoteLandscapeFanart(source, dir, filmName string, candidateIndex int) (bool, error) {
	if candidateIndex < 2 {
		return false, nil
	}
	fanartSwapMu.Lock()
	defer fanartSwapMu.Unlock()
	first, err := FanartPath(source, dir, filmName, 1)
	if err != nil {
		return false, err
	}
	candidate, err := FanartPath(source, dir, filmName, candidateIndex)
	if err != nil {
		return false, err
	}
	if err := recoverFanartSwapPaths(first, candidate); err != nil {
		return false, err
	}
	if err := requireRegularFanart(first); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	firstLandscape, firstErr := fanartIsLandscape(first)
	if firstErr == nil && firstLandscape {
		return true, nil
	}
	if err := requireRegularFanart(candidate); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	candidateLandscape, err := fanartIsLandscape(candidate)
	if err != nil || !candidateLandscape {
		return false, nil
	}
	if err := swapFanartPaths(first, candidate); err != nil {
		return false, err
	}
	return true, nil
}

func swapFanartPaths(first, second string) error {
	oldTemp, newTemp := fanartSwapTempPaths(first, second)
	if err := os.Link(first, oldTemp); err != nil {
		return fmt.Errorf("preserve first fanart for swap: %w", err)
	}
	if err := os.Link(second, newTemp); err != nil {
		_ = os.Remove(oldTemp)
		return fmt.Errorf("preserve second fanart for swap: %w", err)
	}
	if err := fanartRename(newTemp, first); err != nil {
		_ = os.Remove(oldTemp)
		_ = os.Remove(newTemp)
		return fmt.Errorf("promote fanart: %w", err)
	}
	if err := fanartRename(oldTemp, second); err != nil {
		return fmt.Errorf("restore displaced fanart after promotion: %w", err)
	}
	return nil
}

func RecoverFanartSwap(source, dir, filmName string, firstIndex, secondIndex int) error {
	fanartSwapMu.Lock()
	defer fanartSwapMu.Unlock()
	first, err := FanartPath(source, dir, filmName, firstIndex)
	if err != nil {
		return err
	}
	second, err := FanartPath(source, dir, filmName, secondIndex)
	if err != nil {
		return err
	}
	return recoverFanartSwapPaths(first, second)
}

func recoverFanartSwapPaths(first, second string) error {
	oldTemp, newTemp := fanartSwapTempPaths(first, second)
	oldInfo, oldErr := os.Lstat(oldTemp)
	newInfo, newErr := os.Lstat(newTemp)
	oldExists := oldErr == nil
	newExists := newErr == nil
	if oldErr != nil && !os.IsNotExist(oldErr) {
		return oldErr
	}
	if newErr != nil && !os.IsNotExist(newErr) {
		return newErr
	}
	if !oldExists && !newExists {
		return nil
	}
	if oldExists && newExists {
		if !oldInfo.Mode().IsRegular() || !newInfo.Mode().IsRegular() {
			return fmt.Errorf("fanart swap markers are not regular files")
		}
		if err := os.Remove(oldTemp); err != nil {
			return err
		}
		return os.Remove(newTemp)
	}
	if newExists {
		if !newInfo.Mode().IsRegular() {
			return fmt.Errorf("fanart swap marker is not a regular file: %s", newTemp)
		}
		return os.Remove(newTemp)
	}
	if !oldInfo.Mode().IsRegular() {
		return fmt.Errorf("fanart swap marker is not a regular file: %s", oldTemp)
	}
	firstInfo, err := os.Lstat(first)
	if err != nil {
		return err
	}
	secondInfo, err := os.Lstat(second)
	if err != nil {
		return err
	}
	if !firstInfo.Mode().IsRegular() || !secondInfo.Mode().IsRegular() {
		return fmt.Errorf("fanart swap targets are not regular files")
	}
	if os.SameFile(firstInfo, secondInfo) {
		if err := fanartRename(oldTemp, second); err != nil {
			return fmt.Errorf("complete interrupted fanart swap: %w", err)
		}
		return nil
	}
	if os.SameFile(firstInfo, oldInfo) {
		return os.Remove(oldTemp)
	}
	return fmt.Errorf("ambiguous interrupted fanart swap between %s and %s", first, second)
}

func fanartSwapTempPaths(first, second string) (string, string) {
	prefix := "." + filepath.Base(first) + "-" + filepath.Base(second)
	directory := filepath.Dir(first)
	return filepath.Join(directory, prefix+".swap-old"), filepath.Join(directory, prefix+".swap-new")
}

func requireRegularFanart(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("fanart is not a regular file: %s", path)
	}
	return nil
}

func fanartIsLandscape(path string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()
	config, _, err := image.DecodeConfig(file)
	if err != nil {
		return false, err
	}
	return config.Width > config.Height, nil
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
