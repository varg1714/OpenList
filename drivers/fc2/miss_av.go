package fc2

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/gocolly/colly/v2"
	"gorm.io/gorm"
)

func (d *FC2) getMissAvPageFilms(url string) []model.EmbyFileObj {

	var films []model.EmbyFileObj

	collector := colly.NewCollector(func(c *colly.Collector) {
		c.SetRequestTimeout(time.Second * 30)
	})

	collector.OnHTML(".grid", func(table *colly.HTMLElement) {
		table.ForEach(".thumbnail.group", func(i int, element *colly.HTMLElement) {
			fc2Id := element.ChildAttr(".text-secondary", "alt")
			fc2Id = strings.ReplaceAll(fc2Id, "fc2-ppv", "FC2-PPV")
			films = append(films, model.EmbyFileObj{
				ObjThumb: model.ObjThumb{
					Object: model.Object{
						Name: fc2Id,
					},
				},
				Title: element.ChildText(".text-secondary"),
			})
		})
	})

	retryCount := 1
	collector.OnError(func(r *colly.Response, err error) {
		utils.Log.Infof("request failed, retryCount: %d", retryCount)
		if retryCount <= 3 {
			retryCount++
			err = r.Request.Retry()
			if err != nil {
				utils.Log.Warnf("retry failed: %s", err.Error())
			}
		}
	})

	err := collector.Visit(url)
	if err != nil {
		utils.Log.Warnf("failed to visit page: %s, error: %s", url, err.Error())
	}

	return films

}

func (d *FC2) getMissAvFilms(dirName string, urlFunc func(index int) string) ([]model.EmbyFileObj, error) {

	var queriedFilms []model.EmbyFileObj
	page := 1
	preSize := len(queriedFilms)

	// 1. query films
	for page == 1 || (preSize != len(queriedFilms) && page <= d.MissAvMaxPage) {

		preSize = len(queriedFilms)
		films := d.getMissAvPageFilms(urlFunc(page))
		queriedFilms = append(queriedFilms, films...)
		page++

	}

	// 2. add tag
	for i := range queriedFilms {
		queriedFilms[i].Tags = append(queriedFilms[i].Tags, fmt.Sprintf("%s-Top%d", dirName, ((i/30)+1)*30), dirName)
	}
	return []model.EmbyFileObj{}, d.syncMissAvFilms(queriedFilms)

}

func (d *FC2) syncMissAvFilms(films []model.EmbyFileObj) error {
	for _, film := range films {
		code, err := model.NormalizeMediaCode("fc2", film.Name)
		if err != nil {
			utils.Log.Warnf("skip invalid MissAV film %q: %s", film.Name, err)
			continue
		}
		work, err := db.GetFilmWorkByIdentity(d.ID, "fc2", code)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if _, err := d.addStar(code, film.Tags); err != nil {
				utils.Log.Warnf("failed to add MissAV film %s: %s", code, err)
			}
			continue
		}
		if err != nil {
			utils.Log.Warnf("failed to query MissAV film %s: %s", code, err)
			continue
		}
		merged := append(model.StringArray(nil), work.Tags...)
		seen := make(map[string]bool, len(merged))
		for _, tag := range merged {
			seen[tag] = true
		}
		for _, tag := range film.Tags {
			if !seen[tag] {
				merged = append(merged, tag)
				seen[tag] = true
			}
		}
		if len(merged) != len(work.Tags) {
			if err := db.UpdateMediaWorkTags(work.ID, merged, work.TagVersion+1); err != nil {
				utils.Log.Warnf("failed to update MissAV film %s: %s", code, err)
			}
		}
	}
	return nil
}
