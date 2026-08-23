package cache

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

func findNamed(objs []model.Obj, name string) (model.Obj, bool) {
	for _, o := range objs {
		if o.GetName() == name {
			return o, true
		}
	}
	return nil, false
}

func folderTTL(obj model.Obj) (int, bool) {
	add, ok := obj.(model.ObjAdditional)
	if !ok {
		return 0, false
	}
	switch v := add.GetAddition().(type) {
	case FolderAddition:
		return v.TTLHours, true
	case *FolderAddition:
		return v.TTLHours, true
	default:
		return 0, false
	}
}

func TestWrappedFolderKeepsThumb(t *testing.T) {
	obj := fromCachedObj(model.CachedObj{Name: "d", IsFolder: true, Path: "/d", Thumbnail: "http://t"})
	wrapped := wrapFolder(obj, 3)
	thumb, ok := model.GetThumb(wrapped)
	if !ok || thumb != "http://t" {
		t.Errorf("wrapped folder must keep thumbnail, got %q %v", thumb, ok)
	}
	ttl, ok := folderTTL(wrapped)
	if !ok || ttl != 3 {
		t.Errorf("wrapped folder must keep ttl additional, got %d %v", ttl, ok)
	}
}

func TestMkdirConfigHasTTLHours(t *testing.T) {
	d := setup(t)
	items := d.MkdirConfig()
	if len(items) != 1 {
		t.Fatalf("expected 1 mkdir config item, got %d", len(items))
	}
	if items[0].Name != "ttl_hours" {
		t.Errorf("expected ttl_hours field, got %q", items[0].Name)
	}
}

func TestListFoldersHaveTTLAddition(t *testing.T) {
	d := setup(t)
	if err := UpsertCacheDirSetting(d.ID, "/sub", 48); err != nil {
		t.Fatalf("set folder ttl: %v", err)
	}
	objs, err := d.List(context.Background(), rootDir(), model.ListArgs{})
	if err != nil {
		t.Fatalf("list: %+v", err)
	}
	sub, ok := findNamed(objs, "sub")
	if !ok {
		t.Fatalf("expected sub dir, got %v", names(objs))
	}
	ttl, ok := folderTTL(sub)
	if !ok {
		t.Fatalf("folder must expose additional, got %T", sub)
	}
	if ttl != 48 {
		t.Errorf("expected folder ttl 48, got %d", ttl)
	}
	file, ok := findNamed(objs, "a.txt")
	if !ok {
		t.Fatalf("expected a.txt, got %v", names(objs))
	}
	if _, hasAdd := file.(model.ObjAdditional); hasAdd {
		t.Errorf("file must not expose additional")
	}
}

func TestListFolderWithoutOverrideHasZeroTTL(t *testing.T) {
	d := setup(t)
	objs, err := d.List(context.Background(), rootDir(), model.ListArgs{})
	if err != nil {
		t.Fatalf("list: %+v", err)
	}
	sub, ok := findNamed(objs, "sub")
	if !ok {
		t.Fatalf("expected sub dir, got %v", names(objs))
	}
	ttl, ok := folderTTL(sub)
	if !ok {
		t.Fatalf("folder must expose additional even without override, got %T", sub)
	}
	if ttl != 0 {
		t.Errorf("expected ttl 0 (use global), got %d", ttl)
	}
}

func TestRenameFolderSavesTTL(t *testing.T) {
	d := setup(t)
	objs, err := d.List(context.Background(), rootDir(), model.ListArgs{})
	if err != nil {
		t.Fatalf("list: %+v", err)
	}
	sub, ok := findNamed(objs, "sub")
	if !ok {
		t.Fatalf("expected sub dir")
	}
	if err := d.Rename(context.Background(), sub, `{"ttl_hours":72}`); err != nil {
		t.Fatalf("rename folder: %+v", err)
	}
	item, err := GetCacheDirSetting(d.ID, "/sub")
	if err != nil || item == nil {
		t.Fatalf("expected setting, got %v %v", item, err)
	}
	if item.TTLHours != 72 {
		t.Errorf("expected 72, got %d", item.TTLHours)
	}
	root := mustRootPath(d)
	if _, err := os.Stat(filepath.Join(root, "sub")); err != nil {
		t.Errorf("downstream folder must stay named sub: %v", err)
	}
}

func TestRenameFolderZeroClearsTTL(t *testing.T) {
	d := setup(t)
	objs, err := d.List(context.Background(), rootDir(), model.ListArgs{})
	if err != nil {
		t.Fatalf("list: %+v", err)
	}
	sub, ok := findNamed(objs, "sub")
	if !ok {
		t.Fatalf("expected sub dir")
	}
	if err := d.Rename(context.Background(), sub, `{"ttl_hours":8}`); err != nil {
		t.Fatalf("set ttl: %+v", err)
	}
	if err := d.Rename(context.Background(), sub, `{"ttl_hours":0}`); err != nil {
		t.Fatalf("clear ttl: %+v", err)
	}
	item, err := GetCacheDirSetting(d.ID, "/sub")
	if err != nil || item != nil {
		t.Errorf("expected setting cleared, got %v %v", item, err)
	}
}

