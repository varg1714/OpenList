package emby_wrapper

import "github.com/OpenListTeam/OpenList/v4/internal/model"

type FolderAddition struct {
	Actors               string `json:"actors"`
	UseNameAsActor       *bool  `json:"use_name_as_actor"`
	Plot                 string `json:"plot"`
	AppendFileNameToPlot *bool  `json:"append_file_name_to_plot"`
	TvShow               *bool  `json:"tv_show"`
	TvShowName           string `json:"tv_show_name"`
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

// embyFolder 文件夹对象：附带目录 emby 设置，供 UI 展示与重命名表单预填。
type embyFolder struct {
	model.Obj
	addition FolderAddition
}

func (f *embyFolder) GetAddition() model.Additional {
	return f.addition
}

func wrapObj(obj model.Obj, path, actors, plot, tvShowName string, useNameAsActor bool, appendFileNameToPlot *bool, tvShow bool, folder bool) model.Obj {
	wrapped := &wrappedObj{Obj: obj, path: path}
	if !folder {
		return wrapped
	}
	use := useNameAsActor
	return &embyFolder{Obj: wrapped, addition: FolderAddition{
		Actors:               actors,
		UseNameAsActor:       &use,
		Plot:                 plot,
		AppendFileNameToPlot: appendFileNameToPlot,
		TvShow:               &tvShow,
		TvShowName:           tvShowName,
	}}
}

// virtualEpisode 虚拟剧集对象：GetName 返回虚拟名（如 A-S02E01.mp4），
// GetPath 返回下游真实路径，Link 无需拦截即可转发真实文件。
// 不实现 ObjUnwrap：解包会泄露下游真实路径。
type virtualEpisode struct {
	model.Obj
	name string
	path string
}

func (v *virtualEpisode) GetName() string { return v.name }
func (v *virtualEpisode) GetPath() string { return v.path }

func newVirtualEpisode(real model.Obj, name, path string) model.Obj {
	return &virtualEpisode{Obj: real, name: name, path: path}
}
