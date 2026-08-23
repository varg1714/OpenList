package fc2

import (
	"fmt"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

var cacheFC2MediaImage = virtual_file.CacheImageWithError

const posterRetryInterval = 72 * time.Hour

func (d *FC2) scanMediaArtifacts() error {
	utils.Log.Info("start scanning FC2 media artifacts")
	defer utils.Log.Info("finish scanning FC2 media artifacts")

	works, err := db.QueryPendingMediaPosterWorks(d.ID, "fc2", posterRetryInterval)
	if err != nil {
		return fmt.Errorf("list FC2 works for artifact scan: %w", err)
	}
	utils.Log.Infof("found %d FC2 artifact works, ids: %v", len(works), filmWorkIDs(works))
	for index := range works {
		work := works[index]
		identity := virtual_file.MediaIdentity{
			StorageID: work.StorageID, Source: work.Source, PrimaryDir: work.PrimaryDir, Code: work.Code,
		}
		result, cacheErr := cacheFC2MediaImage(virtual_file.MediaInfo{
			Identity: &identity,
			Title:    model.BuildMediaTitle(work.Code, work.RawTitle, work.TranslatedTitle),
			ImgUrl:   work.ImageURL,
			Release:  work.ReleaseDate,
			Actors:   []string(work.Actors),
			Tags:     []string(work.Tags),
		})
		failed := result != virtual_file.Exist && result != virtual_file.CreatedSuccess
		if failed || cacheErr != nil {
			if cacheErr == nil {
				cacheErr = fmt.Errorf("unexpected poster cache result %d", result)
			}
			utils.Log.Warnf("failed to cache FC2 poster for %s: %s", work.Code, cacheErr)
			if err := db.IncrementMediaWorkDMMPosterRetry(work.ID); err != nil {
				return fmt.Errorf("update FC2 poster retry for %s: %w", work.Code, err)
			}
			continue
		}
		if err := db.UpdateMediaWorkDMMPosterStatus(work.ID, model.DMMPosterStatusSuccess); err != nil {
			return fmt.Errorf("update FC2 poster status for %s: %w", work.Code, err)
		}
	}
	return nil
}
