package fc2

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"
	"github.com/OpenListTeam/OpenList/v4/internal/av"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/open_ai"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"gorm.io/gorm"
)

const (
	maxSampleImageCount          = 50
	maxSampleImageRequestsPerRun = 100
)

func (d *FC2) scanSampleImages() {
	utils.Log.Info("start scanning sample images for fc2 films")
	defer utils.Log.Info("finish scanning sample images for fc2 films")

	groups, err := db.QueryFC2SampleImageGroups(72*time.Hour, 20)
	if err != nil {
		utils.Log.Warnf("failed to query FC2 sample-image groups: %s", err.Error())
		return
	}

	remainingRequests := maxSampleImageRequestsPerRun
	for _, group := range groups {
		if d.scanSampleImageGroup(context.Background(), group, &remainingRequests) {
			return
		}
	}
}

func (d *FC2) scanSampleImageGroup(ctx context.Context, group db.FC2SampleImageGroup, remainingRequests *int) bool {
	if group.SampleImageCount >= maxSampleImageCount {
		d.markSampleImageGroupComplete(group)
		return false
	}

	magnetCache, err := db.QueryFC2MagnetCacheByCode(group.URL)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		d.updateSampleImageGroupScanAt(group, fmt.Errorf("missing FC2 magnet cache for %s", group.URL))
		return false
	}
	if err != nil {
		d.updateSampleImageGroupScanAt(group, fmt.Errorf("failed to query FC2 magnet cache for %s: %w", group.URL, err))
		return false
	}
	if magnetCache.Magnet == "" {
		d.updateSampleImageGroupScanAt(group, fmt.Errorf("empty FC2 magnet cache for %s", group.URL))
		return false
	}
	if *remainingRequests == 0 {
		return true
	}
	*remainingRequests--
	whatLinkInfo, err := d.getWhatLinkInfo(magnetCache.Magnet)
	if err != nil {
		d.updateSampleImageGroupScanAt(group, err)
		return false
	}

	screenshots := append([]WhatLinkScreenshot(nil), whatLinkInfo.Screenshots...)
	sort.Slice(screenshots, func(i, j int) bool {
		if screenshots[i].Time == screenshots[j].Time {
			return screenshots[i].Screenshot < screenshots[j].Screenshot
		}
		return screenshots[i].Time < screenshots[j].Time
	})
	if len(screenshots) > maxSampleImageCount {
		screenshots = screenshots[:maxSampleImageCount]
	}
	if group.SampleImageCount >= len(screenshots) {
		d.markSampleImageGroupComplete(group)
		return false
	}

	for index := group.SampleImageCount + 1; index <= len(screenshots); index++ {
		screenshotURL := screenshots[index-1].Screenshot
		if err := validateScreenshotURL(screenshotURL); err != nil {
			d.updateSampleImageGroupScanAt(group, err)
			return false
		}

		destination, err := virtual_file.FanartPath("fc2", group.Actor, group.URL, index)
		if err != nil {
			d.updateSampleImageGroupScanAt(group, err)
			return false
		}
		_, statErr := os.Lstat(destination)
		requestNeeded := os.IsNotExist(statErr)
		if requestNeeded && *remainingRequests == 0 {
			return true
		}
		if requestNeeded {
			*remainingRequests--
		}

		_, err = virtual_file.CacheFanart(ctx, virtual_file.FanartCacheRequest{
			Source:   "fc2",
			Dir:      group.Actor,
			FilmName: group.URL,
			Index:    index,
			URL:      screenshotURL,
			Headers: map[string]string{
				"Referer": "https://mypikpak.com/",
			},
			Client: d.client,
		})
		if err != nil {
			var statusError *virtual_file.HTTPStatusError
			if errors.As(err, &statusError) && statusError.StatusCode >= 400 && statusError.StatusCode < 600 {
				utils.Log.Warnf("skip FC2 sample image %d for %s after HTTP %d: %s", index, group.URL, statusError.StatusCode, screenshotURL)
				if err := db.UpdateFC2SampleImageGroupProgress(group.Actor, group.URL, index, false); err != nil {
					utils.Log.Warnf("failed to update FC2 sample-image progress for %s: %s", group.URL, err.Error())
					return false
				}
				continue
			}
			d.updateSampleImageGroupScanAt(group, err)
			return false
		}
		if err := db.UpdateFC2SampleImageGroupProgress(group.Actor, group.URL, index, false); err != nil {
			utils.Log.Warnf("failed to update FC2 sample-image progress for %s: %s", group.URL, err.Error())
			return false
		}
	}

	d.markSampleImageGroupComplete(group)
	return false
}

func validateScreenshotURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf("invalid screenshot URL: %q", rawURL)
	}
	return nil
}

func (d *FC2) markSampleImageGroupComplete(group db.FC2SampleImageGroup) {
	if err := db.MarkFC2SampleImageGroupComplete(group.Actor, group.URL); err != nil {
		utils.Log.Warnf("failed to mark FC2 sample images complete for %s: %s", group.URL, err.Error())
	}
}

func (d *FC2) updateSampleImageGroupScanAt(group db.FC2SampleImageGroup, cause error) {
	utils.Log.Warnf("failed to scan FC2 sample images for %s: %s", group.URL, cause.Error())
	if err := db.UpdateFC2SampleImageGroupScanAt(group.Actor, group.URL); err != nil {
		utils.Log.Warnf("failed to update FC2 sample-image scan time for %s: %s", group.URL, err.Error())
	}
}

func (d *FC2) reMatchReleaseTime() {

	// rematch release time

	utils.Log.Infof("start rematching release time for fc2")

	incompleteFilms, err := db.QueryIncompleteFilms("fc2", d.BatchScanSize)

	if err != nil {
		utils.Log.Warnf("failed to query no date films: %s", err.Error())
		return
	}

	filmMap := make(map[string]model.Film)

	for _, film := range incompleteFilms {

		code := av.GetFilmCode(film.Name)

		if existFilm, exist := filmMap[code]; exist {
			if film.Title == "" {
				film.Title = existFilm.Title
			}
			if len(film.Actors) == 0 {
				if len(existFilm.Actors) > 0 {
					film.Actors = append(film.Actors, existFilm.Actors...)
				}
			}
		} else {

			ppvdbMediaInfo, err1 := d.getFc2DailyFilm(code)
			if err1 != nil {
				if strings.Contains(err1.Error(), "Not Found") {
					film.Actors = []string{"个人收藏"}
				} else {
					return
				}
			} else {
				if ppvdbMediaInfo.ReleaseTime.Year() != 1 {
					film.Date = ppvdbMediaInfo.ReleaseTime
				} else {
					film.Date = film.CreatedAt
				}

				if film.Title == "" && ppvdbMediaInfo.Title != "" {
					film.Title = open_ai.Translate(ppvdbMediaInfo.Title)
				}

				if len(film.Actors) == 0 {
					if len(ppvdbMediaInfo.Actors) > 0 {
						film.Actors = ppvdbMediaInfo.Actors
					} else {
						film.Actors = []string{"个人收藏"}
					}
				}
			}

		}

		if film.Title == "" {
			sukeMediaInfo, err2 := av.GetMetaFromSuke(code)
			if err2 != nil {
				utils.Log.Warnf("failed to query suke: %s", code)
			} else if len(sukeMediaInfo.Magnets) > 0 {
				film.Title = open_ai.Translate(sukeMediaInfo.Magnets[0].GetName())
			}
		}
		filmMap[code] = film

		err1 := db.UpdateFilm(film)
		if err1 != nil {
			utils.Log.Warnf("failed to update film info: %s", err1.Error())
		}
		virtual_file.UpdateNfo(virtual_file.MediaInfo{
			Source:   "fc2",
			Dir:      film.Actor,
			FileName: virtual_file.AppendImageName(film.Name),
			Release:  film.Date,
			Title:    film.Title,
			Actors:   film.Actors,
			Tags:     film.Tags,
		})

		// avoid 429
		time.Sleep(time.Duration(d.ScanTimeLimit) * time.Second)

	}

	utils.Log.Info("rematching completed")

}

func (d *FC2) refreshNfo() {

	utils.Log.Info("start refresh nfo for fc2")

	films := d.getStars()
	fileNames := make(map[string][]string)

	for _, film := range films {
		virtual_file.UpdateNfo(virtual_file.MediaInfo{
			Source:   "fc2",
			Dir:      film.Path,
			FileName: virtual_file.AppendImageName(film.Name),
			Release:  film.ReleaseTime,
			Title:    film.Title,
			Actors:   film.Actors,
			Tags:     film.Tags,
		})
		fileNames[film.Path] = append(fileNames[film.Path], film.Name)
	}

	// clear unused files
	for dir, names := range fileNames {
		virtual_file.ClearUnUsedFiles("fc2", dir, names)
	}

	utils.Log.Info("finish refresh nfo")
}
