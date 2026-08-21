package javdb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"
	"github.com/OpenListTeam/OpenList/v4/internal/av"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/open_ai"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

const currentTranslationVersion = model.CurrentTranslationVersion

var (
	batchTranslateMediaWorks    = open_ai.BatchTranslate
	batchSynopsisMediaTranslate = open_ai.BatchTranslate
	matchMediaSubtitles         = MatchSubtitleCatSubtitles
)

func (d *Javdb) scanUnresolvedSources() {
	utils.Log.Info("start scanning unresolved JavDB sources")
	defer utils.Log.Info("finish scanning unresolved JavDB sources")

	works, err := db.QueryUnresolvedSourceMediaWorks(DriverName, 20)
	if err != nil {
		utils.Log.Warnf("failed to query unresolved JavDB sources: %s", err)
		return
	}
	utils.Log.Infof("found %d unresolved JavDB sources, ids: %v", len(works), mediaWorkIDs(works))
	for index := range works {
		work := works[index]
		films, searchErr := searchJavdbFilms(d, work.Code)
		if searchErr != nil || !javdbSearchMatchesCode(work.Code, films) {
			if searchErr == nil {
				if err := dropMissingJavdbStar(work); err != nil {
					utils.Log.Warnf("failed to drop missing JavDB star %s: %s", work.Code, err)
				}
				continue
			}
			next := time.Now().Add(6 * time.Hour)
			if err := db.UpdateMediaWorkSourceRetry(work.ID, next, searchErr.Error()); err != nil {
				utils.Log.Warnf("failed to update source retry for %s: %s", work.Code, err)
			}
			if isTransientJavdbSearchError(searchErr) {
				return
			}
			continue
		}
		discovered, buildErr := buildDiscoveredWork(work.StorageID, work.PrimaryDir, films[0])
		if buildErr != nil {
			next := time.Now().Add(6 * time.Hour)
			if err := db.UpdateMediaWorkSourceRetry(work.ID, next, buildErr.Error()); err != nil {
				utils.Log.Warnf("failed to update source retry for %s: %s", work.Code, err)
			}
			continue
		}
		discovered.Tags = work.Tags
		if err := db.UpsertDiscoveredWork(&discovered); err != nil {
			utils.Log.Warnf("failed to resolve JavDB source %s: %s", work.Code, err)
		}
	}
}

