package javdb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"
	"github.com/OpenListTeam/OpenList/v4/internal/av"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/open_ai"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

const (
	maxSampleImageCount          = 50
	maxSampleImageRequestsPerRun = 100
	maxDMMPosterBytes            = 16 << 20
	minDMMMonoPosterBytes        = 50 << 10
	maxDMMPosterDimension        = 12000
	maxDMMPosterPixels           = 60_000_000
)

var (
	dmmFilmCodePattern              = regexp.MustCompile(`(?i)^([a-z0-9]+)-([0-9]+)$`)
	errDMMMonoPosterUnusable        = errors.New("DMM mono poster is unusable")
	promoteLandscapeFanartCandidate = virtual_file.PromoteLandscapeFanart
)

func dmmPosterCID(name string) (string, error) {
	canonicalName := virtual_file.CutString(virtual_file.ClearFilmName(splitCode(name)))
	matches := dmmFilmCodePattern.FindStringSubmatch(canonicalName)
	if len(matches) != 3 {
		return "", fmt.Errorf("unsupported DMM film code: %q", canonicalName)
	}
	numeric := matches[2]
	if len(numeric) < 5 {
		numeric = strings.Repeat("0", 5-len(numeric)) + numeric
	}
	return strings.ToLower(matches[1]) + numeric, nil
}

func dmmMonoPosterCID(name string) (string, error) {
	canonicalName := virtual_file.CutString(virtual_file.ClearFilmName(splitCode(name)))
	matches := dmmFilmCodePattern.FindStringSubmatch(canonicalName)
	if len(matches) != 3 {
		return "", fmt.Errorf("unsupported DMM film code: %q", canonicalName)
	}
	return strings.ToLower(matches[1]) + matches[2], nil
}

func dmmPosterCandidates(cid string) []string {
	firstCID := "1" + cid
	const root = "https://awsimgsrc.dmm.co.jp/pics_dig/digital/video/"
	return []string{
		root + firstCID + "/" + firstCID + "ps.jpg",
		root + cid + "/" + cid + "ps.jpg",
	}
}

func dmmMonoPosterCandidates(cid string) []string {
	const root = "https://pics.dmm.co.jp/mono/movie/adult/"
	return []string{
		root + "118" + cid + "/118" + cid + "pl.jpg",
		root + "1" + cid + "/1" + cid + "pl.jpg",
	}
}

func cropDMMMonoPoster(content []byte) ([]byte, error) {
	if len(content) < minDMMMonoPosterBytes {
		return nil, fmt.Errorf("%w: response is too small: %d bytes", errDMMMonoPosterUnusable, len(content))
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(content))
	if err != nil {
		return nil, fmt.Errorf("invalid DMM mono poster image: %w", err)
	}
	if config.Width != 800 {
		return nil, fmt.Errorf("%w: unexpected width: got %d, want 800", errDMMMonoPosterUnusable, config.Width)
	}
	if config.Height <= 0 || config.Height > maxDMMPosterDimension ||
		int64(config.Width)*int64(config.Height) > maxDMMPosterPixels {
		return nil, fmt.Errorf("%w: dimensions exceed limits: %dx%d", errDMMMonoPosterUnusable, config.Width, config.Height)
	}
	decoded, _, err := image.Decode(bytes.NewReader(content))
	if err != nil {
		return nil, fmt.Errorf("invalid DMM mono poster image data: %w", err)
	}
	bounds := decoded.Bounds()
	cropped := image.NewRGBA(image.Rect(0, 0, 380, bounds.Dy()))
	draw.Draw(cropped, cropped.Bounds(), decoded, image.Pt(bounds.Min.X+420, bounds.Min.Y), draw.Src)

	var output bytes.Buffer
	if err := jpeg.Encode(&output, cropped, &jpeg.Options{Quality: 95}); err != nil {
		return nil, fmt.Errorf("encode cropped DMM mono poster: %w", err)
	}
	return output.Bytes(), nil
}

