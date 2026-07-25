package virtual_file

import (
	"errors"
	"fmt"

	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

var writeNormalizedMediaNFO = UpdateMediaNfo

func SyncMediaNFOs(storageID uint, source string, force bool) error {
	works, err := db.QueryMediaWorksForNFOSync(storageID, source, force, 0)
	if err != nil {
		return fmt.Errorf("query media works for NFO sync: %w", err)
	}

	workErrors := make([]error, 0)
	for index := range works {
		work := works[index]
		identity := MediaIdentity{
			StorageID: work.StorageID, Source: work.Source, PrimaryDir: work.PrimaryDir, Code: work.Code,
		}
		info := MediaInfo{
			Identity: &identity,
			Title:    model.BuildMediaTitle(work.Code, work.RawTitle, work.TranslatedTitle),
			Synopsis: work.Synopsis,
			Release:  work.ReleaseDate,
			Actors:   []string(work.Actors),
			Tags:     []string(work.Tags),
		}
		if writeErr := writeNormalizedMediaNFO(info); writeErr != nil {
			if updateErr := db.UpdateMediaWorkNFOResult(work.ID, work.NfoVersion, writeErr.Error()); updateErr != nil {
				workErrors = append(workErrors, fmt.Errorf("record NFO failure for %s: %w", work.Code, updateErr))
			}
			workErrors = append(workErrors, fmt.Errorf("write NFO for %s: %w", work.Code, writeErr))
			continue
		}
		if updateErr := db.UpdateMediaWorkNFOResult(work.ID, work.MetadataVersion, ""); updateErr != nil {
			workErrors = append(workErrors, fmt.Errorf("record NFO success for %s: %w", work.Code, updateErr))
		}
	}
	return errors.Join(workErrors...)
}
