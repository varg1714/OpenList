package javdb

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/gocolly/colly/v2"
	"github.com/gocolly/colly/v2/extensions"
)

func (d *Javdb) getAiravPageInfo(urlFunc func(index int) string, index int, data []model.EmbyFileObj) ([]model.EmbyFileObj, bool, error) {

	nextPage := false

	collector := colly.NewCollector(func(c *colly.Collector) {
		c.SetRequestTimeout(time.Second * 120)
	})
	extensions.RandomUserAgent(collector)

	collector.OnHTML(".row.row-cols-2.row-cols-lg-4.g-2.mt-0", func(element *colly.HTMLElement) {
		element.ForEach(".col.oneVideo", func(i int, element *colly.HTMLElement) {

			href := element.ChildAttr(".oneVideo-top a", "href")
			title := element.ChildText(".oneVideo-body h5")

			if !strings.Contains(title, "马赛克破坏版") {
				parse, _ := time.Parse(time.DateOnly, element.ChildText(".meta"))
				data = append(data, model.EmbyFileObj{
					ObjThumb: model.ObjThumb{
						Object: model.Object{
							Name:     title,
							IsFolder: false,
							Size:     622857143,
							Modified: parse,
						},
					},
					Title: title,
					Url:   "https://airav.io" + href,
				})
			}

		})

	})

	collector.OnHTML(".col-2.d-flex.align-items-center.px-4.page-input", func(element *colly.HTMLElement) {
		page := element.ChildAttr(".form-control", "max")
		pageNum, _ := strconv.Atoi(page)
		if page != "" && index < pageNum {
			nextPage = true
		}
	})

	url := urlFunc(index)

	utils.Log.Debugf("开始爬取airav页面：%s", url)
	err := collector.Visit(url)

	return data, nextPage, err

}

func (d *Javdb) getAiravNamingAddr(film model.EmbyFileObj) (string, model.EmbyFileObj, error) {

	detailUrl := ""
	actorPageUrl := ""
	var matchedFilm model.EmbyFileObj

	code := splitCode(film.Name)

	searchResult, _, err := d.getAiravPageInfo(func(index int) string {
		return fmt.Sprintf("https://airav.io/cn/search_result?kw=%s", code)
	}, 1, []model.EmbyFileObj{})
	if err != nil {
		utils.Log.Info("airav页面爬取错误", err)
		return actorPageUrl, model.EmbyFileObj{}, err
	}

	// 优先级匹配：完全匹配 > 前缀匹配，有名称 > 仅code
	var bestScore int
	for _, item := range searchResult {
		itemCode, namePart := splitName(item.Name)

		var matchScore int
		if itemCode == code {
			// 完全匹配
			matchScore = 4
		} else if strings.HasPrefix(itemCode, code) {
			// 前缀匹配
			matchScore = 2
		} else {
			continue
		}

		if namePart != "" && namePart != itemCode {
			matchScore += 1 // 有名称部分
		}

		if matchScore > bestScore {
			bestScore = matchScore
			detailUrl = item.Url
			matchedFilm = item
		}
	}

	if matchedFilm.Name == "" {
		return actorPageUrl, model.EmbyFileObj{}, nil
	}

	if detailUrl == "" {
		return actorPageUrl, matchedFilm, nil
	}

	collector := colly.NewCollector(func(c *colly.Collector) {
		c.SetRequestTimeout(time.Second * 30)
	})

	collector.OnHTML(".list-group", func(element *colly.HTMLElement) {

		urls := element.ChildAttrs(".my-2 a", "href")

		var actors []string
		for _, url := range urls {
			if strings.Contains(url, "/cn/actor") {
				actors = append(actors, url)
			}
		}

		// 仅当演员只有一个的时候才进行爬取
		if len(actors) == 1 {
			actorPageUrl = fmt.Sprintf("https://airav.io%s&idx=", actors[0])
		}

	})

	collector.OnHTML(".video-info", func(element *colly.HTMLElement) {
		matchedFilm.Synopsis = element.ChildText("p.my-3")
	})

	collector.OnHTML(".video-title.my-3", func(element *colly.HTMLElement) {
		matchedFilm.Name = element.ChildText("h1")
		matchedFilm.Title = element.ChildText("h1")
	})

	err = collector.Visit(detailUrl)
	if err != nil {
		utils.Log.Info("详情页爬取失败", err)
		return actorPageUrl, matchedFilm, err
	}

	return actorPageUrl, matchedFilm, nil

}