func (d *Javdb) scanDMMPosters() {
	utils.Log.Info("start scanning DMM posters for javdb films")
	defer utils.Log.Info("finish scanning DMM posters")

	films, err := db.QueryDMMPosterFilms(72*time.Hour, 20)
	if err != nil {
		utils.Log.Warnf("failed to query DMM poster films: %s", err.Error())
		return
	}
	for index := range films {
		d.scanDMMPoster(context.Background(), &films[index])
	}
}

func (d *Javdb) scanDMMPoster(ctx context.Context, film *model.Film) {
	cid, err := dmmPosterCID(film.Name)
	if err != nil {
		d.updateDMMPosterStatus(film, model.DMMPosterStatusTransientError, err)
		return
	}

	definitiveMisses := 0
	hadTransientFailure := false
	var failures []error
	for _, candidate := range dmmPosterCandidates(cid) {
		content, definitiveMiss, fetchErr := d.downloadDMMPoster(ctx, candidate)
		if fetchErr != nil {
			failures = append(failures, fmt.Errorf("fetch %s: %w", candidate, fetchErr))
			if definitiveMiss {
				definitiveMisses++
			} else {
				hadTransientFailure = true
			}
			continue
		}

		result, publishErr := virtual_file.PublishPoster(DriverName, film.Actor, film.Name, content)
		if publishErr != nil {
			d.updateDMMPosterStatus(film, model.DMMPosterStatusTransientError, publishErr)
			return
		}
		if result.Published {
			d.updateDMMPosterStatus(film, model.DMMPosterStatusSuccess, nil)
			return
		}
		d.updateDMMPosterStatus(film, model.DMMPosterStatusTransientError, errors.New("DMM poster publication produced no file"))
		return
	}

	monoCID, err := dmmMonoPosterCID(film.Name)
	if err != nil {
		d.updateDMMPosterStatus(film, model.DMMPosterStatusTransientError, err)
		return
	}
	for _, candidate := range dmmMonoPosterCandidates(monoCID) {
		content, definitiveMiss, fetchErr := d.downloadDMMMonoPoster(ctx, candidate)
		if fetchErr != nil {
			failures = append(failures, fmt.Errorf("fetch and crop %s: %w", candidate, fetchErr))
			if definitiveMiss {
				definitiveMisses++
			} else {
				hadTransientFailure = true
			}
			continue
		}
		d.publishDMMPoster(film, content)
		return
	}

	code := virtual_file.CutString(virtual_file.ClearFilmName(splitCode(film.Name)))
	searchCandidate, searchErr := d.fetchDmmPosterSearchImageURL(code)
	if searchErr != nil {
		hadTransientFailure = true
		failures = append(failures, searchErr)
	} else if searchCandidate == "" {
		definitiveMisses++
		failures = append(failures, fmt.Errorf("DMM search returned no matching poster for %s", code))
	} else {
		content, definitiveMiss, fetchErr := d.downloadDMMMonoPoster(ctx, searchCandidate)
		if fetchErr != nil {
			failures = append(failures, fmt.Errorf("fetch and crop search result %s: %w", searchCandidate, fetchErr))
			if definitiveMiss {
				definitiveMisses++
			} else {
				hadTransientFailure = true
			}
		} else {
			d.publishDMMPoster(film, content)
			return
		}
	}

	status := model.DMMPosterStatusTransientError
	if definitiveMisses > 0 && !hadTransientFailure {
		status = model.DMMPosterStatusNotFound
	}
	cause := errors.Join(failures...)
	if cause == nil {
		cause = fmt.Errorf("no usable DMM poster found for %s", film.Name)
	}
	d.updateDMMPosterStatus(film, status, cause)
}

func (d *Javdb) publishDMMPoster(film *model.Film, content []byte) {
	result, err := virtual_file.PublishPoster(DriverName, film.Actor, film.Name, content)
	if err != nil {
		d.updateDMMPosterStatus(film, model.DMMPosterStatusTransientError, err)
		return
	}
	if !result.Published {
		d.updateDMMPosterStatus(film, model.DMMPosterStatusTransientError, errors.New("DMM poster publication produced no file"))
		return
	}
	d.updateDMMPosterStatus(film, model.DMMPosterStatusSuccess, nil)
}

