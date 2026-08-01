package fc2

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/open_ai"
	"github.com/OpenListTeam/OpenList/v4/internal/spider"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/gocolly/colly/v2"
	"github.com/tebeka/selenium"
)

var subTitles, _ = regexp.Compile(".*<a href=\"(.*)\" title=\".*</a>.*")
var magnetUrl, _ = regexp.Compile(".*<a href=\"(.*)\" class=\".*\"><i class=\".*\"></i>Magnet</a>.*")

var actorUrlsRegexp, _ = regexp.Compile(".*/article_search.php\\?id=(.*)")

var dateRegexp, _ = regexp.Compile("\\d{4}-\\d{2}-\\d{2}")

var (
	translateFC2Title = open_ai.Translate
	fetchFC2DailyFilm = func(d *FC2, fc2Id string) (model.EmbyFileObj, error) {
		return d.getFc2DailyFilm(fc2Id)
	}
)

func (d *FC2) findMagnet(url string) (string, error) {

	res, err := base.RestyClient.R().Get(url)
	if err != nil {
		return "", err
	}

	return res.String(), err
}

func (d *FC2) getPageInfo(urlFunc func(index int) string, index int, data []model.EmbyFileObj) ([]model.EmbyFileObj, bool, error) {

	pageUrl := urlFunc(index)
	preLen := len(data)

	collector := colly.NewCollector(func(c *colly.Collector) {
		c.SetRequestTimeout(time.Second * 10)
	})

	tableContainer := ""
	filmDetailContainer := ""
	filmUrlSelector := ""
	filmTitleSelector := ""
	filmImageSelector := ""

	if strings.HasPrefix(pageUrl, "https://adult.contents.fc2.com/users") {
		// user
		tableContainer = ".seller_user_articlesList"
		filmDetailContainer = ".c-cntCard-110-f"
		filmUrlSelector = ".c-cntCard-110-f_itemName"
		filmTitleSelector = ".c-cntCard-110-f_itemName"
		filmImageSelector = ".c-cntCard-110-f_thumb img"
	} else {
		// ranking
		tableContainer = ".c-rankbox-100"
		filmDetailContainer = ".c-ranklist-110"
		filmUrlSelector = ".c-ranklist-110_tmb a"
		filmTitleSelector = ".c-ranklist-110_info a"
		filmImageSelector = ".c-ranklist-110_tmb img"
	}

	collector.OnHTML(tableContainer, func(element *colly.HTMLElement) {
		element.ForEach(filmDetailContainer, func(i int, element *colly.HTMLElement) {

			href := element.ChildAttr(filmUrlSelector, "href")
			title := element.ChildText(filmTitleSelector)

			var image string
			imageAttr := element.ChildAttr(filmImageSelector, "src")
			if strings.HasPrefix(imageAttr, "http") {
				image = imageAttr
			} else {
				image = "https:" + imageAttr
			}

			id := actorUrlsRegexp.ReplaceAllString(href, "$1")
			title = fmt.Sprintf("FC2-PPV-%s %s", id, title)
			data = append(data, model.EmbyFileObj{
				ObjThumb: model.ObjThumb{
					Object: model.Object{
						Name:     title,
						IsFolder: true,
						Size:     622857143,
					},
					Thumbnail: model.Thumbnail{Thumbnail: image},
				},
				Title: title,
				Url:   id,
			})
		})
	})

	err := collector.Visit(pageUrl)
	if err != nil && err.Error() == "Not Found" {
		err = nil
	}

	return data, len(data) != preLen, err

}

func (d *FC2) getStars() []model.EmbyFileObj {
	films, err := virtual_file.ListMediaFiles(d.ID, "fc2", "个人收藏")
	if err != nil {
		utils.Log.Warnf("failed to list FC2 favorite works: %s", err)
		return nil
	}
	return films
}

