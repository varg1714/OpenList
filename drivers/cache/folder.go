package cache

import "github.com/OpenListTeam/OpenList/v4/internal/model"

type FolderAddition struct {
	TTLHours int `json:"ttl_hours"`
}

type cachedFolder struct {
	model.Obj
	addition FolderAddition
}

func (c *cachedFolder) GetAddition() model.Additional {
	return c.addition
}

func (c *cachedFolder) Unwrap() model.Obj {
	return c.Obj
}

func wrapFolder(obj model.Obj, ttlHours int) model.Obj {
	if obj == nil || !obj.IsDir() {
		return obj
	}
	return &cachedFolder{Obj: obj, addition: FolderAddition{TTLHours: ttlHours}}
}