func (d *Javdb) downloadDMMPoster(ctx context.Context, candidate string) ([]byte, bool, error) {
	client := d.client
	if client == nil {
		client = newSampleImageClient()
	}
	response, err := client.R().SetContext(ctx).SetDoNotParseResponse(true).Get(candidate)
	if err != nil {
		return nil, false, err
	}
	body := response.RawBody()
	defer body.Close()

	if response.StatusCode() != http.StatusOK {
		definitiveMiss := response.StatusCode() == http.StatusNotFound || response.StatusCode() == http.StatusGone
		return nil, definitiveMiss, fmt.Errorf("DMM poster request returned HTTP %d", response.StatusCode())
	}
	if response.RawResponse.ContentLength > maxDMMPosterBytes {
		return nil, false, fmt.Errorf("DMM poster exceeds maximum size of %d bytes", maxDMMPosterBytes)
	}
	content, err := io.ReadAll(io.LimitReader(body, maxDMMPosterBytes+1))
	if err != nil {
		return nil, false, err
	}
	if len(content) > maxDMMPosterBytes {
		return nil, false, fmt.Errorf("DMM poster exceeds maximum size of %d bytes", maxDMMPosterBytes)
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(content))
	if err != nil {
		return nil, false, fmt.Errorf("invalid DMM poster image: %w", err)
	}
	if config.Width <= 0 || config.Height <= 0 ||
		config.Width > maxDMMPosterDimension || config.Height > maxDMMPosterDimension ||
		int64(config.Width)*int64(config.Height) > maxDMMPosterPixels {
		return nil, false, fmt.Errorf("DMM poster dimensions exceed limits: %dx%d", config.Width, config.Height)
	}
	if _, _, err := image.Decode(bytes.NewReader(content)); err != nil {
		return nil, false, fmt.Errorf("invalid DMM poster image data: %w", err)
	}
	return content, false, nil
}

func (d *Javdb) downloadDMMMonoPoster(ctx context.Context, candidate string) ([]byte, bool, error) {
	client := d.client
	if client == nil {
		client = newSampleImageClient()
	}
	response, err := client.R().SetContext(ctx).SetDoNotParseResponse(true).Get(candidate)
	if err != nil {
		return nil, false, err
	}
	body := response.RawBody()
	defer body.Close()

	if response.StatusCode() != http.StatusOK {
		definitiveMiss := response.StatusCode() == http.StatusNotFound || response.StatusCode() == http.StatusGone
		return nil, definitiveMiss, fmt.Errorf("DMM mono poster request returned HTTP %d", response.StatusCode())
	}
	if response.RawResponse.ContentLength > maxDMMPosterBytes {
		return nil, false, fmt.Errorf("DMM mono poster exceeds maximum size of %d bytes", maxDMMPosterBytes)
	}
	content, err := io.ReadAll(io.LimitReader(body, maxDMMPosterBytes+1))
	if err != nil {
		return nil, false, err
	}
	if len(content) > maxDMMPosterBytes {
		return nil, false, fmt.Errorf("DMM mono poster exceeds maximum size of %d bytes", maxDMMPosterBytes)
	}
	cropped, err := cropDMMMonoPoster(content)
	if err != nil {
		return nil, errors.Is(err, errDMMMonoPosterUnusable), err
	}
	return cropped, false, nil
}

func (d *Javdb) updateDMMPosterStatus(film *model.Film, status string, cause error) {
	if cause != nil {
		utils.Log.Warnf("DMM poster scan for film %s finished with status %s: %s", film.Name, status, cause.Error())
	}
	if err := db.UpdateDMMPosterStatus(film.ID, status); err != nil {
		utils.Log.Warnf("failed to update DMM poster status for film %s: %s", film.Name, err.Error())
	}
}

