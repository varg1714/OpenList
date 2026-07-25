package fc2

import (
	"fmt"

	"github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

var cacheFC2MediaImage = virtual_file.CacheImage

func (d *FC2) scanMediaArtifacts() error {
	works, err := db.ListFilmWorksByStorageSource(d.ID, "fc2")
	if err != nil {
		return fmt.Errorf("list FC2 works for artifact scan: %w", err)
	}
	for index := range works {
		work := works[index]
		if work.ImageURL == "" {
			continue
		}
		identity := virtual_file.MediaIdentity{
			StorageID: work.StorageID, Source: work.Source, PrimaryDir: work.PrimaryDir, Code: work.Code,
		}
		cacheFC2MediaImage(virtual_file.MediaInfo{
			Identity: &identity,
			Title:    model.BuildMediaTitle(work.Code, work.RawTitle, work.TranslatedTitle),
			ImgUrl:   work.ImageURL,
			Release:  work.ReleaseDate,
			Actors:   []string(work.Actors),
			Tags:     []string(work.Tags),
		})
	}
	return nil
}
