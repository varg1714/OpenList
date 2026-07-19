package fc2

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/av"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/offline_download/tool"
)

func (d *FC2) cloudPlayMedia(ctx context.Context, args model.LinkArgs, file model.FilmFileWithWork) (*model.Link, error) {
	fileName, err := model.BuildMediaFileName(file.Work.Code, file.PartIndex, file.PartCount)
	if err != nil {
		return nil, err
	}
	return tool.CloudPlay(ctx, args, d.CloudPlayDriverType, d.CloudPlayDownloadPath, &model.EmbyFileObj{
		ObjThumb: model.ObjThumb{Object: model.Object{
			Name:     fileName,
			Path:     file.Work.PrimaryDir,
			IsFolder: false,
		}},
		WorkID:     file.WorkID,
		FilmFileID: file.ID,
	}, func(_ model.Obj) (string, error) {
		magnets, err := d.mediaMagnets(ctx, file.Work)
		if err != nil {
			return "", err
		}
		if len(magnets) == 0 {
			return "", fmt.Errorf("no source magnet found for %s", file.Work.Code)
		}
		return magnets[0].MagnetURI, nil
	})
}

func (d *FC2) mediaMagnets(_ context.Context, work model.FilmWork) ([]model.SourceMagnet, error) {
	meta, err := av.GetMetaFromSuke(work.Code)
	if err != nil {
		return nil, err
	}
	if len(meta.Magnets) == 0 {
		return nil, fmt.Errorf("no source magnet found for %s", work.Code)
	}
	now := time.Now()
	result := make([]model.SourceMagnet, 0, len(meta.Magnets))
	for index, magnet := range meta.Magnets {
		uri := magnet.GetMagnet()
		if uri == "" {
			continue
		}
		sum := sha256.Sum256([]byte(uri))
		result = append(result, model.SourceMagnet{
			MagnetURI: uri, Fingerprint: hex.EncodeToString(sum[:]), Provider: "fc2", Priority: index,
			Selected: index == 0, Subtitle: magnet.IsSubTitle(), ScanAt: &now,
		})
	}
	return result, nil
}

func mediaFileFromObj(obj model.Obj) (model.FilmFileWithWork, error) {
	file, ok := obj.(*model.EmbyFileObj)
	if !ok || file.FilmFileID == 0 {
		return model.FilmFileWithWork{}, errors.New("media file identity is missing")
	}
	return db.GetFilmFileWithWork(file.FilmFileID)
}