func TestRenameFileNotSupported(t *testing.T) {
	d := setup(t)
	objs, err := d.List(context.Background(), rootDir(), model.ListArgs{})
	if err != nil {
		t.Fatalf("list: %+v", err)
	}
	file, ok := findNamed(objs, "a.txt")
	if !ok {
		t.Fatalf("expected a.txt")
	}
	if err := d.Rename(context.Background(), file, "c.txt"); err == nil {
		t.Fatal("file rename must not be supported")
	}
	root := mustRootPath(d)
	if _, err := os.Stat(filepath.Join(root, "a.txt")); err != nil {
		t.Errorf("downstream file must stay named a.txt: %v", err)
	}
	objs, err = d.List(context.Background(), rootDir(), model.ListArgs{})
	if err != nil {
		t.Fatalf("list after rename: %+v", err)
	}
	got := names(objs)
	if !contains(got, "a.txt") || contains(got, "c.txt") {
		t.Errorf("cache listing must keep a.txt, got %v", got)
	}
}

func TestListScheduleScanUsesFolderTTL(t *testing.T) {
	d := setup(t)
	d.TTLHours = 24
	subDir := &model.Object{Path: "/sub", Name: "sub", IsFolder: true}
	if _, err := d.List(context.Background(), rootDir(), model.ListArgs{}); err != nil {
		t.Fatalf("prime root: %+v", err)
	}
	if _, err := d.List(context.Background(), subDir, model.ListArgs{}); err != nil {
		t.Fatalf("prime sub: %+v", err)
	}
	if err := UpsertCacheDirSetting(d.ID, "/sub", 1); err != nil {
		t.Fatalf("set folder ttl: %v", err)
	}
	item, err := GetCacheList(d.ID, "/sub")
	if err != nil || item == nil {
		t.Fatalf("get sub row: %v %v", item, err)
	}
	if err := db.GetDb().Model(&model.CacheList{}).Where("id = ?", item.ID).Update("updated_at", time.Now().Add(-2*time.Hour)).Error; err != nil {
		t.Fatalf("age sub row: %v", err)
	}
	root := mustRootPath(d)
	_ = os.WriteFile(filepath.Join(root, "sub", "new.txt"), []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "root-new.txt"), []byte("x"), 0o644)

	subObjs, err := d.List(context.Background(), subDir, model.ListArgs{Refresh: true, ScheduleScan: true})
	if err != nil {
		t.Fatalf("schedule scan sub: %+v", err)
	}
	if !contains(names(subObjs), "new.txt") {
		t.Errorf("expired folder ttl must refetch sub, got %v", names(subObjs))
	}
	rootObjs, err := d.List(context.Background(), rootDir(), model.ListArgs{Refresh: true, ScheduleScan: true})
	if err != nil {
		t.Fatalf("schedule scan root: %+v", err)
	}
	if contains(names(rootObjs), "root-new.txt") {
		t.Errorf("root still on global ttl must stay cached, got %v", names(rootObjs))
	}
}

func TestListScheduleScanFolderTTLKeepsFreshRow(t *testing.T) {
	d := setup(t)
	d.TTLHours = 1
	subDir := &model.Object{Path: "/sub", Name: "sub", IsFolder: true}
	if _, err := d.List(context.Background(), subDir, model.ListArgs{}); err != nil {
		t.Fatalf("prime sub: %+v", err)
	}
	if err := UpsertCacheDirSetting(d.ID, "/sub", 24); err != nil {
		t.Fatalf("set folder ttl: %v", err)
	}
	item, err := GetCacheList(d.ID, "/sub")
	if err != nil || item == nil {
		t.Fatalf("get sub row: %v %v", item, err)
	}
	if err := db.GetDb().Model(&model.CacheList{}).Where("id = ?", item.ID).Update("updated_at", time.Now().Add(-2*time.Hour)).Error; err != nil {
		t.Fatalf("age sub row: %v", err)
	}
	root := mustRootPath(d)
	_ = os.WriteFile(filepath.Join(root, "sub", "new.txt"), []byte("x"), 0o644)

	objs, err := d.List(context.Background(), subDir, model.ListArgs{Refresh: true, ScheduleScan: true})
	if err != nil {
		t.Fatalf("schedule scan sub: %+v", err)
	}
	if contains(names(objs), "new.txt") {
		t.Errorf("fresh folder override must be served from cache, got %v", names(objs))
	}
}

func TestListFolderTTLDoesNotInherit(t *testing.T) {
	d := setup(t)
	d.TTLHours = 24
	subDir := &model.Object{Path: "/sub", Name: "sub", IsFolder: true}
	if _, err := d.List(context.Background(), subDir, model.ListArgs{}); err != nil {
		t.Fatalf("prime sub: %+v", err)
	}
	if err := UpsertCacheDirSetting(d.ID, "/", 1); err != nil {
		t.Fatalf("set root ttl: %v", err)
	}
	item, err := GetCacheList(d.ID, "/sub")
	if err != nil || item == nil {
		t.Fatalf("get sub row: %v %v", item, err)
	}
	if err := db.GetDb().Model(&model.CacheList{}).Where("id = ?", item.ID).Update("updated_at", time.Now().Add(-2*time.Hour)).Error; err != nil {
		t.Fatalf("age sub row: %v", err)
	}
	root := mustRootPath(d)
	_ = os.WriteFile(filepath.Join(root, "sub", "new.txt"), []byte("x"), 0o644)

	objs, err := d.List(context.Background(), subDir, model.ListArgs{Refresh: true, ScheduleScan: true})
	if err != nil {
		t.Fatalf("schedule scan sub: %+v", err)
	}
	if contains(names(objs), "new.txt") {
		t.Errorf("parent ttl must not apply to child listing, got %v", names(objs))
	}
}
