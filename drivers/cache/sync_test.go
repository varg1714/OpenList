package cache

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/OpenListTeam/OpenList/v4/drivers/local"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func init() {
	dB, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		panic("failed to connect database")
	}
	conf.Conf = conf.DefaultConfig("data")
	db.Init(dB)
}

func setup(t *testing.T) *Cache {
	t.Helper()
	tmp := t.TempDir()
	_ = os.MkdirAll(filepath.Join(tmp, "sub"), 0o755)
	_ = os.WriteFile(filepath.Join(tmp, "a.txt"), []byte("hello"), 0o644)
	_ = os.WriteFile(filepath.Join(tmp, "sub", "b.txt"), []byte("world"), 0o644)
	localID, err := op.CreateStorage(context.Background(), model.Storage{
		Driver:    "Local",
		MountPath: "/local",
		Addition:  fmt.Sprintf(`{"root_folder_path":%q}`, tmp),
	})
	if err != nil {
		t.Fatalf("create local storage: %+v", err)
	}
	cacheID, err := op.CreateStorage(context.Background(), model.Storage{
		Driver:    "Cache",
		MountPath: "/cache",
		Addition:  `{"remote_path":"/local","ttl_hours":24,"sync_interval_hours":0}`,
	})
	if err != nil {
		t.Fatalf("create cache storage: %+v", err)
	}
	t.Cleanup(func() {
		_ = op.DeleteStorageById(context.Background(), localID)
		_ = op.DeleteStorageById(context.Background(), cacheID)
	})
	d, err := op.GetStorageByMountPath("/cache")
	if err != nil {
		t.Fatalf("get cache storage: %+v", err)
	}
	return d.(*Cache)
}

func rootDir() model.Obj {
	return &model.Object{Path: "/", Name: "Root", IsFolder: true}
}

func names(objs []model.Obj) []string {
	var res []string
	for _, o := range objs {
		res = append(res, o.GetName())
	}
	return res
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func mustRootPath(d *Cache) string {
	storage, _, err := op.GetStorageAndActualPath(d.RemotePath)
	if err != nil {
		panic(err)
	}
	if r, ok := storage.(driver.IRootPath); ok {
		return r.GetRootPath()
	}
	panic("downstream storage does not expose a root path")
}

func TestListWhitelistFiltersRoot(t *testing.T) {
	d := setup(t)
	d.SyncPaths = "/sub"

	objs, err := d.List(context.Background(), rootDir(), model.ListArgs{})
	if err != nil {
		t.Fatalf("list: %+v", err)
	}
	if len(objs) != 1 || objs[0].GetName() != "sub" {
		t.Errorf("expected only sub dir in root listing, got %v", names(objs))
	}
	item, err := GetCacheList(d.ID, "/")
	if err != nil || item == nil {
		t.Fatalf("expected cache row, got %v %v", item, err)
	}
	if !contains(names(fromCachedObjs(item.Data)), "a.txt") {
		t.Errorf("cache row must keep full listing, got %v", names(fromCachedObjs(item.Data)))
	}

	objs, err = d.List(context.Background(), rootDir(), model.ListArgs{})
	if err != nil {
		t.Fatalf("list from cache: %+v", err)
	}
	if len(objs) != 1 || objs[0].GetName() != "sub" {
		t.Errorf("expected only sub dir from cache, got %v", names(objs))
	}
}

func TestListWhitelistShowsSubtree(t *testing.T) {
	d := setup(t)
	d.SyncPaths = "/sub"

	objs, err := d.List(context.Background(), &model.Object{Path: "/sub", Name: "sub", IsFolder: true}, model.ListArgs{})
	if err != nil {
		t.Fatalf("list sub: %+v", err)
	}
	if len(objs) != 1 || objs[0].GetName() != "b.txt" {
		t.Errorf("expected b.txt in sub listing, got %v", names(objs))
	}
}

func TestListWhitelistOutsideDirEmpty(t *testing.T) {
	d := setup(t)
	d.SyncPaths = "/sub"

	objs, err := d.List(context.Background(), &model.Object{Path: "/other", Name: "other", IsFolder: true}, model.ListArgs{})
	if err != nil {
		t.Fatalf("list other: %+v", err)
	}
	if len(objs) != 0 {
		t.Errorf("expected empty listing outside whitelist, got %v", names(objs))
	}
}

func TestListWhitelistShowsAncestorDirs(t *testing.T) {
	d := setup(t)
	root := mustRootPath(d)
	_ = os.MkdirAll(filepath.Join(root, "sub", "inner"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "sub", "inner", "c.txt"), []byte("c"), 0o644)
	_ = os.MkdirAll(filepath.Join(root, "other"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "other", "d.txt"), []byte("d"), 0o644)
	d.SyncPaths = "/sub/inner"

	objs, err := d.List(context.Background(), rootDir(), model.ListArgs{})
	if err != nil {
		t.Fatalf("list root: %+v", err)
	}
	if len(objs) != 1 || objs[0].GetName() != "sub" {
		t.Errorf("expected sub dir (ancestor of whitelist entry) in root, got %v", names(objs))
	}

	objs, err = d.List(context.Background(), &model.Object{Path: "/sub", Name: "sub", IsFolder: true}, model.ListArgs{})
	if err != nil {
		t.Fatalf("list sub: %+v", err)
	}
	if len(objs) != 1 || objs[0].GetName() != "inner" {
		t.Errorf("expected inner dir only in sub listing, got %v", names(objs))
	}

	objs, err = d.List(context.Background(), &model.Object{Path: "/sub/inner", Name: "inner", IsFolder: true}, model.ListArgs{})
	if err != nil {
		t.Fatalf("list inner: %+v", err)
	}
	if len(objs) != 1 || objs[0].GetName() != "c.txt" {
		t.Errorf("expected c.txt in inner listing, got %v", names(objs))
	}
}

func TestVisibleInSyncPaths(t *testing.T) {
	entries := []string{"/电影/邻居", "/剧集"}
	cases := []struct {
		path string
		want bool
	}{
		{"/", true},           // 根是任何条目的祖先
		{"/电影", true},         // 条目的祖先
		{"/电影/邻居", true},      // 等于条目
		{"/电影/邻居/2024", true}, // 条目的后代
		{"/剧集", true},
		{"/剧集/2024/aaa", true},
		{"/电影2", false},    // 前缀兄弟
		{"/电影/邻居2", false}, // 后缀兄弟
		{"/电影/其他", false},  // 同父兄弟
		{"/其他", false},     // 完全无关
	}
	for _, c := range cases {
		if got := visibleInSyncPaths(c.path, entries); got != c.want {
			t.Errorf("visibleInSyncPaths(%q) = %v, want %v", c.path, got, c.want)
		}
	}
	if visibleInSyncPaths("/a", nil) {
		t.Errorf("empty entries must not be visible")
	}
}

func TestListWhitelistRefreshStillFilters(t *testing.T) {
	d := setup(t)
	d.SyncPaths = "/sub"

	objs, err := d.List(context.Background(), rootDir(), model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list refresh: %+v", err)
	}
	if len(objs) != 1 || objs[0].GetName() != "sub" {
		t.Errorf("expected only sub dir after refresh, got %v", names(objs))
	}
}
