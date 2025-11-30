package fc2

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"
	"github.com/OpenListTeam/OpenList/v4/internal/av"
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

func (d *FC2) findMagnet(url string) (string, error) {

	res, err := base.RestyClient.R().Get(url)
	if err != nil {
		return "", err
	}

	return res.String(), err
}

func (d *FC2) getFilms(urlFunc func(index int) string) ([]model.EmbyFileObj, error) {

	var result []model.EmbyFileObj
	var filmIds []string
	page := 1
	preSize := len(filmIds)

	for page == 1 || (preSize != len(filmIds)) {

		ids, err2 := d.getFc2DailyPageFilms(urlFunc(page))
		if err2 != nil && !strings.Contains(err2.Error(), "Not Found") {
			utils.Log.Warnf("影片爬取失败: %s", err2.Error())
			return result, nil
		} else {
			page++
			preSize = len(filmIds)
			filmIds = append(filmIds, ids...)
		}

	}

	unCachedFilms := db.QueryNoMagnetFilms(filmIds)
	if len(unCachedFilms) == 0 {
		return result, nil
	}

	unMissedFilms := db.QueryUnMissedFilms(unCachedFilms)
	if len(unMissedFilms) == 0 {
		return result, nil
	}

	utils.Log.Infof("以下影片首次扫描到需添加入库：%v", unCachedFilms)
	var notExitedFilms []string
	for _, id := range unCachedFilms {
		_, err := d.addStar(id, []string{})
		if err != nil {
			notExitedFilms = append(notExitedFilms, id)
		}
		time.Sleep(time.Duration(d.ScanTimeLimit) * time.Second)
	}

	if len(notExitedFilms) > 0 {
		utils.Log.Infof("以下影片未获取到下载信息：%v", notExitedFilms)
		err := db.CreateMissedFilms(notExitedFilms)
		if err != nil {
			utils.Log.Warnf("影片信息保存失败: %s", err.Error())
		}
	}

	return result, nil

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
	return virtual_file.GetStorageFilms("fc2", "个人收藏", false)
}

func (d *FC2) addStar(code string, tags []string) (model.EmbyFileObj, error) {

	if code == "" {
		return model.EmbyFileObj{}, nil
	}

	fc2Id := code
	if !strings.HasPrefix(fc2Id, "FC2-PPV") {
		fc2Id = fmt.Sprintf("FC2-PPV-%s", code)
	}

	// 1. get cache from db
	magnetCache := db.QueryMagnetCacheByCode(fc2Id)
	if magnetCache.Magnet != "" {
		return model.EmbyFileObj{}, errors.New("已存在该文件")
	}

	// 2. get magnet from suke
	sukeMeta, err := av.GetMetaFromSuke(fc2Id)
	if err != nil {
		utils.Log.Warn("failed to get the magnet info from suke:", err.Error())
		return model.EmbyFileObj{}, err
	} else if len(sukeMeta.Magnets) == 0 || sukeMeta.Magnets[0].GetMagnet() == "" {

		sukeMeta, err = av.GetMetaFromSuke(code)
		if err == nil && len(sukeMeta.Magnets) > 0 {
			fc2Id = code
		} else {
			return model.EmbyFileObj{}, errors.New("查询结果为空")
		}

	}

	// 3. translate film name
	title := open_ai.Translate(virtual_file.ClearFilmName(sukeMeta.Magnets[0].GetName()))
	magnet := sukeMeta.Magnets[0].GetMagnet()

	// 4. save film info

	// 4.1 get film thumbnail
	ppvFilmInfo, err := d.getFc2DailyFilm(fc2Id)
	if err == nil {
		if len(ppvFilmInfo.Actors) == 0 {
			ppvFilmInfo.Actors = append(ppvFilmInfo.Actors, "个人收藏")
		}
	}

	if ppvFilmInfo.ReleaseTime.Year() == 1 {
		ppvFilmInfo.ReleaseTime = time.Now()
	}
	// 4.2 build the film info to be cached
	cachingFiles := buildCacheFile(len(sukeMeta.Magnets[0].GetFiles()), fc2Id, title, ppvFilmInfo.ReleaseTime, ppvFilmInfo.Actors, tags)
	if len(cachingFiles) > 0 {
		cachingFiles[0].Thumbnail.Thumbnail = ppvFilmInfo.Thumb()
	}

	// 4.3 save the magnets info
	var magnetCaches []model.MagnetCache
	for _, file := range cachingFiles {
		magnetCaches = append(magnetCaches, model.MagnetCache{
			DriverType: "fc2",
			Magnet:     magnet,
			Name:       file.Name,
			Code:       av.GetFilmCode(file.Name),
			ScanAt:     time.Now(),
		})
	}
	err = db.BatchCreateMagnetCache(magnetCaches)
	if err != nil {
		utils.Log.Warn("failed to cache film magnet:", err.Error())
		return model.EmbyFileObj{}, err
	}

	// 4.4 save the film info
	err = db.CreateFilms("fc2", "个人收藏", "个人收藏", cachingFiles)
	if err != nil {
		utils.Log.Warn("failed to cache film info:", err.Error())
		return model.EmbyFileObj{}, err
	}

	// 4.5 save the film meta, including nfo and images
	_ = virtual_file.CacheImageAndNfo(virtual_file.MediaInfo{
		Source:   "fc2",
		Dir:      "个人收藏",
		FileName: virtual_file.AppendImageName(cachingFiles[0].Name),
		Title:    title,
		ImgUrl:   ppvFilmInfo.Thumb(),
		Actors:   ppvFilmInfo.Actors,
		Release:  ppvFilmInfo.ReleaseTime,
		Tags:     tags,
	})

	var noImageFiles []model.EmbyFileObj
	for _, file := range cachingFiles {
		if file.Thumb() == "" {
			noImageFiles = append(noImageFiles, file)
		}
	}
	if len(noImageFiles) > 0 {

		whatLinkInfo := d.getWhatLinkInfo(magnet)
		imgs := whatLinkInfo.Screenshots
		if len(imgs) > 0 {
			for index, file := range noImageFiles {
				_ = virtual_file.CacheImage(virtual_file.MediaInfo{
					Source:   "fc2",
					Dir:      "个人收藏",
					FileName: virtual_file.AppendImageName(file.Name),
					Title:    title,
					ImgUrl:   imgs[index%len(imgs)].Screenshot,
					ImgUrlHeaders: map[string]string{
						"Referer": "https://mypikpak.com/",
					},
					Actors:  ppvFilmInfo.Actors,
					Release: ppvFilmInfo.ReleaseTime,
					Tags:    tags,
				})
			}
		}

	}

	return cachingFiles[0], err

}

