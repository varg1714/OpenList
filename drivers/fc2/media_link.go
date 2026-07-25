package fc2

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/av"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/offline_download/tool"
)

var getFC2SukeMeta = av.GetMetaFromSuke

func (d *FC2) cloudPlayMedia(ctx context.Context, args model.LinkArgs, file model.FilmFileWithWork) (*model.Link, error) {
	fileName, err := model.BuildMediaFileName(file.Work.Code, file.PartIndex, file.PartCount)
	if err != nil {
		return nil, err
	}
	magnets, err := d.playbackMagnets(ctx, file.Work)
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
	}, magnets)
}

func (d *FC2) mediaMagnets(_ context.Context, work model.FilmWork) ([]model.SourceMagnet, error) {
	meta, err := getFC2SukeMeta(work.Code)
	if err != nil {
		return nil, err
	}
	if len(meta.Magnets) == 0 {
		return nil, fmt.Errorf("no source magnet found for %s", work.Code)
	}
	now := time.Now()
	result := make([]model.SourceMagnet, 0, len(meta.Magnets))
	var selectedFiles []av.File
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
		if len(result) == 1 {
			selectedFiles = append([]av.File(nil), magnet.GetFiles()...)
		}
	}
	if work.ID != 0 && len(selectedFiles) > 0 {
		selectedFiles = slices.DeleteFunc(selectedFiles, func(file av.File) bool {
			return file.Size <= 100*1024*1024
		})
		slices.SortFunc(selectedFiles, func(left, right av.File) int {
			return cmp.Compare(left.Name, right.Name)
		})
		files := make([]model.FilmFile, len(selectedFiles))
		for index, file := range selectedFiles {
			if file.Size > math.MaxInt64 {
				return nil, fmt.Errorf("source file %q size exceeds int64", file.Name)
			}
			files[index] = model.FilmFile{
				PartIndex: index + 1, PartCount: len(selectedFiles),
				SourcePath: file.Name, SourceSize: int64(file.Size),
			}
		}
		if len(files) > 0 {
			if err := db.ReplaceFilmFiles(work.ID, files); err != nil {
				return nil, fmt.Errorf("replace FC2 file topology: %w", err)
			}
		}
	}
	return result, nil
}

func (d *FC2) playbackMagnets(ctx context.Context, work model.FilmWork) ([]model.SourceMagnet, error) {
	magnets, err := db.ListPlaybackSourceMagnets(work.ID)
	if err != nil {
		return nil, fmt.Errorf("list persisted FC2 magnets: %w", err)
	}
	if len(magnets) > 0 {
		return magnets, nil
	}

	discovered, err := d.mediaMagnets(ctx, work)
	if err != nil {
		return nil, err
	}
	if err := db.UpsertSourceMagnets(work.ID, discovered); err != nil {
		return nil, fmt.Errorf("persist FC2 magnets: %w", err)
	}
	magnets, err = db.ListPlaybackSourceMagnets(work.ID)
	if err != nil {
		return nil, fmt.Errorf("reload FC2 magnets: %w", err)
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
