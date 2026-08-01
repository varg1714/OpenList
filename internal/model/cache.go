package model

import "time"

type CachedObj struct {
	ID        string
	Path      string
	Name      string
	Size      int64
	Modified  time.Time
	Ctime     time.Time
	IsFolder  bool
	HashInfo  map[string]string // hash type name -> hash value
	Thumbnail string
}

type CacheList struct {
	ID        uint        `gorm:"primaryKey"`
	StorageID uint        `gorm:"uniqueIndex:idx_cache_storage_dir"`
	DirPath   string      `gorm:"uniqueIndex:idx_cache_storage_dir"`
	Data      []CachedObj `gorm:"type:json;serializer:json"`
	UpdatedAt time.Time
}