func (d *Javdb) scanTranslations() {
	utils.Log.Info("start scanning JavDB translations")
	defer utils.Log.Info("finish scanning JavDB translations")

	works, err := db.QueryTranslationMediaWorks(DriverName, currentTranslationVersion, 20)
	if err != nil {
		utils.Log.Warnf("failed to query JavDB translation works: %s", err)
		return
	}
	utils.Log.Infof("found %d JavDB translation works, ids: %v", len(works), mediaWorkIDs(works))
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
			items[index].Candidate = candidateTitle
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

type mediaSynopsisCandidate struct {
	workID uint
	code   string
	origin string
}

func (d *Javdb) scanMediaSynopsis() {
	utils.Log.Info("start scanning JavDB synopsis")
	defer utils.Log.Info("finish scanning JavDB synopsis")

	limit := d.SynopsisScanLimit
	if limit <= 0 {
		limit = 20
	}
	works, err := db.QueryEmptySynopsisMediaWorks(DriverName, 72*time.Hour, limit)
	if err != nil {
		utils.Log.Warnf("failed to query JavDB synopsis works: %s", err)
		return
	}
	utils.Log.Infof("found %d JavDB empty-synopsis works, ids: %v", len(works), mediaWorkIDs(works))

	var collected []mediaSynopsisCandidate
	for index := range works {
		work := works[index]
		file := model.EmbyFileObj{
			ObjThumb: model.ObjThumb{Object: model.Object{Name: work.Code}},
			Code:     work.Code, SourceRef: work.SourceRef, SourceURL: work.SourceURL, Url: work.SourceURL,
			Title: model.BuildMediaTitle(work.Code, work.RawTitle, work.TranslatedTitle),
		}
		_, candidate, airErr := d.getAiravNamingAddr(file)
		if airErr == nil && candidate.Synopsis != "" {
			collected = append(collected, mediaSynopsisCandidate{workID: work.ID, code: work.Code, origin: candidate.Synopsis})
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
		collected = append(collected, mediaSynopsisCandidate{workID: work.ID, code: work.Code, origin: synopsis})
	}

	persistMediaSynopses(collected)
}

func persistMediaSynopses(collected []mediaSynopsisCandidate) {
	if len(collected) == 0 {
		return
	}
	items := make([]open_ai.TranslateItem, len(collected))
	for i, c := range collected {
		items[i] = open_ai.TranslateItem{Origin: c.origin}
	}
	translations := batchSynopsisMediaTranslate(items)
	for i, c := range collected {
		translated := ""
		if i < len(translations) {
			translated = translations[i]
		}
		if translated == "" {
			next := time.Now().Add(72 * time.Hour)
			if err := db.UpdateMediaWorkSynopsisRetry(c.workID, next, "translation returned an empty result"); err != nil {
				utils.Log.Warnf("failed to update synopsis retry for %s: %s", c.code, err)
			}
			continue
		}
		if err := db.UpdateMediaWorkSynopsis(c.workID, translated); err != nil {
			utils.Log.Warnf("failed to save synopsis for %s: %s", c.code, err)
		}
	}
}

func (d *Javdb) scanMediaMetadataAndMagnets() {
	utils.Log.Info("start scanning JavDB metadata and magnets")
	defer utils.Log.Info("finish scanning JavDB metadata and magnets")

	limit := d.MatchFilmTagLimit
	if limit <= 0 {
		return
	}
	works, err := db.QueryPendingMediaWorks(DriverName, db.MediaWorkScanAll, limit)
	if err != nil {
		utils.Log.Warnf("failed to query JavDB metadata works: %s", err)
		return
	}
	utils.Log.Infof("found %d JavDB pending metadata works, ids: %v", len(works), mediaWorkIDs(works))
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
			if err := db.UpdateMediaWorkActorRetry(work.ID, next, fetchErr.Error()); err != nil {
				utils.Log.Warnf("failed to update actor retry for %s: %s", work.Code, err)
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
		for _, actor := range mapScrapedActors(work.StorageID, meta.Actors) {
			if !seenActors[actor] {
				actors = append(actors, actor)
				seenActors[actor] = true
			}
		}
		if err := db.UpdateMediaWorkActors(work.ID, actors); err != nil {
			utils.Log.Warnf("failed to update actors for %s: %s", work.Code, err)
			next := time.Now().Add(6 * time.Hour)
			if updateErr := db.UpdateMediaWorkActorRetry(work.ID, next, err.Error()); updateErr != nil {
				utils.Log.Warnf("failed to update actor retry for %s: %s", work.Code, updateErr)
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

func mapScrapedActors(storageID uint, scraped []av.Actor) []string {
	existActors := db.QueryActor(strconv.FormatUint(uint64(storageID), 10))
	mapping := make(map[string]string, len(existActors))
	for _, actor := range existActors {
		if actor.Url != "" {
			mapping[actor.Url] = actor.Name
		}
	}
	actors := make([]string, 0, len(scraped))
	seen := make(map[string]bool, len(scraped))
	for _, actor := range scraped {
		name := actor.Name
		if mapped := mapping[actor.Id]; mapped != "" {
			name = mapped
		}
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		actors = append(actors, name)
	}
	return actors
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
	utils.Log.Info("start scanning JavDB subtitles")
	defer utils.Log.Info("finish scanning JavDB subtitles")

	works, err := db.QuerySubtitleMediaWorks(DriverName, d.SubtitlesScanLimit)
	if err != nil {
		utils.Log.Warnf("failed to query JavDB subtitle works: %s", err)
		return
	}
	utils.Log.Infof("found %d JavDB subtitle works, ids: %v", len(works), mediaWorkIDs(works))
	for index := range works {
		work := works[index]
		subtitles, matchErr := matchMediaSubtitles(work.Code)
		if matchErr != nil {
			next := time.Now().Add(24 * time.Hour)
			if err := db.UpdateMediaWorkSubtitleScan(work.ID, &next, matchErr.Error()); err != nil {
				utils.Log.Warnf("failed to update subtitle retry for %s: %s", work.Code, err)
			}
			continue
		}
		if len(subtitles) == 0 {
			var nextRetryAt *time.Time
			if !work.ReleaseDate.Before(time.Now().AddDate(-1, 0, 0)) {
				next := time.Now().AddDate(0, 0, 7)
				nextRetryAt = &next
			}
			if err := db.UpdateMediaWorkSubtitleScan(work.ID, nextRetryAt, ""); err != nil {
				utils.Log.Warnf("failed to complete empty subtitle scan for %s: %s", work.Code, err)
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

func (d *Javdb) scanMediaDMMPosters() {
	utils.Log.Info("start scanning JavDB DMM posters")
	defer utils.Log.Info("finish scanning JavDB DMM posters")

	works, err := db.QueryDMMPosterMediaWorks(72*time.Hour, 20)
	if err != nil {
		utils.Log.Warnf("failed to query JavDB DMM poster works: %s", err)
		return
	}
	utils.Log.Infof("found %d JavDB DMM poster works, ids: %v", len(works), mediaWorkIDs(works))
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

func mediaWorkIDs(works []model.FilmWork) []uint {
	ids := make([]uint, len(works))
	for i, w := range works {
		ids[i] = w.ID
	}
	return ids
}

func mediaIdentity(work model.FilmWork) virtual_file.MediaIdentity {
	return virtual_file.MediaIdentity{StorageID: work.StorageID, Source: work.Source, PrimaryDir: work.PrimaryDir, Code: work.Code}
}
