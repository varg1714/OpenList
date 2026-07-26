package virtual_file

import (
	"errors"
	"fmt"
	"strings"

	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

var writeNormalizedMediaNFO = UpdateMediaNfo

type MediaNFOSyncOptions struct {
	Force       bool
	IncludeCode bool
}

func SyncMediaNFOs(storageID uint, source string, options MediaNFOSyncOptions) error {
	works, err := db.QueryMediaWorksForNFOSync(storageID, source, options.Force, 0)
	if err != nil {
		return fmt.Errorf("query media works for NFO sync: %w", err)
	}

	workErrors := make([]error, 0)
	for index := range works {
		work := works[index]
		identity := MediaIdentity{
			StorageID: work.StorageID, Source: work.Source, PrimaryDir: work.PrimaryDir, Code: work.Code,
		}
		title := strings.TrimSpace(work.TranslatedTitle)
		if title == "" {
			title = strings.TrimSpace(work.RawTitle)
		}
		if options.IncludeCode || title == "" {
			title = model.BuildMediaTitle(work.Code, work.RawTitle, work.TranslatedTitle)
		}
		info := MediaInfo{
			Identity: &identity,
			Title:    title,
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
