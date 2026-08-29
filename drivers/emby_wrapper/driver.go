package emby_wrapper

import (
	"bytes"
	"context"
	stdpath "path"
	"strings"

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
		return nil, err
	}
	actors := ""
	use := false
	if obj.IsDir() {
		if item, e := GetEmbyDirSetting(d.ID, path); e != nil {
			utils.Log.Warnf("emby wrapper: get dir setting %s: %+v", path, e)
		} else if item != nil {
			actors, use = item.Actors, item.UseNameAsActor
		}
	}
	return wrapObj(obj, path, actors, use, obj.IsDir()), nil
}

// virtualNFOForPath 尝试为 .nfo 路径构建虚拟对象。
// 返回 (obj, true, nil)：命中虚拟 nfo；(nil, false, nil)：应转发下游（无设置/无匹配影片/存在真实 nfo）。
func (d *EmbyWrapper) virtualNFOForPath(ctx context.Context, path string) (model.Obj, bool, error) {
	parentDir := stdpath.Dir(path)
	setting, err := d.resolveSetting(parentDir)
	if err != nil {
		return nil, false, err
	}
	if setting == nil {
		return nil, false, nil
	}
	remoteStorage, remoteActualPath, err := d.remote()
	if err != nil {
		return nil, false, err
	}
	objs, err := op.List(ctx, remoteStorage, stdpath.Join(remoteActualPath, parentDir), model.ListArgs{})
	if err != nil {
		return nil, false, err
	}
	base := strings.TrimSuffix(stdpath.Base(path), ".nfo")
	var movieObj model.Obj
	for _, o := range objs {
		if o.IsDir() {
			continue
		}
		if strings.EqualFold(utils.Ext(o.GetName()), "nfo") && strings.EqualFold(nfoBaseName(o.GetName()), base) {
			// 下游存在真实 nfo，交给下游 Get 返回
			return nil, false, nil
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
	content, err := buildNFOContent(base, setting)
	if err != nil {
		return nil, false, err
	}
	return &virtualNFO{
		Object: model.Object{
			Name:     stdpath.Base(path),
			Size:     int64(len(content)),
			Modified: movieObj.ModTime(),
			Path:     path,
			ID:       "vnfo-" + stdpath.Base(path),
		},
		content: content,
	}, true, nil
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
		actors, use := "", false
		if ok {
			actors, use = s.Actors, s.UseNameAsActor
		}
		out[i] = wrapObj(o, p, actors, use, o.IsDir())
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
	return UpsertEmbyDirSetting(d.ID, srcObj.GetPath(), req.Actors, req.UseNameAsActor)
}

var (
	_ driver.Driver      = (*EmbyWrapper)(nil)
	_ driver.MkdirConfig = (*EmbyWrapper)(nil)
	_ driver.Rename      = (*EmbyWrapper)(nil)
)
