package pornhub

import (
	"fmt"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

var cachePornhubMediaImage = virtual_file.CacheImageWithError

const posterRetryInterval = 72 * time.Hour

func (d *Pornhub) scanMediaArtifacts() error {
	utils.Log.Info("start scanning Pornhub media artifacts")
	defer utils.Log.Info("finish scanning Pornhub media artifacts")

	works, err := db.QueryPendingMediaPosterWorks(d.ID, DriverName, posterRetryInterval)
	if err != nil {
		return fmt.Errorf("list Pornhub works for artifact scan: %w", err)
	}
	utils.Log.Infof("found %d Pornhub artifact works, ids: %v", len(works), filmWorkIDs(works))
	for index := range works {
		work := works[index]
		identity := pornhubMediaIdentity(&work)
		result, cacheErr := cachePornhubMediaImage(virtual_file.MediaInfo{
			Identity:      &identity,
			Title:         model.BuildMediaTitle(work.Code, work.RawTitle, work.TranslatedTitle),
			ImgUrl:        work.ImageURL,
			ImgUrlHeaders: map[string]string{"Referer": d.ServerUrl},
			Release:       work.ReleaseDate,
			Actors:        []string(work.Actors),
			Tags:          []string(work.Tags),
		})
		status := model.DMMPosterStatusSuccess
		if result != virtual_file.Exist && result != virtual_file.CreatedSuccess {
			status = model.DMMPosterStatusTransientError
			if cacheErr == nil {
				cacheErr = fmt.Errorf("unexpected poster cache result %d", result)
			}
		}
		if cacheErr != nil {
			status = model.DMMPosterStatusTransientError
			utils.Log.Warnf("failed to cache Pornhub poster for %s: %s", work.Code, cacheErr)
		}
		if err := db.UpdateMediaWorkDMMPosterStatus(work.ID, status); err != nil {
			return fmt.Errorf("update Pornhub poster status for %s: %w", work.Code, err)
		}
	}
	return nil
}

func pornhubMediaIdentity(work *model.FilmWork) virtual_file.MediaIdentity {
	return virtual_file.MediaIdentity{
		StorageID:  work.StorageID,
		Source:     work.Source,
		PrimaryDir: work.PrimaryDir,
		Code:       work.Code,
	}
}

func filmWorkIDs(works []model.FilmWork) []uint {
	ids := make([]uint, len(works))
	for i, w := range works {
		ids[i] = w.ID
	}
	return ids
}
