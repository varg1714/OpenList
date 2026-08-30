package model

// EmbyDirSetting 某个目录的 Emby 元数据设置。各字段独立生效（分维度继承）：
// Actors 为空且 UseNameAsActor 为 false 表示未配置演员；Plot 为空表示未配置简介；
// AppendFileNameToPlot 为 nil 表示未配置（false 为显式关闭，阻断上层继承）。
// 全部字段未配置时不应存在该行。
type EmbyDirSetting struct {
	ID                   uint   `gorm:"primaryKey"`
	StorageID            uint   `gorm:"uniqueIndex:idx_emby_dir_setting"`
	DirPath              string `gorm:"uniqueIndex:idx_emby_dir_setting"`
	Actors               string
	UseNameAsActor       bool
	Plot                 string
	AppendFileNameToPlot *bool
}
