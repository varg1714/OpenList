package emby_wrapper

import (
	"bytes"
	"context"
	stdpath "path"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/internal/stream"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/pkg/errors"
)

type EmbyWrapper struct {
	model.Storage
	Addition
	supportSuffix map[string]struct{}
}

func (d *EmbyWrapper) Config() driver.Config {
	cfg := config
	if remote, _, err := op.GetStorageAndActualPath(d.RemotePath); err == nil {
		rc := remote.Config()
		cfg.OnlyProxy = rc.OnlyProxy
		cfg.NoLinkURL = rc.NoLinkURL
	}
	return cfg
}

func (d *EmbyWrapper) GetAddition() driver.Additional {
	return &d.Addition
}

func (d *EmbyWrapper) Init(ctx context.Context) error {
	if strings.TrimSpace(d.RemotePath) == "" {
		return errors.New("remote path must not be empty")
	}
	d.RemotePath = utils.FixAndCleanPath(d.RemotePath)
	d.syncProxy()
	if strings.TrimSpace(d.FilterFileTypes) == "" {
		d.FilterFileTypes = "mp4,mkv,flv,avi,wmv,ts,rmvb,webm,mp3,flac,aac,wav,ogg,m4a,wma,alac"
	}
	d.supportSuffix = map[string]struct{}{}
	for _, ext := range strings.Split(d.FilterFileTypes, ",") {
		ext = strings.ToLower(strings.TrimSpace(ext))
		if ext != "" {
			d.supportSuffix[ext] = struct{}{}
		}
	}
	return nil
}

func (d *EmbyWrapper) Drop(ctx context.Context) error {
	return nil
}

// 继承下游代理配置，理由同 cache 驱动（转发驱动必须同步，
// 否则 HTTP/WebDAV 的代理判定读取请求命中存储的字段时会丢失下游配置）。
func (d *EmbyWrapper) syncProxy() {
	if storage, _, err := op.GetStorageAndActualPath(d.RemotePath); err == nil {
		rs := storage.GetStorage()
		d.Storage.WebProxy = rs.WebProxy
		d.Storage.WebdavPolicy = rs.WebdavPolicy
		d.Storage.ProxyRange = rs.ProxyRange
		d.Storage.DownProxyURL = rs.DownProxyURL
		d.Storage.DisableProxySign = rs.DisableProxySign
	}
}

func (d *EmbyWrapper) remote() (driver.Driver, string, error) {
	storage, actualPath, err := op.GetStorageAndActualPath(d.RemotePath)
	if err == nil {
		d.syncProxy()
	}
	return storage, actualPath, err
}

func (d *EmbyWrapper) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {
	remoteStorage, remoteActualPath, err := d.remote()
	if err != nil {
		return nil, err
	}
	objs, err := op.List(ctx, remoteStorage, stdpath.Join(remoteActualPath, dir.GetPath()), args)
	if err != nil {
		return nil, err
	}
	objs = d.decorate(dir.GetPath(), objs)
	if rootPath, showName, ok, err := d.tvShowAncestor(dir.GetPath()); err != nil {
		utils.Log.Warnf("emby wrapper: tv show ancestor %s: %+v", dir.GetPath(), err)
	} else if ok {
		return d.withTVShowNFOs(ctx, dir, rootPath, showName, objs), nil
	}
	return d.withVirtualNFOs(dir.GetPath(), objs), nil
}

func (d *EmbyWrapper) Get(ctx context.Context, path string) (model.Obj, error) {
	if utils.PathEqual(path, "/") {
		return &model.Object{Name: "Root", IsFolder: true, Path: "/"}, nil
	}
	if strings.HasSuffix(strings.ToLower(path), ".nfo") {
		if obj, ok, err := d.virtualNFOForPath(ctx, path); err != nil {
			return nil, err
		} else if ok {
			return obj, nil
		}
	}
	remoteStorage, remoteActualPath, err := d.remote()
	if err != nil {
		return nil, err
	}
	obj, err := op.Get(ctx, remoteStorage, stdpath.Join(remoteActualPath, path))
	if err != nil {
		if ep, ok, e2 := d.resolveEpisodePath(ctx, path); e2 != nil {
			return nil, e2
		} else if ok {
			return ep, nil
		}
		return nil, err
	}
	actors, use, plot, tvShowName, tvShow := "", false, "", "", false
	var appendFlag *bool
	if obj.IsDir() {
		if item, e := GetEmbyDirSetting(d.ID, path); e != nil {
			utils.Log.Warnf("emby wrapper: get dir setting %s: %+v", path, e)
		} else if item != nil {
			actors, use, plot = item.Actors, item.UseNameAsActor, item.Plot
			appendFlag = item.AppendFileNameToPlot
			tvShowName, tvShow = item.TvShowName, item.TvShow
		}
	}
	return wrapObj(obj, path, actors, plot, tvShowName, use, appendFlag, tvShow, obj.IsDir()), nil
}