func (d *FC2) addStar(code string, tags []string) (model.EmbyFileObj, error) {

	if code == "" {
		return model.EmbyFileObj{}, nil
	}

	fc2Id, err := model.NormalizeMediaCode("fc2", code)
	if err != nil {
		return model.EmbyFileObj{}, err
	}

	if existing, findErr := db.GetFilmWorkByIdentity(d.ID, "fc2", fc2Id); findErr == nil {
		files, err := db.ListFilmFiles(existing.ID)
		if err != nil {
			return model.EmbyFileObj{}, err
		}
		if len(files) == 0 {
			return model.EmbyFileObj{}, errors.New("existing FC2 work has no files")
		}
		return virtual_file.ConvertMediaFileToEmbyFile(model.FilmFileWithWork{FilmFile: files[0], Work: existing})
	}

	// 2. get magnet from suke
	sukeMeta, err := getFC2SukeMeta(fc2Id)
	if err != nil {
		utils.Log.Warn("failed to get the magnet info from suke:", err.Error())
		return model.EmbyFileObj{}, err
	} else if len(sukeMeta.Magnets) == 0 || sukeMeta.Magnets[0].GetMagnet() == "" {

		sukeMeta, err = getFC2SukeMeta(code)
		if err == nil && len(sukeMeta.Magnets) > 0 {
			fc2Id, err = model.NormalizeMediaCode("fc2", code)
			if err != nil {
				return model.EmbyFileObj{}, err
			}
		} else {
			return model.EmbyFileObj{}, errors.New("查询结果为空")
		}

	}

	// 3. translate film name
	rawTitle := magnetDisplayTitle(stripFC2CodePrefix(fc2Id, sukeMeta.Magnets[0].GetName()))
	title := translateFC2Title(rawTitle)
	// 4. save film info

	// 4.1 get film thumbnail
	ppvFilmInfo, err := fetchFC2DailyFilm(d, fc2Id)
	if err == nil {
		if len(ppvFilmInfo.Actors) == 0 {
			ppvFilmInfo.Actors = append(ppvFilmInfo.Actors, "个人收藏")
		}
	}

	if ppvFilmInfo.ReleaseTime.Year() == 1 {
		ppvFilmInfo.ReleaseTime = time.Now()
	}
	fileCount := max(1, len(sukeMeta.Magnets[0].GetFiles()))
	work, err := buildDiscoveredWork(d.ID, "个人收藏", fc2Id, fc2Id, rawTitle, ppvFilmInfo.Thumb())
	if err != nil {
		return model.EmbyFileObj{}, err
	}
	if err := db.UpsertDiscoveredWork(&work); err != nil {
		return model.EmbyFileObj{}, err
	}
	files := make([]model.FilmFile, fileCount)
	for index := range files {
		files[index].PartIndex = index + 1
		files[index].PartCount = len(files)
	}
	if err := db.ReplaceFilmFiles(work.ID, files); err != nil {
		return model.EmbyFileObj{}, err
	}
	if err := db.UpdateMediaWorkDetails(work.ID, title, "", model.StringArray(ppvFilmInfo.Actors), model.StringArray(tags)); err != nil {
		return model.EmbyFileObj{}, err
	}
	work.TranslatedTitle = title
	work.Actors = model.StringArray(ppvFilmInfo.Actors)
	work.Tags = model.StringArray(tags)
	work.ReleaseDate = ppvFilmInfo.ReleaseTime
	magnets, err := d.mediaMagnets(context.Background(), work)
	if err != nil {
		return model.EmbyFileObj{}, err
	}
	if err := db.UpsertSourceMagnets(work.ID, magnets); err != nil {
		return model.EmbyFileObj{}, err
	}
	storedFiles, err := db.ListFilmFiles(work.ID)
	if err != nil || len(storedFiles) == 0 {
		return model.EmbyFileObj{}, err
	}
	return virtual_file.ConvertMediaFileToEmbyFile(model.FilmFileWithWork{FilmFile: storedFiles[0], Work: work})

}

func (d *FC2) getWhatLinkInfo(magnet string) (WhatLinkInfo, error) {
	var whatLinkInfo WhatLinkInfo
	client := d.client
	if client == nil {
		client = newFC2HTTPClient()
	}
	response, err := client.R().SetHeaders(map[string]string{
		"Referer": "https://mypikpak.net/",
		"Origin":  "https://mypikpak.net/",
	}).SetQueryParam("url", magnet).SetResult(&whatLinkInfo).Get("https://whatslink.info/api/v1/link")
	if err != nil {
		return whatLinkInfo, err
	}
	if response == nil || response.StatusCode() != 200 {
		status := 0
		if response != nil {
			status = response.StatusCode()
		}
		return whatLinkInfo, fmt.Errorf("WhatsLink returned HTTP %d", status)
	}
	if whatLinkInfo.Error != "" {
		return whatLinkInfo, fmt.Errorf("WhatsLink API error: %s", whatLinkInfo.Error)
	}
	return whatLinkInfo, nil
}

func (d *FC2) getPageFilms(url string) ([]string, error) {

	var ids []string

	err := spider.Visit(d.SpiderServer, url, time.Duration(d.SpiderMaxWaitTime)*time.Second, func(wd selenium.WebDriver) {
		elements, _ := wd.FindElements(selenium.ByCSSSelector, ".absolute.top-0.left-0.text-white.bg-gray-800.px-1")
		for _, element := range elements {
			text, err1 := element.Text()
			if err1 != nil {
				utils.Log.Warnf("failed to fetch element: %s", err1.Error())
			} else {
				ids = append(ids, fmt.Sprintf("FC2-PPV-%s", text))
			}
		}
	})

	return ids, err

}
