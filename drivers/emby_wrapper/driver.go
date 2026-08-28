package emby_wrapper

import (
	"context"
	stdpath "path"
	"strings"

	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
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
	return d.withFolderAddition(objs), nil
}

func (d *EmbyWrapper) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
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

func (d *EmbyWrapper) withFolderAddition(objs []model.Obj) []model.Obj {
	settings, err := ListEmbyDirSettings(d.ID)
	if err != nil {
		utils.Log.Warnf("emby wrapper: list dir settings: %+v", err)
		settings = map[string]string{}
	}
	out := make([]model.Obj, len(objs))
	for i, o := range objs {
		out[i] = wrapFolder(o, settings[o.GetPath()])
	}
	return out
}

var _ driver.Driver = (*EmbyWrapper)(nil)
