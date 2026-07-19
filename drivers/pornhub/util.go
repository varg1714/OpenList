package pornhub

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/spider"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/go-resty/resty/v2"
	"github.com/robertkrimen/otto"
	"github.com/tebeka/selenium"
)

var viewKeyCompile = regexp.MustCompile(`/view_video.php\?viewkey=([^&\s]+)`)
var cacheDiscoveredImageAndNFO = virtual_file.CacheImageAndNfo
var updateDiscoveredMediaNFO = virtual_file.UpdateMediaNfo

func (d *Pornhub) getFilms(dirName, pageKey string) ([]model.EmbyFileObj, error) {
	var films []PornFilm

	if strings.Contains(pageKey, "/playlist/") {
		key := strings.ReplaceAll(pageKey, "/playlist/", "")
		playListFilms, err := d.getPlayListFilms(key, dirName)
		if err != nil {
			return virtual_file.ListMediaFiles(d.ID, DriverName, dirName)
		}
		films = playListFilms
	} else {

		if strings.Contains(pageKey, "/model/") {
			pageKey = pageKey + "/videos"
		}

		actorFilms, err := d.getActorFilms(dirName, pageKey)
		if err != nil {
			return virtual_file.ListMediaFiles(d.ID, DriverName, dirName)
		}
		films = actorFilms
	}

	if len(films) == 0 {
		return virtual_file.ListMediaFiles(d.ID, DriverName, dirName)
	}

	for _, film := range films {
		canonicalURL, err := canonicalVideoURL(d.ServerUrl, film.SourceURL)
		if err != nil {
			utils.Log.Warnf("failed to normalize Pornhub URL %q: %s", film.SourceURL, err)
			continue
		}
		film.SourceURL = canonicalURL
		work, err := buildDiscoveredWork(d.ID, dirName, film)
		if err != nil {
			utils.Log.Warnf("failed to normalize Pornhub discovery %q: %s", film.ViewKey, err)
			continue
		}
		if err := db.UpsertDiscoveredWork(&work); err != nil {
			utils.Log.Warnf("failed to upsert Pornhub work %s: %s", work.Code, err)
			continue
		}
		if _, err := db.EnsureSingleFilmFile(work.ID); err != nil {
			utils.Log.Warnf("failed to ensure Pornhub file %s: %s", work.Code, err)
			continue
		}
		if cacheDiscoveredWorkArtifacts(work) == virtual_file.CreatedFailed {
			utils.Log.Warnf("failed to cache Pornhub artifacts for %s", work.Code)
		}
	}

	return virtual_file.ListMediaFiles(d.ID, DriverName, dirName)

}

func cacheDiscoveredWorkArtifacts(work model.FilmWork) int {
	identity := virtual_file.MediaIdentity{
		StorageID: work.StorageID, Source: work.Source, PrimaryDir: work.PrimaryDir, Code: work.Code,
	}
	return cacheDiscoveredImageAndNFO(virtual_file.MediaInfo{
		Identity: &identity,
		Title:    model.BuildMediaTitle(work.Code, work.RawTitle, work.TranslatedTitle),
		Synopsis: work.Synopsis,
		ImgUrl:   work.ImageURL,
		Release:  work.ReleaseDate,
		Actors:   []string(work.Actors),
		Tags:     []string(work.Tags),
	})
}

