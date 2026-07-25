package fc2

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/open_ai"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"gorm.io/gorm"
)

func (d *FC2) rematchMediaReleaseTime() {
	works, err := db.QueryReleaseMediaWorks("fc2", d.BatchScanSize)
	if err != nil {
		utils.Log.Warnf("failed to query FC2 release works: %s", err)
		return
	}
	for index := range works {
		work := works[index]
		info, fetchErr := d.getFc2DailyFilm(work.Code)
		if fetchErr != nil {
			next := time.Now().Add(time.Duration(d.ReleaseScanTime) * time.Minute)
			if next.Before(time.Now().Add(time.Minute)) {
				next = time.Now().Add(time.Hour)
			}
			if err := db.UpdateMediaWorkReleaseRetry(work.ID, next, fetchErr.Error()); err != nil {
				utils.Log.Warnf("failed to update FC2 release retry for %s: %s", work.Code, err)
			}
			continue
		}
		release := info.ReleaseTime
		if release.IsZero() || release.Year() == 1 {
			release = work.CreatedAt
		}
		if err := db.UpdateMediaWorkRelease(work.ID, release); err != nil {
			utils.Log.Warnf("failed to update FC2 release for %s: %s", work.Code, err)
			continue
		}
		if len(info.Actors) > 0 {
			if err := db.UpdateMediaWorkActors(work.ID, model.StringArray(info.Actors)); err != nil {
				utils.Log.Warnf("failed to update FC2 actors for %s: %s", work.Code, err)
			}
		}
		if work.TranslatedTitle == "" && info.Title != "" {
			translated := open_ai.Translate(info.Title)
			if translated != "" {
				if err := db.UpdateMediaWorkTranslation(work.ID, translated, model.CurrentTranslationVersion); err != nil {
					utils.Log.Warnf("failed to update FC2 title for %s: %s", work.Code, err)
				}
			}
		}
		if d.ScanTimeLimit > 0 {
			time.Sleep(time.Duration(d.ScanTimeLimit) * time.Second)
		}
	}
}

func (d *FC2) scanMediaSampleImages() {
	works, err := db.QuerySampleImageMediaWorks("fc2", 72*time.Hour, 20)
	if err != nil {
		utils.Log.Warnf("failed to query FC2 sample works: %s", err)
		return
	}
	remaining := maxSampleImageRequestsPerRun
	for index := range works {
		work := works[index]
		magnet, magnetErr := db.GetSelectedSourceMagnet(work.ID)
		if errors.Is(magnetErr, gorm.ErrRecordNotFound) {
			magnets, fetchErr := d.mediaMagnets(context.Background(), work)
			if fetchErr == nil {
				fetchErr = db.UpsertSourceMagnets(work.ID, magnets)
			}
			if fetchErr == nil {
				magnet, magnetErr = db.GetSelectedSourceMagnet(work.ID)
			} else {
				magnetErr = fetchErr
			}
		}
		if magnetErr != nil {
			if err := db.UpdateMediaWorkSampleScan(work.ID, false); err != nil {
				utils.Log.Warnf("failed to update FC2 sample retry for %s: %s", work.Code, err)
			}
			continue
		}
		if remaining == 0 {
			return
		}
		remaining--
		linkInfo, linkErr := d.getWhatLinkInfo(magnet.MagnetURI)
		if linkErr != nil {
			if err := db.UpdateMediaWorkSampleScan(work.ID, false); err != nil {
				utils.Log.Warnf("failed to update FC2 sample retry for %s: %s", work.Code, err)
			}
			continue
		}
		screenshots := append([]WhatLinkScreenshot(nil), linkInfo.Screenshots...)
		sort.Slice(screenshots, func(i, j int) bool {
			if screenshots[i].Time == screenshots[j].Time {
				return screenshots[i].Screenshot < screenshots[j].Screenshot
			}
			return screenshots[i].Time < screenshots[j].Time
		})
		if len(screenshots) > maxSampleImageCount {
			screenshots = screenshots[:maxSampleImageCount]
		}
		identity := fc2MediaIdentity(work)
		completedCount := work.SampleImageCount
		failed := false
		for sampleIndex := work.SampleImageCount + 1; sampleIndex <= len(screenshots); sampleIndex++ {
			if remaining == 0 {
				return
			}
			if err := validateScreenshotURL(screenshots[sampleIndex-1].Screenshot); err != nil {
				if updateErr := db.UpdateMediaWorkSampleScan(work.ID, false); updateErr != nil {
					utils.Log.Warnf("failed to update FC2 sample error for %s: %s", work.Code, updateErr)
				}
				failed = true
				break
			}
			remaining--
			_, cacheErr := virtual_file.CacheMediaFanart(context.Background(), identity, sampleIndex, virtual_file.HTTPFileCacheRequest{
				URL: screenshots[sampleIndex-1].Screenshot, Headers: map[string]string{"Referer": "https://mypikpak.com/"}, Client: d.client,
			})
			if cacheErr != nil {
				if updateErr := db.UpdateMediaWorkSampleScan(work.ID, false); updateErr != nil {
					utils.Log.Warnf("failed to update FC2 sample retry for %s: %s", work.Code, updateErr)
				}
				failed = true
				break
			}
			if err := db.UpdateMediaWorkSampleProgress(work.ID, sampleIndex, false); err != nil {
				utils.Log.Warnf("failed to update FC2 sample progress for %s: %s", work.Code, err)
				failed = true
				break
			}
			completedCount = sampleIndex
		}
		if !failed && completedCount >= len(screenshots) {
			if err := db.UpdateMediaWorkSampleScan(work.ID, true); err != nil {
				utils.Log.Warnf("failed to complete FC2 samples for %s: %s", work.Code, err)
			}
		}
	}
}

func fc2MediaIdentity(work model.FilmWork) virtual_file.MediaIdentity {
	return virtual_file.MediaIdentity{StorageID: work.StorageID, Source: work.Source, PrimaryDir: work.PrimaryDir, Code: work.Code}
}
