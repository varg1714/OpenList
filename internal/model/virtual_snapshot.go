package model

import "time"

// VirtualDirSnapshot 虚拟目录驱动的列表持久化快照（bilibili 首个接入者）。
// StorageID+DirKey 唯一：DirKey 由驱动自定义（bilibili = 目录 obj.ID，
// 如 followings / favs / up_{mid} / fav_{media_id}），不随显示名变化。
// Owner 为可选账号标识（bilibili = 登录 uid），换账号时用于清理旧数据。
// Data 为驱动自管 JSON 文本（原始 API 条目 + 格式版本）。
type VirtualDirSnapshot struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	StorageID uint      `gorm:"uniqueIndex:idx_vdir_snap,priority:1" json:"storage_id"`
	DirKey    string    `gorm:"uniqueIndex:idx_vdir_snap,priority:2;type:varchar(255)" json:"dir_key"`
	Owner     string    `gorm:"index:idx_vdir_snap_owner;type:varchar(64);default:''" json:"owner"`
	Data      string    `gorm:"type:text" json:"data"`
	UpdatedAt time.Time `json:"updated_at"`
}
