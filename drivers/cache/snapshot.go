package cache

import (
	stdpath "path"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

type CachedObj struct {
	ID        string
	Path      string
	Name      string
	Size      int64
	Modified  time.Time
	Ctime     time.Time
	IsFolder  bool
	HashInfo  utils.HashInfo
	Thumbnail string
}

func toCachedObj(dirPath string, obj model.Obj) CachedObj {
	c := CachedObj{
		ID:       obj.GetID(),
		Path:     stdpath.Join(dirPath, obj.GetName()),
		Name:     obj.GetName(),
		Size:     obj.GetSize(),
		Modified: obj.ModTime(),
		Ctime:    obj.CreateTime(),
		IsFolder: obj.IsDir(),
		HashInfo: obj.GetHash(),
	}
	if thumb, ok := model.GetThumb(obj); ok {
		c.Thumbnail = thumb
	}
	return c
}

func fromCachedObj(c CachedObj) model.Obj {
	obj := model.Object{
		ID:       c.ID,
		Path:     c.Path,
		Name:     c.Name,
		Size:     c.Size,
		Modified: c.Modified,
		Ctime:    c.Ctime,
		IsFolder: c.IsFolder,
		HashInfo: c.HashInfo,
	}
	if c.Thumbnail != "" {
		return &model.ObjThumb{
			Object:    obj,
			Thumbnail: model.Thumbnail{Thumbnail: c.Thumbnail},
		}
	}
	return &obj
}

func marshalObjs(snaps []CachedObj) (string, error) {
	data, err := utils.Json.Marshal(snaps)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func unmarshalObjs(data string) ([]model.Obj, error) {
	var snaps []CachedObj
	if err := utils.Json.Unmarshal([]byte(data), &snaps); err != nil {
		return nil, err
	}
	objs := make([]model.Obj, 0, len(snaps))
	for i := range snaps {
		objs = append(objs, fromCachedObj(snaps[i]))
	}
	return objs, nil
}
