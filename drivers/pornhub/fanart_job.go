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

	works, err := db.QueryFanartMediaWorks(d.ID, DriverName, d.fanartRetryInterval(), d.FanartScanLimit, d.FanartCount)
	if err != nil {
		utils.Log.Warnf("failed to query fanart works: %s", err.Error())
		return
	}

	for workIndex := range works {
		if err := ctx.Err(); err != nil {
			return
		}
		d.scanFilmFanart(ctx, &works[workIndex])
	}
}

func (d *Pornhub) scanFilmFanart(ctx context.Context, work *model.FilmWork) {
	count := d.FanartCount
	if count <= 0 {
		return
	}
	identity := pornhubMediaIdentity(work)
	removeBg := d.removeBackgroundCb
	if removeBg == nil {
		removeBg = virtual_file.RemoveMediaBackground
	}
	backgroundRemoved := false
	cleanupBackground := func() error {
		if backgroundRemoved {
			return nil
		}
		if err := removeBg(identity); err != nil {
			return err
		}
		if err := virtual_file.PromoteLegacyMediaPoster(identity); err != nil {
			return err
		}
		backgroundRemoved = true
		return nil
	}
	// Resume: scan existing regular fanart files and advance progress.
	// Clean background for recovered existing fanart before advancing.
	startIndex, finalIndex, err := d.advanceExistingFanart(work, count, cleanupBackground)
	if err != nil {
		d.updateSampleImageScanAt(ctx, work, err)
		return
	}
	if finalIndex == count {
		d.finalizeFanart(ctx, work, count)
		return
	}

	// Resolve a fresh playback URL (kept in memory only).
	resolve := d.fanartGetVideo
	if resolve == nil {
		resolve = d.getVideoLink
	}
	videoURL, err := resolve(ctx, work.SourceRef)
	if err != nil {
		d.updateSampleImageScanAt(ctx, work, err)
		return
	}

	// Probe duration.
	media := d.fanartMedia
	if media == nil {
		media = &fanartFFmpeg{serverURL: d.ServerUrl}
	}
	duration, err := media.ProbeDuration(ctx, videoURL)
	if err != nil {
		d.updateSampleImageScanAt(ctx, work, err)
		return
	}

	// Extract evenly spaced frames strictly inside duration.
	for index := startIndex; index <= count; index++ {
		path, err := virtual_file.MediaFanartPath(identity, index)
		if err != nil {
			d.updateSampleImageScanAt(ctx, work, err)
			return
		}
		info, statErr := os.Lstat(path)
		if statErr == nil && info.Mode().IsRegular() {
			if err := cleanupBackground(); err != nil {
				d.updateSampleImageScanAt(ctx, work, err)
				return
			}
			if index == count {
				d.finalizeFanart(ctx, work, count)
				return
			}
			if err := db.UpdateMediaWorkSampleProgress(work.ID, index, false); err != nil {
				d.updateSampleImageScanAt(ctx, work, err)
				return
			}
			work.SampleImageCount = index
			work.SampleImageComplete = false
			continue
		}
		if statErr != nil && !os.IsNotExist(statErr) {
			d.updateSampleImageScanAt(ctx, work, statErr)
			return
		}

		position := duration * float64(index) / float64(count+1)

		frameData, err := media.ExtractFrame(ctx, videoURL, position)
		if err != nil {
			d.updateSampleImageScanAt(ctx, work, err)
			return
		}

		_, err = virtual_file.PublishMediaFanart(identity, index, frameData)
		if err != nil {
			d.updateSampleImageScanAt(ctx, work, err)
			return
		}

		// Cleanup failure prevents progress advancement so the film is retried.
		if err := cleanupBackground(); err != nil {
			utils.Log.Warnf("failed to remove background for work %s: %s", work.Code, err.Error())
			d.updateSampleImageScanAt(ctx, work, err)
			return
		}

		if index == count {
			d.finalizeFanart(ctx, work, count)
			return
		}
		if err := db.UpdateMediaWorkSampleProgress(work.ID, index, false); err != nil {
			d.updateSampleImageScanAt(ctx, work, err)
			return
		}
		work.SampleImageCount = index
		work.SampleImageComplete = false
	}
}

// advanceExistingFanart scans existing regular fanart files and updates DB progress.
// Removes background for recovered fanart before advancing. Returns the next index
// that needs extraction and the final existing index.
func (d *Pornhub) advanceExistingFanart(work *model.FilmWork, count int, removeBg func() error) (int, int, error) {
	identity := pornhubMediaIdentity(work)
	startIndex := 1
	finalIndex := 0
	for index := startIndex; index <= count; index++ {
		path, err := virtual_file.MediaFanartPath(identity, index)
		if err != nil {
			return 0, 0, err
		}
		info, err := os.Lstat(path)
		if err != nil && !os.IsNotExist(err) {
			return 0, 0, err
		}
		exists := err == nil && info.Mode().IsRegular()
		if !exists {
			return index, finalIndex, nil
		}
		if err := removeBg(); err != nil {
			return 0, 0, err
		}
		finalIndex = index
		if index < count {
			if err := db.UpdateMediaWorkSampleProgress(work.ID, index, false); err != nil {
				return 0, 0, err
			}
			work.SampleImageCount = index
			work.SampleImageComplete = false
		}
	}
	return count + 1, finalIndex, nil
}

func (d *Pornhub) updateSampleImageScanAt(ctx context.Context, work *model.FilmWork, scanErr error) {
	utils.Log.Warnf("failed to scan fanart for work %s: %s", work.Code, scanErr.Error())
	if errors.Is(scanErr, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return
	}
	if err := db.UpdateMediaWorkSampleScanAt(work.ID); err != nil {
		utils.Log.Warnf("failed to update sample-image scan time for work %s: %s", work.Code, err.Error())
	}
}
