package model

import "time"

type CachedObj struct {
	ID        string            `json:"id"`
	Path      string            `json:"path"`
	Name      string            `json:"name"`
	Size      int64             `json:"size"`
	Modified  time.Time         `json:"modified"`
	Ctime     time.Time         `json:"ctime"`
	IsFolder  bool              `json:"is_folder"`
	HashInfo  map[string]string `json:"hash_info"` // hash type name -> hash value
	Thumbnail string            `json:"thumbnail"`
}

type CacheList struct {
	ID        uint        `gorm:"primaryKey"`
	StorageID uint        `gorm:"uniqueIndex:idx_cache_storage_dir"`
	DirPath   string      `gorm:"uniqueIndex:idx_cache_storage_dir"`
	Data      []CachedObj `gorm:"type:json;serializer:json"`
	UpdatedAt time.Time
}
