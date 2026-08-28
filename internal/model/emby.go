package model

// EmbyDirSetting 某个目录的 Emby 元数据设置。Actors 为空表示未设置（nfo 生成时继承最近祖先的设置）。
type EmbyDirSetting struct {
	ID        uint   `gorm:"primaryKey"`
	StorageID uint   `gorm:"uniqueIndex:idx_emby_dir_setting"`
	DirPath   string `gorm:"uniqueIndex:idx_emby_dir_setting"`
	Actors    string
}
