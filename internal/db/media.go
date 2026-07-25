package db

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"gorm.io/gorm"
)

func UpsertDiscoveredWork(work *model.FilmWork) error {
	return db.Transaction(func(tx *gorm.DB) error {
		var existing model.FilmWork
		err := tx.Where("storage_id = ? AND source = ? AND code = ?", work.StorageID, work.Source, work.Code).First(&existing).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			work.Actors = stableUnionStringArrays(nil, work.Actors)
			work.Tags = stableUnionStringArrays(nil, work.Tags)
			if work.MetadataVersion <= work.NfoVersion {
				work.MetadataVersion = work.NfoVersion + 1
			}
			return tx.Create(work).Error
		}
		if err != nil {
			return err
		}
		work.ID = existing.ID

		updates := map[string]interface{}{"updated_at": time.Now()}
		if work.SourceRef != "" && work.SourceRef != existing.SourceRef {
			updates["source_ref"] = work.SourceRef
		}
		if work.SourceURL != "" && work.SourceURL != existing.SourceURL {
			updates["source_url"] = work.SourceURL
		}

		rawTitle := existing.RawTitle
		if work.RawTitle != "" && work.RawTitle != existing.RawTitle {
			updates["raw_title"] = work.RawTitle
			rawTitle = work.RawTitle
		}
		if work.ImageURL != "" && work.ImageURL != existing.ImageURL {
			updates["image_url"] = work.ImageURL
		}
		releaseDate := existing.ReleaseDate
		if !work.ReleaseDate.IsZero() && !work.ReleaseDate.Equal(existing.ReleaseDate) {
			updates["release_date"] = work.ReleaseDate
			releaseDate = work.ReleaseDate
		}
		actors := stableUnionStringArrays(existing.Actors, work.Actors)
		if !slices.Equal(existing.Actors, actors) {
			updates["actors"] = actors
		}
		tags := stableUnionStringArrays(existing.Tags, work.Tags)
		if !slices.Equal(existing.Tags, tags) {
			updates["tags"] = tags
		}
		existingNFORelease := ""
		if !existing.ReleaseDate.IsZero() {
			existingNFORelease = existing.ReleaseDate.Format(time.DateOnly)
		}
		candidateNFORelease := ""
		if !releaseDate.IsZero() {
			candidateNFORelease = releaseDate.Format(time.DateOnly)
		}
		metadataChanged := model.BuildMediaTitle(existing.Code, existing.RawTitle, existing.TranslatedTitle) != model.BuildMediaTitle(existing.Code, rawTitle, existing.TranslatedTitle) ||
			existingNFORelease != candidateNFORelease ||
			!slices.Equal(existing.Actors, actors) ||
			!slices.Equal(existing.Tags, tags)
		if metadataChanged {
			updates["metadata_version"] = gorm.Expr("metadata_version + 1")
		}
		return tx.Model(&model.FilmWork{}).Where("id = ?", existing.ID).Updates(updates).Error
	})
}

func stableUnionStringArrays(current, incoming model.StringArray) model.StringArray {
	seen := make(map[string]struct{}, len(current)+len(incoming))
	result := make(model.StringArray, 0, len(current)+len(incoming))
	for _, values := range []model.StringArray{current, incoming} {
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			if _, exists := seen[value]; exists {
				continue
			}
			seen[value] = struct{}{}
			result = append(result, value)
		}
	}
	return result
}

func EnsureSingleFilmFile(workID uint) (model.FilmFile, error) {
	var file model.FilmFile
	err := db.Transaction(func(tx *gorm.DB) error {
		var files []model.FilmFile
		if err := tx.Where("work_id = ?", workID).Order("part_index ASC").Find(&files).Error; err != nil {
			return err
		}

		switch len(files) {
		case 0:
			file = model.FilmFile{WorkID: workID, PartIndex: 1, PartCount: 1}
			return tx.Create(&file).Error
		case 1:
			if files[0].PartIndex != 1 || files[0].PartCount != 1 {
				return fmt.Errorf("work %d has non-single file topology", workID)
			}
			file = files[0]
			return nil
		default:
			return fmt.Errorf("work %d has multipart file topology", workID)
		}
	})
	return file, err
}

