package emby_wrapper

import (
	"errors"
	"strings"

	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"gorm.io/gorm"
)

func GetEmbyDirSetting(storageID uint, dirPath string) (*model.EmbyDirSetting, error) {
	var item model.EmbyDirSetting
	err := db.GetDb().Where("storage_id = ? AND dir_path = ?", storageID, dirPath).First(&item).Error
	if err == nil {
		return &item, nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return nil, err
}

// UpsertEmbyDirSetting 保存目录设置。各字段独立合并：
// actors/plot/tvShowName 去空格后为空表示清除对应字段；useNameAsActor/appendFileNameToPlot/tvShow/tvShowSubfolders 为 nil 表示未提供（保持原值）。
// 所有字段均未配置时删除该目录的设置行。
func UpsertEmbyDirSetting(storageID uint, dirPath, actors, plot, tvShowName string, useNameAsActor, appendFileNameToPlot, tvShow, tvShowSubfolders *bool) error {
	actors = strings.TrimSpace(actors)
	plot = strings.TrimSpace(plot)
	tvShowName = strings.TrimSpace(tvShowName)
	item, err := GetEmbyDirSetting(storageID, dirPath)
	if err != nil {
		return err
	}
	use := false
	var appendFlag *bool
	tv := false
	tvSub := false
	if item != nil {
		use = item.UseNameAsActor
		appendFlag = item.AppendFileNameToPlot
		tv = item.TvShow
		tvSub = item.TvShowSubfolders
	}
	if useNameAsActor != nil {
		use = *useNameAsActor
	}
	if appendFileNameToPlot != nil {
		appendFlag = appendFileNameToPlot
	}
	if tvShow != nil {
		tv = *tvShow
	}
	if tvShowSubfolders != nil {
		tvSub = *tvShowSubfolders
	}
	if actors == "" && plot == "" && tvShowName == "" && !use && !tv && !tvSub && (appendFlag == nil || !*appendFlag) {
		return db.GetDb().Where("storage_id = ? AND dir_path = ?", storageID, dirPath).Delete(&model.EmbyDirSetting{}).Error
	}
	if item != nil {
		item.Actors = actors
		item.Plot = plot
		item.TvShowName = tvShowName
		item.UseNameAsActor = use
		item.AppendFileNameToPlot = appendFlag
		item.TvShow = tv
		item.TvShowSubfolders = tvSub
		return db.GetDb().Save(item).Error
	}
	return db.GetDb().Create(&model.EmbyDirSetting{
		StorageID:            storageID,
		DirPath:              dirPath,
		Actors:               actors,
		Plot:                 plot,
		TvShowName:           tvShowName,
		UseNameAsActor:       use,
		AppendFileNameToPlot: appendFlag,
		TvShow:               tv,
		TvShowSubfolders:     tvSub,
	}).Error
}

// ListEmbyDirSettings 返回该存储全部目录设置：dirPath -> setting。
func ListEmbyDirSettings(storageID uint) (map[string]model.EmbyDirSetting, error) {
	var items []model.EmbyDirSetting
	if err := db.GetDb().Where("storage_id = ?", storageID).Find(&items).Error; err != nil {
		return nil, err
	}
	out := make(map[string]model.EmbyDirSetting, len(items))
	for _, item := range items {
		out[item.DirPath] = item
	}
	return out, nil
}
