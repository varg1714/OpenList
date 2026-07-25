package pornhub

import (
	"fmt"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/gocolly/colly/v2"
	"time"
)

var waitPornhubTagScan = time.Sleep

func (d *Pornhub) reMatchTags() {

	if d.MatchFilmTagLimit <= 0 {
		return
	}

	utils.Log.Info("start to match porn tags")
	defer utils.Log.Info("finish match porn tags")

	films, err := db.QueryPendingMediaWorks(DriverName, db.MediaWorkScanTags, d.MatchFilmTagLimit)
	if err != nil {
		utils.Log.Warn("failed to query films:", err.Error())
		return
	}

	for _, film := range films {

		collector := colly.NewCollector(func(c *colly.Collector) {
			c.SetRequestTimeout(time.Second * 10)
		})

		tagMap := make(map[string]bool)
		for _, tag := range film.Tags {
			tagMap[tag] = true
		}

		collector.OnHTML(".tagsWrapper", func(tagEle *colly.HTMLElement) {

			tagEle.ForEach(".gtm-event-video-underplayer.item span", func(i int, element *colly.HTMLElement) {

				if !tagMap[element.Text] {
					film.Tags = append(film.Tags, element.Text)
					tagMap[element.Text] = true
				}

			})

		})

		err1 := collector.Visit(fmt.Sprintf("%s/view_video.php?viewkey=%s", d.ServerUrl, film.SourceRef))

		if err1 != nil {
			utils.Log.Infof("failed to get film: %s tag info, error message: %s", film.Code, err1.Error())
			next := time.Now().Add(time.Hour)
			if updateErr := db.UpdateMediaWorkTagRetry(film.ID, next, err1.Error()); updateErr != nil {
				utils.Log.Warnf("failed to update tag retry for %s: %s", film.Code, updateErr)
			}
			continue
		}

		film.Tags = append(film.Tags, DriverName)
		err1 = db.UpdateMediaWorkTags(film.ID, film.Tags, film.TagVersion+1)
		if err1 != nil {
			utils.Log.Infof("failed to update film: %s tag info, error: %s", film.Code, err1.Error())
			continue
		}

		waitPornhubTagScan(3 * time.Second)
	}

}
