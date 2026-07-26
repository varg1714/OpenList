package db

import (
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

func QueryUnscannedOrDueMediaTagWorks(source string, limit int) ([]model.FilmWork, error) {
	var works []model.FilmWork
	now := time.Now()
	query := db.Where("source = ?", source).
		Where("(tag_scan_at IS NULL AND tag_next_retry_at IS NULL) OR (tag_next_retry_at IS NOT NULL AND tag_next_retry_at <= ?)", now).
		Order("id DESC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	return works, query.Find(&works).Error
}

func QueryGeneratedSampleImageMediaWorks(source string, scanInterval time.Duration, limit int) ([]model.FilmWork, error) {
	var works []model.FilmWork
	query := db.Where("source = ?", source).
		Where("sample_image_complete = ?", false).
		Where("sample_image_scan_at IS NULL OR sample_image_scan_at < ?", time.Now().Add(-scanInterval)).
		Order("release_date DESC, id DESC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	return works, query.Find(&works).Error
}
