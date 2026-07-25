package javdb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"
	"github.com/OpenListTeam/OpenList/v4/internal/av"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/open_ai"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

const currentTranslationVersion = model.CurrentTranslationVersion

var batchTranslateMediaWorks = open_ai.BatchTranslate

func (d *Javdb) scanTranslations() {
	works, err := db.QueryTranslationMediaWorks(DriverName, currentTranslationVersion, 20)
	if err != nil {
		utils.Log.Warnf("failed to query JavDB translation works: %s", err)
		return
	}
	items := make([]open_ai.TranslateItem, len(works))
	for index, work := range works {
		items[index] = open_ai.TranslateItem{Origin: work.RawTitle}
		file := model.EmbyFileObj{
			ObjThumb: model.ObjThumb{Object: model.Object{Name: work.Code}},
			Code:     work.Code, SourceRef: work.SourceRef, SourceURL: work.SourceURL, Url: work.SourceURL,
			Title: model.BuildMediaTitle(work.Code, work.RawTitle, ""),
		}
		if _, candidate, candidateErr := d.getAiravNamingAddr(file); candidateErr == nil && candidate.Title != "" {
			_, candidateTitle := splitName(candidate.Title)
			items[index].Candidate = virtual_file.ClearFilmName(candidateTitle)
		}
	}
	if len(items) == 0 {
		return
	}
	translations := batchTranslateMediaWorks(items)
	for index, work := range works {
		translated := ""
		if index < len(translations) {
			translated = translations[index]
		}
		if translated == "" {
			next := time.Now().Add(6 * time.Hour)
			if err := db.UpdateMediaWorkTranslationRetry(work.ID, next, "translation returned an empty result", currentTranslationVersion); err != nil {
				utils.Log.Warnf("failed to update translation retry for %s: %s", work.Code, err)
			}
			continue
		}
		if err := db.UpdateMediaWorkTranslation(work.ID, translated, currentTranslationVersion); err != nil {
			utils.Log.Warnf("failed to update translation for %s: %s", work.Code, err)
		}
	}
}

func (d *Javdb) scanMediaSynopsis() {
	limit := d.SynopsisScanLimit
	if limit <= 0 {
		limit = 20
	}
	works, err := db.QueryEmptySynopsisMediaWorks(DriverName, 72*time.Hour, limit)
	if err != nil {
		utils.Log.Warnf("failed to query JavDB synopsis works: %s", err)
		return
	}
	for index := range works {
		work := works[index]
		file := model.EmbyFileObj{
			ObjThumb: model.ObjThumb{Object: model.Object{Name: work.Code}},
			Code:     work.Code, SourceRef: work.SourceRef, SourceURL: work.SourceURL, Url: work.SourceURL,
			Title: model.BuildMediaTitle(work.Code, work.RawTitle, work.TranslatedTitle),
		}
		_, candidate, airErr := d.getAiravNamingAddr(file)
		if airErr == nil && candidate.Synopsis != "" {
			if err := db.UpdateMediaWorkSynopsis(work.ID, candidate.Synopsis); err != nil {
				utils.Log.Warnf("failed to save JavDB synopsis for %s: %s", work.Code, err)
			}
			continue
		}
		synopsis, dmmErr := d.fetchDmmSynopsis(work.Code)
		if dmmErr != nil {
			next := time.Now().Add(72 * time.Hour)
			if err := db.UpdateMediaWorkSynopsisRetry(work.ID, next, dmmErr.Error()); err != nil {
				utils.Log.Warnf("failed to update synopsis retry for %s: %s", work.Code, err)
			}
			continue
		}
		if synopsis == "" {
			if !work.ReleaseDate.IsZero() && work.ReleaseDate.Before(time.Now().AddDate(0, -1, 0)) {
				if err := db.MarkMediaWorkSynopsisExcluded(work.ID); err != nil {
					utils.Log.Warnf("failed to exclude synopsis for %s: %s", work.Code, err)
				}
			} else {
				next := time.Now().Add(72 * time.Hour)
				if err := db.UpdateMediaWorkSynopsisRetry(work.ID, next, "no synopsis found"); err != nil {
					utils.Log.Warnf("failed to update synopsis retry for %s: %s", work.Code, err)
				}
			}
			continue
		}
		translated := open_ai.Translate(synopsis)
		if translated == "" {
			translated = synopsis
		}
		if err := db.UpdateMediaWorkSynopsis(work.ID, translated); err != nil {
			utils.Log.Warnf("failed to save DMM synopsis for %s: %s", work.Code, err)
		}
	}
}

