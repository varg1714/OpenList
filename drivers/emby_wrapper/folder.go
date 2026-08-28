package emby_wrapper

import "github.com/OpenListTeam/OpenList/v4/internal/model"

type FolderAddition struct {
	Actors string `json:"actors"`
}

// wrappedObj 将下游对象包装进本驱动的路径命名空间（GetPath 返回本驱动相对路径）。
// 不实现 ObjUnwrap：解包会泄露下游真实路径（如本地驱动的文件系统路径），
// 导致 Rename/Link 的路径拼接错误。
type wrappedObj struct {
	model.Obj
	path string
}

func (w *wrappedObj) GetPath() string {
	return w.path
}

// embyFolder 文件夹对象：附带目录 emby 设置，供 UI 展示。
type embyFolder struct {
	model.Obj
	addition FolderAddition
}

func (f *embyFolder) GetAddition() model.Additional {
	return f.addition
}

func wrapObj(obj model.Obj, path, actors string, folder bool) model.Obj {
	wrapped := &wrappedObj{Obj: obj, path: path}
	if !folder {
		return wrapped
	}
	return &embyFolder{Obj: wrapped, addition: FolderAddition{Actors: actors}}
}
