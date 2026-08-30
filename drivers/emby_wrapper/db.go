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
// actors/plot 去空格后为空表示清除对应字段；useNameAsActor/appendFileNameToPlot 为 nil 表示未提供（保持原值）。
// 所有字段均未配置时删除该目录的设置行。
func UpsertEmbyDirSetting(storageID uint, dirPath, actors, plot string, useNameAsActor, appendFileNameToPlot *bool) error {
	actors = strings.TrimSpace(actors)
	plot = strings.TrimSpace(plot)
	item, err := GetEmbyDirSetting(storageID, dirPath)
	if err != nil {
		return err
	}
	use := false
	var appendFlag *bool
	if item != nil {
		use = item.UseNameAsActor
		appendFlag = item.AppendFileNameToPlot
	}
	if useNameAsActor != nil {
		use = *useNameAsActor
	}
	if appendFileNameToPlot != nil {
		// 与 use_name_as_actor 一致：false = 清除（恢复上层继承），不写入显式关闭态
		if *appendFileNameToPlot {
			appendFlag = appendFileNameToPlot
		} else {
			appendFlag = nil
		}
	}
	if actors == "" && plot == "" && !use && appendFlag == nil {
		return db.GetDb().Where("storage_id = ? AND dir_path = ?", storageID, dirPath).Delete(&model.EmbyDirSetting{}).Error
	}
	if item != nil {
		item.Actors = actors
		item.Plot = plot
		item.UseNameAsActor = use
		item.AppendFileNameToPlot = appendFlag
		return db.GetDb().Save(item).Error
	}
	return db.GetDb().Create(&model.EmbyDirSetting{
		StorageID:            storageID,
		DirPath:              dirPath,
		Actors:               actors,
		Plot:                 plot,
		UseNameAsActor:       use,
		AppendFileNameToPlot: appendFlag,
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
