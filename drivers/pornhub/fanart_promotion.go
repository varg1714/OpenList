package pornhub

import (
	"context"
	"fmt"

	"github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

var promoteLandscapeFanartCandidate = virtual_file.PromoteLandscapeFanart

func (d *Pornhub) finalizeFanart(ctx context.Context, film *model.Film, count int) {
	if _, err := d.promoteLandscapeFanart(film, count); err != nil {
		d.updateSampleImageScanAt(ctx, film, err)
		return
	}
	if err := db.UpdateSampleImageProgress(film.ID, count, true); err != nil {
		d.updateSampleImageScanAt(ctx, film, err)
		return
	}
	film.SampleImageCount = count
	film.SampleImageComplete = true
	d.updateFanartAudit(film)
}

func (d *Pornhub) promoteLandscapeFanart(film *model.Film, count int) (bool, error) {
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
