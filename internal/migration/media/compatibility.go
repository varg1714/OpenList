package media

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"gorm.io/gorm"
)

const legacyListingFileSize int64 = 1417381701

type plannedCacheAlias struct {
	work       *plannedWork
	row        model.MagnetCache
	name       string
	existingID uint
}

func validateNormalizedCompatibility(tx *gorm.DB, plan *migrationPlan) error {
	state, err := loadNormalizedFileState(tx)
	if err != nil {
		return err
	}
	if err := assignPlannedFileIDs(plan, state); err != nil {
		return err
	}

	for _, planned := range plan.works {
		existingWork, workExists := state.workByIdentity[planned.identity.String()]
		if workExists {
			if err := validateExistingWorkCompatibility(&existingWork, planned); err != nil {
				return err
			}
			if err := validateExistingFilesCompatibility(state.filesByWork[existingWork.ID], planned); err != nil {
				return err
			}
		}
		for _, file := range planned.files {
			if file.legacyID == 0 {
				continue
			}
			existingFile, exists := state.fileByID[file.legacyID]
			if !exists {
				continue
			}
			if !workExists || existingFile.WorkID != existingWork.ID || existingFile.PartIndex != file.partIndex {
				return normalizedCollision(planned, fmt.Sprintf("legacy file ID %d is owned by another normalized file", file.legacyID))
			}
		}
	}
	return validateExistingMagnetCompatibility(tx, plan, state.workByIdentity)
}

func validateExistingWorkCompatibility(existing *model.FilmWork, planned *plannedWork) error {
	if existing.PrimaryDir != planned.work.PrimaryDir {
		return normalizedCollision(planned, fmt.Sprintf("normalized primary directory %q differs from legacy %q", existing.PrimaryDir, planned.work.PrimaryDir))
	}
	if existing.SourceURL != "" && planned.work.SourceURL != "" && existing.SourceURL != planned.work.SourceURL {
		return normalizedCollision(planned, fmt.Sprintf("normalized source URL %q differs from legacy %q", existing.SourceURL, planned.work.SourceURL))
	}
	return nil
}

func validateExistingFilesCompatibility(existing []model.FilmFile, planned *plannedWork) error {
	if len(existing) == 0 {
		return nil
	}
	if len(existing) != len(planned.files) {
		return normalizedCollision(planned, fmt.Sprintf("normalized file count %d differs from legacy %d", len(existing), len(planned.files)))
	}
	for index, got := range existing {
		want := planned.files[index]
		if got.PartIndex != want.partIndex || got.PartCount != want.partCount {
			return normalizedCollision(planned, "normalized multipart topology differs from legacy")
		}
		if got.ID != want.fileID {
			return normalizedCollision(planned, fmt.Sprintf("normalized file ID %d differs from planned %d", got.ID, want.fileID))
		}
		if got.SourcePath != "" && got.SourcePath != want.sourcePath {
			return normalizedCollision(planned, fmt.Sprintf("normalized source path %q differs from legacy %q", got.SourcePath, want.sourcePath))
		}
		if got.SourceSize != 0 && got.SourceSize != want.sourceSize {
			return normalizedCollision(planned, fmt.Sprintf("normalized source size %d differs from legacy %d", got.SourceSize, want.sourceSize))
		}
		if timeDiffers(got.CreatedAt, want.createdAt) || timeDiffers(got.UpdatedAt, want.updatedAt) {
			return normalizedCollision(planned, "normalized file timestamps differ from legacy")
		}
	}
	return nil
}

func validateExistingMagnetCompatibility(tx *gorm.DB, plan *migrationPlan, works map[string]model.FilmWork) error {
	if !tx.Migrator().HasTable(&model.SourceMagnet{}) {
		return nil
	}
	for _, planned := range plan.magnets {
		work, exists := works[planned.work.identity.String()]
		if !exists {
			continue
		}
		var existing model.SourceMagnet
		err := tx.Where("work_id = ? AND fingerprint = ?", work.ID, planned.fingerprint).First(&existing).Error
		if err == nil && existing.MagnetURI != planned.magnetURI {
			return normalizedCollision(planned.work, "magnet fingerprint is attached to a different URI")
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("load normalized magnet for compatibility preflight: %w", err)
		}
	}
	return nil
}

