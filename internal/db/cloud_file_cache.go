package db

import (
	"fmt"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"gorm.io/gorm"
)

func GetCloudFileCache(storageIdentity string, filmFileID uint, fingerprint string) (model.CloudFileCache, error) {
	var cache model.CloudFileCache
	err := db.Where(
		"storage_identity = ? AND film_file_id = ? AND magnet_fingerprint = ?",
		storageIdentity,
		filmFileID,
		fingerprint,
	).First(&cache).Error
	return cache, err
}

func ReplaceCloudFileCaches(storageIdentity, fingerprint string, caches []model.CloudFileCache) error {
	return db.Transaction(func(tx *gorm.DB) error {
		filmFileIDs := make([]uint, len(caches))
		rows := make([]model.CloudFileCache, len(caches))
		for i := range caches {
			if caches[i].StorageIdentity != storageIdentity {
				return fmt.Errorf("cloud file cache %d has storage identity %q, want %q", i, caches[i].StorageIdentity, storageIdentity)
			}
			if caches[i].MagnetFingerprint != fingerprint {
				return fmt.Errorf("cloud file cache %d has magnet fingerprint %q, want %q", i, caches[i].MagnetFingerprint, fingerprint)
			}
			filmFileIDs[i] = caches[i].FilmFileID
			rows[i] = caches[i]
			rows[i].ID = 0
		}
		if len(rows) == 0 {
			return nil
		}

		if err := tx.Where("storage_identity = ? AND film_file_id IN ?", storageIdentity, filmFileIDs).Delete(&model.CloudFileCache{}).Error; err != nil {
			return err
		}
		return tx.Create(&rows).Error
	})
}

func DeleteCloudFileCache(storageIdentity string, filmFileID uint) error {
	return db.Where("storage_identity = ? AND film_file_id = ?", storageIdentity, filmFileID).
		Delete(&model.CloudFileCache{}).Error
}

func DeleteStaleCloudFileCaches(workID uint, fingerprint string) error {
	filmFileIDs := db.Model(&model.FilmFile{}).Select("id").Where("work_id = ?", workID)
	return db.Where("film_file_id IN (?) AND magnet_fingerprint <> ?", filmFileIDs, fingerprint).
		Delete(&model.CloudFileCache{}).Error
}
