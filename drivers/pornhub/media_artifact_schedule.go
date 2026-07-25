package pornhub

import (
	"fmt"

	"github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

var cachePornhubMediaImage = virtual_file.CacheImage

func (d *Pornhub) scanMediaArtifacts() error {
	utils.Log.Info("start scanning Pornhub media artifacts")
	defer utils.Log.Info("finish scanning Pornhub media artifacts")

	works, err := db.ListFilmWorksByStorageSource(d.ID, DriverName)
	if err != nil {
		return fmt.Errorf("list Pornhub works for artifact scan: %w", err)
	}
	utils.Log.Infof("found %d Pornhub artifact works, ids: %v", len(works), filmWorkIDs(works))
	for index := range works {
		work := works[index]
		if work.ImageURL == "" {
			continue
		}
		identity := pornhubMediaIdentity(&work)
		cachePornhubMediaImage(virtual_file.MediaInfo{
			Identity:      &identity,
			Title:         model.BuildMediaTitle(work.Code, work.RawTitle, work.TranslatedTitle),
			ImgUrl:        work.ImageURL,
			ImgUrlHeaders: map[string]string{"Referer": d.ServerUrl},
			Release:       work.ReleaseDate,
			Actors:        []string(work.Actors),
			Tags:          []string(work.Tags),
		})
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
