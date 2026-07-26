package javdb

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

func (d *Javdb) scanMediaSampleImages() {
	utils.Log.Info("start scanning JavDB sample images")
	defer utils.Log.Info("finish scanning JavDB sample images")

	works, err := db.QuerySampleImageMediaWorks(DriverName, 72*time.Hour, 20)
	if err != nil {
		utils.Log.Warnf("failed to query JavDB sample works: %s", err)
		return
	}
	utils.Log.Infof("found %d JavDB sample-image works, ids: %v", len(works), mediaWorkIDs(works))
	remaining := maxSampleImageRequestsPerRun
	for index := range works {
		work := works[index]
		for sampleIndex := work.SampleImageCount + 1; sampleIndex <= maxSampleImageCount; sampleIndex++ {
			if remaining == 0 {
				return
			}
			remoteURL, pathErr := sampleImageURL(work.ImageURL, sampleIndex)
			if pathErr != nil {
				markMediaSampleRetry(work, pathErr)
				break
			}
			remaining--
			_, cacheErr := virtual_file.CacheMediaFanart(context.Background(), mediaIdentity(work), sampleIndex, virtual_file.HTTPFileCacheRequest{
				URL: remoteURL, Headers: map[string]string{"Referer": work.SourceURL}, Client: d.client,
			})
			if cacheErr != nil {
				var statusErr *virtual_file.HTTPStatusError
				if errors.As(cacheErr, &statusErr) && statusErr.StatusCode == http.StatusForbidden {
					if sampleIndex > 1 {
						if err := completeMediaSamples(work, sampleIndex-1); err != nil {
							markMediaSampleRetry(work, err)
						}
					} else {
						markMediaSampleRetry(work, cacheErr)
					}
				} else {
					markMediaSampleRetry(work, cacheErr)
				}
				break
			}
			if err := db.UpdateMediaWorkSampleProgress(work.ID, sampleIndex, false); err != nil {
				utils.Log.Warnf("failed to update sample progress for %s: %s", work.Code, err)
				break
			}
			if sampleIndex == maxSampleImageCount {
				if err := completeMediaSamples(work, sampleIndex); err != nil {
					markMediaSampleRetry(work, err)
				}
				break
			}
		}
	}
}

func completeMediaSamples(work model.FilmWork, count int) error {
	identity := mediaIdentity(work)
	for index := 2; index <= count; index++ {
		if err := virtual_file.RecoverMediaFanartSwap(identity, 1, index); err != nil {
			return fmt.Errorf("recover fanart promotion at index %d: %w", index, err)
		}
	}
	for index := 2; index <= count; index++ {
		ready, err := virtual_file.PromoteLandscapeMediaFanart(identity, index)
		if err != nil {
			return fmt.Errorf("promote landscape fanart at index %d: %w", index, err)
		}
		if ready {
			break
		}
	}
	return db.UpdateMediaWorkSampleProgress(work.ID, count, true)
}

func markMediaSampleRetry(work model.FilmWork, cause error) {
	utils.Log.Warnf("failed to cache sample image for work %s: %s", work.Code, cause)
	if err := db.UpdateMediaWorkSampleScan(work.ID, false); err != nil {
		utils.Log.Warnf("failed to update sample retry for %s: %s", work.Code, err)
	}
}
