package db

import (
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GetVirtualDirSnapshot 读快照；不存在返回 (nil, nil)（调用方少一个分支）
func GetVirtualDirSnapshot(storageID uint, dirKey string) (*model.VirtualDirSnapshot, error) {
	var snap model.VirtualDirSnapshot
	err := db.Where("storage_id = ? AND dir_key = ?", storageID, dirKey).First(&snap).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.WithStack(err)
	}
	return &snap, nil
}

// UpsertVirtualDirSnapshot 同 key 覆盖写入（Data 整体替换 + UpdatedAt 刷新）
func UpsertVirtualDirSnapshot(s *model.VirtualDirSnapshot) error {
	s.UpdatedAt = time.Now()
	return errors.WithStack(db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "storage_id"}, {Name: "dir_key"}},
		DoUpdates: clause.AssignmentColumns([]string{"data", "owner", "updated_at"}),
	}).Create(s).Error)
}

// DeleteVirtualDirSnapshotsNotOwner 清掉非当前 owner 的快照（换账号清理）。
// 注意 owner="" 的无主行也会被删——语义即"只保留当前账号的数据"。
func DeleteVirtualDirSnapshotsNotOwner(storageID uint, owner string) error {
	return errors.WithStack(db.Where("storage_id = ? AND owner <> ?", storageID, owner).
		Delete(&model.VirtualDirSnapshot{}).Error)
}
