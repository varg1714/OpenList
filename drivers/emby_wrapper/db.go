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

// UpsertEmbyDirSetting 保存目录设置；actors 去空格后为空则删除该目录的设置。
func UpsertEmbyDirSetting(storageID uint, dirPath, actors string) error {
	actors = strings.TrimSpace(actors)
	if actors == "" {
		return db.GetDb().Where("storage_id = ? AND dir_path = ?", storageID, dirPath).Delete(&model.EmbyDirSetting{}).Error
	}
	item, err := GetEmbyDirSetting(storageID, dirPath)
	if err != nil {
		return err
	}
	if item != nil {
		item.Actors = actors
		return db.GetDb().Save(item).Error
	}
	return db.GetDb().Create(&model.EmbyDirSetting{
		StorageID: storageID,
		DirPath:   dirPath,
		Actors:    actors,
	}).Error
}

// ListEmbyDirSettings 返回该存储全部目录设置：dirPath -> actors。
func ListEmbyDirSettings(storageID uint) (map[string]string, error) {
	var items []model.EmbyDirSetting
	if err := db.GetDb().Where("storage_id = ?", storageID).Find(&items).Error; err != nil {
		return nil, err
	}
	out := make(map[string]string, len(items))
	for _, item := range items {
		out[item.DirPath] = item.Actors
	}
	return out, nil
}
