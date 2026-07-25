package media

import (
	"fmt"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"gorm.io/gorm"
)

type normalizedFileState struct {
	workByIdentity map[string]model.FilmWork
	filesByWork    map[uint][]model.FilmFile
	fileByID       map[uint]model.FilmFile
}

func loadNormalizedFileState(tx *gorm.DB) (normalizedFileState, error) {
	state := normalizedFileState{
		workByIdentity: make(map[string]model.FilmWork),
		filesByWork:    make(map[uint][]model.FilmFile),
		fileByID:       make(map[uint]model.FilmFile),
	}
	if tx.Migrator().HasTable(&model.FilmWork{}) {
		var works []model.FilmWork
		if err := tx.Order("id ASC").Find(&works).Error; err != nil {
			return normalizedFileState{}, fmt.Errorf("load normalized works for compatibility preflight: %w", err)
		}
		for _, work := range works {
			identity := Identity{StorageID: work.StorageID, Source: work.Source, Code: work.Code}
			state.workByIdentity[identity.String()] = work
		}
	}
	if tx.Migrator().HasTable(&model.FilmFile{}) {
		var files []model.FilmFile
		if err := tx.Order("work_id ASC, part_index ASC").Find(&files).Error; err != nil {
			return normalizedFileState{}, fmt.Errorf("load normalized files for compatibility preflight: %w", err)
		}
		for _, file := range files {
			state.filesByWork[file.WorkID] = append(state.filesByWork[file.WorkID], file)
			state.fileByID[file.ID] = file
		}
	}
	return state, nil
}

func assignPlannedFileIDs(plan *migrationPlan, state normalizedFileState) error {
	reserved := make(map[uint]struct{}, len(state.fileByID))
	var maxExistingID uint
	for id := range state.fileByID {
		reserved[id] = struct{}{}
		if id > maxExistingID {
			maxExistingID = id
		}
	}
	for _, work := range plan.works {
		for index := range work.files {
			if work.files[index].legacyID != 0 {
				work.files[index].fileID = work.files[index].legacyID
				reserved[work.files[index].legacyID] = struct{}{}
			}
		}
	}

	nextID := maxExistingID + 1
	for _, work := range plan.works {
		existingWork, workExists := state.workByIdentity[work.identity.String()]
		existingByPart := make(map[int]uint)
		if workExists {
			for _, file := range state.filesByWork[existingWork.ID] {
				existingByPart[file.PartIndex] = file.ID
			}
		}
		for index := range work.files {
			file := &work.files[index]
			if file.legacyID != 0 {
				continue
			}
			if existingID, exists := existingByPart[file.partIndex]; exists {
				file.fileID = existingID
				continue
			}
			for {
				if nextID == 0 {
					return fmt.Errorf("allocate synthetic FilmFile ID for %s: ID space exhausted", work.identity)
				}
				if _, exists := reserved[nextID]; !exists {
					break
				}
				nextID++
			}
			file.fileID = nextID
			reserved[nextID] = struct{}{}
			nextID++
		}
	}
	return nil
}