func (d *Javdb) scanMediaMetadataAndMagnets() {
	limit := d.MatchFilmTagLimit
	if limit <= 0 {
		return
	}
	works, err := db.QueryTagMediaWorks(DriverName, limit)
	if err != nil {
		utils.Log.Warnf("failed to query JavDB metadata works: %s", err)
		return
	}
	for index := range works {
		work := works[index]
		meta, fetchErr := getJavdbMeta(work.SourceURL)
		if fetchErr != nil || len(meta.Magnets) == 0 {
			if fetchErr == nil {
				fetchErr = errors.New("JavDB returned no magnets")
			}
			next := time.Now().Add(6 * time.Hour)
			if err := db.UpdateMediaWorkMagnetScan(work.ID, &next, fetchErr.Error()); err != nil {
				utils.Log.Warnf("failed to update magnet retry for %s: %s", work.Code, err)
			}
			if err := db.UpdateMediaWorkTagRetry(work.ID, next, fetchErr.Error()); err != nil {
				utils.Log.Warnf("failed to update tag retry for %s: %s", work.Code, err)
			}
			continue
		}
		magnets := sourceMagnetsFromMeta(meta)
		if err := db.UpsertSourceMagnets(work.ID, magnets); err != nil {
			next := time.Now().Add(6 * time.Hour)
			if updateErr := db.UpdateMediaWorkMagnetScan(work.ID, &next, err.Error()); updateErr != nil {
				utils.Log.Warnf("failed to persist magnet error for %s: %s", work.Code, updateErr)
			}
			continue
		}
		if err := db.UpdateMediaWorkMagnetScan(work.ID, nil, ""); err != nil {
			utils.Log.Warnf("failed to complete magnet scan for %s: %s", work.Code, err)
		}
		actors := append(model.StringArray(nil), work.Actors...)
		seenActors := make(map[string]bool, len(actors))
		for _, actor := range actors {
			seenActors[actor] = true
		}
		for _, actor := range meta.Actors {
			if actor.Name != "" && !seenActors[actor.Name] {
				actors = append(actors, actor.Name)
				seenActors[actor.Name] = true
			}
		}
		if len(actors) > 0 {
			if err := db.UpdateMediaWorkActors(work.ID, actors); err != nil {
				utils.Log.Warnf("failed to update actors for %s: %s", work.Code, err)
			}
		}
		tags := append(model.StringArray(nil), work.Tags...)
		seenTags := make(map[string]bool, len(tags))
		for _, tag := range tags {
			seenTags[tag] = true
		}
		for _, tag := range meta.Magnets[0].GetTags() {
			if tag != "" && !seenTags[tag] {
				tags = append(tags, tag)
				seenTags[tag] = true
			}
		}
		if meta.Magnets[0].IsSubTitle() && !seenTags[model.TagSubtitle] {
			tags = append(tags, model.TagSubtitle)
		}
		if err := db.UpdateMediaWorkTags(work.ID, tags, work.TagVersion+1); err != nil {
			utils.Log.Warnf("failed to update tags for %s: %s", work.Code, err)
		}
	}
}

func sourceMagnetsFromMeta(meta av.Meta) []model.SourceMagnet {
	now := time.Now()
	result := make([]model.SourceMagnet, 0, len(meta.Magnets))
	for index, magnet := range meta.Magnets {
		uri := magnet.GetMagnet()
		if uri == "" {
			continue
		}
		sum := sha256.Sum256([]byte(uri))
		result = append(result, model.SourceMagnet{
			MagnetURI: uri, Fingerprint: hex.EncodeToString(sum[:]), Provider: DriverName,
			Priority: index, Selected: index == 0, Subtitle: magnet.IsSubTitle(), ScanAt: &now,
		})
	}
	return result
}

