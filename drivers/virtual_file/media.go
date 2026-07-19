package virtual_file

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"gorm.io/gorm"
)

func ListMediaFiles(storageID uint, source, primaryDir string) ([]model.EmbyFileObj, error) {
	rows, err := db.ListFilmFilesWithWorks(storageID, source, primaryDir)
	if err != nil {
		return nil, err
	}
	result := make([]model.EmbyFileObj, 0)
	for _, row := range rows {
		projected, err := ConvertMediaFileToEmbyFile(row)
		if err != nil {
			return nil, err
		}
		result = append(result, projected)
	}
	return result, nil
}

func DeleteMediaFile(fileID uint) error {
	return db.DeleteMediaFile(fileID)
}

func ResolveMediaObj(storageID uint, source, primaryDir, groupName, fileName string) (model.Obj, error) {
	works, err := db.ListFilmWorks(storageID, source, primaryDir)
	if err != nil {
		return nil, err
	}
	for _, work := range works {
		if work.Code != groupName {
			continue
		}
		files, err := db.ListFilmFiles(work.ID)
		if err != nil {
			return nil, err
		}
		projected := make([]model.EmbyFileObj, 0, len(files))
		for _, file := range files {
			item, err := ConvertMediaFileToEmbyFile(model.FilmFileWithWork{FilmFile: file, Work: work})
			if err != nil {
				return nil, err
			}
			projected = append(projected, item)
			if fileName != "" && item.Name == fileName {
				return &projected[len(projected)-1], nil
			}
		}
		if fileName == "" && len(projected) > 0 {
			wrapped := WrapMediaFiles(projected)
			return &wrapped[0], nil
		}
		break
	}
	return nil, gorm.ErrRecordNotFound
}

func DeleteMediaWork(workID uint) error {
	work, err := db.GetFilmWork(workID)
	if err != nil {
		return err
	}
	if err := db.DeleteFilmWork(workID); err != nil {
		return err
	}
	identity := MediaIdentity{StorageID: work.StorageID, Source: work.Source, PrimaryDir: work.PrimaryDir, Code: work.Code}
	if err := DeleteMediaArtifacts(identity); err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	return nil
}

func ResolveMediaActorTreeObj(storageID uint, source, path, rootID string, modified time.Time) (model.Obj, error) {
	if path == "" || path == "/" {
		return &model.Object{ID: rootID, Name: "root", Modified: modified, IsFolder: true}, nil
	}
	parts := strings.SplitN(strings.Trim(path, "/"), "/", 2)
	if parts[0] == "关注演员" {
		if len(parts) == 1 {
			return mediaDirectory("关注演员", modified), nil
		}
		parts = strings.SplitN(parts[1], "/", 2)
	}
	primaryDir := parts[0]
	if primaryDir != "个人收藏" {
		actors := db.QueryActor(strconv.FormatUint(uint64(storageID), 10))
		found := false
		for _, actor := range actors {
			if actor.Name == primaryDir {
				found = true
				modified = actor.UpdatedAt
				break
			}
		}
		if !found {
			return nil, errs.ObjectNotFound
		}
	}
	if len(parts) == 1 {
		return mediaDirectory(primaryDir, modified), nil
	}
	groupName, fileName := SplitFilmPath(parts[1])
	obj, err := ResolveMediaObj(storageID, source, primaryDir, groupName, fileName)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errs.ObjectNotFound
	}
	return obj, err
}

func mediaDirectory(name string, modified time.Time) model.Obj {
	return &model.ObjThumb{Object: model.Object{Name: name, ID: name, IsFolder: true, Size: 622857143, Modified: modified}}
}

func ConvertMediaFileToEmbyFile(item model.FilmFileWithWork) (model.EmbyFileObj, error) {
	file := item.FilmFile
	work := item.Work
	name, err := model.BuildMediaFileName(work.Code, file.PartIndex, file.PartCount)
	if err != nil {
		return model.EmbyFileObj{}, err
	}

	return model.EmbyFileObj{
		ObjThumb: model.ObjThumb{
			Object: model.Object{
				ID:       strconv.FormatUint(uint64(file.ID), 10),
				Path:     work.PrimaryDir,
				Name:     name,
				Size:     file.SourceSize,
				Modified: file.UpdatedAt,
				Ctime:    file.CreatedAt,
			},
			Thumbnail: model.Thumbnail{Thumbnail: work.ImageURL},
		},
		WorkID:      file.WorkID,
		FilmFileID:  file.ID,
		Code:        work.Code,
		PartIndex:   file.PartIndex,
		PartCount:   file.PartCount,
		SourceRef:   work.SourceRef,
		SourceURL:   work.SourceURL,
		Url:         work.SourceURL,
		Title:       model.BuildMediaTitle(work.Code, work.RawTitle, work.TranslatedTitle),
		Synopsis:    work.Synopsis,
		Actors:      []string(work.Actors),
		ReleaseTime: work.ReleaseDate,
		Translated:  strings.TrimSpace(work.TranslatedTitle) != "",
		Tags:        []string(work.Tags),
	}, nil
}

func WrapMediaFiles(files []model.EmbyFileObj) []model.EmbyFileDirWrapper {
	fileGroups := make(map[uint][]model.EmbyFileObj)
	for _, file := range files {
		fileGroups[file.WorkID] = append(fileGroups[file.WorkID], file)
	}

	result := make([]model.EmbyFileDirWrapper, 0, len(fileGroups))
	for workID, group := range fileGroups {
		firstFile := group[0]
		result = append(result, model.EmbyFileDirWrapper{
			EmbyFiles: group,
			ObjThumb: model.ObjThumb{
				Object: model.Object{
					IsFolder: true,
					Name:     firstFile.Code,
					ID:       strconv.FormatUint(uint64(workID), 10),
					Ctime:    firstFile.Ctime,
					Modified: firstFile.Modified,
				},
				Thumbnail: firstFile.Thumbnail,
			},
		})
	}
	return result
}
