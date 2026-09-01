package model

// EmbyDirSetting 某个目录的 Emby 元数据设置。各字段独立生效（分维度继承）：
// Actors 为空且 UseNameAsActor 为 false 表示未配置演员；Plot 为空表示未配置简介；
// AppendFileNameToPlot 为 nil 表示未配置（false 为显式关闭，阻断上层继承）。
// TvShow 标记该目录为电视剧（本地生效，不继承）；TvShowName 为自定义剧名（空回退文件夹名）；
// TvShowSubfolders 开启后直接子文件夹视同标记为电视剧（动态继承，仅一层）。
// 全部字段未配置（append 为 nil 或 false）时该行会被删除，删除后恢复上层继承。
type EmbyDirSetting struct {
	ID                   uint   `gorm:"primaryKey"`
	StorageID            uint   `gorm:"uniqueIndex:idx_emby_dir_setting"`
	DirPath              string `gorm:"uniqueIndex:idx_emby_dir_setting"`
	Actors               string
	UseNameAsActor       bool
	Plot                 string
	AppendFileNameToPlot *bool
	TvShow               bool
	TvShowName           string
	TvShowSubfolders     bool
}
