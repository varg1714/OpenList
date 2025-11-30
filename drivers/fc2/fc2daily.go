package fc2

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/gocolly/colly/v2"
)

func (d *FC2) getFc2DailyFilm(fc2Id string) (model.EmbyFileObj, error) {

	collector := colly.NewCollector(func(c *colly.Collector) {
		c.SetRequestTimeout(time.Second * 10)
	})

	var actors []string

	collector.OnHTML(`a[href^="/fc2daily/actor/"]`, func(element *colly.HTMLElement) {
		actors = append(actors, element.Text)
	})

	re := regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)
	var releaseTime time.Time
	collector.OnHTML(`div.text-center`, func(e *colly.HTMLElement) {
		timeStr := strings.TrimSpace(e.Text)
		if re.MatchString(timeStr) {
			if dateRegexp.MatchString(timeStr) {
				tempTime, err := time.Parse("2006-01-02", timeStr)
				if err == nil {
					releaseTime = tempTime
				} else {
					utils.Log.Infof("failed to parse release time:%s,error message:%v", timeStr, err)
				}
			}
		}
	})

	title := ""
	collector.OnHTML(`h2.text-center`, func(e *colly.HTMLElement) {
		title = strings.ReplaceAll(strings.TrimSpace(e.Text), " - ", " ")
	})

	err := collector.Visit(fmt.Sprintf("https://paipancon.com/fc2daily/detail/%s", fc2Id))
	if err != nil {
		utils.Log.Infof("failed to query fc2 film info for:[%s], error message:%s", fc2Id, err.Error())
		return model.EmbyFileObj{}, err
	}

	return model.EmbyFileObj{
		ObjThumb: model.ObjThumb{
			Object: model.Object{
				IsFolder: false,
				Name:     title,
			},
		},
		Title:       title,
		Actors:      actors,
		ReleaseTime: releaseTime,
	}, nil

}

func (d *FC2) getFc2DailyPageFilms(url string) ([]string, error) {

	collector := colly.NewCollector(func(c *colly.Collector) {
		c.SetRequestTimeout(time.Second * 10)
	})

	var ids []string
	idSet := make(map[string]bool)

	collector.OnHTML(`a[href^="/fc2daily/detail/"]`, func(element *colly.HTMLElement) {
		href := element.Attr("href")
		href = strings.TrimPrefix(href, "/fc2daily/detail/")
		idSet[href] = true
	})

	err := collector.Visit(url)

	for id := range idSet {
		ids = append(ids, id)
	}

	return ids, err

}
