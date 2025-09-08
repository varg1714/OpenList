package db

import (
	"fmt"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

func CreateMovedItem(movedItem model.MovedItem) error {
	return errors.WithStack(db.Create(&movedItem).Error)
}

func QueryMovedItemByParent(parent string) ([]model.MovedItem, error) {
	var movedItems []model.MovedItem
	err := errors.WithStack(db.Where(model.MovedItem{Parent: parent}).Find(&movedItems).Error)
	return movedItems, err
}

func QueryMovedItemByShareId(storageId uint, shareId, sourcePath string) ([]model.MovedItem, error) {
	var movedItems []model.MovedItem
	err := errors.WithStack(db.Where("share_id = ?", shareId).
		Where("source like ?", fmt.Sprintf("%%%s%%", sourcePath)).
		Where("storage_id = ?", storageId).
		Find(&movedItems).Error)
	return movedItems, err
}

func QueryMovedItemByFileId(fileId, sourcePath string) (model.MovedItem, error) {
	var movedItem model.MovedItem
	tx := db.Where("file_id = ?", fileId).
		Where("source like ?", fmt.Sprintf("%%%s%%", sourcePath)).
		Limit(1).Find(&movedItem)
	return movedItem, errors.WithStack(tx.Error)
}

func UpdateMovedItem(movedItem model.MovedItem) error {
	return errors.WithStack(db.Updates(&movedItem).Error)
}

func UpdateMovedItemSource(storageId uint, oldSource, newSource string) error {

	tx := db.Model(&model.MovedItem{}).Where("storage_id = ?", storageId).Where("source like ?", fmt.Sprintf("%s%%", oldSource)).
		Updates(map[string]interface{}{
			"source": gorm.Expr("replace(source, ?, ?)", oldSource, newSource),
		})

	return errors.WithStack(tx.Error)

}
