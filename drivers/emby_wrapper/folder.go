package emby_wrapper

import "github.com/OpenListTeam/OpenList/v4/internal/model"

type FolderAddition struct {
	Actors               string `json:"actors"`
	UseNameAsActor       *bool  `json:"use_name_as_actor"`
	Plot                 string `json:"plot"`
	AppendFileNameToPlot *bool  `json:"append_file_name_to_plot"`
	TvShow               *bool  `json:"tv_show"`
	TvShowName           string `json:"tv_show_name"`
	TvShowSubfolders     *bool  `json:"tv_show_subfolders"`
}

// wrappedObj 将下游对象包装进本驱动的路径命名空间（GetPath 返回本驱动相对路径）。
// 不实现 ObjUnwrap：解包会泄露下游真实路径（如本地驱动的文件系统路径），
// 导致 Rename/Link 的路径拼接错误。
// 缩略图在包装时提取存入（实现 model.Thumb），避免依赖 Unwrap 解包链。
type wrappedObj struct {
	model.Obj
	path  string
	thumb string
}

func (w *wrappedObj) GetPath() string {
	return w.path
}

func (w *wrappedObj) Thumb() string {
	return w.thumb
}

// embyFolder 文件夹对象：附带目录 emby 设置，供 UI 展示与重命名表单预填。
type embyFolder struct {
	model.Obj
	addition FolderAddition
	thumb    string
}

func (f *embyFolder) GetAddition() model.Additional {
	return f.addition
}

func (f *embyFolder) Thumb() string {
	return f.thumb
}

func wrapObj(obj model.Obj, path, actors, plot, tvShowName string, useNameAsActor bool, appendFileNameToPlot *bool, tvShow, tvShowSubfolders bool, folder bool) model.Obj {
	thumb, _ := model.GetThumb(obj)
	wrapped := &wrappedObj{Obj: obj, path: path, thumb: thumb}
	if !folder {
		return wrapped
	}
	use := useNameAsActor
	return &embyFolder{Obj: wrapped, thumb: thumb, addition: FolderAddition{
		Actors:               actors,
		UseNameAsActor:       &use,
		Plot:                 plot,
		AppendFileNameToPlot: appendFileNameToPlot,
		TvShow:               &tvShow,
		TvShowName:           tvShowName,
		TvShowSubfolders:     &tvShowSubfolders,
	}}
}

// virtualObj 虚拟对象：GetName 返回展示名（剧集文件名如 S02E01.mp4、季别名如 S02），
// GetPath 返回下游真实路径，Link 无需拦截即可转发真实文件/目录。
// 不实现 ObjUnwrap：解包会泄露下游真实路径。
// 缩略图在包装时提取存入（实现 model.Thumb）。
type virtualObj struct {
	model.Obj
	name  string
	path  string
	thumb string
}

func (v *virtualObj) GetName() string { return v.name }
func (v *virtualObj) GetPath() string { return v.path }

func (v *virtualObj) Thumb() string { return v.thumb }

func newVirtualObj(real model.Obj, name, path string) model.Obj {
	thumb, _ := model.GetThumb(real)
	return &virtualObj{Obj: real, name: name, path: path, thumb: thumb}
}
