package tool

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	_115 "github.com/OpenListTeam/OpenList/v4/drivers/115"
	"github.com/OpenListTeam/OpenList/v4/internal/av"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	driver2 "github.com/SheltonZhu/115driver/pkg/driver"
	"math"
	"regexp"
	"slices"
	"strconv"
	"time"
)

func downloadMagnet(ctx context.Context, driverType string, driverPath string, magnet string, fileName string) (*Status, *DownloadTask, error) {

	// 1. 下载该文件
	task, err := AddURL(ctx, &AddURLArgs{
		URL:          magnet,
		DstDirPath:   driverPath,
		Tool:         driverType,
		DeletePolicy: DeleteAlways,
	})
	if err != nil {
		return nil, nil, err
	} else if task.GetErr() != nil {
		return nil, nil, task.GetErr()
	}

	utils.Log.Infof("提交离线下载任务：%s", fileName)
	downloadTask := task.(*DownloadTask)

	i := 0
	completed := false
	status, err := downloadTask.tool.Status(downloadTask)

	for i < 15 && !completed {
		if err != nil {
			return nil, nil, err
		}
		if downloadTask.GetErr() != nil {
			return nil, nil, downloadTask.GetErr()
		}

		utils.Log.Infof("当前任务下载进度：%f", func() float64 {
			if status == nil {
				return 0.0
			} else {
				return status.Progress
			}
		}())

		if status == nil || !(status.Completed || math.Dim(100.0, status.Progress) <= 0.01) {
			i++
			time.Sleep(2 * time.Second)
			status, err = downloadTask.tool.Status(downloadTask)
		} else {
			completed = true
		}

	}

	if status == nil || !completed {
		return nil, nil, errors.New("文件仍未下载完成")
	}

	return status, downloadTask, nil

}

var ed2kFileSizeRegexp = regexp.MustCompile(`^ed2k://\|file\|[^|]+\|(\d+)\|`)

// ed2kFileSize returns the file size encoded in an ed2k URI, or 0 when the
// URI is not a valid ed2k file link.
func ed2kFileSize(magnet string) int64 {
	matches := ed2kFileSizeRegexp.FindStringSubmatch(magnet)
	if len(matches) != 2 {
		return 0
	}
	size, err := strconv.ParseInt(matches[1], 10, 64)
	if err != nil || size <= 0 {
		return 0
	}
	return size
}

// filterEd2kFile narrows the files to the one described by an ed2k magnet.
// 115 reports the save directory as the offline task's file ID, so the
// directory listing may contain other works. Matching the exact ed2k size
// prevents caching a different file from the same directory.
func filterEd2kFile(magnet string, files []model.Obj) []model.Obj {
	size := ed2kFileSize(magnet)
	if size <= 0 {
		return files
	}
	matched := utils.SliceFilter(files, func(f model.Obj) bool {
		return f.GetSize() == size
	})
	if len(matched) != 1 {
		return files
	}
	return matched
}

// filterActualFile narrows the listing to the concrete file reported by the
// offline task (115 ed2k exposes it as delete_file_id). If the listing does
// not contain that ID, keep the full list so cacheFiles can still fall back.
func filterActualFile(actualFileID string, files []model.Obj) []model.Obj {
	if actualFileID == "" {
		return files
	}
	matched := utils.SliceFilter(files, func(f model.Obj) bool {
		return f.GetID() == actualFileID
	})
	if len(matched) == 0 {
		return files
	}
	return matched
}

func cacheFiles(driverType, magnet, lookingFileName string, files []model.Obj, cacheOptionFunc func(obj model.Obj) map[string]string) model.Obj {

	// 仅包含100M大小以上的文件
	validFiles := utils.SliceFilter(files, func(f model.Obj) bool {
		return f.GetSize()/(1024*1024) > 100
	})

	// 按名称正序
	slices.SortFunc(validFiles, func(a, b model.Obj) int {
		return cmp.Compare(a.GetName(), b.GetName())
	})
	validFiles = filterEd2kFile(magnet, validFiles)

	if len(validFiles) == 0 {
		return nil
	} else if len(validFiles) == 1 {
		err := db.CreateMagnetCache(model.MagnetCache{
			DriverType: driverType,
			Magnet:     magnet,
			FileId:     validFiles[0].GetID(),
			Name:       lookingFileName,
			Code:       av.GetFilmCode(lookingFileName),
			Option:     cacheOptionFunc(validFiles[0]),
		})
		if err != nil {
			utils.Log.Warnf("文件缓存失败:%s", err.Error())
		}
		return validFiles[0]
	} else {

		var lookedFile model.Obj
		nameRegexp, _ := regexp.Compile("(.*?)(-cd\\d+).mp4")

		if !nameRegexp.MatchString(lookingFileName) {
			lookedFile = validFiles[0]
			err := db.CreateMagnetCache(model.MagnetCache{
				DriverType: driverType,
				Magnet:     magnet,
				FileId:     lookedFile.GetID(),
				Name:       lookingFileName,
				Code:       av.GetFilmCode(lookingFileName),
				Option:     cacheOptionFunc(lookedFile),
			})
			if err != nil {
				utils.Log.Warnf("文件缓存失败:%s", err.Error())
			}
		} else {
			code := nameRegexp.ReplaceAllString(lookingFileName, "$1")
			for index, file := range validFiles {
				realName := fmt.Sprintf("%s-cd%d.mp4", code, index+1)
				if realName == lookingFileName {
					lookedFile = file
				}
				err := db.CreateMagnetCache(model.MagnetCache{
					DriverType: driverType,
					Magnet:     magnet,
					FileId:     file.GetID(),
					Name:       realName,
					Code:       av.GetFilmCode(realName),
					Option:     cacheOptionFunc(file),
				})
				if err != nil {
					utils.Log.Warnf("文件缓存失败:%s", err.Error())
				}
			}
		}
		return lookedFile
	}

}

func getLinkByCache(ctx context.Context, args model.LinkArgs, driverType string, storage driver.Driver, magnetCache model.MagnetCache) (*model.Link, error) {

	switch driverType {
	case "PikPak":
		link, err := storage.Link(ctx, &model.ObjThumb{
			Object: model.Object{ID: magnetCache.FileId},
		}, args)

		if err != nil {
			utils.Log.Warnf("cached PikPak file is unavailable, retrying from source magnet: %s", err)
			return nil, nil
		}
		return link, nil
	case "115 Cloud":
		link, err := storage.Link(ctx, &_115.FileObj{
			File: driver2.File{
				FileID:   magnetCache.FileId,
				PickCode: magnetCache.Option["pickCode"],
			},
		}, args)

		if err != nil {
			utils.Log.Warnf("cached 115 file is unavailable, retrying from source magnet: %s", err)
			return nil, nil
		}
		return link, nil
	}

	return nil, nil

}
