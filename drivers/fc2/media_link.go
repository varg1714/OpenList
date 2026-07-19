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
	return tool.CloudPlayMedia(ctx, args, tool.CloudPlayRequest{
		Provider: d.CloudPlayDriverType, DriverPath: d.CloudPlayDownloadPath, File: file, MagnetGetter: d.mediaMagnets,
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
		manifest := make(model.MagnetFileManifest, 0, len(magnet.GetFiles()))
		for _, sourceFile := range magnet.GetFiles() {
			manifest = append(manifest, model.MagnetFileEntry{Path: sourceFile.Name, Size: int64(sourceFile.Size)})
		}
		result = append(result, model.SourceMagnet{
			MagnetURI: uri, Fingerprint: hex.EncodeToString(sum[:]), Provider: "fc2", Priority: index,
			Selected: index == 0, Subtitle: magnet.IsSubTitle(), FileManifest: manifest, ScanAt: &now,
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
