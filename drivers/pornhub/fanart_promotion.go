package pornhub

import (
	"context"
	"fmt"

	"github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

var promoteLandscapeFanartCandidate = virtual_file.PromoteLandscapeMediaFanart

func (d *Pornhub) finalizeFanart(ctx context.Context, work *model.FilmWork, count int) {
	if _, err := d.promoteLandscapeFanart(work, count); err != nil {
		d.updateSampleImageScanAt(ctx, work, err)
		return
	}
	if err := db.UpdateMediaWorkSampleProgress(work.ID, count, true); err != nil {
		d.updateSampleImageScanAt(ctx, work, err)
		return
	}
	work.SampleImageCount = count
	work.SampleImageComplete = true
}

func (d *Pornhub) promoteLandscapeFanart(work *model.FilmWork, count int) (bool, error) {
	if count < 2 {
		return false, nil
	}
	identity := pornhubMediaIdentity(work)
	for index := 2; index <= count; index++ {
		if err := virtual_file.RecoverMediaFanartSwap(identity, 1, index); err != nil {
			return false, fmt.Errorf("recover fanart promotion at index %d: %w", index, err)
		}
	}
	for index := 2; index <= count; index++ {
		landscapeReady, err := promoteLandscapeFanartCandidate(identity, index)
		if err != nil {
			return false, fmt.Errorf("promote landscape fanart at index %d: %w", index, err)
		}
		if landscapeReady {
			return true, nil
		}
	}
	return false, nil
}