func timeDiffers(existing, legacy time.Time) bool {
	return !legacy.IsZero() && !existing.IsZero() && !existing.Equal(legacy)
}

func normalizedCollision(planned *plannedWork, reason string) error {
	return &IdentityCollisionError{
		Identity: planned.identity, LegacyFilmIDs: append([]uint(nil), planned.filmIDs...), Reason: reason,
	}
}

func isCloudCacheWithRemoteHandle(cache model.MagnetCache) bool {
	if strings.TrimSpace(cache.FileId) == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(cache.DriverType)) {
	case "115 cloud", "pikpak":
		return true
	default:
		return false
	}
}

func planCacheAliases(existing []model.MagnetCache, candidates []plannedCacheAlias) ([]plannedCacheAlias, error) {
	existingByKey := make(map[string][]model.MagnetCache)
	for _, cache := range existing {
		key := cache.DriverType + "\x00" + cache.Name
		existingByKey[key] = append(existingByKey[key], cache)
	}

	aliases := make([]plannedCacheAlias, 0, len(candidates))
	aliasByKey := make(map[string]int, len(candidates))
	for _, candidate := range candidates {
		candidate.row.ID = 0
		candidate.row.Name = candidate.name
		key := candidate.row.DriverType + "\x00" + candidate.name
		if index, exists := aliasByKey[key]; exists {
			if !cacheAliasEqual(aliases[index].row, candidate.row) {
				return nil, cacheAliasCollision(candidate, "multiple legacy rows require different canonical aliases")
			}
			continue
		}
		aliasByKey[key] = len(aliases)
		aliases = append(aliases, candidate)
	}

	for index := range aliases {
		alias := &aliases[index]
		key := alias.row.DriverType + "\x00" + alias.name
		rows := existingByKey[key]
		if len(rows) > 1 {
			return nil, cacheAliasCollision(*alias, "canonical cache alias has multiple existing rows")
		}
		if len(rows) == 0 {
			continue
		}
		if !cacheAliasEqual(rows[0], alias.row) {
			return nil, cacheAliasCollision(*alias, "canonical cache alias conflicts with an existing row")
		}
		alias.existingID = rows[0].ID
	}
	return aliases, nil
}

func populateCacheAliases(tx *gorm.DB, plan *migrationPlan) error {
	for index := range plan.cacheAliases {
		alias := &plan.cacheAliases[index]
		if alias.existingID != 0 {
			continue
		}
		row := alias.row
		if err := tx.Create(&row).Error; err != nil {
			return fmt.Errorf("create canonical cache alias %s/%s: %w", row.DriverType, row.Name, err)
		}
		alias.existingID = row.ID
	}
	return nil
}

func validateCacheAliases(tx *gorm.DB, plan *migrationPlan) error {
	for _, alias := range plan.cacheAliases {
		var rows []model.MagnetCache
		if err := tx.Where("driver_type = ? AND name = ?", alias.row.DriverType, alias.name).Find(&rows).Error; err != nil {
			return fmt.Errorf("validate canonical cache alias %s/%s: %w", alias.row.DriverType, alias.name, err)
		}
		if len(rows) != 1 || !cacheAliasEqual(rows[0], alias.row) {
			return &ValidationError{Identity: alias.work.identity, Reason: fmt.Sprintf("canonical cache alias %s/%s is incomplete", alias.row.DriverType, alias.name)}
		}
	}
	return nil
}

func cacheAliasEqual(left, right model.MagnetCache) bool {
	return left.DriverType == right.DriverType && left.Name == right.Name && left.Magnet == right.Magnet &&
		left.FileId == right.FileId && left.Code == right.Code && maps.Equal(left.Option, right.Option) &&
		left.Subtitle == right.Subtitle && left.ScanAt.Equal(right.ScanAt) && left.ScanCount == right.ScanCount &&
		slices.Equal(left.SubtitleUrls, right.SubtitleUrls)
}

func cacheAliasCollision(alias plannedCacheAlias, reason string) error {
	return normalizedCollision(alias.work, fmt.Sprintf("%s for %s/%s", reason, alias.row.DriverType, alias.name))
}