func GetFilmWork(id uint) (model.FilmWork, error) {
	var work model.FilmWork
	err := db.First(&work, id).Error
	return work, err
}

func GetFilmWorkByIdentity(storageID uint, source, code string) (model.FilmWork, error) {
	var work model.FilmWork
	err := db.Where("storage_id = ? AND source = ? AND code = ?", storageID, source, code).First(&work).Error
	return work, err
}

func UpdateMediaWorkDetails(workID uint, translatedTitle, synopsis string, actors, tags model.StringArray) error {
	updates := map[string]interface{}{
		"translated_title": translatedTitle,
		"synopsis":         synopsis,
		"actors":           actors,
		"tags":             tags,
		"metadata_version": gorm.Expr("metadata_version + 1"),
	}
	return db.Model(&model.FilmWork{}).Where("id = ?", workID).Updates(updates).Error
}

func GetFilmFile(id uint) (model.FilmFile, error) {
	var file model.FilmFile
	err := db.First(&file, id).Error
	return file, err
}

func GetFilmFileWithWork(id uint) (model.FilmFileWithWork, error) {
	var file model.FilmFileWithWork
	err := db.Model(&model.FilmFile{}).Preload("Work").First(&file, id).Error
	return file, err
}

func ListFilmWorks(storageID uint, source, primaryDir string) ([]model.FilmWork, error) {
	var works []model.FilmWork
	err := db.Where("storage_id = ? AND source = ? AND primary_dir = ?", storageID, source, primaryDir).
		Order("id ASC").
		Find(&works).Error
	return works, err
}

func ListFilmWorksByStorageSource(storageID uint, source string) ([]model.FilmWork, error) {
	var works []model.FilmWork
	err := db.Where("storage_id = ? AND source = ?", storageID, source).Order("id ASC").Find(&works).Error
	return works, err
}

func QueryFilmWorksByCodePrefixes(storageID uint, source string, prefixes []string) ([]model.FilmWork, error) {
	if len(prefixes) == 0 {
		return []model.FilmWork{}, nil
	}
	prefixQuery := db.Where("code LIKE ?", prefixes[0]+"%")
	for _, prefix := range prefixes[1:] {
		prefixQuery = prefixQuery.Or("code LIKE ?", prefix+"%")
	}
	var works []model.FilmWork
	err := db.Where("storage_id = ? AND source = ?", storageID, source).
		Where(prefixQuery).
		Order("id ASC").
		Find(&works).Error
	return works, err
}

func ListFilmFiles(workID uint) ([]model.FilmFile, error) {
	var files []model.FilmFile
	err := db.Where("work_id = ?", workID).Order("part_index ASC").Find(&files).Error
	return files, err
}

func ListFilmFilesWithWorks(storageID uint, source, primaryDir string) ([]model.FilmFileWithWork, error) {
	works, err := ListFilmWorks(storageID, source, primaryDir)
	if err != nil || len(works) == 0 {
		return []model.FilmFileWithWork{}, err
	}
	workIDs := make([]uint, len(works))
	workByID := make(map[uint]model.FilmWork, len(works))
	for i, work := range works {
		workIDs[i] = work.ID
		workByID[work.ID] = work
	}

	var files []model.FilmFile
	if err := db.Where("work_id IN ?", workIDs).Order("work_id ASC, part_index ASC").Find(&files).Error; err != nil {
		return nil, err
	}
	rows := make([]model.FilmFileWithWork, 0, len(files))
	for _, file := range files {
		rows = append(rows, model.FilmFileWithWork{FilmFile: file, Work: workByID[file.WorkID]})
	}
	return rows, nil
}

