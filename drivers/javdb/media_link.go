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

func (d *Javdb) cloudPlayMedia(ctx context.Context, args model.LinkArgs, provider string, file model.FilmFileWithWork) (*model.Link, error) {
	return tool.CloudPlayMedia(ctx, args, tool.CloudPlayRequest{
		Provider: provider, DriverPath: d.CloudPlayDownloadPath, File: file, MagnetGetter: d.mediaMagnets,
	})
}

func (d *Javdb) mediaMagnets(_ context.Context, work model.FilmWork) ([]model.SourceMagnet, error) {
	meta, primaryErr := av.GetMetaFromJavdb(work.SourceURL)
	if primaryErr == nil && len(meta.Magnets) > 0 {
		return sourceMagnetsFromMeta(meta), nil
	}
	backup, backupErr := av.GetMetaFromSuke(work.Code)
	if backupErr != nil {
		return nil, errors.Join(primaryErr, backupErr)
	}
	if len(backup.Magnets) == 0 {
		return nil, fmt.Errorf("no source magnet found for %s", work.Code)
	}
	return sourceMagnetsFromMeta(backup), nil
}

func mediaFileFromObj(obj model.Obj) (model.FilmFileWithWork, error) {
	file, ok := obj.(*model.EmbyFileObj)
	if !ok || file.FilmFileID == 0 {
		return model.FilmFileWithWork{}, errors.New("media file identity is missing")
	}
	return db.GetFilmFileWithWork(file.FilmFileID)
}