func (d *Pornhub) syncDiscoveredNFO(files []model.EmbyFileObj) error {
	if !d.SyncNfo {
		return nil
	}
	var firstErr error
	for _, file := range files {
		identity := virtual_file.MediaIdentity{
			StorageID: d.ID, Source: DriverName, PrimaryDir: file.Path, Code: file.Code,
		}
		err := updateDiscoveredMediaNFO(virtual_file.MediaInfo{
			Identity: &identity,
			Title:    model.BuildMediaTitle(file.Code, file.Title, ""),
			Synopsis: file.Synopsis,
			Release:  file.ReleaseTime,
			Actors:   file.Actors,
			Tags:     file.Tags,
		})
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func buildDiscoveredWork(storageID uint, primaryDir string, film PornFilm) (model.FilmWork, error) {
	code, err := model.NormalizeMediaCode(DriverName, film.ViewKey)
	if err != nil {
		return model.FilmWork{}, err
	}
	canonical, err := canonicalVideoURL("", film.SourceURL)
	if err != nil {
		return model.FilmWork{}, err
	}
	return model.FilmWork{
		StorageID: storageID, Source: DriverName, Code: code,
		SourceRef: code, SourceURL: canonical, PrimaryDir: primaryDir,
		RawTitle: film.Title, ImageURL: film.Image, Actors: model.StringArray{film.Username},
		ReleaseDate: time.Now(),
	}, nil
}

func canonicalVideoURL(base, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("missing Pornhub source URL")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid Pornhub source URL: %w", err)
	}
	if !parsed.IsAbs() {
		if base == "" {
			return "", fmt.Errorf("invalid Pornhub source URL: %q", raw)
		}
		root, err := url.Parse(strings.TrimRight(base, "/"))
		if err != nil || !root.IsAbs() || root.Host == "" {
			return "", fmt.Errorf("invalid Pornhub server URL: %q", base)
		}
		parsed = root.ResolveReference(parsed)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.Path == "" {
		return "", fmt.Errorf("invalid Pornhub source URL: %q", raw)
	}
	return parsed.String(), nil
}

func (d *Pornhub) getVideoLink(viewKey string) (string, error) {

	client := resty.New()
	res, err := client.R().SetQueryParam("viewkey", viewKey).Get(fmt.Sprintf("%s/view_video.php", d.ServerUrl))
	if err != nil {
		utils.Log.Warnf("failed to get film page info from pornhub, %s", err.Error())
		return "", err
	}

	html := res.String()

	scriptRegexp := regexp.MustCompile(`<script\b[^>]*>([\s\S]*?)</script>`)

	matchers := scriptRegexp.FindAllStringSubmatch(html, -1)
	var encryptedScript string

	for _, scripts := range matchers {
		script := scripts[1]
		if !strings.Contains(script, "flashvars_") {
			continue
		} else {
			encryptedScript = script
			break
		}
	}

	flashId := regexp.MustCompile(`flashvars_\d+`).FindString(encryptedScript)

	vm := otto.New()
	_, err = vm.Run(`var playerObjList = {};` + encryptedScript + fmt.Sprintf(`;var __VM__OUTPUT = JSON.stringify(%s.mediaDefinitions)`, flashId))
	if err != nil {
		utils.Log.Warnf("failed to run script, %s", err.Error())
		return "", err
	}

	value, err := vm.Get("__VM__OUTPUT")
	if err != nil {
		utils.Log.Warnf("failed to get console result, %v", err)
		return "", err
	}

	type MediaDefinition struct {
		Format   string `json:"format"`
		VideoURL string `json:"videoUrl"`
	}

	mediaDefinitions := make([]MediaDefinition, 0)

	if str, err1 := value.ToString(); err1 != nil {
		return "", err
	} else {
		if err2 := json.Unmarshal([]byte(str), &mediaDefinitions); err2 != nil {
			return "", err
		}
	}

	var mp4MediaDefinition *MediaDefinition

	for _, mediaDefinition := range mediaDefinitions {
		if mediaDefinition.Format == "mp4" {
			mp4MediaDefinition = &mediaDefinition
		}
	}

	if mp4MediaDefinition == nil {
		return "", errors.New("failed to find mp4 video")
	}

	pornVideos := make([]videoInfo, 0)

	_, err = client.R().SetHeaders(map[string]string{
		"Referer": mp4MediaDefinition.VideoURL,
	}).SetResult(&pornVideos).Get(mp4MediaDefinition.VideoURL)

	if err != nil {
		return "", err
	} else if len(pornVideos) == 0 {
		return "", errors.New("failed to find mp4 video")
	}

	return pornVideos[len(pornVideos)-1].VideoURL, nil

}

func (d *Pornhub) getPlayListFilms(playlistId, dirName string) ([]PornFilm, error) {

	var films []PornFilm

	err := spider.Visit(d.SpiderServer, fmt.Sprintf("%s/playlist/%s", d.ServerUrl, playlistId), time.Duration(d.SpiderMaxWaitTime)*time.Second, func(wd selenium.WebDriver) {

		source, _ := wd.PageSource()

		compile := regexp.MustCompile("token=(.*)\"")
		tokenStr := compile.FindString(source)
		token := compile.ReplaceAllString(tokenStr, "$1")

		page := 1
		preLen := len(films)

		for page == 1 || preLen != len(films) {

			preLen = len(films)

			err := wd.Get(fmt.Sprintf("%s/playlist/viewChunked?id=%s&token=%s&page=%d", d.ServerUrl, playlistId, token, page))
			if err != nil {
				utils.Log.Warnf("failed to get playlist, %s", err.Error())
				return
			}

			time.Sleep(time.Duration(d.SpiderMaxWaitTime) * time.Second)

			films = append(films, resolveFilms(wd, PlayList)...)
			page++

		}

	})

	if err != nil {
		utils.Log.Warnf("failed to get pornhub films, %s", err.Error())
		return nil, err
	}

	return films, nil

}

func (d *Pornhub) getActorFilms(dirName, pageKey string) ([]PornFilm, error) {

	var films []PornFilm
	page := 1
	pageUrl := fmt.Sprintf("%s%s?o=mr&page=%d", d.ServerUrl, pageKey, page)

	err := spider.Visit(d.SpiderServer, pageUrl, time.Duration(d.SpiderMaxWaitTime)*time.Second, func(wd selenium.WebDriver) {

		var newFilmIds []string

		newFilms := resolveFilms(wd, Model)
		films = append(films, newFilms...)
		page++

		for _, film := range newFilms {
			newFilmIds = append(newFilmIds, film.ViewKey)
		}

		nextPageFunc := func() bool {
			nextPage := false

			_, err := wd.FindElement(selenium.ByCSSSelector, ".page_next.omega")
			if err == nil {
				// find next button
				nextPage = true
				_, err1 := wd.FindElement(selenium.ByCSSSelector, ".page_next.disabled.omega")
				if err1 == nil {
					// next page is disabled
					nextPage = false
				}
			}

			return nextPage
		}

		for nextPageFunc() {

			pageUrl = fmt.Sprintf("%s%s?page=%d", d.ServerUrl, pageKey, page)

			err := wd.Get(pageUrl)
			if err != nil {
				utils.Log.Warnf("failed to get actor films, %s", err.Error())
				return
			}

			time.Sleep(time.Duration(d.SpiderMaxWaitTime) * time.Second)

			pornFilms := resolveFilms(wd, Model)
			clear(newFilmIds)
			for _, film := range pornFilms {
				newFilmIds = append(newFilmIds, film.ViewKey)
			}

			films = append(films, pornFilms...)
			page++

		}

	})

	if err != nil {
		utils.Log.Warnf("failed to get pornhub films, %s", err.Error())
		return nil, err
	}

	return films, nil

}

func resolveFilms(wd selenium.WebDriver, actorType int) []PornFilm {

	var films []PornFilm

	var parentEle selenium.WebElement
	var err error

	if actorType == PlayList {
		parentEle, err = wd.FindElement(selenium.ByTagName, "body")
	} else {
		parentEle, err = wd.FindElement(selenium.ByCSSSelector, ".videoUList")
	}

	if err != nil {
		utils.Log.Warnf("failed to find parent element, %s", err.Error())
		return films
	}

	filmElements, _ := parentEle.FindElements(selenium.ByCSSSelector, ".wrap.flexibleHeight")
	for _, filmElement := range filmElements {

		aEles, err2 := filmElement.FindElements(selenium.ByCSSSelector, "a")
		if err2 != nil {
			utils.Log.Warnf("failed to find a elements, %s", err2.Error())
		}

		href := ""
		title := ""

		for i := 1; i < len(aEles) && (href == "" || title == ""); i++ {

			if tempHref, _ := aEles[i].GetAttribute("href"); tempHref != "" && strings.Contains(tempHref, "view_video.php") {
				href = tempHref
				tempTitle, _ := aEles[i].GetAttribute("title")
				title = tempTitle
			}

		}

		if href == "" {
			continue
		}

		imgEle, err1 := filmElement.FindElement(selenium.ByCSSSelector, "img")
		if err1 != nil {
			utils.Log.Warnf("failed to find img, %s", err1.Error())
			return films
		}
		imgSrc, err1 := imgEle.GetAttribute("src")
		if err1 != nil {
			utils.Log.Warnf("failed to find src, %s", err1.Error())
			return films
		}

		username := ""
		usernameEle, err1 := filmElement.FindElement(selenium.ByCSSSelector, ".usernameBadgesWrapper")
		if err1 == nil {
			username, err1 = usernameEle.Text()
			if err1 != nil {
				utils.Log.Warnf("failed to find username, %s", err1.Error())
				return films
			}
		}
		films = append(films, PornFilm{
			Image: imgSrc,
			Title: title,
			ViewKey: func() string {
				findString := viewKeyCompile.FindString(href)
				return viewKeyCompile.ReplaceAllString(findString, "$1")
			}(),
			SourceURL: href,
			Username:  username,
		})

	}
	return films
}

func convertFilms(actor string, films []PornFilm) ([]model.EmbyFileObj, error) {
	return utils.SliceConvert(films, func(src PornFilm) (model.EmbyFileObj, error) {
		return model.EmbyFileObj{
			ObjThumb: model.ObjThumb{
				Object: model.Object{
					Name:     src.ViewKey,
					IsFolder: false,
					Size:     622857143,
					Modified: time.Now(),
				},
				Thumbnail: model.Thumbnail{Thumbnail: src.Image},
			},
			ReleaseTime: time.Now(),
			Code:        src.ViewKey,
			SourceRef:   src.ViewKey,
			SourceURL:   src.SourceURL,
			Url:         src.SourceURL,
			Actors: func() []string {
				if src.Username != "" {
					return []string{src.Username}
				}
				return []string{actor}
			}(),
			Title: src.Title,
			Tags:  []string{},
		}, nil
	})
}
