package cache

import (
	"context"
	stdpath "path"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/internal/syncpaths"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
)

type Cache struct {
	model.Storage
	Addition
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
	if d.TTLHours <= 0 {
		d.TTLHours = 24
	}
	d.syncProxy()
	if _, actualPath, err := op.GetStorageAndActualPath(d.RemotePath); err == nil {
		syncpaths.ToRelEntries(actualPath, d.SyncPaths)
	} else {
		log.Warnf("cache: resolve remote for sync paths %s: %+v", d.RemotePath, err)
	}
	return nil
}

func (d *Cache) Drop(ctx context.Context) error {
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
	remoteStorage, remoteActualPath, err := d.remote()
	if err != nil {
		return nil, err
	}
	entries, whitelisted := syncpaths.ToRelEntries(remoteActualPath, d.SyncPaths)
	if whitelisted && dirPath != "/" && !visibleInSyncPaths(dirPath, entries) {
		return nil, nil
	}
	// 定时扫描（ScheduleScan）按 TTL 门控回源：行新鲜时直接 serve 缓存，
	// 过期或缺失才回源刷新；手动刷新（无 ScheduleScan）总是回源。
	// 目录若有 ttl_hours 覆盖则只作用于该目录本身，不向子目录继承。
	ttl := d.ttlFor(dirPath)
	if item, err := GetCacheList(d.ID, dirPath); err != nil {
		log.Errorf("cache: get list %s: %+v", dirPath, err)
	} else if item != nil && (!args.Refresh || (args.ScheduleScan && time.Since(item.UpdatedAt) < ttl)) {
		return d.withFolderAddition(fromCachedObjs(filterCachedObjs(item.Data, entries, whitelisted))), nil
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
	return d.withFolderAddition(fromCachedObjs(filterCachedObjs(snaps, entries, whitelisted))), nil
}

// filterCachedObjs 原地过滤快照列表：白名单启用时仅保留可见条目
// （与任一白名单条目同链：条目本身、其祖先或后代）。
// 缓存行本身始终保存全量快照，过滤只是展示层行为。
func filterCachedObjs(snaps []model.CachedObj, entries []string, enabled bool) []model.CachedObj {
	if !enabled {
		return snaps
	}
	kept := snaps[:0]
	for _, s := range snaps {
		if visibleInSyncPaths(s.Path, entries) {
			kept = append(kept, s)
		}
	}
	return kept
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
				return d.wrapObj(fromCachedObj(c)), nil
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
	return d.wrapObj(&model.Object{
		ID:       obj.GetID(),
		Path:     path,
		Name:     obj.GetName(),
		Size:     obj.GetSize(),
		Modified: obj.ModTime(),
		Ctime:    obj.CreateTime(),
		IsFolder: obj.IsDir(),
		HashInfo: obj.GetHash(),
	}), nil
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

func (d *Cache) MkdirConfig() []driver.Item {
	return []driver.Item{
		{
			Name:    "ttl_hours",
			Type:    conf.TypeNumber,
			Default: "0",
			Help:    "this folder's cache validity in hours; 0 = use the storage default",
		},
	}
}

func (d *Cache) Rename(ctx context.Context, srcObj model.Obj, newName string) error {
	if !srcObj.IsDir() {
		return errors.New("cache driver does not support renaming files")
	}
	var req FolderAddition
	if err := utils.Json.UnmarshalFromString(newName, &req); err != nil {
		return errors.Wrap(err, "invalid folder cache setting")
	}
	return UpsertCacheDirSetting(d.ID, srcObj.GetPath(), req.TTLHours)
}

func (d *Cache) ttlFor(dirPath string) time.Duration {
	hours := d.TTLHours
	if item, err := GetCacheDirSetting(d.ID, dirPath); err != nil {
		log.Errorf("cache: get dir setting %s: %+v", dirPath, err)
	} else if item != nil && item.TTLHours > 0 {
		hours = item.TTLHours
	}
	if hours <= 0 {
		hours = 24
	}
	return time.Duration(hours) * time.Hour
}

func (d *Cache) withFolderAddition(objs []model.Obj) []model.Obj {
	settings, err := ListCacheDirSettings(d.ID)
	if err != nil {
		log.Errorf("cache: list dir settings: %+v", err)
		settings = map[string]int{}
	}
	out := make([]model.Obj, len(objs))
	for i, o := range objs {
		out[i] = wrapFolder(o, settings[o.GetPath()])
	}
	return out
}

func (d *Cache) wrapObj(obj model.Obj) model.Obj {
	if !obj.IsDir() {
		return obj
	}
	ttl := 0
	if item, err := GetCacheDirSetting(d.ID, obj.GetPath()); err != nil {
		log.Errorf("cache: get dir setting %s: %+v", obj.GetPath(), err)
	} else if item != nil {
		ttl = item.TTLHours
	}
	return wrapFolder(obj, ttl)
}

var (
	_ driver.Driver      = (*Cache)(nil)
	_ driver.MkdirConfig = (*Cache)(nil)
	_ driver.Rename      = (*Cache)(nil)
)
