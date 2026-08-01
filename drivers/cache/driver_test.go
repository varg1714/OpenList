package cache_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/OpenListTeam/OpenList/v4/drivers/local"

	"github.com/OpenListTeam/OpenList/v4/drivers/cache"
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

func setup(t *testing.T) *cache.Cache {
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
	return d.(*cache.Cache)
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

func TestListMissFillsCache(t *testing.T) {
	d := setup(t)
	objs, err := d.List(context.Background(), rootDir(), model.ListArgs{})
	if err != nil {
		t.Fatalf("list: %+v", err)
	}
	if len(objs) != 2 {
		t.Fatalf("expected 2 objs, got %d", len(objs))
	}
	item, err := cache.GetCacheList(d.ID, "/")
	if err != nil || item == nil {
		t.Fatalf("expected cache row, got %v %v", item, err)
	}
	_, sub, ok := findSub(objs)
	if !ok {
		t.Fatalf("expected sub dir")
	}
	subObjs, err := d.List(context.Background(), sub, model.ListArgs{})
	if err != nil {
		t.Fatalf("list sub: %+v", err)
	}
	if len(subObjs) != 1 || subObjs[0].GetName() != "b.txt" {
		t.Errorf("bad sub listing: %+v", subObjs)
	}
}

func TestListHitServesCache(t *testing.T) {
	d := setup(t)
	_, _ = d.List(context.Background(), rootDir(), model.ListArgs{})
	root := mustRootPath(d)
	_ = os.Remove(filepath.Join(root, "a.txt"))
	_ = os.WriteFile(filepath.Join(root, "new.txt"), []byte("x"), 0o644)
	objs, err := d.List(context.Background(), rootDir(), model.ListArgs{})
	if err != nil {
		t.Fatalf("list: %+v", err)
	}
	got := names(objs)
	if !contains(got, "a.txt") {
		t.Errorf("expected cached a.txt, got %v", got)
	}
	if contains(got, "new.txt") {
		t.Errorf("expected no new.txt from cache, got %v", got)
	}
}

func TestListRefresh(t *testing.T) {
	d := setup(t)
	_, _ = d.List(context.Background(), rootDir(), model.ListArgs{})
	root := mustRootPath(d)
	_ = os.WriteFile(filepath.Join(root, "new.txt"), []byte("x"), 0o644)
	objs, err := d.List(context.Background(), rootDir(), model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list refresh: %+v", err)
	}
	if !contains(names(objs), "new.txt") {
		t.Errorf("expected new.txt after refresh, got %v", names(objs))
	}
	_ = os.Remove(filepath.Join(root, "a.txt"))
	objs, err = d.List(context.Background(), rootDir(), model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list after refresh: %+v", err)
	}
	if contains(names(objs), "a.txt") {
		t.Errorf("expected no a.txt, got %v", names(objs))
	}
}

func TestListMissReturnsError(t *testing.T) {
	d := setup(t)
	_ = op.DeleteStorageById(context.Background(), mustLocalStorageID(d))
	_, err := d.List(context.Background(), rootDir(), model.ListArgs{})
	if err == nil {
		t.Errorf("expected error when downstream missing")
	}
}

func TestGetFromCache(t *testing.T) {
	d := setup(t)
	_, _ = d.List(context.Background(), rootDir(), model.ListArgs{})
	obj, err := d.Get(context.Background(), "/a.txt")
	if err != nil {
		t.Fatalf("get: %+v", err)
	}
	if obj.GetName() != "a.txt" || obj.GetSize() != 5 {
		t.Errorf("bad get: %+v", obj)
	}
}

func TestGetMissFetchesDownstream(t *testing.T) {
	d := setup(t)
	obj, err := d.Get(context.Background(), "/a.txt")
	if err != nil {
		t.Fatalf("get: %+v", err)
	}
	if obj.GetName() != "a.txt" || obj.GetPath() != "/a.txt" {
		t.Errorf("bad get: %+v", obj)
	}
}

func TestLinkForwards(t *testing.T) {
	d := setup(t)
	obj, err := d.Get(context.Background(), "/a.txt")
	if err != nil {
		t.Fatalf("get: %+v", err)
	}
	l, err := d.Link(context.Background(), obj, model.LinkArgs{})
	if err != nil {
		t.Fatalf("link: %+v", err)
	}
	if l.RangeReader == nil {
		t.Errorf("expected range reader, got %+v", l)
	}
}

func findSub(objs []model.Obj) (string, model.Obj, bool) {
	for _, o := range objs {
		if o.IsDir() {
			return o.GetPath(), o, true
		}
	}
	return "", nil, false
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func mustRootPath(d *cache.Cache) string {
	storage, _, err := op.GetStorageAndActualPath(d.RemotePath)
	if err != nil {
		panic(err)
	}
	if r, ok := storage.(driver.IRootPath); ok {
		return r.GetRootPath()
	}
	panic("downstream storage does not expose a root path")
}

func mustLocalStorageID(d *cache.Cache) uint {
	storage, _, err := op.GetStorageAndActualPath(d.RemotePath)
	if err != nil {
		panic(err)
	}
	return storage.GetStorage().ID
}
