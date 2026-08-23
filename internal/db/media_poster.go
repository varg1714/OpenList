package db

import (
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

const (
	mediaPosterBatchSize = 20
	posterRetryLimit     = 3
)

func QueryPendingMediaPosterWorks(storageID uint, source string, retryInterval time.Duration) ([]model.FilmWork, error) {
	var works []model.FilmWork
	err := db.Where("storage_id = ? AND source = ?", storageID, source).
		Where("image_url IS NOT NULL AND image_url <> ''").
		Where("dmm_poster_retry_count < ?", posterRetryLimit).
		Where("dmm_poster_status IS NULL OR dmm_poster_status = '' OR dmm_poster_status = ? OR (dmm_poster_status = ? AND (dmm_poster_scan_at IS NULL OR dmm_poster_scan_at < ?))",
			model.DMMPosterStatusPending, model.DMMPosterStatusTransientError, time.Now().Add(-retryInterval)).
		Order("id ASC").
		Limit(mediaPosterBatchSize).
		Find(&works).Error
	return works, err
}
