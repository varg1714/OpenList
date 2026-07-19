package virtual_file

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/OpenListTeam/OpenList/v4/cmd/flags"
	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

type MediaIdentity struct {
	StorageID  uint
	Source     string
	PrimaryDir string
	Code       string
}

type MediaArtifactPaths struct {
	Root         string
	Poster       string
	LegacyPoster string
	Background   string
	NFO          string
}

func ResolveMediaArtifactPaths(identity MediaIdentity) (MediaArtifactPaths, error) {
	components := []struct {
		label string
		value string
	}{
		{"source", identity.Source},
		{"primary directory", identity.PrimaryDir},
		{"code", identity.Code},
	}
	for _, component := range components {
		if err := validateFanartPathComponent(component.label, component.value); err != nil {
			return MediaArtifactPaths{}, err
		}
	}

	root, err := filepath.Abs(filepath.Join(flags.DataDir, "emby", identity.Source, identity.PrimaryDir, identity.Code))
	if err != nil {
		return MediaArtifactPaths{}, err
	}
	dataRoot, err := filepath.Abs(flags.DataDir)
	if err != nil {
		return MediaArtifactPaths{}, err
	}
	if err := ensureFanartPathContained(dataRoot, root); err != nil {
		return MediaArtifactPaths{}, err
	}
	return MediaArtifactPaths{
		Root:         root,
		Poster:       filepath.Join(root, "poster.jpg"),
		LegacyPoster: filepath.Join(root, identity.Code+".jpg"),
		Background:   filepath.Join(root, identity.Code+"-background.jpg"),
		NFO:          filepath.Join(root, identity.Code+".nfo"),
	}, nil
}

func MediaFanartPath(identity MediaIdentity, index int) (string, error) {
	if index < 1 {
		return "", fmt.Errorf("invalid fanart index: %d", index)
	}
	paths, err := ResolveMediaArtifactPaths(identity)
	if err != nil {
		return "", err
	}
	return filepath.Join(paths.Root, fmt.Sprintf("fanart%d.jpg", index)), nil
}

func MediaSubtitlePath(identity MediaIdentity, partIndex, subtitleIndex int, ext string) (string, error) {
	if partIndex < 1 || subtitleIndex < 1 {
		return "", fmt.Errorf("invalid subtitle part/index: %d/%d", partIndex, subtitleIndex)
	}
	ext = strings.TrimPrefix(strings.TrimSpace(ext), ".")
	if err := validateFanartPathComponent("subtitle extension", ext); err != nil {
		return "", err
	}
	paths, err := ResolveMediaArtifactPaths(identity)
	if err != nil {
		return "", err
	}
	stem := identity.Code
	if partIndex > 1 {
		stem = fmt.Sprintf("%s-cd%d", identity.Code, partIndex)
	}
	return filepath.Join(paths.Root, fmt.Sprintf("%s.%d.%s", stem, subtitleIndex, ext)), nil
}

func PublishMediaPoster(identity MediaIdentity, content []byte) error {
	paths, err := ResolveMediaArtifactPaths(identity)
	if err != nil {
		return err
	}
	if err := ensureMediaArtifactRoot(identity); err != nil {
		return err
	}
	if err := atomicWriteMediaArtifact(paths.Poster, content, 0o644); err != nil {
		return err
	}
	for _, obsolete := range []string{paths.LegacyPoster, paths.Background} {
		info, statErr := os.Lstat(obsolete)
		if os.IsNotExist(statErr) {
			continue
		}
		if statErr != nil {
			return statErr
		}
		if obsolete == paths.Background && info.Mode().IsRegular() {
			continue
		}
		if err := os.Remove(obsolete); err != nil {
			return err
		}
	}
	return nil
}

func CacheMediaFanart(ctx context.Context, identity MediaIdentity, index int, request HTTPFileCacheRequest) (HTTPFileCacheResult, error) {
	destination, err := MediaFanartPath(identity, index)
	if err != nil {
		return HTTPFileCacheResult{}, err
	}
	if err := ensureMediaArtifactRoot(identity); err != nil {
		return HTTPFileCacheResult{}, err
	}
	request.Destination = destination
	return CacheHTTPFile(ctx, request)
}

func SaveMediaSubtitles(identity MediaIdentity, partIndex int, subtitles []string) error {
	if err := ensureMediaArtifactRoot(identity); err != nil {
		return err
	}
	for index, subtitle := range subtitles {
		if strings.TrimSpace(subtitle) == "" {
			continue
		}
		ext := utils.SourceExt(subtitle)
		destination, err := MediaSubtitlePath(identity, partIndex, index+1, ext)
		if err != nil {
			return err
		}
		if info, statErr := os.Lstat(destination); statErr == nil && info.Mode().IsRegular() {
			continue
		} else if statErr != nil && !os.IsNotExist(statErr) {
			return statErr
		}
		response, err := base.RestyClient.R().Get(subtitle)
		if err != nil {
			return err
		}
		if response.IsError() {
			return fmt.Errorf("subtitle request returned HTTP %d", response.StatusCode())
		}
		if err := atomicWriteMediaArtifact(destination, response.Body(), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func DeleteMediaArtifacts(identity MediaIdentity) error {
	paths, err := ResolveMediaArtifactPaths(identity)
	if err != nil {
		return err
	}
	return os.RemoveAll(paths.Root)
}

func ensureMediaArtifactRoot(identity MediaIdentity) error {
	paths, err := ResolveMediaArtifactPaths(identity)
	if err != nil {
		return err
	}
	dataRoot, err := filepath.Abs(flags.DataDir)
	if err != nil {
		return err
	}
	components := []string{"emby", identity.Source, identity.PrimaryDir, identity.Code}
	if err := os.MkdirAll(dataRoot, 0o755); err != nil {
		return err
	}
	if err := ensureFanartDirectory(dataRoot); err != nil {
		return err
	}
	current := dataRoot
	for _, component := range components {
		current = filepath.Join(current, component)
		if err := ensureFanartDirectory(current); err != nil {
			return err
		}
	}
	if current != paths.Root {
		return fmt.Errorf("media artifact root mismatch: %s", current)
	}
	return nil
}

func atomicWriteMediaArtifact(destination string, content []byte, mode os.FileMode) (resultErr error) {
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".media-artifact-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	closed := false
	defer func() {
		if !closed {
			resultErr = errors.Join(resultErr, temporary.Close())
		}
		if removeErr := os.Remove(temporaryPath); removeErr != nil && !os.IsNotExist(removeErr) {
			resultErr = errors.Join(resultErr, removeErr)
		}
	}()
	if _, err := temporary.Write(content); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Chmod(mode); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	closed = true
	return os.Rename(temporaryPath, destination)
}
