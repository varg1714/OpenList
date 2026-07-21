package pornhub

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

func (d *Pornhub) fanartRetryInterval() time.Duration {
	return 72 * time.Hour
}

func (d *Pornhub) scanFanart(ctx context.Context) {
	if d.FanartCount <= 0 || d.FanartScanLimit <= 0 {
		return
	}
	utils.Log.Info("start scanning fanart for pornhub films")
	defer utils.Log.Info("finish scanning fanart for pornhub films")

	films, err := db.QueryFanartFilms(DriverName, d.fanartRetryInterval(), d.FanartScanLimit, d.FanartCount)
	if err != nil {
		utils.Log.Warnf("failed to query sample-image films: %s", err.Error())
		return
	}

	for filmIndex := range films {
		if err := ctx.Err(); err != nil {
			return
		}
		d.scanFilmFanart(ctx, &films[filmIndex])
	}
}

func (d *Pornhub) scanFilmFanart(ctx context.Context, film *model.Film) {
	count := d.FanartCount
	if count <= 0 {
		return
	}
	removeBg := d.removeBackgroundCb
	if removeBg == nil {
		removeBg = virtual_file.RemoveBackground
	}
	backgroundRemoved := false
	cleanupBackground := func(source, dir, filmName string) error {
		if backgroundRemoved {
			return nil
		}
		if err := removeBg(source, dir, filmName); err != nil {
			return err
		}
		if err := virtual_file.PromoteLegacyPoster(source, dir, filmName); err != nil {
			return err
		}
		backgroundRemoved = true
		return nil
	}
	// Resume: scan existing regular fanart files and advance progress.
	// Clean background for recovered existing fanart before advancing.
	startIndex, err := d.advanceExistingFanart(film, count, cleanupBackground)
	if err != nil {
		d.updateSampleImageScanAt(ctx, film, err)
		return
	}
	if startIndex > count {
		d.updateFanartAudit(film)
		return
	}

	// Resolve a fresh playback URL (kept in memory only).
	resolve := d.fanartGetVideo
	if resolve == nil {
		resolve = d.getVideoLink
	}
	videoURL, err := resolve(ctx, film.Url)
	if err != nil {
		d.updateSampleImageScanAt(ctx, film, err)
		return
	}

	// Probe duration.
	media := d.fanartMedia
	if media == nil {
		media = &fanartFFmpeg{serverURL: d.ServerUrl}
	}
	duration, err := media.ProbeDuration(ctx, videoURL)
	if err != nil {
		d.updateSampleImageScanAt(ctx, film, err)
		return
	}

	// Extract evenly spaced frames strictly inside duration.
	for index := startIndex; index <= count; index++ {
		path, err := virtual_file.FanartPath(DriverName, film.Actor, film.Name, index)
		if err != nil {
			d.updateSampleImageScanAt(ctx, film, err)
			return
		}
		info, statErr := os.Lstat(path)
		if statErr == nil && info.Mode().IsRegular() {
			if err := cleanupBackground(DriverName, film.Actor, film.Name); err != nil {
				d.updateSampleImageScanAt(ctx, film, err)
				return
			}
			complete := index == count
			if err := db.UpdateSampleImageProgress(film.ID, index, complete); err != nil {
				d.updateSampleImageScanAt(ctx, film, err)
				return
			}
			film.SampleImageCount = index
			film.SampleImageComplete = complete
			continue
		}
		if statErr != nil && !os.IsNotExist(statErr) {
			d.updateSampleImageScanAt(ctx, film, statErr)
			return
		}

		position := duration * float64(index) / float64(count+1)

		frameData, err := media.ExtractFrame(ctx, videoURL, position)
		if err != nil {
			d.updateSampleImageScanAt(ctx, film, err)
			return
		}

		_, err = virtual_file.PublishFanart(virtual_file.FanartPublishRequest{
			Source:   DriverName,
			Dir:      film.Actor,
			FilmName: film.Name,
			Index:    index,
			Content:  frameData,
		})
		if err != nil {
			d.updateSampleImageScanAt(ctx, film, err)
			return
		}

		// Cleanup failure prevents progress advancement so the film is retried.
		if err := cleanupBackground(DriverName, film.Actor, film.Name); err != nil {
			utils.Log.Warnf("failed to remove background for film %s: %s", film.Name, err.Error())
			d.updateSampleImageScanAt(ctx, film, err)
			return
		}

		complete := index == count
		if err := db.UpdateSampleImageProgress(film.ID, index, complete); err != nil {
			d.updateSampleImageScanAt(ctx, film, err)
			return
		}
		film.SampleImageCount = index
		film.SampleImageComplete = complete
	}
	if film.SampleImageComplete {
		d.updateFanartAudit(film)
	}
}

// advanceExistingFanart scans existing regular fanart files and updates DB progress.
// Removes background for recovered fanart before advancing. Returns the next index
// that needs extraction, or count+1 if all frames already exist.
func (d *Pornhub) advanceExistingFanart(film *model.Film, count int, removeBg func(string, string, string) error) (int, error) {
	startIndex := 1
	for index := startIndex; index <= count; index++ {
		path, err := virtual_file.FanartPath(DriverName, film.Actor, film.Name, index)
		if err != nil {
			return 0, err
		}
		info, err := os.Lstat(path)
		if err != nil && !os.IsNotExist(err) {
			return 0, err
		}
		exists := err == nil && info.Mode().IsRegular()
		if !exists {
			return index, nil
		}
		if err := removeBg(DriverName, film.Actor, film.Name); err != nil {
			return 0, err
		}
		complete := index >= count
		if err := db.UpdateSampleImageProgress(film.ID, index, complete); err != nil {
			return 0, err
		}
		film.SampleImageCount = index
		film.SampleImageComplete = complete
	}
	return count + 1, nil
}

func (d *Pornhub) updateSampleImageScanAt(ctx context.Context, film *model.Film, scanErr error) {
	utils.Log.Warnf("failed to scan fanart for film %s: %s", film.Name, scanErr.Error())
	if errors.Is(scanErr, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return
	}
	if err := db.UpdateSampleImageScanAt(film.ID); err != nil {
		utils.Log.Warnf("failed to update sample-image scan time for film %s: %s", film.Name, err.Error())
	}
}

func (d *Pornhub) updateFanartAudit(film *model.Film) {
	if err := db.UpdateSampleImageScanAt(film.ID); err != nil {
		utils.Log.Warnf("failed to record fanart audit for film %s: %s", film.Name, err.Error())
	}
}
