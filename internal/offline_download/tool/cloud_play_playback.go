package tool

import (
	"context"
	"errors"
	"fmt"

	_115 "github.com/OpenListTeam/OpenList/v4/drivers/115"
	"github.com/OpenListTeam/OpenList/v4/internal/av"
	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/internal/setting"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

type cloudPlaySession struct {
	args            model.LinkArgs
	driverType      string
	driverPath      string
	downloadingFile model.Obj
	storage         driver.Driver
}

var (
	getCloudPlayStorage     = op.GetBalancedStorage
	queryCloudCache         = db.QueryMagnetCacheByName
	resolveCloudCacheLink   = getLinkByCache
	attemptCloudMagnet      = playSingleMagnet
	updateSourceMagnetError = db.UpdateSourceMagnetLastError
	selectPlaybackMagnet    = db.SelectSourceMagnet
)

func CloudPlay(
	ctx context.Context,
	args model.LinkArgs,
	driverType string,
	driverPath string,
	downloadingFile model.Obj,
	magnets []model.SourceMagnet,
) (*model.Link, error) {
	if driverPath == "" {
		switch driverType {
		case "115 Cloud":
			driverPath = setting.GetStr(conf.Pan115TempDir)
		case "PikPak":
			driverPath = setting.GetStr(conf.PikPakTempDir)
		}
	}
	if driverPath == "" {
		return nil, errors.New("尚未配置用于云播的网盘")
	}

	storage := getCloudPlayStorage(driverPath)
	if storage == nil {
		return nil, errors.New("网盘配置未找到")
	}
	session := cloudPlaySession{
		args: args, driverType: driverType, driverPath: driverPath,
		downloadingFile: downloadingFile, storage: storage,
	}

	fileCache := queryCloudCache(driverType, downloadingFile.GetName())
	if fileCache.FileId != "" {
		link, err := resolveCloudCacheLink(ctx, args, driverType, storage, fileCache)
		if err != nil {
			utils.Log.Warnf("failed to resolve cached cloud-play file %s: %s", fileCache.Name, err)
		} else if link != nil {
			return link, nil
		}
	}

	seen := make(map[string]struct{}, len(magnets))
	attemptErrors := make([]error, 0, len(magnets))
	for _, magnet := range magnets {
		fingerprint := magnet.Fingerprint
		if fingerprint == "" {
			fingerprint = magnet.MagnetURI
		}
		if _, exists := seen[fingerprint]; exists {
			continue
		}
		seen[fingerprint] = struct{}{}

		link, err := attemptCloudMagnet(ctx, session, magnet)
		if err != nil {
			attemptErrors = append(attemptErrors, fmt.Errorf("magnet %s: %w", fingerprint, err))
			if updateErr := updateSourceMagnetError(magnet.ID, err.Error()); updateErr != nil {
				attemptErrors = append(attemptErrors, fmt.Errorf("record magnet %d failure: %w", magnet.ID, updateErr))
			}
			continue
		}
		if link == nil {
			attemptErrors = append(attemptErrors, fmt.Errorf("magnet %s returned no playback link", fingerprint))
			continue
		}
		if err := selectPlaybackMagnet(magnet.WorkID, magnet.ID); err != nil {
			return nil, fmt.Errorf("select source magnet %d: %w", magnet.ID, err)
		}
		if err := updateSourceMagnetError(magnet.ID, ""); err != nil {
			return nil, fmt.Errorf("clear source magnet %d failure: %w", magnet.ID, err)
		}
		return link, nil
	}

	if len(attemptErrors) == 0 {
		return nil, errors.New("磁力信息获取为空")
	}
	return nil, errors.Join(attemptErrors...)
}

func playSingleMagnet(ctx context.Context, session cloudPlaySession, magnet model.SourceMagnet) (*model.Link, error) {
	fileName := session.downloadingFile.GetName()
	status, _, err := downloadMagnet(
		ctx,
		session.driverType,
		fmt.Sprintf("%s/%s", session.driverPath, session.downloadingFile.GetPath()),
		magnet.MagnetURI,
		fileName,
	)
	if err != nil {
		return nil, err
	}

	downloadedFile := &model.ObjThumb{Object: model.Object{ID: status.FileInfo.FileId}}
	fileList, err := session.storage.List(ctx, downloadedFile, model.ListArgs{})
	if err != nil {
		return nil, err
	}

	switch session.driverType {
	case "PikPak":
		if len(fileList) == 0 {
			if err := db.CreateMagnetCache(model.MagnetCache{
				DriverType: session.driverType,
				Magnet:     magnet.MagnetURI,
				FileId:     status.FileInfo.FileId,
				Name:       fileName,
				Code:       av.GetFilmCode(fileName),
			}); err != nil {
				utils.Log.Warnf("failed to cache cloud-play file %s: %s", fileName, err)
			}
			return session.storage.Link(ctx, downloadedFile, session.args)
		}
		lookedFile := cacheFiles(session.driverType, magnet.MagnetURI, fileName, fileList, func(model.Obj) map[string]string { return nil })
		if lookedFile == nil {
			return nil, fmt.Errorf("downloaded magnet %s contained no playable file", magnet.Fingerprint)
		}
		return session.storage.Link(ctx, lookedFile, session.args)
	case "115 Cloud":
		lookedFile := cacheFiles(session.driverType, magnet.MagnetURI, fileName, fileList, func(obj model.Obj) map[string]string {
			return map[string]string{"pickCode": obj.(*_115.FileObj).PickCode}
		})
		if lookedFile == nil {
			return nil, fmt.Errorf("downloaded magnet %s contained no playable file", magnet.Fingerprint)
		}
		return session.storage.Link(ctx, lookedFile, session.args)
	default:
		return nil, fmt.Errorf("unsupported cloud-play driver %q", session.driverType)
	}
}