// virtualNFOForPath 尝试为 .nfo 路径构建虚拟对象。
// 返回 (obj, true, nil)：命中虚拟 nfo；(nil, false, nil)：应转发下游（无设置/无匹配影片/存在真实 nfo）。
func (d *EmbyWrapper) virtualNFOForPath(ctx context.Context, path string) (model.Obj, bool, error) {
	parentDir := stdpath.Dir(path)
	base := strings.TrimSuffix(stdpath.Base(path), ".nfo")
	rootPath, showName, isTV, err := d.tvShowAncestor(parentDir)
	if err != nil {
		return nil, false, err
	}
	setting, err := d.resolveSetting(parentDir)
	if err != nil {
		return nil, false, err
	}
	if setting == nil && !isTV {
		return nil, false, nil
	}
	if setting == nil {
		setting = &model.EmbyDirSetting{}
	}
	remoteStorage, remoteActualPath, err := d.remote()
	if err != nil {
		return nil, false, err
	}
	objs, err := op.List(ctx, remoteStorage, stdpath.Join(remoteActualPath, parentDir), model.ListArgs{})
	if err != nil {
		return nil, false, err
	}
	// 真实 nfo 优先：下游存在同名真实 nfo 时交给下游 Get（含 tvshow.nfo/season.nfo）
	for _, o := range objs {
		if o.IsDir() {
			continue
		}
		if strings.EqualFold(utils.Ext(o.GetName()), "nfo") && strings.EqualFold(nfoBaseName(o.GetName()), base) {
			return nil, false, nil
		}
	}
	// TV 模式分支
	if isTV {
		idx, err := d.buildTVIndex(ctx, rootPath)
		if err != nil {
			return nil, false, err
		}
		// tvshow.nfo：仅剧集根目录
		if utils.PathEqual(parentDir, rootPath) && strings.EqualFold(base, "tvshow") {
			content, err := buildTVShowNFO(showName, setting.Plot, setting)
			if err != nil {
				return nil, false, err
			}
			modified := time.Time{}
			if idx.last != nil {
				modified = idx.last.ModTime()
			}
			return d.newVirtualNFO(path, content, modified), true, nil
		}
		// season.nfo：直接子文件夹（季）
		if !utils.PathEqual(parentDir, rootPath) && utils.PathEqual(stdpath.Dir(parentDir), rootPath) && strings.EqualFold(base, "season") {
			if seasonNo, ok := idx.seasonNo[parentDir]; ok {
				modified := time.Time{}
				if idx.last != nil {
					modified = idx.last.ModTime()
				}
				return d.newVirtualNFO(path, buildSeasonNFO(seasonNo, stdpath.Base(parentDir)), modified), true, nil
			}
		}
		// 剧集 nfo：虚拟名匹配
		epName, ok := idx.nfoBases[strings.ToLower(base)]
		if !ok {
			return nil, false, nil
		}
		real := idx.resolve(epName)
		content, err := buildEpisodeNFO(idx.titles[strings.ToLower(epName)], setting)
		if err != nil {
			return nil, false, err
		}
		return d.newVirtualNFO(path, content, real.ModTime()), true, nil
	}
	// 影片模式（原有逻辑）：basename 匹配真实影片文件
	var movieObj model.Obj
	for _, o := range objs {
		if o.IsDir() {
			continue
		}
		if nfoBaseName(o.GetName()) == base {
			if _, ok := d.supportSuffix[utils.Ext(o.GetName())]; ok {
				movieObj = o
			}
		}
	}
	if movieObj == nil {
		return nil, false, nil
	}
	content, err := buildNFOContent(base, movieObj.GetName(), setting)
	if err != nil {
		return nil, false, err
	}
	return d.newVirtualNFO(path, content, movieObj.ModTime()), true, nil
}

// resolveEpisodePath 在父目录处于某部电视剧内时按虚拟名反查真实文件。
// 返回 (包装对象, true, nil)：命中虚拟剧集；(nil, false, nil)：非 TV 树或未命中。
func (d *EmbyWrapper) resolveEpisodePath(ctx context.Context, path string) (model.Obj, bool, error) {
	parentDir := stdpath.Dir(path)
	rootPath, _, ok, err := d.tvShowAncestor(parentDir)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, nil
	}
	idx, err := d.buildTVIndex(ctx, rootPath)
	if err != nil {
		return nil, false, err
	}
	real := idx.resolve(stdpath.Base(path))
	if real == nil {
		return nil, false, nil
	}
	return newVirtualEpisode(real, stdpath.Base(path), stdpath.Join(parentDir, real.GetName())), true, nil
}

// newVirtualNFO 构造虚拟 nfo 对象。
func (d *EmbyWrapper) newVirtualNFO(path string, content []byte, modified time.Time) model.Obj {
	return &virtualNFO{
		Object: model.Object{
			Name:     stdpath.Base(path),
			Size:     int64(len(content)),
			Modified: modified,
			Path:     path,
			ID:       "vnfo-" + stdpath.Base(path),
		},
		content: content,
	}
}

