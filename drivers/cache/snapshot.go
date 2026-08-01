package cache

import (
	stdpath "path"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

func toCachedObj(dirPath string, obj model.Obj) model.CachedObj {
	c := model.CachedObj{
		ID:       obj.GetID(),
		Path:     stdpath.Join(dirPath, obj.GetName()),
		Name:     obj.GetName(),
		Size:     obj.GetSize(),
		Modified: obj.ModTime(),
		Ctime:    obj.CreateTime(),
		IsFolder: obj.IsDir(),
	}
	if hi := obj.GetHash().Export(); len(hi) > 0 {
		hashMap := make(map[string]string, len(hi))
		for ht, v := range hi {
			hashMap[ht.Name] = v
		}
		c.HashInfo = hashMap
	}
	if thumb, ok := model.GetThumb(obj); ok {
		c.Thumbnail = thumb
	}
	return c
}

func fromCachedObj(c model.CachedObj) model.Obj {
	obj := model.Object{
		ID:       c.ID,
		Path:     c.Path,
		Name:     c.Name,
		Size:     c.Size,
		Modified: c.Modified,
		Ctime:    c.Ctime,
		IsFolder: c.IsFolder,
	}
	if len(c.HashInfo) > 0 {
		hi := make(map[*utils.HashType]string, len(c.HashInfo))
		for name, v := range c.HashInfo {
			if ht, ok := utils.GetHashByName(name); ok {
				hi[ht] = v
			}
		}
		obj.HashInfo = utils.NewHashInfoByMap(hi)
	}
	if c.Thumbnail != "" {
		return &model.ObjThumb{
			Object:    obj,
			Thumbnail: model.Thumbnail{Thumbnail: c.Thumbnail},
		}
	}
	return &obj
}