func sampleImageURL(imageURL string, index int) (string, error) {
	if index < 1 || index > maxSampleImageCount {
		return "", fmt.Errorf("unsupported sample image index: %d", index)
	}

	parsed, err := url.Parse(imageURL)
	if err != nil {
		return "", err
	}
	hostname := strings.ToLower(parsed.Hostname())
	trustedHost := hostname == "jdbstatic.com" || strings.HasSuffix(hostname, ".jdbstatic.com")
	if parsed.Scheme != "https" || !trustedHost || parsed.RawPath != "" || !strings.HasSuffix(parsed.Path, ".jpg") {
		return "", fmt.Errorf("unsupported cover URL: %s", imageURL)
	}

	segments := strings.Split(parsed.Path, "/")
	foundCovers := false
	for segmentIndex := range segments {
		if segments[segmentIndex] == "covers" {
			segments[segmentIndex] = "samples"
			foundCovers = true
			break
		}
	}
	if !foundCovers {
		return "", fmt.Errorf("unsupported cover URL: %s", imageURL)
	}

	parsed.Path = strings.TrimSuffix(strings.Join(segments, "/"), ".jpg") + fmt.Sprintf("_l_%d.jpg", index)
	parsed.RawPath = ""
	return parsed.String(), nil
}

func (d *Javdb) scanSampleImages() {
	utils.Log.Info("start scanning sample images for javdb films")
	defer utils.Log.Info("finish scanning sample images")

	films, err := db.QuerySampleImageFilms(DriverName, 72*time.Hour, 20)
	if err != nil {
		utils.Log.Warnf("failed to query sample-image films: %s", err.Error())
		return
	}

	remainingRequests := maxSampleImageRequestsPerRun
	for filmIndex := range films {
		if d.scanFilmSampleImagesWithBudget(context.Background(), &films[filmIndex], &remainingRequests) {
			return
		}
	}
}

func (d *Javdb) scanFilmSampleImages(ctx context.Context, film *model.Film) {
	d.scanFilmSampleImagesWithBudget(ctx, film, nil)
}

func (d *Javdb) scanFilmSampleImagesWithBudget(ctx context.Context, film *model.Film, remainingRequests *int) bool {
	if film.SampleImageComplete {
		return false
	}
	removeBackground := d.removeBackground
	if removeBackground == nil {
		removeBackground = virtual_file.RemoveBackground
	}
	backgroundRemoved := false
	if film.SampleImageCount > 0 {
		firstFanart, err := virtual_file.FanartPath(DriverName, film.Actor, film.Name, 1)
		if err != nil {
			d.updateSampleImageScanAt(film, err)
			return false
		}
		info, err := os.Lstat(firstFanart)
		if err != nil && !os.IsNotExist(err) {
			d.updateSampleImageScanAt(film, err)
			return false
		}
		if err == nil && info.Mode().IsRegular() {
			if err := removeBackground(DriverName, film.Actor, film.Name); err != nil {
				d.updateSampleImageScanAt(film, err)
				return false
			}
			backgroundRemoved = true
		}
	}
	landscapeReady, err := d.promoteLandscapeFanart(film, film.SampleImageCount)
	if err != nil {
		d.updateSampleImageScanAt(film, err)
		return false
	}
	if film.SampleImageCount >= maxSampleImageCount {
		if err := db.MarkSampleImageComplete(film.ID); err != nil {
			utils.Log.Warnf("failed to mark sample images complete for film %s: %s", film.Name, err.Error())
		}
		return false
	}

	for index := film.SampleImageCount + 1; index <= maxSampleImageCount; index++ {
		remoteURL, err := sampleImageURL(film.Image, index)
		if err != nil {
			d.updateSampleImageScanAt(film, err)
			return false
		}

		destination, err := virtual_file.FanartPath(DriverName, film.Actor, film.Name, index)
		if err != nil {
			d.updateSampleImageScanAt(film, err)
			return false
		}
		info, statErr := os.Lstat(destination)
		requestNeeded := os.IsNotExist(statErr)
		if statErr == nil && info.Mode().IsRegular() {
			requestNeeded = false
		}
		if requestNeeded && remainingRequests != nil {
			if *remainingRequests == 0 {
				return true
			}
			*remainingRequests--
		}
		_, err = virtual_file.CacheFanart(ctx, virtual_file.FanartCacheRequest{
			Source:   DriverName,
			Dir:      film.Actor,
			FilmName: film.Name,
			Index:    index,
			URL:      remoteURL,
			Client:   d.client,
			Headers: map[string]string{
				"Referer": film.Url,
			},
		})
		if err != nil {
			var statusError *virtual_file.HTTPStatusError
			if errors.As(err, &statusError) && statusError.StatusCode == 403 {
				if markErr := db.MarkSampleImageComplete(film.ID); markErr != nil {
					utils.Log.Warnf("failed to mark sample images complete for film %s: %s", film.Name, markErr.Error())
				}
				return false
			}
			d.updateSampleImageScanAt(film, err)
			return false
		}
		if !backgroundRemoved {
			if err := removeBackground(DriverName, film.Actor, film.Name); err != nil {
				utils.Log.Warnf("failed to remove background for film %s: %s", film.Name, err.Error())
				d.updateSampleImageScanAt(film, err)
				return false
			}
			backgroundRemoved = true
		}
		if !landscapeReady {
			landscapeReady, err = d.promoteLandscapeFanart(film, index)
			if err != nil {
				d.updateSampleImageScanAt(film, err)
				return false
			}
		}

		complete := index == maxSampleImageCount
		if err := db.UpdateSampleImageProgress(film.ID, index, complete); err != nil {
			utils.Log.Warnf("failed to update sample-image progress for film %s: %s", film.Name, err.Error())
			return false
		}
		film.SampleImageCount = index
		film.SampleImageComplete = complete
		if complete {
			return false
		}
	}
	return false
}

