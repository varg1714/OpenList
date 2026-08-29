package model

// EmbyDirSetting 某个目录的 Emby 元数据设置。Actors 为空且 UseNameAsActor 为 false 表示未设置。
// UseNameAsActor 开启后，该目录的直接子文件夹以各自名称为 actor（后代继承）；手动 Actors 优先。
type EmbyDirSetting struct {
	ID             uint   `gorm:"primaryKey"`
	StorageID      uint   `gorm:"uniqueIndex:idx_emby_dir_setting"`
	DirPath        string `gorm:"uniqueIndex:idx_emby_dir_setting"`
	Actors         string
	UseNameAsActor bool
}
