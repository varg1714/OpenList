package cache

import (
	"errors"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"gorm.io/gorm"
)

func GetCacheList(storageID uint, dirPath string) (*model.CacheList, error) {
	var item model.CacheList
	err := db.GetDb().Where("storage_id = ? AND dir_path = ?", storageID, dirPath).First(&item).Error
	if err == nil {
		return &item, nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return nil, err
}

func UpsertCacheList(storageID uint, dirPath string, data []model.CachedObj) error {
	item, err := GetCacheList(storageID, dirPath)
	if err != nil {
		return err
	}
	if item != nil {
		item.Data = data
		item.UpdatedAt = time.Now()
		return db.GetDb().Save(item).Error
	}
	return db.GetDb().Create(&model.CacheList{
		StorageID: storageID,
		DirPath:   dirPath,
		Data:      data,
		UpdatedAt: time.Now(),
	}).Error
}

func DeleteCacheList(storageID uint, dirPath string) error {
	return db.GetDb().Where("storage_id = ? AND dir_path = ?", storageID, dirPath).Delete(&model.CacheList{}).Error
}