func (d *Javdb) promoteLandscapeFanart(film *model.Film, count int) (bool, error) {
	if count < 2 {
		return false, nil
	}
	for index := 2; index <= count; index++ {
		if err := virtual_file.RecoverFanartSwap(DriverName, film.Actor, film.Name, 1, index); err != nil {
			return false, fmt.Errorf("recover fanart promotion at index %d: %w", index, err)
		}
	}
	for index := 2; index <= count; index++ {
		landscapeReady, err := promoteLandscapeFanartCandidate(DriverName, film.Actor, film.Name, index)
		if err != nil {
			return false, fmt.Errorf("promote landscape fanart at index %d: %w", index, err)
		}
		if landscapeReady {
			return true, nil
		}
	}
	return false, nil
}

func (d *Javdb) updateSampleImageScanAt(film *model.Film, scanErr error) {
	utils.Log.Warnf("failed to cache sample image for film %s: %s", film.Name, scanErr.Error())
	if err := db.UpdateSampleImageScanAt(film.ID); err != nil {
		utils.Log.Warnf("failed to update sample-image scan time for film %s: %s", film.Name, err.Error())
	}
}

func (d *Javdb) reMatchSubtitles() {
	d.scanMediaSubtitles()
}

func (d *Javdb) refreshNfo() {

	utils.Log.Info("start refresh nfo for javdb")
	defer utils.Log.Info("finish refresh nfo")

	var actorNames []string
	actors := db.QueryActor(strconv.Itoa(int(d.ID)))
	for _, actor := range actors {
		actorNames = append(actorNames, actor.Name)
	}
	for _, actor := range actorNames {

		films := virtual_file.GetStorageFilms(DriverName, actor, false)

		// refresh nfo
		mappingNameFilms, err := d.mappingNames(actor, films)
		if err != nil {
			utils.Log.Warn("failed to get mapping names:", err.Error())
			continue
		}

		var filmNames []string
		for _, film := range mappingNameFilms {
			virtual_file.UpdateNfo(virtual_file.MediaInfo{
				Source:   DriverName,
				Dir:      film.Path,
				FileName: virtual_file.AppendImageName(film.Name),
				Release:  film.ReleaseTime,
				Title:    film.Title,
				Actors:   film.Actors,
				Tags:     film.Tags,
			})
			filmNames = append(filmNames, film.Name)
		}

		// clear unused files
		virtual_file.ClearUnUsedFiles(DriverName, actor, filmNames)

	}

}

