package javdb

import (
	"fmt"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/gocolly/colly/v2"
	log "github.com/sirupsen/logrus"
)

func MatchSubtitleCatSubtitles(code string) ([]string, error) {

	collector := colly.NewCollector(func(c *colly.Collector) {
		c.SetRequestTimeout(time.Second * 30)
	})

	var subtitlePages []string

	collector.OnHTML(".table.sub-table", func(e *colly.HTMLElement) {

		e.ForEach("tr", func(i int, trEle *colly.HTMLElement) {
			href := trEle.ChildAttr("td:nth-child(1) a:last-of-type", "href")
			text := trEle.ChildText("td:nth-child(1) a:last-of-type")
			if text != "" && strings.Contains(strings.ToLower(text), strings.ToLower(code)) {
				subtitlePages = append(subtitlePages, fmt.Sprintf("https://subtitlecat.com/%s", href))
			}
		})

	})

	err := collector.Visit(fmt.Sprintf("https://subtitlecat.com/index.php?search=%s", code))
	if err != nil {
		log.Warnf("failed to fetch subtitles from subtitlecat: %s", err.Error())
		return subtitlePages, err
	}

	var result []string
	collector.OnHTML("#download_zh-CN", func(e *colly.HTMLElement) {
		link := e.Attr("href")
		if link != "" {
			result = append(result, fmt.Sprintf("https://subtitlecat.com%s", link))
		}
	})

	if len(subtitlePages) > 0 {

		for _, page := range subtitlePages {
			err := collector.Visit(page)
			if err != nil {
				utils.Log.Warnf("failed to fetch subtitles from subtitlecat: %s", err.Error())
				return result, err
			}
		}

	}

	return result, nil

}