func ReplaceFilmFiles(workID uint, files []model.FilmFile) error {
	return db.Transaction(func(tx *gorm.DB) error {
		partCount := len(files)
		if partCount == 0 {
			return fmt.Errorf("work %d must have at least one film file", workID)
		}
		seen := make([]bool, partCount)
		for i := range files {
			if files[i].PartCount != partCount {
				return fmt.Errorf("part %d has part count %d, want %d", files[i].PartIndex, files[i].PartCount, partCount)
			}
			if files[i].PartIndex < 1 || files[i].PartIndex > partCount {
				return fmt.Errorf("part index %d is outside contiguous range 1..%d", files[i].PartIndex, partCount)
			}
			if seen[files[i].PartIndex-1] {
				return fmt.Errorf("duplicate part index %d", files[i].PartIndex)
			}
			seen[files[i].PartIndex-1] = true
		}

		var existing []model.FilmFile
		if err := tx.Where("work_id = ?", workID).Find(&existing).Error; err != nil {
			return err
		}
		existingByPart := make(map[int]model.FilmFile, len(existing))
		for _, file := range existing {
			existingByPart[file.PartIndex] = file
		}
		keptIDs := make(map[uint]struct{}, len(files))
		for i := range files {
			files[i].WorkID = workID
			if current, ok := existingByPart[files[i].PartIndex]; ok {
				files[i].ID = current.ID
				keptIDs[current.ID] = struct{}{}
				if err := tx.Model(&model.FilmFile{}).Where("id = ?", current.ID).Updates(map[string]interface{}{
					"part_count":  files[i].PartCount,
					"source_path": files[i].SourcePath,
					"source_size": files[i].SourceSize,
				}).Error; err != nil {
					return err
				}
			}
		}
		removedIDs := make([]uint, 0)
		for _, current := range existing {
			if _, ok := keptIDs[current.ID]; !ok {
				removedIDs = append(removedIDs, current.ID)
			}
		}
		if len(removedIDs) > 0 {
			if err := tx.Where("id IN ?", removedIDs).Delete(&model.FilmFile{}).Error; err != nil {
				return err
			}
		}
		newFiles := make([]model.FilmFile, 0, len(files))
		for _, file := range files {
			if file.ID == 0 {
				newFiles = append(newFiles, file)
			}
		}
		if len(newFiles) > 0 {
			return tx.Create(&newFiles).Error
		}
		return nil
	})
}

