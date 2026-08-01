package model

import "time"

type CacheList struct {
	ID        uint      `gorm:"primaryKey"`
	StorageID uint      `gorm:"uniqueIndex:idx_cache_storage_dir"`
	DirPath   string    `gorm:"uniqueIndex:idx_cache_storage_dir"`
	Data      string    `gorm:"type:text"`
	UpdatedAt time.Time
}