func (d *Javdb) scanMediaSubtitles() {
	works, err := db.QuerySubtitleMediaWorks(DriverName, d.SubtitlesScanLimit)
	if err != nil {
		utils.Log.Warnf("failed to query JavDB subtitle works: %s", err)
		return
	}
	for index := range works {
		work := works[index]
		subtitles, matchErr := MatchSubtitleCatSubtitles(work.Code)
		if matchErr != nil {
			next := time.Now().Add(24 * time.Hour)
			if err := db.UpdateMediaWorkSubtitleScan(work.ID, &next, matchErr.Error()); err != nil {
				utils.Log.Warnf("failed to update subtitle retry for %s: %s", work.Code, err)
			}
			continue
		}
		identity := mediaIdentity(work)
		files, listErr := db.ListFilmFiles(work.ID)
		if listErr != nil {
			next := time.Now().Add(24 * time.Hour)
			if err := db.UpdateMediaWorkSubtitleScan(work.ID, &next, listErr.Error()); err != nil {
				utils.Log.Warnf("failed to persist subtitle list error for %s: %s", work.Code, err)
			}
			continue
		}
		failed := false
		for _, file := range files {
			if err := virtual_file.SaveMediaSubtitles(identity, file.PartIndex, subtitles); err != nil {
				next := time.Now().Add(24 * time.Hour)
				if updateErr := db.UpdateMediaWorkSubtitleScan(work.ID, &next, err.Error()); updateErr != nil {
					utils.Log.Warnf("failed to persist subtitle error for %s: %s", work.Code, updateErr)
				}
				failed = true
				break
			}
		}
		if failed {
			continue
		}
		if err := db.UpdateMediaWorkSubtitleScan(work.ID, nil, ""); err != nil {
			utils.Log.Warnf("failed to complete subtitle scan for %s: %s", work.Code, err)
		}
	}
}

func (d *Javdb) scanMediaSampleImages() {
	works, err := db.QuerySampleImageMediaWorks(DriverName, 72*time.Hour, 20)
	if err != nil {
		utils.Log.Warnf("failed to query JavDB sample works: %s", err)
		return
	}
	remaining := maxSampleImageRequestsPerRun
	for index := range works {
		work := works[index]
		for sampleIndex := work.SampleImageCount + 1; sampleIndex <= maxSampleImageCount; sampleIndex++ {
			if remaining == 0 {
				return
			}
			remoteURL, pathErr := sampleImageURL(work.ImageURL, sampleIndex)
			if pathErr != nil {
				if err := db.UpdateMediaWorkSampleScan(work.ID, false); err != nil {
					utils.Log.Warnf("failed to update sample scan for %s: %s", work.Code, err)
				}
				break
			}
			remaining--
			_, cacheErr := virtual_file.CacheMediaFanart(context.Background(), mediaIdentity(work), sampleIndex, virtual_file.HTTPFileCacheRequest{
				URL: remoteURL, Headers: map[string]string{"Referer": work.SourceURL}, Client: d.client,
			})
			if cacheErr != nil {
				var statusErr *virtual_file.HTTPStatusError
				if errors.As(cacheErr, &statusErr) && statusErr.StatusCode == http.StatusForbidden {
					if err := db.UpdateMediaWorkSampleProgress(work.ID, sampleIndex-1, true); err != nil {
						utils.Log.Warnf("failed to complete samples for %s: %s", work.Code, err)
					}
				} else if err := db.UpdateMediaWorkSampleScan(work.ID, false); err != nil {
					utils.Log.Warnf("failed to update sample retry for %s: %s", work.Code, err)
				}
				break
			}
			complete := sampleIndex == maxSampleImageCount
			if err := db.UpdateMediaWorkSampleProgress(work.ID, sampleIndex, complete); err != nil {
				utils.Log.Warnf("failed to update sample progress for %s: %s", work.Code, err)
				break
			}
			if complete {
				break
			}
		}
	}
}