func (d *Javdb) scanSynopsis() {

	utils.Log.Info("start scanning synopsis for javdb films")
	defer utils.Log.Info("finish scanning synopsis")

	limit := d.SynopsisScanLimit
	if limit <= 0 {
		limit = 20
	}

	films, err := db.QueryEmptySynopsisFilms(DriverName, time.Hour*72, limit)
	if err != nil {
		utils.Log.Warnf("failed to query empty synopsis films: %s", err.Error())
		return
	}

	if len(films) == 0 {
		return
	}

	utils.Log.Infof("found %d films without synopsis", len(films))

	var dmmFilms []model.Film
	var dmmSynopses []string

	for _, film := range films {
		d.scanFilmSynopsis(&film, &dmmFilms, &dmmSynopses)
		time.Sleep(3 * time.Second)
	}

	d.flushDmmSynopses(dmmFilms, dmmSynopses)
}

// scanFilmSynopsis 扫描单个影片的简介：先尝试 airav，失败则尝试 DMM
func (d *Javdb) scanFilmSynopsis(film *model.Film, dmmFilms *[]model.Film, dmmSynopses *[]string) {

	embyObj := model.EmbyFileObj{
		ObjThumb: model.ObjThumb{
			Object: model.Object{Name: film.Name},
		},
		Title: film.Title,
		Url:   film.Url,
	}

	_, result, err := d.getAiravNamingAddr(embyObj)

	if err != nil {
		utils.Log.Infof("scanSynopsis: failed to fetch airav page for %s: %s", film.Name, err.Error())
	} else if result.Synopsis != "" {
		d.saveScanResult(film, result.Synopsis, "airav")
		return
	}

	// airav 失败或未找到，尝试 DMM
	code := splitCode(film.Name)
	dmmSynopsis, err := d.fetchDmmSynopsis(code)
	if err != nil {
		utils.Log.Infof("scanSynopsis: DMM爬取失败 %s: %s", film.Name, err.Error())
		err = db.UpdateSynopsisScanAt(film.ID)
		if err != nil {
			utils.Log.Warnf("failed to update synopsis scan at for film %s: %s", film.Name, err.Error())
		}
		return
	}
	if dmmSynopsis != "" {
		*dmmFilms = append(*dmmFilms, *film)
		*dmmSynopses = append(*dmmSynopses, dmmSynopsis)
	} else {
		d.saveScanResult(film, "", "")
	}
}

// saveScanResult 保存扫描结果：有简介则写入 DB+NFO，无简介则标记排除或重试
func (d *Javdb) saveScanResult(film *model.Film, synopsis, source string) {

	if synopsis != "" {
		err := db.UpdateFilmSynopsis(film.ID, synopsis)
		if err != nil {
			utils.Log.Warnf("failed to update synopsis for film %s: %s", film.Name, err.Error())
			return
		}
		d.updateNfo(film, synopsis)
		utils.Log.Infof("updated synopsis for film: %s (%s)", film.Name, source)
		return
	}

	if film.Date.Before(time.Now().AddDate(0, -1, 0)) {
		err := db.MarkSynopsisExcluded(film.ID)
		if err != nil {
			utils.Log.Warnf("failed to mark synopsis excluded for film %s: %s", film.Name, err.Error())
		}
	} else {
		err := db.UpdateSynopsisScanAt(film.ID)
		if err != nil {
			utils.Log.Warnf("failed to update synopsis scan at for film %s: %s", film.Name, err.Error())
		}
	}
}

