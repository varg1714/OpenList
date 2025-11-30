package fc2

import (
	"fmt"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/gocolly/colly/v2"
)

func (d *FC2) getPpvdbFilm(code string) (model.EmbyFileObj, error) {

	split := strings.Split(code, "-")
	code = split[len(split)-1]

	collector := colly.NewCollector(func(c *colly.Collector) {
		c.SetRequestTimeout(time.Second * 10)
	})

	imageUrl := ""

	var actors []string
	actorMap := make(map[string]bool)

	collector.OnHTML(fmt.Sprintf(`img[alt="%s"]`, code), func(element *colly.HTMLElement) {

		srcImage := element.Attr("src")
		if srcImage != "" && !strings.Contains(srcImage, "no-image.jpg") {
			imageUrl = srcImage
		}
	})

	collector.OnHTML(".text-white.title-font.text-lg.font-medium", func(element *colly.HTMLElement) {
		title := element.Attr("title")
		if title != "" {
			actorMap[title] = true
		}

	})

	var releaseTime time.Time
	collector.OnHTML("div[class*='lg:pl-8'][class*='lg:w-3/5']", func(element *colly.HTMLElement) {

		element.ForEach("span", func(i int, spanElement *colly.HTMLElement) {
			timeStr := spanElement.Text
			if dateRegexp.MatchString(timeStr) {
				tempTime, err := time.Parse("2006-01-02", timeStr)
				if err == nil {
					releaseTime = tempTime
				} else {
					utils.Log.Infof("failed to parse release time:%s,error message:%v", timeStr, err)
				}
			}
		})

	})

	title := ""
	collector.OnHTML(".items-center.text-white.text-lg.title-font.font-medium.mb-1 a", func(element *colly.HTMLElement) {
		title = element.Text
	})

	err := collector.Visit(fmt.Sprintf("https://fc2ppvdb.com/articles/%s", code))
	if err != nil {
		utils.Log.Infof("failed to query fc2 film info for:[%s], error message:%s", code, err.Error())
		return model.EmbyFileObj{}, err
	}

	for actor, _ := range actorMap {
		actors = append(actors, actor)
	}

	return model.EmbyFileObj{
		ObjThumb: model.ObjThumb{
			Object: model.Object{
				IsFolder: false,
				Name:     title,
			},
			Thumbnail: model.Thumbnail{Thumbnail: imageUrl},
		},
		Title:       title,
		Actors:      actors,
		ReleaseTime: releaseTime,
	}, nil

}
