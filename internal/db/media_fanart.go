package db

import (
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

func QueryFanartMediaWorks(storageID uint, source string, scanInterval time.Duration, limit, targetCount int) ([]model.FilmWork, error) {
	var works []model.FilmWork
	query := db.Where("storage_id = ? AND source = ?", storageID, source).
		Where("source_ref IS NOT NULL AND source_ref <> ''").
		Where("(COALESCE(sample_image_count, 0) < ? OR sample_image_complete = ?)", targetCount, true).
		Where("sample_image_scan_at IS NULL OR sample_image_scan_at < ?", time.Now().Add(-scanInterval)).
		Order("release_date DESC, id DESC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	return works, query.Find(&works).Error
}

func UpdateMediaWorkSampleScanAt(workID uint) error {
	return db.Model(&model.FilmWork{}).
		Where("id = ?", workID).
		Update("sample_image_scan_at", time.Now()).Error
}