func QueryTranslationMediaWorks(source string, translationVersion uint, limit int) ([]model.FilmWork, error) {
	var works []model.FilmWork
	now := time.Now()
	query := db.Where("source = ?", source).
		Where("translation_version < ? OR translated_title = '' OR translated_title IS NULL", translationVersion).
		Where("translation_next_retry_at IS NULL OR translation_next_retry_at <= ?", now).
		Where("translation_status IS NULL OR translation_status <> ?", "success").
		Order("id ASC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	return works, query.Find(&works).Error
}

func UpdateMediaWorkTranslation(workID uint, translatedTitle string, translationVersion uint) error {
	return db.Model(&model.FilmWork{}).Where("id = ?", workID).Updates(map[string]interface{}{
		"translated_title":          translatedTitle,
		"translation_status":        "success",
		"translation_next_retry_at": nil,
		"translation_last_error":    "",
		"translation_version":       translationVersion,
		"metadata_version":          gorm.Expr("metadata_version + 1"),
	}).Error
}

func UpdateMediaWorkTranslationRetry(workID uint, nextRetryAt time.Time, lastError string, translationVersion uint) error {
	return db.Model(&model.FilmWork{}).Where("id = ?", workID).Updates(map[string]interface{}{
		"translation_status":        "retry_wait",
		"translation_attempts":      gorm.Expr("translation_attempts + 1"),
		"translation_next_retry_at": nextRetryAt,
		"translation_last_error":    lastError,
		"translation_version":       translationVersion,
	}).Error
}

func QueryEmptySynopsisMediaWorks(source string, scanInterval time.Duration, limit int) ([]model.FilmWork, error) {
	var works []model.FilmWork
	now := time.Now()
	query := db.Where("source = ?", source).
		Where("synopsis IS NULL OR synopsis = ''").
		Where("synopsis_excluded = ?", false).
		Where("(synopsis_scan_at IS NULL OR synopsis_scan_at < ?) AND (synopsis_next_retry_at IS NULL OR synopsis_next_retry_at <= ?)", now.Add(-scanInterval), now).
		Order("release_date DESC, id DESC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	return works, query.Find(&works).Error
}

func UpdateMediaWorkSynopsis(workID uint, synopsis string) error {
	return db.Model(&model.FilmWork{}).Where("id = ?", workID).Updates(map[string]interface{}{
		"synopsis":               synopsis,
		"synopsis_scan_at":       time.Now(),
		"synopsis_next_retry_at": nil,
		"synopsis_last_error":    "",
		"synopsis_excluded":      false,
		"metadata_version":       gorm.Expr("metadata_version + 1"),
	}).Error
}

func UpdateMediaWorkSynopsisRetry(workID uint, nextRetryAt time.Time, lastError string) error {
	now := time.Now()
	return db.Model(&model.FilmWork{}).Where("id = ?", workID).Updates(map[string]interface{}{
		"synopsis_scan_at":       now,
		"synopsis_next_retry_at": nextRetryAt,
		"synopsis_last_error":    lastError,
	}).Error
}

func MarkMediaWorkSynopsisExcluded(workID uint) error {
	return db.Model(&model.FilmWork{}).Where("id = ?", workID).Updates(map[string]interface{}{
		"synopsis_excluded":      true,
		"synopsis_scan_at":       time.Now(),
		"synopsis_next_retry_at": nil,
		"synopsis_last_error":    "",
	}).Error
}

func QueryTagMediaWorks(source string, limit int) ([]model.FilmWork, error) {
	var works []model.FilmWork
	now := time.Now()
	query := db.Where("source = ?", source).
		Where("(tags IS NULL OR tags = ? OR tag_scan_at IS NULL)", "[]").
		Where("tag_next_retry_at IS NULL OR tag_next_retry_at <= ?", now).
		Order("id ASC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	return works, query.Find(&works).Error
}

func UpdateMediaWorkTags(workID uint, tags model.StringArray, tagVersion uint) error {
	var existing model.FilmWork
	if err := db.Select("tags").First(&existing, workID).Error; err != nil {
		return err
	}
	tags = stableUnionStringArrays(existing.Tags, tags)
	updates := map[string]interface{}{
		"tag_scan_at":       time.Now(),
		"tag_next_retry_at": nil,
		"tag_last_error":    "",
		"tag_version":       tagVersion,
	}
	if !slices.Equal([]string(existing.Tags), []string(tags)) {
		updates["tags"] = tags
		updates["metadata_version"] = gorm.Expr("metadata_version + 1")
	}
	return db.Model(&model.FilmWork{}).Where("id = ?", workID).Updates(updates).Error
}

func MergePendingMediaWorkTags(workID uint, tags model.StringArray) error {
	return db.Model(&model.FilmWork{}).Where("id = ?", workID).Updates(map[string]interface{}{
		"tags":             tags,
		"metadata_version": gorm.Expr("metadata_version + 1"),
	}).Error
}

func UpdateMediaWorkTagRetry(workID uint, nextRetryAt time.Time, lastError string) error {
	return db.Model(&model.FilmWork{}).Where("id = ?", workID).Updates(map[string]interface{}{
		"tag_scan_at":       time.Now(),
		"tag_next_retry_at": nextRetryAt,
		"tag_last_error":    lastError,
	}).Error
}

func UpdateMediaWorkActors(workID uint, actors model.StringArray) error {
	var existing model.FilmWork
	if err := db.Select("actors").First(&existing, workID).Error; err != nil {
		return err
	}
	actors = stableUnionStringArrays(existing.Actors, actors)
	updates := map[string]interface{}{
		"actor_scan_at":       time.Now(),
		"actor_next_retry_at": nil,
		"actor_last_error":    "",
	}
	if !slices.Equal([]string(existing.Actors), []string(actors)) {
		updates["actors"] = actors
		updates["metadata_version"] = gorm.Expr("metadata_version + 1")
	}
	return db.Model(&model.FilmWork{}).Where("id = ?", workID).Updates(updates).Error
}

func UpdateMediaWorkActorRetry(workID uint, nextRetryAt time.Time, lastError string) error {
	return db.Model(&model.FilmWork{}).Where("id = ?", workID).Updates(map[string]interface{}{
		"actor_scan_at":       time.Now(),
		"actor_next_retry_at": nextRetryAt,
		"actor_last_error":    lastError,
	}).Error
}

func UpdateMediaWorkMagnetScan(workID uint, nextRetryAt *time.Time, lastError string) error {
	return db.Model(&model.FilmWork{}).Where("id = ?", workID).Updates(map[string]interface{}{
		"magnet_scan_at":       time.Now(),
		"magnet_next_retry_at": nextRetryAt,
		"magnet_last_error":    lastError,
	}).Error
}

func QueryReleaseMediaWorks(source string, limit int) ([]model.FilmWork, error) {
	var works []model.FilmWork
	now := time.Now()
	query := db.Where("source = ?", source).
		Where("release_date IS NULL OR release_date = ?", time.Time{}).
		Where("release_next_retry_at IS NULL OR release_next_retry_at <= ?", now).
		Order("id ASC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	return works, query.Find(&works).Error
}

func UpdateMediaWorkRelease(workID uint, releaseDate time.Time) error {
	return db.Model(&model.FilmWork{}).Where("id = ?", workID).Updates(map[string]interface{}{
		"release_date":          releaseDate,
		"release_scan_at":       time.Now(),
		"release_next_retry_at": nil,
		"release_last_error":    "",
		"metadata_version":      gorm.Expr("metadata_version + 1"),
	}).Error
}

func UpdateMediaWorkReleaseRetry(workID uint, nextRetryAt time.Time, lastError string) error {
	return db.Model(&model.FilmWork{}).Where("id = ?", workID).Updates(map[string]interface{}{
		"release_scan_at":       time.Now(),
		"release_next_retry_at": nextRetryAt,
		"release_last_error":    lastError,
	}).Error
}

func QuerySampleImageMediaWorks(source string, scanInterval time.Duration, limit int) ([]model.FilmWork, error) {
	var works []model.FilmWork
	query := db.Where("source = ?", source).
		Where("image_url IS NOT NULL AND image_url <> ''").
		Where("sample_image_complete = ?", false).
		Where("sample_image_scan_at IS NULL OR sample_image_scan_at < ?", time.Now().Add(-scanInterval)).
		Order("release_date DESC, id DESC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	return works, query.Find(&works).Error
}

func UpdateMediaWorkSampleProgress(workID uint, count int, complete bool) error {
	updates := map[string]interface{}{
		"sample_image_count": gorm.Expr("CASE WHEN sample_image_count < ? THEN ? ELSE sample_image_count END", count, count),
	}
	if complete {
		updates["sample_image_complete"] = true
		updates["sample_image_scan_at"] = time.Now()
	}
	return db.Model(&model.FilmWork{}).Where("id = ?", workID).Updates(updates).Error
}

func UpdateMediaWorkSampleScan(workID uint, complete bool) error {
	return db.Model(&model.FilmWork{}).Where("id = ?", workID).Updates(map[string]interface{}{
		"sample_image_complete": complete,
		"sample_image_scan_at":  time.Now(),
	}).Error
}

func QueryDMMPosterMediaWorks(retryInterval time.Duration, limit int) ([]model.FilmWork, error) {
	var works []model.FilmWork
	query := db.Where("source = ?", "javdb").
		Where("dmm_poster_status IS NULL OR dmm_poster_status = '' OR dmm_poster_status = ? OR (dmm_poster_status = ? AND (dmm_poster_scan_at IS NULL OR dmm_poster_scan_at < ?))",
			model.DMMPosterStatusPending, model.DMMPosterStatusTransientError, time.Now().Add(-retryInterval)).
		Order("release_date DESC, id DESC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	return works, query.Find(&works).Error
}

func UpdateMediaWorkDMMPosterStatus(workID uint, status string) error {
	return db.Model(&model.FilmWork{}).Where("id = ?", workID).Updates(map[string]interface{}{
		"dmm_poster_status":  status,
		"dmm_poster_scan_at": time.Now(),
	}).Error
}

func QuerySubtitleMediaWorks(source string, limit int) ([]model.FilmWork, error) {
	now := time.Now()
	query := db.Where("source = ? AND ((subtitle_scan_at IS NULL AND subtitle_next_retry_at IS NULL) OR (subtitle_next_retry_at IS NOT NULL AND subtitle_next_retry_at <= ?))", source, now).Order("id ASC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	var works []model.FilmWork
	return works, query.Find(&works).Error
}

func UpdateMediaWorkSubtitleScan(workID uint, nextRetryAt *time.Time, lastError string) error {
	return db.Model(&model.FilmWork{}).Where("id = ?", workID).Updates(map[string]interface{}{
		"subtitle_scan_at":       time.Now(),
		"subtitle_next_retry_at": nextRetryAt,
		"subtitle_last_error":    lastError,
	}).Error
}

func QueryStaleNFOMediaWorks(source string, limit int) ([]model.FilmWork, error) {
	var works []model.FilmWork
	query := db.Where("source = ? AND nfo_version < metadata_version", source).Order("id ASC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	return works, query.Find(&works).Error
}

func QueryMediaWorksForNFOSync(storageID uint, source string, force bool, limit int) ([]model.FilmWork, error) {
	var works []model.FilmWork
	query := db.Where("storage_id = ? AND source = ?", storageID, source).Order("id ASC")
	if !force {
		query = query.Where("nfo_version < metadata_version")
	}
	if limit > 0 {
		query = query.Limit(limit)
	}
	return works, query.Find(&works).Error
}

func UpdateMediaWorkNFOResult(workID, nfoVersion uint, lastError string) error {
	updates := map[string]interface{}{"nfo_last_error": lastError}
	if lastError == "" {
		updates["nfo_version"] = nfoVersion
	}
	return db.Model(&model.FilmWork{}).Where("id = ?", workID).Updates(updates).Error
}

func DeleteFilmWork(workID uint) error {
	return db.Transaction(func(tx *gorm.DB) error {
		return deleteFilmWork(tx, workID)
	})
}

func DeleteMediaFile(fileID uint) error {
	return db.Transaction(func(tx *gorm.DB) error {
		var file model.FilmFile
		if err := tx.First(&file, fileID).Error; err != nil {
			return err
		}
		return deleteFilmWork(tx, file.WorkID)
	})
}

func deleteFilmWork(tx *gorm.DB, workID uint) error {
	var work model.FilmWork
	if err := tx.First(&work, workID).Error; err != nil {
		return err
	}
	if err := tx.Where("code = ?", work.Code).Delete(&model.MagnetCache{}).Error; err != nil {
		return err
	}
	if err := tx.Where("work_id = ?", workID).Delete(&model.SourceMagnet{}).Error; err != nil {
		return err
	}
	if err := tx.Where("work_id = ?", workID).Delete(&model.FilmFile{}).Error; err != nil {
		return err
	}
	result := tx.Where("id = ?", workID).Delete(&model.FilmWork{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
