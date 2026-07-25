package virtual_file

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"sync"
)

var (
	fanartSwapMu sync.Mutex
	fanartRename = os.Rename
)

type fanartPathResolver func(index int) (string, error)

func SwapFanart(source, dir, filmName string, firstIndex, secondIndex int) error {
	return swapFanart(func(index int) (string, error) {
		return FanartPath(source, dir, filmName, index)
	}, firstIndex, secondIndex)
}

func PromoteLandscapeFanart(source, dir, filmName string, candidateIndex int) (bool, error) {
	return promoteLandscapeFanart(func(index int) (string, error) {
		return FanartPath(source, dir, filmName, index)
	}, candidateIndex)
}

func RecoverFanartSwap(source, dir, filmName string, firstIndex, secondIndex int) error {
	return recoverFanartSwap(func(index int) (string, error) {
		return FanartPath(source, dir, filmName, index)
	}, firstIndex, secondIndex)
}

func PromoteLandscapeMediaFanart(identity MediaIdentity, candidateIndex int) (bool, error) {
	return promoteLandscapeFanart(func(index int) (string, error) {
		return MediaFanartPath(identity, index)
	}, candidateIndex)
}

func RecoverMediaFanartSwap(identity MediaIdentity, firstIndex, secondIndex int) error {
	return recoverFanartSwap(func(index int) (string, error) {
		return MediaFanartPath(identity, index)
	}, firstIndex, secondIndex)
}

func swapFanart(resolve fanartPathResolver, firstIndex, secondIndex int) error {
	if firstIndex == secondIndex {
		return nil
	}
	fanartSwapMu.Lock()
	defer fanartSwapMu.Unlock()
	first, err := resolve(firstIndex)
	if err != nil {
		return err
	}
	second, err := resolve(secondIndex)
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

func promoteLandscapeFanart(resolve fanartPathResolver, candidateIndex int) (bool, error) {
	if candidateIndex < 2 {
		return false, nil
	}
	fanartSwapMu.Lock()
	defer fanartSwapMu.Unlock()
	first, err := resolve(1)
	if err != nil {
		return false, err
	}
	candidate, err := resolve(candidateIndex)
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

func recoverFanartSwap(resolve fanartPathResolver, firstIndex, secondIndex int) error {
	fanartSwapMu.Lock()
	defer fanartSwapMu.Unlock()
	first, err := resolve(firstIndex)
	if err != nil {
		return err
	}
	second, err := resolve(secondIndex)
	if err != nil {
		return err
	}
	return recoverFanartSwapPaths(first, second)
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
