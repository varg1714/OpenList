package pornhub

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/spider"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/tebeka/selenium"
)

var viewKeyCompile = regexp.MustCompile(`/view_video.php\?viewkey=([^&\s]+)`)

func (d *Pornhub) getPlayListFilms(playlistID string) ([]PornFilm, error) {
	var films []PornFilm
	err := spider.Visit(d.SpiderServer, fmt.Sprintf("%s/playlist/%s", d.ServerUrl, playlistID), time.Duration(d.SpiderMaxWaitTime)*time.Second, func(wd selenium.WebDriver) {
		source, _ := wd.PageSource()
		compile := regexp.MustCompile("token=(.*)\"")
		tokenStr := compile.FindString(source)
		token := compile.ReplaceAllString(tokenStr, "$1")

		page := 1
		previousLength := len(films)
		for page == 1 || previousLength != len(films) {
			previousLength = len(films)
			pageURL := fmt.Sprintf("%s/playlist/viewChunked?id=%s&token=%s&page=%d", d.ServerUrl, playlistID, token, page)
			if err := wd.Get(pageURL); err != nil {
				utils.Log.Warnf("failed to get playlist, %s", err.Error())
				return
			}

			time.Sleep(time.Duration(d.SpiderMaxWaitTime) * time.Second)
			resolvedFilms, _ := resolveFilms(wd, PlayList)
			films = append(films, resolvedFilms...)
			page++
		}
	})
	if err != nil {
		utils.Log.Warnf("failed to get pornhub films, %s", err.Error())
		return nil, err
	}
	return films, nil
}

func (d *Pornhub) getActorFilms(pageKey string) ([]PornFilm, error) {
	var films []PornFilm
	pageURL := fmt.Sprintf("%s%s?o=mr&page=1", d.ServerUrl, pageKey)
	err := spider.Visit(d.SpiderServer, pageURL, time.Duration(d.SpiderMaxWaitTime)*time.Second, func(wd selenium.WebDriver) {
		films = d.resolveActorFilms(wd, pageKey)
	})
	if err != nil {
		utils.Log.Warnf("failed to get pornhub films, %s", err.Error())
		return nil, err
	}
	return films, nil
}

type filmElementFinder interface {
	FindElement(by, value string) (selenium.WebElement, error)
}

type actorFilmPage interface {
	filmElementFinder
	Get(url string) error
}

func (d *Pornhub) resolveActorFilms(wd actorFilmPage, pageKey string) []PornFilm {
	films, available := resolveFilms(wd, Model)
	if !available || len(films) == 0 {
		return films
	}

	for page := 2; hasNextActorFilmPage(wd); page++ {
		pageURL := fmt.Sprintf("%s%s?page=%d", d.ServerUrl, pageKey, page)
		if err := wd.Get(pageURL); err != nil {
			utils.Log.Warnf("failed to get actor films, %s", err.Error())
			return films
		}

		time.Sleep(time.Duration(d.SpiderMaxWaitTime) * time.Second)
		pageFilms, pageAvailable := resolveFilms(wd, Model)
		if !pageAvailable || len(pageFilms) == 0 {
			return films
		}
		films = append(films, pageFilms...)
	}
	return films
}

func hasNextActorFilmPage(wd actorFilmPage) bool {
	if _, err := wd.FindElement(selenium.ByCSSSelector, ".page_next.omega"); err != nil {
		return false
	}
	_, err := wd.FindElement(selenium.ByCSSSelector, ".page_next.disabled.omega")
	return err != nil
}

func resolveFilms(wd filmElementFinder, actorType int) ([]PornFilm, bool) {
	var films []PornFilm
	var parentElement selenium.WebElement
	var err error
	if actorType == PlayList {
		parentElement, err = wd.FindElement(selenium.ByTagName, "body")
	} else {
		parentElement, err = wd.FindElement(selenium.ByCSSSelector, ".videoUList")
	}
	if err != nil {
		utils.Log.Warnf("failed to find parent element, %s", err.Error())
		return films, false
	}

	filmElements, _ := parentElement.FindElements(selenium.ByCSSSelector, ".wrap.flexibleHeight")
	for _, filmElement := range filmElements {
		anchorElements, err := filmElement.FindElements(selenium.ByCSSSelector, "a")
		if err != nil {
			utils.Log.Warnf("failed to find a elements, %s", err.Error())
		}

		href := ""
		title := ""
		for index := 1; index < len(anchorElements) && (href == "" || title == ""); index++ {
			candidateHref, _ := anchorElements[index].GetAttribute("href")
			if candidateHref != "" && strings.Contains(candidateHref, "view_video.php") {
				href = candidateHref
				title, _ = anchorElements[index].GetAttribute("title")
			}
		}
		if href == "" {
			continue
		}

		imageElement, err := filmElement.FindElement(selenium.ByCSSSelector, "img")
		if err != nil {
			utils.Log.Warnf("failed to find img, %s", err.Error())
			return films, true
		}
		imageSource, err := imageElement.GetAttribute("src")
		if err != nil {
			utils.Log.Warnf("failed to find src, %s", err.Error())
			return films, true
		}

		username := ""
		usernameElement, err := filmElement.FindElement(selenium.ByCSSSelector, ".usernameBadgesWrapper")
		if err == nil {
			username, err = usernameElement.Text()
			if err != nil {
				utils.Log.Warnf("failed to find username, %s", err.Error())
				return films, true
			}
		}
		viewKey := viewKeyCompile.FindString(href)
		films = append(films, PornFilm{
			Image:     imageSource,
			Title:     title,
			ViewKey:   viewKeyCompile.ReplaceAllString(viewKey, "$1"),
			SourceURL: href,
			Username:  username,
		})
	}
	return films, true
}