// flushDmmSynopses 批量 AI 翻译 DMM 简介并写入 DB+NFO
func (d *Javdb) flushDmmSynopses(films []model.Film, synopses []string) {

	if len(synopses) == 0 {
		return
	}

	items := make([]open_ai.TranslateItem, len(synopses))
	for i, s := range synopses {
		items[i] = open_ai.TranslateItem{Origin: s}
	}

	translations := open_ai.BatchTranslate(items)
	for i, translated := range translations {
		film := films[i]
		if translated == "" {
			translated = synopses[i]
		}
		err := db.UpdateFilmSynopsis(film.ID, translated)
		if err != nil {
			utils.Log.Warnf("failed to update synopsis for film %s: %s", film.Name, err.Error())
			continue
		}
		d.updateNfo(&film, translated)
		utils.Log.Infof("updated synopsis for film: %s (dmm)", film.Name)
	}
}

// updateNfo 更新影片的 NFO 文件
func (d *Javdb) updateNfo(film *model.Film, synopsis string) {
	virtual_file.UpdateNfo(virtual_file.MediaInfo{
		Source:   DriverName,
		Dir:      film.Actor,
		FileName: virtual_file.AppendImageName(film.Name),
		Title:    film.Title,
		Synopsis: synopsis,
		Release:  film.Date,
		Actors:   film.Actors,
		Tags:     film.Tags,
	})
}

func (d *Javdb) filterFilms() error {
	prefixes := make([]string, 0)
	for _, raw := range strings.Split(d.Filter, ",") {
		prefix := strings.ToUpper(strings.TrimSpace(raw))
		if prefix != "" {
			prefixes = append(prefixes, prefix)
		}
	}
	works, err := db.QueryFilmWorksByCodePrefixes(d.ID, DriverName, prefixes)
	if err != nil {
		return fmt.Errorf("query normalized JavDB filter works: %w", err)
	}
	deleteErrors := make([]error, 0)
	for _, work := range works {
		if err := virtual_file.DeleteMediaWork(work.ID); err != nil {
			deleteErrors = append(deleteErrors, fmt.Errorf("delete filtered work %s: %w", work.Code, err))
		}
	}
	return errors.Join(deleteErrors...)
}

func (d *Javdb) reMatchTags() {
	d.scanMediaMetadataAndMagnets()
}

func (d *Javdb) fetchJavTopFilms() {

	utils.Log.Infof("start to fetch javdb top films")
	defer utils.Log.Infof("finish fetch javdb top films")

	var missedFilms []string

	defer func() {
		if len(missedFilms) > 0 {
			err := db.CreateMissedFilms(missedFilms)
			if err != nil {
				utils.Log.Warn("failed to create missed films:", err.Error())
			}
		}
	}()

	addFilmFunc := func(codes, tags []string) error {

		unMissedFilms := db.QueryUnMissedFilms(codes)

		for _, code := range unMissedFilms {

			if strings.HasPrefix(code, "FC2-") {
				continue
			}
			_, err := d.addStar(code, tags)
			if err != nil {
				if strings.Contains(err.Error(), "未查询到") {
					missedFilms = append(missedFilms, code)
				} else {
					utils.Log.Warnf("failed to add film for code: %s, error: %s", code, err.Error())
					return err
				}
			}
		}

		return nil
	}

	// top 250 yearly
	year := time.Now().Year()
	for i := d.MatchTopFilmsStarter; i <= year; i++ {
		codes := av.QueryJavSql(d.SpiderServer, fmt.Sprintf("SELECT SUBSTR(name, 0, 40) FROM ranks WHERE note = 'JavDB %d TOP250'", i), d.SpiderMaxWaitTime)
		err := addFilmFunc(codes, []string{fmt.Sprintf("JavDB-TOP250-%d", i)})
		if err != nil {
			return
		}
	}

	// top 250
	codes := av.QueryJavSql(d.SpiderServer, "SELECT SUBSTR(name, 0, 40) FROM ranks WHERE note = 'JavDB TOP250'", d.SpiderMaxWaitTime)
	_ = addFilmFunc(codes, []string{"JavDB-TOP250"})

}
