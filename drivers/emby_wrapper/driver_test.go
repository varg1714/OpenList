package emby_wrapper_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/OpenListTeam/OpenList/v4/drivers/local"

	"github.com/OpenListTeam/OpenList/v4/drivers/emby_wrapper"
	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
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

func setup(t *testing.T) *emby_wrapper.EmbyWrapper {
	t.Helper()
	tmp := t.TempDir()
	_ = os.MkdirAll(filepath.Join(tmp, "Movies"), 0o755)
	_ = os.WriteFile(filepath.Join(tmp, "Movies", "AAA.mkv"), []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(tmp, "readme.txt"), []byte("hi"), 0o644)
	localID, err := op.CreateStorage(context.Background(), model.Storage{
		Driver:    "Local",
		MountPath: "/local",
		Addition:  fmt.Sprintf(`{"root_folder_path":%q}`, tmp),
	})
	if err != nil {
		t.Fatalf("create local storage: %+v", err)
	}
	wrapperID, err := op.CreateStorage(context.Background(), model.Storage{
		Driver:    "EmbyWrapper",
		MountPath: "/ew",
		Addition:  `{"remote_path":"/local"}`,
	})
	if err != nil {
		t.Fatalf("create emby wrapper storage: %+v", err)
	}
	t.Cleanup(func() {
		_ = op.DeleteStorageById(context.Background(), localID)
		_ = op.DeleteStorageById(context.Background(), wrapperID)
	})
	d, err := op.GetStorageByMountPath("/ew")
	if err != nil {
		t.Fatalf("get emby wrapper storage: %+v", err)
	}
	return d.(*emby_wrapper.EmbyWrapper)
}

func getSettingForTest(d *emby_wrapper.EmbyWrapper, dirPath string) (*model.EmbyDirSetting, error) {
	return emby_wrapper.GetEmbyDirSetting(d.ID, dirPath)
}

func names(objs []model.Obj) []string {
	out := make([]string, 0, len(objs))
	for _, o := range objs {
		out = append(out, o.GetName())
	}
	return out
}

func TestListPassthrough(t *testing.T) {
	d := setup(t)
	root, err := d.List(context.Background(), &model.Object{Name: "Root", Path: "/", IsFolder: true}, model.ListArgs{})
	if err != nil {
		t.Fatalf("list root: %+v", err)
	}
	got := names(root)
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %v", got)
	}
	movies, err := d.List(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, model.ListArgs{})
	if err != nil {
		t.Fatalf("list Movies: %+v", err)
	}
	if got := names(movies); len(got) != 1 || got[0] != "AAA.mkv" {
		t.Errorf("expected [AAA.mkv], got %v", got)
	}
}

func TestFoldersExposeActorsAddition(t *testing.T) {
	d := setup(t)
	root, err := d.List(context.Background(), &model.Object{Name: "Root", Path: "/", IsFolder: true}, model.ListArgs{})
	if err != nil {
		t.Fatalf("list root: %+v", err)
	}
	for _, o := range root {
		if o.GetName() != "Movies" {
			continue
		}
		add, ok := o.(model.ObjAdditional)
		if !ok {
			t.Fatal("folder must expose additional")
		}
		fa, ok := add.GetAddition().(emby_wrapper.FolderAddition)
		if !ok {
			t.Fatalf("unexpected addition type %T", add.GetAddition())
		}
		if fa.Actors != "" {
			t.Errorf("expected empty actors, got %q", fa.Actors)
		}
		return
	}
	t.Fatal("Movies folder not found")
}
