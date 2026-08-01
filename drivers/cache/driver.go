package cache

import (
	"context"
	stdpath "path"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/pkg/cron"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
)

type Cache struct {
	model.Storage
	Addition
	cron *cron.Cron
}

func (d *Cache) Config() driver.Config {
	cfg := config
	if remote, _, err := op.GetStorageAndActualPath(d.RemotePath); err == nil {
		rc := remote.Config()
		cfg.OnlyProxy = rc.OnlyProxy
		cfg.NoLinkURL = rc.NoLinkURL
	}
	return cfg
}

func (d *Cache) GetAddition() driver.Additional {
	return &d.Addition
}

func (d *Cache) Init(ctx context.Context) error {
	if strings.TrimSpace(d.RemotePath) == "" {
		return errors.New("remote path must not be empty")
	}
	d.RemotePath = utils.FixAndCleanPath(d.RemotePath)
	d.syncProxy()
	if d.TTLHours <= 0 {
		d.TTLHours = 24
	}
	if d.cron != nil {
		d.cron.Stop()
		d.cron = nil
	}
	if d.SyncIntervalHours > 0 {
		d.cron = cron.NewCron(time.Duration(d.SyncIntervalHours) * time.Hour)
		d.cron.Do(d.syncAll)
	}
	return nil
}

func (d *Cache) Drop(ctx context.Context) error {
	if d.cron != nil {
		d.cron.Stop()
		d.cron = nil
	}
	return nil
}

// 继承下游代理配置：代理判定（ShouldProxy/canProxy/webdav.go）读取的是
// 请求命中存储的 Config().MustProxy() 与 Storage.WebdavPolicy/WebProxy 等字段，
// 转发驱动不继承的话，下游的 native_proxy 等配置会被丢弃导致直链播放。
func (d *Cache) syncProxy() {
	if storage, _, err := op.GetStorageAndActualPath(d.RemotePath); err == nil {
		rs := storage.GetStorage()
		d.Storage.WebProxy = rs.WebProxy
		d.Storage.WebdavPolicy = rs.WebdavPolicy
		d.Storage.ProxyRange = rs.ProxyRange
		d.Storage.DownProxyURL = rs.DownProxyURL
		d.Storage.DisableProxySign = rs.DisableProxySign
	}
}

func (d *Cache) remote() (driver.Driver, string, error) {
	storage, actualPath, err := op.GetStorageAndActualPath(d.RemotePath)
	if err == nil {
		d.syncProxy()
	}
	return storage, actualPath, err
}

func (d *Cache) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {
	dirPath := dir.GetPath()
	if !args.Refresh {
		if item, err := GetCacheList(d.ID, dirPath); err != nil {
			log.Errorf("cache: get list %s: %+v", dirPath, err)
		} else if item != nil {
			return fromCachedObjs(item.Data), nil
		}
	}
	remoteStorage, remoteActualPath, err := d.remote()
	if err != nil {
		return nil, err
	}
	remoteObjs, err := op.List(ctx, remoteStorage, stdpath.Join(remoteActualPath, dirPath), args)
	if err != nil {
		return nil, err
	}
	snaps := make([]model.CachedObj, 0, len(remoteObjs))
	for _, o := range remoteObjs {
		snaps = append(snaps, toCachedObj(dirPath, o))
	}
	if err := UpsertCacheList(d.ID, dirPath, snaps); err != nil {
		log.Errorf("cache: upsert %s: %+v", dirPath, err)
	}
	return fromCachedObjs(snaps), nil
}

func fromCachedObjs(snaps []model.CachedObj) []model.Obj {
	objs := make([]model.Obj, 0, len(snaps))
	for i := range snaps {
		objs = append(objs, fromCachedObj(snaps[i]))
	}
	return objs
}

func (d *Cache) Get(ctx context.Context, path string) (model.Obj, error) {
	if utils.PathEqual(path, "/") {
		return &model.Object{Name: "Root", IsFolder: true, Path: "/"}, nil
	}
	parentDir := stdpath.Dir(path)
	if item, err := GetCacheList(d.ID, parentDir); err != nil {
		log.Errorf("cache: get list %s: %+v", parentDir, err)
	} else if item != nil {
		name := stdpath.Base(path)
		for _, c := range item.Data {
			if c.Name == name {
				return fromCachedObj(c), nil
			}
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
	return &model.Object{
		ID:       obj.GetID(),
		Path:     path,
		Name:     obj.GetName(),
		Size:     obj.GetSize(),
		Modified: obj.ModTime(),
		Ctime:    obj.CreateTime(),
		IsFolder: obj.IsDir(),
		HashInfo: obj.GetHash(),
	}, nil
}

func (d *Cache) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
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

var _ driver.Driver = (*Cache)(nil)