func buildCacheFile(fileCount int, fc2Id string, title string, releaseTime time.Time, actors, tags []string) []model.EmbyFileObj {

	var cachingFiles []model.EmbyFileObj
	if fileCount <= 1 {
		cachingFiles = append(cachingFiles, model.EmbyFileObj{
			ObjThumb: model.ObjThumb{
				Object: model.Object{
					Name:     virtual_file.AppendFilmName(fc2Id),
					IsFolder: false,
					Size:     622857143,
					Modified: time.Now(),
					Path:     "个人收藏",
				},
			},
			Title:       title,
			ReleaseTime: releaseTime,
			Url:         fc2Id,
			Actors:      actors,
			Tags:        tags,
		})
	} else {
		for index := range fileCount {
			realName := virtual_file.AppendFilmName(fmt.Sprintf("%s-cd%d", fc2Id, index+1))
			cachingFiles = append(cachingFiles, model.EmbyFileObj{
				ObjThumb: model.ObjThumb{
					Object: model.Object{
						Name:     realName,
						IsFolder: false,
						Size:     622857143,
						Modified: time.Now(),
						Path:     "个人收藏",
					},
				},
				Title:       title,
				ReleaseTime: releaseTime,
				Url:         fc2Id,
				Actors:      actors,
				Tags:        tags,
			})
		}
	}
	return cachingFiles
}

func (d *FC2) getWhatLinkInfo(magnet string) WhatLinkInfo {

	var whatLinkInfo WhatLinkInfo

	_, err := base.RestyClient.R().SetHeaders(map[string]string{
		"Referer": "https://mypikpak.net/",
		"Origin":  "https://mypikpak.net/",
	}).SetQueryParam("url", magnet).SetResult(&whatLinkInfo).Get("https://whatslink.info/api/v1/link")

	if err != nil {
		utils.Log.Info("磁力图片获取失败", err.Error())
		return whatLinkInfo
	}

	return whatLinkInfo

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

func (d *FC2) deleteFilm(obj model.Obj) error {
	err := db.DeleteAllMagnetCacheByCode(av.GetFilmCode(obj.GetName()))
	if err != nil {
		utils.Log.Warnf("影片缓存信息删除失败：%s", err.Error())
	}
	err = virtual_file.DeleteImageAndNfo("fc2", "个人收藏", obj.GetName())
	if err != nil {
		utils.Log.Warnf("影片附件信息删除失败：%s", err.Error())
	}

	err = db.CreateMissedFilms([]string{av.GetFilmCode(obj.GetName())})
	if err != nil {
		utils.Log.Warnf("影片黑名单信息失败：%s", err.Error())
	}

	err = db.DeleteFilmsByCode("fc2", "个人收藏", av.GetFilmCode(obj.GetName()))
	if err != nil {
		utils.Log.Warnf("影片删除失败：%s", err.Error())
	}

	return err
}
