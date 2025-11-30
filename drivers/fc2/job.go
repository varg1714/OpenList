package fc2

import (
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"
	"github.com/OpenListTeam/OpenList/v4/internal/av"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/open_ai"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

func (d *FC2) reMatchReleaseTime() {

	// rematch release time

	utils.Log.Infof("start rematching release time for fc2")

	incompleteFilms, err := db.QueryIncompleteFilms("fc2", d.BatchScanSize)

	if err != nil {
		utils.Log.Warnf("failed to query no date films: %s", err.Error())
		return
	}

	filmMap := make(map[string]model.Film)

	for _, film := range incompleteFilms {

		code := av.GetFilmCode(film.Name)

		if existFilm, exist := filmMap[code]; exist {
			if film.Title == "" {
				film.Title = existFilm.Title
			}
			if len(film.Actors) == 0 {
				if len(existFilm.Actors) > 0 {
					film.Actors = append(film.Actors, existFilm.Actors...)
				}
			}
		} else {

			ppvdbMediaInfo, err1 := d.getFc2DailyFilm(code)
			if err1 != nil {
				if strings.Contains(err1.Error(), "Not Found") {
					film.Actors = []string{"个人收藏"}
				} else {
					return
				}
			} else {
				if ppvdbMediaInfo.ReleaseTime.Year() != 1 {
					film.Date = ppvdbMediaInfo.ReleaseTime
				} else {
					film.Date = film.CreatedAt
				}

				if film.Title == "" && ppvdbMediaInfo.Title != "" {
					film.Title = open_ai.Translate(ppvdbMediaInfo.Title)
				}

				if len(film.Actors) == 0 {
					if len(ppvdbMediaInfo.Actors) > 0 {
						film.Actors = ppvdbMediaInfo.Actors
					} else {
						film.Actors = []string{"个人收藏"}
					}
				}
			}

		}

		if film.Title == "" {
			sukeMediaInfo, err2 := av.GetMetaFromSuke(code)
			if err2 != nil {
				utils.Log.Warnf("failed to query suke: %s", code)
			} else if len(sukeMediaInfo.Magnets) > 0 {
				film.Title = open_ai.Translate(sukeMediaInfo.Magnets[0].GetName())
			}
		}
		filmMap[code] = film

		err1 := db.UpdateFilm(film)
		if err1 != nil {
			utils.Log.Warnf("failed to update film info: %s", err1.Error())
		}
		virtual_file.UpdateNfo(virtual_file.MediaInfo{
			Source:   "fc2",
			Dir:      film.Actor,
			FileName: virtual_file.AppendImageName(film.Name),
			Release:  film.Date,
			Title:    film.Title,
			Actors:   film.Actors,
			Tags:     film.Tags,
		})

		// avoid 429
		time.Sleep(time.Duration(d.ScanTimeLimit) * time.Second)

	}

	utils.Log.Info("rematching completed")

}

func (d *FC2) refreshNfo() {

	utils.Log.Info("start refresh nfo for fc2")

	films := d.getStars()
	fileNames := make(map[string][]string)

	for _, film := range films {
		virtual_file.UpdateNfo(virtual_file.MediaInfo{
			Source:   "fc2",
			Dir:      film.Path,
			FileName: virtual_file.AppendImageName(film.Name),
			Release:  film.ReleaseTime,
			Title:    film.Title,
			Actors:   film.Actors,
			Tags:     film.Tags,
		})
		fileNames[film.Path] = append(fileNames[film.Path], film.Name)
	}

	// clear unused files
	for dir, names := range fileNames {
		virtual_file.ClearUnUsedFiles("fc2", dir, names)
	}

	utils.Log.Info("finish refresh nfo")
}
