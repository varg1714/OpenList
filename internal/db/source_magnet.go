package db

import (
	"errors"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"gorm.io/gorm"
)

func UpsertSourceMagnets(workID uint, magnets []model.SourceMagnet) error {
	return db.Transaction(func(tx *gorm.DB) error {
		for i := range magnets {
			magnet := magnets[i]
			var existing model.SourceMagnet
			err := tx.Where("work_id = ? AND fingerprint = ?", workID, magnet.Fingerprint).First(&existing).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				magnet.ID = 0
				magnet.WorkID = workID
				if err := tx.Create(&magnet).Error; err != nil {
					return err
				}
				continue
			}
			if err != nil {
				return err
			}

			if err := tx.Model(&existing).Updates(map[string]interface{}{
				"magnet_uri":    magnet.MagnetURI,
				"provider":      magnet.Provider,
				"priority":      magnet.Priority,
				"selected":      magnet.Selected,
				"subtitle":      magnet.Subtitle,
				"file_manifest": magnet.FileManifest,
				"scan_at":       magnet.ScanAt,
				"last_error":    magnet.LastError,
			}).Error; err != nil {
				return err
			}
		}

		_, err := ensureSelectedSourceMagnet(tx, workID)
		return err
	})
}

func GetSelectedSourceMagnet(workID uint) (model.SourceMagnet, error) {
	var selected model.SourceMagnet
	err := db.Transaction(func(tx *gorm.DB) error {
		var err error
		selected, err = ensureSelectedSourceMagnet(tx, workID)
		return err
	})
	return selected, err
}

func SelectSourceMagnet(workID, magnetID uint) error {
	return db.Transaction(func(tx *gorm.DB) error {
		var candidate model.SourceMagnet
		if err := tx.Where("id = ? AND work_id = ?", magnetID, workID).First(&candidate).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.SourceMagnet{}).Where("work_id = ?", workID).Update("selected", false).Error; err != nil {
			return err
		}
		return tx.Model(&candidate).Update("selected", true).Error
	})
}

func ListSourceMagnets(workID uint) ([]model.SourceMagnet, error) {
	var magnets []model.SourceMagnet
	err := db.Where("work_id = ?", workID).Order("priority ASC, id ASC").Find(&magnets).Error
	return magnets, err
}

func ensureSelectedSourceMagnet(tx *gorm.DB, workID uint) (model.SourceMagnet, error) {
	var magnets []model.SourceMagnet
	if err := tx.Where("work_id = ?", workID).Order("selected DESC, priority ASC, id ASC").Find(&magnets).Error; err != nil {
		return model.SourceMagnet{}, err
	}
	if len(magnets) == 0 {
		return model.SourceMagnet{}, gorm.ErrRecordNotFound
	}

	selected := magnets[0]
	if err := tx.Model(&model.SourceMagnet{}).Where("work_id = ?", workID).Update("selected", false).Error; err != nil {
		return model.SourceMagnet{}, err
	}
	if err := tx.Model(&selected).Update("selected", true).Error; err != nil {
		return model.SourceMagnet{}, err
	}
	selected.Selected = true
	return selected, nil
}