func (d *EmbyWrapper) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
	if nfo, ok := file.(*virtualNFO); ok {
		return &model.Link{
			RangeReader:   stream.GetRangeReaderFromMFile(int64(len(nfo.content)), bytes.NewReader(nfo.content)),
			ContentLength: int64(len(nfo.content)),
		}, nil
	}
	remoteStorage, remoteActualPath, err := d.remote()
	if err != nil {
		return nil, err
	}
	l, _, err := op.Link(ctx, remoteStorage, stdpath.Join(remoteActualPath, file.GetPath()), args)
	if err != nil {
		return nil, err
	}
	resultLink := *l
	resultLink.SyncClosers = utils.NewSyncClosers(l)
	return &resultLink, nil
}

// decorate 将下游对象包装进本驱动路径命名空间，并给文件夹附带目录设置（用于 UI 展示与表单预填）。
func (d *EmbyWrapper) decorate(dirPath string, objs []model.Obj) []model.Obj {
	settings, err := ListEmbyDirSettings(d.ID)
	if err != nil {
		utils.Log.Warnf("emby wrapper: list dir settings: %+v", err)
		settings = map[string]model.EmbyDirSetting{}
	}
	out := make([]model.Obj, len(objs))
	for i, o := range objs {
		p := stdpath.Join(dirPath, o.GetName())
		s, ok := settings[p]
		actors, use, plot, tvShowName, tvShow := "", false, "", "", false
		var appendFlag *bool
		if ok {
			actors, use, plot = s.Actors, s.UseNameAsActor, s.Plot
			appendFlag = s.AppendFileNameToPlot
			tvShowName, tvShow = s.TvShowName, s.TvShow
		}
		out[i] = wrapObj(o, p, actors, plot, tvShowName, use, appendFlag, tvShow, o.IsDir())
	}
	return out
}

func (d *EmbyWrapper) MkdirConfig() []driver.Item {
	return []driver.Item{
		{
			Name:    "actors",
			Type:    conf.TypeString,
			Default: "",
			Help:    "演员列表，逗号分隔；仅对文件夹修改生效，设置后该文件夹及子文件夹内的影片会生成对应的虚拟 nfo 文件（内存构建，配合 strm 驱动落盘；strm 的 DownloadFileTypes 需包含 nfo）",
		},
		{
			Name:    "use_name_as_actor",
			Type:    conf.TypeBool,
			Default: "false",
			Help:    "开启后该文件夹的直接子文件夹以各自名称为 actor（后代继承），手动设置的 actors 优先；仅反映本目录自身状态，子目录的继承状态不在此显示",
		},
		{
			Name:    "plot",
			Type:    conf.TypeString,
			Default: "",
			Help:    "影片标题与简介；设置后该文件夹及子文件夹内的影片 nfo 的 title 和 plot 均使用该值（append 开启时追加文件名），分维度独立继承，不影响 actors",
		},
		{
			Name:    "append_file_name_to_plot",
			Type:    conf.TypeBool,
			Default: "false",
			Help:    "将去扩展名的影片文件名追加到 plot（格式：plot-文件名；plot 未设置时直接以文件名为 plot）",
		},
		{
			Name:    "tv_show",
			Type:    conf.TypeBool,
			Default: "false",
			Help:    "标记该文件夹为电视剧：根目录直接文件为第 1 季，直接子文件夹按创建时间+名称排序分配季号（保留原名并生成 season.nfo 供 Emby 识别）；季内文件按创建时间编号为 原基础名-S{季}E{集}.mp4；生成剧集 nfo（保留演员、无简介）与 tvshow.nfo（剧名/简介）；本地生效不继承",
		},
		{
			Name:    "tv_show_name",
			Type:    conf.TypeString,
			Default: "",
			Help:    "电视剧名称，写入 tvshow.nfo 的 title；为空时使用文件夹名",
		},
	}
}

func (d *EmbyWrapper) Rename(ctx context.Context, srcObj model.Obj, newName string) error {
	if !srcObj.IsDir() {
		return errors.New("emby wrapper driver does not support renaming files")
	}
	var req FolderAddition
	if err := utils.Json.UnmarshalFromString(newName, &req); err != nil {
		return errors.Wrap(err, "invalid folder emby setting")
	}
	return UpsertEmbyDirSetting(d.ID, srcObj.GetPath(), req.Actors, req.Plot, req.TvShowName, req.UseNameAsActor, req.AppendFileNameToPlot, req.TvShow)
}

var (
	_ driver.Driver      = (*EmbyWrapper)(nil)
	_ driver.MkdirConfig = (*EmbyWrapper)(nil)
	_ driver.Rename      = (*EmbyWrapper)(nil)
)