func (d *Javdb) scanMediaDMMPosters() {
	works, err := db.QueryDMMPosterMediaWorks(72*time.Hour, 20)
	if err != nil {
		utils.Log.Warnf("failed to query JavDB DMM poster works: %s", err)
		return
	}
	for index := range works {
		d.scanMediaDMMPoster(context.Background(), works[index])
	}
}

func (d *Javdb) scanMediaDMMPoster(ctx context.Context, work model.FilmWork) {
	cid, err := dmmPosterCID(work.Code)
	if err != nil {
		d.updateMediaDMMStatus(work, model.DMMPosterStatusTransientError, err)
		return
	}
	hadTransient := false
	definitiveMisses := 0
	var failures []error
	for _, candidate := range dmmPosterCandidates(cid) {
		content, definitive, fetchErr := d.downloadDMMPoster(ctx, candidate)
		if fetchErr != nil {
			failures = append(failures, fetchErr)
			if definitive {
				definitiveMisses++
			} else {
				hadTransient = true
			}
			continue
		}
		if err := virtual_file.PublishMediaPoster(mediaIdentity(work), content); err != nil {
			d.updateMediaDMMStatus(work, model.DMMPosterStatusTransientError, err)
			return
		}
		d.updateMediaDMMStatus(work, model.DMMPosterStatusSuccess, nil)
		return
	}
	monoCID, monoErr := dmmMonoPosterCID(work.Code)
	if monoErr != nil {
		hadTransient = true
		failures = append(failures, monoErr)
	} else {
		for _, candidate := range dmmMonoPosterCandidates(monoCID) {
			content, definitive, fetchErr := d.downloadDMMMonoPoster(ctx, candidate)
			if fetchErr != nil {
				failures = append(failures, fetchErr)
				if definitive {
					definitiveMisses++
				} else {
					hadTransient = true
				}
				continue
			}
			if err := virtual_file.PublishMediaPoster(mediaIdentity(work), content); err != nil {
				d.updateMediaDMMStatus(work, model.DMMPosterStatusTransientError, err)
				return
			}
			d.updateMediaDMMStatus(work, model.DMMPosterStatusSuccess, nil)
			return
		}
		searchURL, searchErr := d.fetchDmmPosterSearchImageURL(work.Code)
		if searchErr != nil {
			hadTransient = true
			failures = append(failures, searchErr)
		} else if searchURL == "" {
			definitiveMisses++
		} else {
			content, definitive, fetchErr := d.downloadDMMMonoPoster(ctx, searchURL)
			if fetchErr != nil {
				failures = append(failures, fetchErr)
				if definitive {
					definitiveMisses++
				} else {
					hadTransient = true
				}
			} else if err := virtual_file.PublishMediaPoster(mediaIdentity(work), content); err != nil {
				d.updateMediaDMMStatus(work, model.DMMPosterStatusTransientError, err)
				return
			} else {
				d.updateMediaDMMStatus(work, model.DMMPosterStatusSuccess, nil)
				return
			}
		}
	}
	status := model.DMMPosterStatusTransientError
	if definitiveMisses > 0 && !hadTransient {
		status = model.DMMPosterStatusNotFound
	}
	cause := errors.Join(failures...)
	if cause == nil {
		cause = fmt.Errorf("no DMM poster found for %s", work.Code)
	}
	d.updateMediaDMMStatus(work, status, cause)
}

func (d *Javdb) updateMediaDMMStatus(work model.FilmWork, status string, cause error) {
	if cause != nil {
		utils.Log.Warnf("DMM poster scan for %s failed: %s", work.Code, cause)
	}
	if err := db.UpdateMediaWorkDMMPosterStatus(work.ID, status); err != nil {
		utils.Log.Warnf("failed to update DMM status for %s: %s", work.Code, err)
	}
}

func mediaIdentity(work model.FilmWork) virtual_file.MediaIdentity {
	return virtual_file.MediaIdentity{StorageID: work.StorageID, Source: work.Source, PrimaryDir: work.PrimaryDir, Code: work.Code}
}
