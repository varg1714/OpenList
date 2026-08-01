package cache

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

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

func mustLocalStorageID(d *Cache) uint {
	storage, _, err := op.GetStorageAndActualPath(d.RemotePath)
	if err != nil {
		panic(err)
	}
	return storage.GetStorage().ID
}

func TestSyncAllRefreshesExpired(t *testing.T) {
	d := setup(t)
	_, _ = d.List(context.Background(), rootDir(), model.ListArgs{})

	root := mustRootPath(d)
	_ = os.WriteFile(filepath.Join(root, "new.txt"), []byte("x"), 0o644)
	_ = os.Remove(filepath.Join(root, "a.txt"))
	_ = os.MkdirAll(filepath.Join(root, "newdir"), 0o755)

	item, err := GetCacheList(d.ID, "/")
	if err != nil || item == nil {
		t.Fatalf("get cache row: %v %v", item, err)
	}
	if err := db.GetDb().Model(&model.CacheList{}).Where("id = ?", item.ID).Update("updated_at", time.Now().Add(-48*time.Hour)).Error; err != nil {
		t.Fatalf("age row: %v", err)
	}

	d.syncAll()

	objs, err := d.List(context.Background(), rootDir(), model.ListArgs{})
	if err != nil {
		t.Fatalf("list: %+v", err)
	}
	got := names(objs)
	if contains(got, "a.txt") {
		t.Errorf("expected a.txt removed, got %v", got)
	}
	if !contains(got, "new.txt") || !contains(got, "newdir") {
		t.Errorf("expected new.txt and newdir added, got %v", got)
	}
	subObjs, err := d.List(context.Background(), &model.Object{Path: "/newdir", Name: "newdir", IsFolder: true}, model.ListArgs{})
	if err != nil {
		t.Fatalf("list newdir: %+v", err)
	}
	if len(subObjs) != 0 {
		t.Errorf("expected empty newdir, got %+v", subObjs)
	}
}

func TestSyncAllSkipsFresh(t *testing.T) {
	d := setup(t)
	_, _ = d.List(context.Background(), rootDir(), model.ListArgs{})
	root := mustRootPath(d)
	_ = os.WriteFile(filepath.Join(root, "new.txt"), []byte("x"), 0o644)

	d.syncAll()

	objs, err := d.List(context.Background(), rootDir(), model.ListArgs{})
	if err != nil {
		t.Fatalf("list: %+v", err)
	}
	if contains(names(objs), "new.txt") {
		t.Errorf("fresh row must not be refreshed, got %v", names(objs))
	}
}

func TestSyncAllKeepsRowOnFailure(t *testing.T) {
	d := setup(t)
	_, _ = d.List(context.Background(), rootDir(), model.ListArgs{})
	_ = os.RemoveAll(mustRootPath(d))

	item, err := GetCacheList(d.ID, "/")
	if err != nil || item == nil {
		t.Fatalf("get cache row: %v %v", item, err)
	}
	oldData := item.Data
	if err := db.GetDb().Model(&model.CacheList{}).Where("id = ?", item.ID).Update("updated_at", time.Now().Add(-48*time.Hour)).Error; err != nil {
		t.Fatalf("age row: %v", err)
	}

	d.syncAll()

	row, err := GetCacheList(d.ID, "/")
	if err != nil || row == nil {
		t.Fatalf("expected row kept after sync failure, got %v %v", row, err)
	}
	if len(row.Data) != len(oldData) {
		t.Errorf("expected stale data kept unchanged, got %d entries, want %d", len(row.Data), len(oldData))
	}
}

func TestParseSyncPaths(t *testing.T) {
	cases := []struct {
		raw  string
		want []string
	}{
		{"", nil},
		{"   \n  ", nil},
		{"..", nil},
		{"/", nil},
		{"/sub", []string{"/sub"}},
		{"sub", []string{"/sub"}},
		{"/sub\n/sub2", []string{"/sub", "/sub2"}},
		{"/a,/b\n/c", []string{"/a", "/b", "/c"}},
		{"/a\n/a,/b", []string{"/a", "/b"}},
	}
	for _, c := range cases {
		if got := parseSyncPaths(c.raw); !slices.Equal(got, c.want) {
			t.Errorf("parseSyncPaths(%q) = %v, want %v", c.raw, got, c.want)
		}
	}
}

func TestWithinSyncPaths(t *testing.T) {
	entries := []string{"/movies", "/tv"}
	cases := []struct {
		path string
		want bool
	}{
		{"/movies", true},
		{"/movies/2024", true},
		{"/movies2", false},
		{"/tv/series/a", true},
		{"/tvx", false},
		{"/", false},
		{"/other", false},
	}
	for _, c := range cases {
		if got := withinSyncPaths(c.path, entries); got != c.want {
			t.Errorf("withinSyncPaths(%q) = %v, want %v", c.path, got, c.want)
		}
	}
	if !withinSyncPaths("/a/b", []string{"/"}) {
		t.Errorf("root entry must match everything")
	}
}

func TestSyncPathEntries(t *testing.T) {
	d := setup(t)
	cases := []struct {
		raw     string
		want    []string
		enabled bool
	}{
		{"", nil, false},
		{"/sub", []string{"/sub"}, true},
		{"/sub\n/missing", []string{"/sub", "/missing"}, true},
		{"..", nil, true},
	}
	for _, c := range cases {
		d.SyncPaths = c.raw
		got, enabled := d.syncPathEntries("/")
		if enabled != c.enabled || !slices.Equal(got, c.want) {
			t.Errorf("syncPathEntries(%q) = (%v, %v), want (%v, %v)", c.raw, got, enabled, c.want, c.enabled)
		}
	}
	d.SyncPaths = "/sub"
	got, enabled := d.syncPathEntries("/other")
	if !enabled || len(got) != 0 {
		t.Errorf("entries outside actualPath must be ignored, got (%v, %v)", got, enabled)
	}
}
