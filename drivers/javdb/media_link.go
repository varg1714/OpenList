package javdb

import (
	"context"
	"errors"
	"fmt"

	"github.com/OpenListTeam/OpenList/v4/internal/av"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/offline_download/tool"
)

var (
	getJavdbMeta     = av.GetMetaFromJavdb
	getJavdbSukeMeta = av.GetMetaFromSuke
)

func (d *Javdb) cloudPlayMedia(ctx context.Context, args model.LinkArgs, provider string, file model.FilmFileWithWork) (*model.Link, error) {
	fileName, err := model.BuildMediaFileName(file.Work.Code, file.PartIndex, file.PartCount)
	if err != nil {
		return nil, err
	}
	magnets, err := d.playbackMagnets(ctx, file.Work)
	if err != nil {
		return nil, err
	}
	return tool.CloudPlay(ctx, args, provider, d.CloudPlayDownloadPath, &model.EmbyFileObj{
		ObjThumb: model.ObjThumb{Object: model.Object{
			Name:     fileName,
			Path:     file.Work.PrimaryDir,
			IsFolder: false,
		}},
		WorkID:     file.WorkID,
		FilmFileID: file.ID,
	}, magnets)
}

func (d *Javdb) mediaMagnets(_ context.Context, work model.FilmWork) ([]model.SourceMagnet, error) {
	meta, primaryErr := getJavdbMeta(work.SourceURL)
	if primaryErr == nil && len(meta.Magnets) > 0 {
		return sourceMagnetsFromMeta(meta), nil
	}
	backup, backupErr := getJavdbSukeMeta(work.Code)
	if backupErr != nil {
		return nil, errors.Join(primaryErr, backupErr)
	}
	if len(backup.Magnets) == 0 {
		return nil, fmt.Errorf("no source magnet found for %s", work.Code)
	}
	return sourceMagnetsFromMeta(backup), nil
}

func (d *Javdb) playbackMagnets(ctx context.Context, work model.FilmWork) ([]model.SourceMagnet, error) {
	magnets, err := db.ListPlaybackSourceMagnets(work.ID)
	if err != nil {
		return nil, fmt.Errorf("list persisted JavDB magnets: %w", err)
	}
	if len(magnets) > 0 {
		return magnets, nil
	}

	discovered, err := d.mediaMagnets(ctx, work)
	if err != nil {
		return nil, err
	}
	if err := db.UpsertSourceMagnets(work.ID, discovered); err != nil {
		return nil, fmt.Errorf("persist JavDB magnets: %w", err)
	}
	magnets, err = db.ListPlaybackSourceMagnets(work.ID)
	if err != nil {
		return nil, fmt.Errorf("reload JavDB magnets: %w", err)
	}
	return magnets, nil
}

func mediaFileFromObj(obj model.Obj) (model.FilmFileWithWork, error) {
	file, ok := obj.(*model.EmbyFileObj)
	if !ok || file.FilmFileID == 0 {
		return model.FilmFileWithWork{}, errors.New("media file identity is missing")
	}
	return db.GetFilmFileWithWork(file.FilmFileID)
}
