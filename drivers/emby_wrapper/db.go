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

// UpsertEmbyDirSetting 保存目录设置。
// actors 去空格后为空表示清除 actors；useNameAsActor 为 nil 表示未提供（保持原值）。
// actors 为空且 useNameAsActor 最终为 false 时删除该目录的设置行。
func UpsertEmbyDirSetting(storageID uint, dirPath, actors string, useNameAsActor *bool) error {
	actors = strings.TrimSpace(actors)
	item, err := GetEmbyDirSetting(storageID, dirPath)
	if err != nil {
		return err
	}
	use := false
	if item != nil {
		use = item.UseNameAsActor
	}
	if useNameAsActor != nil {
		use = *useNameAsActor
	}
	if actors == "" && !use {
		return db.GetDb().Where("storage_id = ? AND dir_path = ?", storageID, dirPath).Delete(&model.EmbyDirSetting{}).Error
	}
	if item != nil {
		item.Actors = actors
		item.UseNameAsActor = use
		return db.GetDb().Save(item).Error
	}
	return db.GetDb().Create(&model.EmbyDirSetting{
		StorageID:      storageID,
		DirPath:        dirPath,
		Actors:         actors,
		UseNameAsActor: use,
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
