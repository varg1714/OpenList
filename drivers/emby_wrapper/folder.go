package emby_wrapper

import "github.com/OpenListTeam/OpenList/v4/internal/model"

type FolderAddition struct {
	Actors string `json:"actors"`
}

type embyFolder struct {
	model.Obj
	addition FolderAddition
}

func (f *embyFolder) GetAddition() model.Additional {
	return f.addition
}

func (f *embyFolder) Unwrap() model.Obj {
	return f.Obj
}

func wrapFolder(obj model.Obj, actors string) model.Obj {
	if obj == nil || !obj.IsDir() {
		return obj
	}
	return &embyFolder{Obj: obj, addition: FolderAddition{Actors: actors}}
}
