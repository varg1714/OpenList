package virtual_file

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/OpenListTeam/OpenList/v4/cmd/flags"
	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

func CacheImage(mediaInfo MediaInfo) int {
	result, err := CacheImageWithError(mediaInfo)
	if err != nil {
		utils.Log.Warnf("failed to cache image: %s", err)
	}
	return result
}

func CacheImageWithError(mediaInfo MediaInfo) (int, error) {
	if mediaInfo.Identity != nil {
		return cacheIdentityImage(mediaInfo)
	}
	return cacheLegacyImage(mediaInfo)
}

func cacheIdentityImage(mediaInfo MediaInfo) (int, error) {
	paths, err := ResolveMediaArtifactPaths(*mediaInfo.Identity)
	if err != nil {
		return CreatedFailed, fmt.Errorf("resolve media poster path: %w", err)
	}
	info, err := os.Lstat(paths.Poster)
	if err == nil {
		if !info.Mode().IsRegular() {
			return CreatedFailed, fmt.Errorf("media poster is not a regular file: %s", paths.Poster)
		}
		return Exist, nil
	}
	if !os.IsNotExist(err) {
		return CreatedFailed, fmt.Errorf("inspect media poster: %w", err)
	}
	content, err := downloadMediaImage(mediaInfo)
	if err != nil {
		return CreatedFailed, err
	}
	if err := PublishMediaPoster(*mediaInfo.Identity, content); err != nil {
		return CreatedFailed, fmt.Errorf("publish media poster: %w", err)
	}
	return CreatedSuccess, nil
}

func cacheLegacyImage(mediaInfo MediaInfo) (int, error) {
	paths, err := PosterPaths(mediaInfo.Source, mediaInfo.Dir, mediaInfo.FileName)
	if err != nil {
		return CreatedFailed, fmt.Errorf("resolve legacy poster path: %w", err)
	}
	info, err := os.Lstat(paths.Poster)
	if err == nil {
		if !info.Mode().IsRegular() {
			return CreatedFailed, fmt.Errorf("legacy poster is not a regular file: %s", paths.Poster)
		}
		return Exist, nil
	}
	if !os.IsNotExist(err) {
		return CreatedFailed, fmt.Errorf("inspect legacy poster: %w", err)
	}
	filePath := filepath.Join(flags.DataDir, "emby", mediaInfo.Source, mediaInfo.Dir, GetRealName(mediaInfo.FileName), mediaInfo.FileName)
	if utils.Exists(filePath) {
		return Exist, nil
	}
	content, err := downloadMediaImage(mediaInfo)
	if err != nil {
		return CreatedFailed, err
	}
	if err := os.MkdirAll(filepath.Dir(filePath), 0o777); err != nil {
		return CreatedFailed, fmt.Errorf("create legacy poster directory: %w", err)
	}
	if err := os.WriteFile(filePath, content, 0o777); err != nil {
		return CreatedFailed, fmt.Errorf("write legacy poster: %w", err)
	}
	ext := filepath.Ext(mediaInfo.FileName)
	backgroundPath := filepath.Join(filepath.Dir(filePath), fmt.Sprintf("%s-background%s", strings.TrimSuffix(mediaInfo.FileName, ext), ext))
	if _, err := os.Stat(backgroundPath); err == nil {
		return CreatedSuccess, nil
	} else if !os.IsNotExist(err) {
		return CreatedFailed, fmt.Errorf("inspect legacy poster background: %w", err)
	}
	if err := os.Symlink(mediaInfo.FileName, backgroundPath); err != nil {
		return CreatedFailed, fmt.Errorf("create legacy poster background symlink: %w", err)
	}
	return CreatedSuccess, nil
}

func downloadMediaImage(mediaInfo MediaInfo) ([]byte, error) {
	if mediaInfo.ImgUrl == "" {
		return nil, errors.New("media poster URL is empty")
	}
	response, err := base.RestyClient.R().SetHeaders(mediaInfo.ImgUrlHeaders).Get(mediaInfo.ImgUrl)
	if err != nil {
		return nil, fmt.Errorf("download media poster: %w", err)
	}
	if response.IsError() {
		return nil, fmt.Errorf("download media poster: HTTP %d", response.StatusCode())
	}
	return response.Body(), nil
}
