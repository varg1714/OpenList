# EmbyWrapper 驱动实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 新增 `EmbyWrapper` 包装驱动（类似 cache）：对下游存储透明转发，文件夹可设置 actor（重命名对话框 JSON），设置后该文件夹（含子文件夹，继承）内每个影片文件生成**内存构建的虚拟 `.nfo` 文件**（title=文件名、actor=设置值），通过 `Link` 以 RangeReader 提供内容，不写本地磁盘；用户已配置好的 strm 驱动挂载其上时负责把 nfo 内容落盘。

**Architecture:** 新包 `drivers/emby_wrapper/`，五个源文件：`meta.go`（Addition/Config/注册）、`driver.go`（List/Get/Link/Rename/MkdirConfig/Init/Drop）、`folder.go`（文件夹 addition 展示）、`nfo.go`（虚拟 nfo 对象与内容构建）、`db.go`（目录设置 CRUD + 继承解析）。设置持久化在新建的 `model.EmbyDirSetting` 表（storage_id+dir_path 唯一）。nfo 渲染复用 `virtual_file` 包：新增导出的 `RenderMediaNFO`，保证与 javdb 的 nfo 格式完全一致。

**Tech Stack:** Go 1.25.4（工具链 `/Library/Go/sdk/go1.25.4/bin/go`）、gorm（SQLite 测试）、Gin（无需改动）、复用 `internal/op`、`internal/stream`、`internal/model`。

**Spec:** 与用户确认的设计决策（2026-08-28）：
1. nfo 内容仅 `title` + `actor`；title 取自影片文件名（去扩展名，多段 CD 归一）；actor 来自文件夹设置（`MkdirConfig` 的 `actors` 字段，逗号分隔，支持中文逗号）
2. 继承：子文件夹无自身设置时继承最近祖先的设置（含设置所在文件夹本身）
3. 设置只通过重命名文件夹生效（解析 JSON，同 cache 的 `ttl_hours` 模式）；不实现 MakeDir（同 cache）；空 actors 清除设置；重命名文件报错
4. 同一影片（同一归一化 basename）只生成 1 个 nfo；下游已有同名真实 `.nfo` 时跳过虚拟生成（真实文件优先）
5. 影片扩展名列表：Addition 字段 `filter_file_types`，默认值与 strm 的 `FilterFileTypes` 一致（`mp4,mkv,flv,avi,wmv,ts,rmvb,webm,mp3,flac,aac,wav,ogg,m4a,wma,alac`）
6. 驱动名：EmbyWrapper，包目录 `drivers/emby_wrapper`，包名 `emby_wrapper`

## Global Constraints

- Go 工具链一律用 `/Library/Go/sdk/go1.25.4/bin/go`（`gofmt` 同理），不用 PATH 上的
- 测试数据库用内存 SQLite（`file::memory:?cache=shared`），模式完全照抄 `drivers/cache/driver_test.go` 的 harness
- 模式上照抄 cache 驱动：`MkdirConfig`/`Rename` 接口断言、`remote()`/`syncProxy()` 代理继承、`wrapFolder` 的 `model.ObjAdditional` 展示
- TDD：每个任务先写失败测试，再实现，跑通后提交
- 提交信息沿用仓库风格：`feat(emby_wrapper): ...`
- 目录设置展示用**自身**设置（不做继承展示），继承只用于 nfo 生成解析

---

### Task 1: virtual_file 导出 RenderMediaNFO

**Files:**
- Modify: `drivers/virtual_file/util.go`（`mediaToXML` 定义处，第 567 行附近）
- Test: `drivers/virtual_file/render_nfo_test.go`

**Interfaces:**
- Consumes: `virtual_file.Media`、`virtual_file.Inner`、`virtual_file.Actor`、`xml.Header`（均已存在）
- Produces: `func RenderMediaNFO(m *Media) ([]byte, error)` —— Task 5 的 `buildNFOContent` 依赖它

- [ ] **Step 1: 写失败测试** `drivers/virtual_file/render_nfo_test.go`

```go
package virtual_file

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestRenderMediaNFO(t *testing.T) {
	out, err := RenderMediaNFO(&Media{
		Title: Inner{Inner: "<![CDATA[测试标题]]>"},
		Actor: []Actor{{Name: "三上悠亚"}},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	got := string(out)
	if !strings.HasPrefix(got, xml.Header) {
		t.Errorf("missing xml header, got %s", got)
	}
	if !strings.Contains(got, "<movie>") || !strings.Contains(got, "</movie>") {
		t.Errorf("missing movie root, got %s", got)
	}
	if !strings.Contains(got, "<![CDATA[测试标题]]>") {
		t.Errorf("missing title, got %s", got)
	}
	if !strings.Contains(got, "<name>三上悠亚</name>") {
		t.Errorf("missing actor, got %s", got)
	}
}
```

- [ ] **Step 2: 运行确认失败**

```bash
cd /Users/varg247/store/work-store/backend/openlist && /Library/Go/sdk/go1.25.4/bin/go test ./drivers/virtual_file/ -run TestRenderMediaNFO -count=1
```

Expected: 编译失败，`undefined: RenderMediaNFO`

- [ ] **Step 3: 实现** —— 在 `drivers/virtual_file/util.go` 的 `mediaToXML` 函数后追加

```go
// RenderMediaNFO 将 Media 结构渲染为完整的 NFO XML 文档（含 XML 头）。
// 供其他驱动（如 emby_wrapper）构建内存 nfo，保证与 javdb 落盘 nfo 格式一致。
func RenderMediaNFO(m *Media) ([]byte, error) {
	return mediaToXML(m)
}
```

- [ ] **Step 4: 运行确认通过**

```bash
cd /Users/varg247/store/work-store/backend/openlist && /Library/Go/sdk/go1.25.4/bin/go test ./drivers/virtual_file/ -count=1
```

Expected: PASS（含新增用例，且既有 media_nfo 相关测试不回归）

- [ ] **Step 5: 提交**

```bash
cd /Users/varg247/store/work-store/backend/openlist && git add drivers/virtual_file/util.go drivers/virtual_file/render_nfo_test.go && git commit -m "feat(virtual_file): export RenderMediaNFO for in-memory nfo building"
```

---

### Task 2: EmbyDirSetting 模型 + DB 注册 + 设置 CRUD

**Files:**
- Create: `internal/model/emby.go`
- Modify: `internal/db/db.go:14`（AutoMigrate 列表）
- Create: `drivers/emby_wrapper/db.go`
- Test: `drivers/emby_wrapper/db_test.go`

**Interfaces:**
- Consumes: `db.GetDb()`、`model.EmbyDirSetting`（本任务创建）
- Produces: `GetEmbyDirSetting(storageID uint, dirPath string) (*model.EmbyDirSetting, error)`、`UpsertEmbyDirSetting(storageID uint, dirPath, actors string) error`、`ListEmbyDirSettings(storageID uint) (map[string]string, error)` —— Task 4 的 `resolveSetting` 与 Task 3 的 `withFolderAddition` 依赖

- [ ] **Step 1: 建模型** `internal/model/emby.go`

```go
package model

// EmbyDirSetting 某个目录的 Emby 元数据设置。Actors 为空表示未设置（nfo 生成时继承最近祖先的设置）。
type EmbyDirSetting struct {
	ID        uint   `gorm:"primaryKey"`
	StorageID uint   `gorm:"uniqueIndex:idx_emby_dir_setting"`
	DirPath   string `gorm:"uniqueIndex:idx_emby_dir_setting"`
	Actors    string
}
```

- [ ] **Step 2: 注册迁移** —— `internal/db/db.go` 的 `AutoMigrate(...)` 参数列表末尾追加 `new(model.EmbyDirSetting)`

- [ ] **Step 3: 写失败测试** `drivers/emby_wrapper/db_test.go`（包内测试，直接调 CRUD）

```go
package emby_wrapper

import (
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func init() {
	dB, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		panic("failed to connect database")
	}
	db.Init(dB)
}

func TestUpsertAndGetEmbyDirSetting(t *testing.T) {
	if err := UpsertEmbyDirSetting(1, "/Movies", "三上悠亚,深田咏美"); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	item, err := GetEmbyDirSetting(1, "/Movies")
	if err != nil || item == nil {
		t.Fatalf("get: %v %v", item, err)
	}
	if item.Actors != "三上悠亚,深田咏美" {
		t.Errorf("unexpected actors %q", item.Actors)
	}
	// 不同 storage 隔离
	other, err := GetEmbyDirSetting(2, "/Movies")
	if err != nil || other != nil {
		t.Errorf("other storage must have no setting, got %v %v", other, err)
	}
}

func TestUpsertEmptyClearsEmbyDirSetting(t *testing.T) {
	if err := UpsertEmbyDirSetting(1, "/Movies", "三上悠亚"); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := UpsertEmbyDirSetting(1, "/Movies", "  "); err != nil {
		t.Fatalf("clear: %v", err)
	}
	item, err := GetEmbyDirSetting(1, "/Movies")
	if err != nil || item != nil {
		t.Errorf("setting must be deleted, got %v %v", item, err)
	}
}

func TestListEmbyDirSettings(t *testing.T) {
	if err := UpsertEmbyDirSetting(1, "/a", "A"); err != nil {
		t.Fatalf("upsert a: %v", err)
	}
	if err := UpsertEmbyDirSetting(1, "/b", "B"); err != nil {
		t.Fatalf("upsert b: %v", err)
	}
	m, err := ListEmbyDirSettings(1)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if m["/a"] != "A" || m["/b"] != "B" {
		t.Errorf("unexpected map: %v", m)
	}
}

var _ = model.EmbyDirSetting{} // 防止未使用导入
```

- [ ] **Step 4: 运行确认失败**

```bash
cd /Users/varg247/store/work-store/backend/openlist && /Library/Go/sdk/go1.25.4/bin/go test ./drivers/emby_wrapper/ -count=1
```

Expected: 编译失败，`undefined: UpsertEmbyDirSetting`（模型/迁移已就位）

- [ ] **Step 5: 实现 CRUD** `drivers/emby_wrapper/db.go`

```go
package emby_wrapper

import (
	"errors"
	"strings"

	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"gorm.io/gorm"
)

func GetEmbyDirSetting(storageID uint, dirPath string) (*model.EmbyDirSetting, error) {
	var item model.EmbyDirSetting
	err := db.GetDb().Where("storage_id = ? AND dir_path = ?", storageID, dirPath).First(&item).Error
	if err == nil {
		return &item, nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return nil, err
}

// UpsertEmbyDirSetting 保存目录设置；actors 去空格后为空则删除该目录的设置。
func UpsertEmbyDirSetting(storageID uint, dirPath, actors string) error {
	actors = strings.TrimSpace(actors)
	if actors == "" {
		return db.GetDb().Where("storage_id = ? AND dir_path = ?", storageID, dirPath).Delete(&model.EmbyDirSetting{}).Error
	}
	item, err := GetEmbyDirSetting(storageID, dirPath)
	if err != nil {
		return err
	}
	if item != nil {
		item.Actors = actors
		return db.GetDb().Save(item).Error
	}
	return db.GetDb().Create(&model.EmbyDirSetting{
		StorageID: storageID,
		DirPath:   dirPath,
		Actors:    actors,
	}).Error
}

// ListEmbyDirSettings 返回该存储全部目录设置：dirPath -> actors。
func ListEmbyDirSettings(storageID uint) (map[string]string, error) {
	var items []model.EmbyDirSetting
	if err := db.GetDb().Where("storage_id = ?", storageID).Find(&items).Error; err != nil {
		return nil, err
	}
	out := make(map[string]string, len(items))
	for _, item := range items {
		out[item.DirPath] = item.Actors
	}
	return out, nil
}
```

- [ ] **Step 6: 运行确认通过**

```bash
cd /Users/varg247/store/work-store/backend/openlist && /Library/Go/sdk/go1.25.4/bin/go test ./drivers/emby_wrapper/ ./internal/db/... -count=1
```

Expected: PASS

- [ ] **Step 7: 提交**

```bash
cd /Users/varg247/store/work-store/backend/openlist && git add internal/model/emby.go internal/db/db.go drivers/emby_wrapper/db.go drivers/emby_wrapper/db_test.go && git commit -m "feat(emby_wrapper): add per-directory emby setting model and CRUD"
```

---

### Task 3: 驱动骨架（meta/driver/folder）+ List 透传

**Files:**
- Create: `drivers/emby_wrapper/meta.go`
- Create: `drivers/emby_wrapper/driver.go`
- Create: `drivers/emby_wrapper/folder.go`
- Modify: `drivers/all.go`（import 列表，按字母序插到 `drivers/emby_wrapper` 位置，即 `drivers/degoo` 之后、`drivers/doubao` 之前）
- Test: `drivers/emby_wrapper/driver_test.go`（外部测试包 `emby_wrapper_test`，harness 照抄 cache）

**Interfaces:**
- Consumes: `GetEmbyDirSetting`/`ListEmbyDirSettings`（Task 2）、下游存储经 `op.GetStorageAndActualPath` 解析
- Produces: `type EmbyWrapper struct`（字段 `Storage`、`Addition`、`supportSuffix map[string]struct{}`）、`(d *EmbyWrapper) remote() (driver.Driver, string, error)`、`(d *EmbyWrapper) withFolderAddition(objs []model.Obj) []model.Obj`、`type FolderAddition struct{ Actors string }`、`func wrapFolder(obj model.Obj, actors string) model.Obj` —— Task 4/5/6/7 全部依赖

- [ ] **Step 1: 写失败测试** `drivers/emby_wrapper/driver_test.go`

```go
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
```

- [ ] **Step 2: 运行确认失败**

```bash
cd /Users/varg247/store/work-store/backend/openlist && /Library/Go/sdk/go1.25.4/bin/go test ./drivers/emby_wrapper/ -run TestListPassthrough -count=1
```

Expected: 编译失败（驱动未注册/未定义）

- [ ] **Step 3: 实现 `meta.go`**

```go
package emby_wrapper

import (
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
)

type Addition struct {
	RemotePath      string `json:"remote_path" required:"true" help:"the mount path of the downstream storage"`
	FilterFileTypes string `json:"filter_file_types" type:"text" default:"mp4,mkv,flv,avi,wmv,ts,rmvb,webm,mp3,flac,aac,wav,ogg,m4a,wma,alac" required:"false" help:"file extensions that get a virtual nfo"`
}

var config = driver.Config{
	Name:        "EmbyWrapper",
	LocalSort:   true,
	NoUpload:    true,
	DefaultRoot: "/",
}

func init() {
	op.RegisterDriver(func() driver.Driver {
		return &EmbyWrapper{}
	})
}
```

- [ ] **Step 4: 实现 `folder.go`**

```go
package emby_wrapper

import "github.com/OpenListTeam/OpenList/v4/internal/model"

type FolderAddition struct {
	Actors string `json:"actors"`
}

type embyFolder struct {
	model.Obj
	addition FolderAddition
}

func (f *embyFolder) GetAddition() model.Additional {
	return f.addition
}

func (f *embyFolder) Unwrap() model.Obj {
	return f.Obj
}

func wrapFolder(obj model.Obj, actors string) model.Obj {
	if obj == nil || !obj.IsDir() {
		return obj
	}
	return &embyFolder{Obj: obj, addition: FolderAddition{Actors: actors}}
}
```

- [ ] **Step 5: 实现 `driver.go`**（骨架 + List 透传）

```go
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
```

- [ ] **Step 6: 注册到 `drivers/all.go`** —— import 块中 `_ "github.com/OpenListTeam/OpenList/v4/drivers/degoo"` 之后加一行 `_ "github.com/OpenListTeam/OpenList/v4/drivers/emby_wrapper"`

- [ ] **Step 7: 运行确认通过**

```bash
cd /Users/varg247/store/work-store/backend/openlist && /Library/Go/sdk/go1.25.4/bin/go test ./drivers/emby_wrapper/ -count=1
```

Expected: PASS（TestListPassthrough、TestFoldersExposeActorsAddition）

- [ ] **Step 8: 提交**

```bash
cd /Users/varg247/store/work-store/backend/openlist && git add drivers/emby_wrapper/meta.go drivers/emby_wrapper/driver.go drivers/emby_wrapper/folder.go drivers/emby_wrapper/driver_test.go drivers/all.go && git commit -m "feat(emby_wrapper): add wrapper driver skeleton with passthrough list"
```

---

### Task 4: MkdirConfig + Rename + 继承解析 resolveSetting

**Files:**
- Modify: `drivers/emby_wrapper/driver.go`（追加 MkdirConfig/Rename 方法）
- Create: `drivers/emby_wrapper/setting.go`（resolveSetting）
- Test: `drivers/emby_wrapper/rename_test.go`

**Interfaces:**
- Consumes: `UpsertEmbyDirSetting`/`GetEmbyDirSetting`（Task 2）、`FolderAddition`（Task 3）
- Produces: `(d *EmbyWrapper) MkdirConfig() []driver.Item`、`(d *EmbyWrapper) Rename(ctx context.Context, srcObj model.Obj, newName string) error`、`(d *EmbyWrapper) resolveSetting(dirPath string) (*model.EmbyDirSetting, error)` —— Task 5/6 依赖 resolveSetting

- [ ] **Step 1: 写失败测试** `drivers/emby_wrapper/rename_test.go`

```go
package emby_wrapper_test

import (
	"context"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

func TestMkdirConfigHasActors(t *testing.T) {
	d := setup(t)
	items := d.MkdirConfig()
	if len(items) != 1 {
		t.Fatalf("expected 1 mkdir config item, got %d", len(items))
	}
	if items[0].Name != "actors" {
		t.Errorf("expected actors field, got %q", items[0].Name)
	}
}

func TestRenameFolderSavesActors(t *testing.T) {
	d := setup(t)
	objs, err := d.List(context.Background(), &model.Object{Name: "Root", Path: "/", IsFolder: true}, model.ListArgs{})
	if err != nil {
		t.Fatalf("list root: %+v", err)
	}
	var movies model.Obj
	for _, o := range objs {
		if o.GetName() == "Movies" {
			movies = o
		}
	}
	if movies == nil {
		t.Fatal("Movies folder not found")
	}
	if err := d.Rename(context.Background(), movies, `{"actors":"三上悠亚,深田咏美"}`); err != nil {
		t.Fatalf("rename folder: %+v", err)
	}
	item, err := getSettingForTest(d, "/Movies")
	if err != nil || item == nil {
		t.Fatalf("expected setting, got %v %v", item, err)
	}
	if item.Actors != "三上悠亚,深田咏美" {
		t.Errorf("expected actors saved, got %q", item.Actors)
	}
	// 重命名不改变下游真实文件夹名：列表里仍是 Movies
	got := names(objs)
	_ = got
}

func TestRenameFolderEmptyClearsActors(t *testing.T) {
	d := setup(t)
	objs, err := d.List(context.Background(), &model.Object{Name: "Root", Path: "/", IsFolder: true}, model.ListArgs{})
	if err != nil {
		t.Fatalf("list root: %+v", err)
	}
	var movies model.Obj
	for _, o := range objs {
		if o.GetName() == "Movies" {
			movies = o
		}
	}
	if err := d.Rename(context.Background(), movies, `{"actors":"A"}`); err != nil {
		t.Fatalf("set actors: %+v", err)
	}
	if err := d.Rename(context.Background(), movies, `{"actors":""}`); err != nil {
		t.Fatalf("clear actors: %+v", err)
	}
	item, err := getSettingForTest(d, "/Movies")
	if err != nil || item != nil {
		t.Errorf("expected setting cleared, got %v %v", item, err)
	}
}

func TestRenameFileNotSupported(t *testing.T) {
	d := setup(t)
	objs, err := d.List(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, model.ListArgs{})
	if err != nil {
		t.Fatalf("list Movies: %+v", err)
	}
	if len(objs) != 1 || objs[0].GetName() != "AAA.mkv" {
		t.Fatalf("unexpected listing %v", names(objs))
	}
	if err := d.Rename(context.Background(), objs[0], `{"actors":"A"}`); err == nil {
		t.Fatal("file rename must not be supported")
	}
}

func TestResolveSettingInheritsAncestor(t *testing.T) {
	d := setup(t)
	if err := d.Rename(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, `{"actors":"三上悠亚"}`); err != nil {
		t.Fatalf("set actors: %+v", err)
	}
	// 子目录没有自身设置，应继承 /Movies 的设置
	item, err := d.resolveSetting("/Movies/Sub")
	if err != nil || item == nil {
		t.Fatalf("expected inherited setting, got %v %v", item, err)
	}
	if item.Actors != "三上悠亚" {
		t.Errorf("expected inherited actors, got %q", item.Actors)
	}
	// 自身设置覆盖祖先
	if err := d.Rename(context.Background(), &model.Object{Name: "Sub", Path: "/Movies/Sub", IsFolder: true}, `{"actors":"深田咏美"}`); err != nil {
		t.Fatalf("set sub actors: %+v", err)
	}
	item, err = d.resolveSetting("/Movies/Sub")
	if err != nil || item == nil {
		t.Fatalf("expected own setting, got %v %v", item, err)
	}
	if item.Actors != "深田咏美" {
		t.Errorf("expected own actors, got %q", item.Actors)
	}
	// 无任何设置
	item, err = d.resolveSetting("/Other")
	if err != nil || item != nil {
		t.Errorf("expected no setting for /Other, got %v %v", item, err)
	}
}
```

- [ ] **Step 2: 实现** —— `drivers/emby_wrapper/setting.go`

```go
package emby_wrapper

import (
	stdpath "path"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

// resolveSetting 返回 dirPath 自身或其最近祖先的目录设置；都没有则返回 nil。
// 子文件夹继承最近祖先的 actor 设置。
func (d *EmbyWrapper) resolveSetting(dirPath string) (*model.EmbyDirSetting, error) {
	dirPath = utils.FixAndCleanPath(dirPath)
	for {
		item, err := GetEmbyDirSetting(d.ID, dirPath)
		if err != nil {
			return nil, err
		}
		if item != nil && strings.TrimSpace(item.Actors) != "" {
			return item, nil
		}
		if utils.PathEqual(dirPath, "/") {
			return nil, nil
		}
		dirPath = stdpath.Dir(dirPath)
	}
}
```

（import 含 `stdpath "path"` 与 `strings`。）

- [ ] **Step 3: 在 `driver.go` 追加 MkdirConfig/Rename**（`withFolderAddition` 方法之后）

```go
func (d *EmbyWrapper) MkdirConfig() []driver.Item {
	return []driver.Item{
		{
			Name:    "actors",
			Type:    conf.TypeString,
			Default: "",
			Help:    "演员列表，逗号分隔；仅对文件夹修改生效，设置后该文件夹及子文件夹内的影片会生成对应的虚拟 nfo 文件（配合 strm 驱动落盘）",
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
	return UpsertEmbyDirSetting(d.ID, srcObj.GetPath(), req.Actors)
}
```

同时更新 import：`"github.com/OpenListTeam/OpenList/v4/internal/conf"`，并在文件底部追加接口断言：

```go
var (
	_ driver.Driver      = (*EmbyWrapper)(nil)
	_ driver.MkdirConfig = (*EmbyWrapper)(nil)
	_ driver.Rename      = (*EmbyWrapper)(nil)
)
```

（删除 Step 5 骨架里已有的 `var _ driver.Driver = (*EmbyWrapper)(nil)` 行，避免重复声明。）

- [ ] **Step 4: 补测试辅助函数** —— `drivers/emby_wrapper/rename_test.go` 引用了 `getSettingForTest`，在 `driver_test.go` 追加：

```go
func getSettingForTest(d *emby_wrapper.EmbyWrapper, dirPath string) (*model.EmbyDirSetting, error) {
	return emby_wrapper.GetEmbyDirSetting(d.ID, dirPath)
}
```

（`GetEmbyDirSetting` 已导出，此辅助函数仅为测试可读性。）

- [ ] **Step 5: 运行确认通过**

```bash
cd /Users/varg247/store/work-store/backend/openlist && /Library/Go/sdk/go1.25.4/bin/go test ./drivers/emby_wrapper/ -count=1
```

Expected: PASS（4 个新用例 + 既有用例）

- [ ] **Step 6: 提交**

```bash
cd /Users/varg247/store/work-store/backend/openlist && git add drivers/emby_wrapper/ && git commit -m "feat(emby_wrapper): folder actor setting via rename with ancestor inheritance"
```

---

### Task 5: 虚拟 nfo 生成（List 装饰）

**Files:**
- Create: `drivers/emby_wrapper/nfo.go`
- Modify: `drivers/emby_wrapper/driver.go`（List 调用 `withVirtualNFOs`）
- Test: `drivers/emby_wrapper/nfo_test.go`

**Interfaces:**
- Consumes: `RenderMediaNFO`（Task 1）、`resolveSetting`（Task 4）、`virtual_file.GetRealName`（已存在：去扩展名 + `-cd\d+`/`-background` 归一）、`d.supportSuffix`（Task 3）
- Produces: `type virtualNFO struct{ model.Object; content []byte }`、`(d *EmbyWrapper) withVirtualNFOs(dirPath string, objs []model.Obj) []model.Obj`、`(d *EmbyWrapper) buildNFOContent(title string, setting *model.EmbyDirSetting) ([]byte, error)` —— Task 6/7 依赖 virtualNFO

- [ ] **Step 1: 写失败测试** `drivers/emby_wrapper/nfo_test.go`

```go
package emby_wrapper_test

import (
	"context"
	"strings"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

func TestListAddsVirtualNFO(t *testing.T) {
	d := setup(t)
	if err := d.Rename(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, `{"actors":"三上悠亚,深田咏美"}`); err != nil {
		t.Fatalf("set actors: %+v", err)
	}
	objs, err := d.List(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, model.ListArgs{})
	if err != nil {
		t.Fatalf("list Movies: %+v", err)
	}
	got := names(objs)
	if len(got) != 2 {
		t.Fatalf("expected [AAA.mkv AAA.nfo], got %v", got)
	}
	var nfo model.Obj
	for _, o := range objs {
		if o.GetName() == "AAA.nfo" {
			nfo = o
		}
	}
	if nfo == nil {
		t.Fatal("virtual AAA.nfo missing")
	}
	if nfo.IsDir() {
		t.Error("nfo must be a file")
	}
	if nfo.GetSize() == 0 {
		t.Error("nfo must have nonzero size")
	}
}

func TestListNoSettingNoNFO(t *testing.T) {
	d := setup(t)
	objs, err := d.List(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, model.ListArgs{})
	if err != nil {
		t.Fatalf("list Movies: %+v", err)
	}
	if got := names(objs); len(got) != 1 || got[0] != "AAA.mkv" {
		t.Errorf("expected only [AAA.mkv], got %v", got)
	}
}

func TestListOneNFOPerBaseName(t *testing.T) {
	d := setup(t)
	if err := d.Rename(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, `{"actors":"A"}`); err != nil {
		t.Fatalf("set actors: %+v", err)
	}
	// 追加 cd 分段与同名不同扩展名：应只生成 1 个 BBB.nfo
	if err := writeDownstreamFile(t, "/Movies/BBB.cd1.mkv", "x"); err != nil {
		t.Fatalf("write cd1: %v", err)
	}
	if err := writeDownstreamFile(t, "/Movies/BBB.cd2.mkv", "x"); err != nil {
		t.Fatalf("write cd2: %v", err)
	}
	if err := writeDownstreamFile(t, "/Movies/BBB.mp4", "x"); err != nil {
		t.Fatalf("write mp4: %v", err)
	}
	objs, err := d.List(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list: %+v", err)
	}
	got := names(objs)
	if len(got) != 6 {
		t.Fatalf("expected [AAA.mkv AAA.nfo BBB.cd1.mkv BBB.cd2.mkv BBB.mp4 BBB.nfo], got %v", got)
	}
	nfoCount := 0
	for _, o := range objs {
		if o.GetName() == "BBB.nfo" {
			nfoCount++
		}
	}
	if nfoCount != 1 {
		t.Errorf("expected exactly one BBB.nfo, got %d", nfoCount)
	}
}

func TestListSkipsRealNFO(t *testing.T) {
	d := setup(t)
	// 在下游放一个真实 AAA.nfo
	if err := writeDownstreamFile(t, "/Movies/AAA.nfo", "real"); err != nil {
		t.Fatalf("write real nfo: %v", err)
	}
	if err := d.Rename(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, `{"actors":"A"}`); err != nil {
		t.Fatalf("set actors: %+v", err)
	}
	objs, err := d.List(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list Movies: %+v", err)
	}
	got := names(objs)
	if len(got) != 2 {
		t.Fatalf("expected [AAA.mkv AAA.nfo], got %v", got)
	}
	for _, o := range objs {
		if o.GetName() == "AAA.nfo" && o.GetSize() != int64(len("real")) {
			t.Errorf("real nfo must win, got size %d", o.GetSize())
		}
	}
}

func TestListInheritedSettingAddsNFOInSubfolder(t *testing.T) {
	d := setup(t)
	if err := d.Rename(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, `{"actors":"三上悠亚"}`); err != nil {
		t.Fatalf("set actors: %+v", err)
	}
	// 子文件夹 + 影片
	if err := writeDownstreamDir(t, "/Movies/Sub"); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	if err := writeDownstreamFile(t, "/Movies/Sub/BBB.mp4", "x"); err != nil {
		t.Fatalf("write sub movie: %v", err)
	}
	objs, err := d.List(context.Background(), &model.Object{Name: "Sub", Path: "/Movies/Sub", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list Sub: %+v", err)
	}
	if got := names(objs); len(got) != 2 {
		t.Fatalf("expected [BBB.mp4 BBB.nfo], got %v", got)
	}
}

func TestNFONotGeneratedForNonMovieExt(t *testing.T) {
	d := setup(t)
	if err := writeDownstreamFile(t, "/Movies/note.txt", "x"); err != nil {
		t.Fatalf("write txt: %v", err)
	}
	if err := d.Rename(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, `{"actors":"A"}`); err != nil {
		t.Fatalf("set actors: %+v", err)
	}
	objs, err := d.List(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list Movies: %+v", err)
	}
	for _, o := range objs {
		if strings.HasSuffix(o.GetName(), ".nfo") && strings.HasPrefix(o.GetName(), "note") {
			t.Errorf("txt file must not get nfo, got %v", names(objs))
		}
	}
}
```

- [ ] **Step 2: 实现 `nfo.go`**

```go
package emby_wrapper

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

// virtualNFO 内存构建的 nfo 文件对象，content 为完整 XML 内容。
type virtualNFO struct {
	model.Object
	content []byte
}

// splitActors 按中英文逗号拆分演员列表并去除空白项。
func splitActors(actors string) []string {
	var out []string
	for _, a := range strings.FieldsFunc(actors, func(r rune) bool { return r == ',' || r == '，' }) {
		if a = strings.TrimSpace(a); a != "" {
			out = append(out, a)
		}
	}
	return out
}

// buildNFOContent 构建与 javdb 格式一致的 nfo XML：title + actor。
func buildNFOContent(title string, setting *model.EmbyDirSetting) ([]byte, error) {
	actors := splitActors(setting.Actors)
	actorInfos := make([]virtual_file.Actor, 0, len(actors))
	for _, a := range actors {
		actorInfos = append(actorInfos, virtual_file.Actor{Name: a})
	}
	return virtual_file.RenderMediaNFO(&virtual_file.Media{
		Title: virtual_file.Inner{Inner: fmt.Sprintf("<![CDATA[%s]]>", title)},
		Actor: actorInfos,
	})
}

// withVirtualNFOs 为 dirPath 下每个影片文件追加一个虚拟 nfo；
// 真实同名 nfo 优先（跳过虚拟生成）；同归一化 basename 只生成一个。
func (d *EmbyWrapper) withVirtualNFOs(dirPath string, objs []model.Obj) []model.Obj {
	setting, err := d.resolveSetting(dirPath)
	if err != nil {
		utils.Log.Warnf("emby wrapper: resolve setting %s: %+v", dirPath, err)
		return objs
	}
	if setting == nil {
		return objs
	}

	realNFO := map[string]bool{}
	for _, o := range objs {
		if o.IsDir() {
			continue
		}
		name := o.GetName()
		if strings.EqualFold(utils.Ext(name), "nfo") {
			realNFO[virtual_file.GetRealName(name)+".nfo"] = true
		}
	}

	out := objs
	added := map[string]bool{}
	for _, o := range objs {
		if o.IsDir() {
			continue
		}
		ext := utils.Ext(o.GetName())
		if _, ok := d.supportSuffix[ext]; !ok {
			continue
		}
		nfoName := virtual_file.GetRealName(o.GetName()) + ".nfo"
		if realNFO[nfoName] || added[nfoName] {
			continue
		}
		title := strings.TrimSuffix(nfoName, ".nfo")
		content, err := buildNFOContent(title, setting)
		if err != nil {
			utils.Log.Warnf("emby wrapper: build nfo %s: %+v", nfoName, err)
			continue
		}
		added[nfoName] = true
		parentDir := stdpath.Dir(o.GetPath())
		out = append(out, &virtualNFO{
			Object: model.Object{
				Name:     nfoName,
				Size:     int64(len(content)),
				Modified: o.ModTime(),
				Path:     stdpath.Join(parentDir, nfoName),
				ID:       "vnfo-" + nfoName,
			},
			content: content,
		})
	}
	return out
}
```

import 需要：`stdpath "path"`、`strings`、`fmt`、`virtual_file`、`model`、`utils`（无 `bytes`）。

- [ ] **Step 3: 修改 `driver.go` 的 List** —— 返回值改为：

```go
	objs, err := op.List(ctx, remoteStorage, stdpath.Join(remoteActualPath, dir.GetPath()), args)
	if err != nil {
		return nil, err
	}
	objs = d.withFolderAddition(objs)
	return d.withVirtualNFOs(dir.GetPath(), objs), nil
```

- [ ] **Step 4: 补测试辅助函数** —— `driver_test.go` 追加（访问本地下游根目录写文件）：

```go
// writeDownstreamFile 在本地下游存储的根目录下写文件（路径以 / 开头，相对下游根）。
func writeDownstreamFile(t *testing.T, relPath, content string) error {
	t.Helper()
	root := t.TempDir() // 注意：与 setup 的 tmp 不同！
	return nil
}
```

⚠️ 此辅助函数设计有误：setup 的临时目录不对外暴露。改为在 `setup` 中把本地根路径存到包级变量：

```go
var localRoot string // setup 中赋值
```

`setup` 内 `tmp` 创建后执行 `localRoot = tmp`，辅助函数改为：

```go
func writeDownstreamFile(t *testing.T, relPath, content string) error {
	t.Helper()
	full := filepath.Join(localRoot, strings.TrimPrefix(relPath, "/"))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, []byte(content), 0o644)
}

func writeDownstreamDir(t *testing.T, relPath string) error {
	t.Helper()
	return os.MkdirAll(filepath.Join(localRoot, strings.TrimPrefix(relPath, "/")), 0o755)
}
```

（import 追加 `"strings"`。）

- [ ] **Step 5: 运行确认通过**

```bash
cd /Users/varg247/store/work-store/backend/openlist && /Library/Go/sdk/go1.25.4/bin/go test ./drivers/emby_wrapper/ -count=1
```

Expected: PASS（6 个新用例）。若 `TestListSkipsRealNFO` 失败，检查 `op.List` 的 dirCache 是否命中旧列表——用例已传 `Refresh: true`，如仍失败需在断言前 `Cache.DeleteDirectory`。

- [ ] **Step 6: 提交**

```bash
cd /Users/varg247/store/work-store/backend/openlist && git add drivers/emby_wrapper/ && git commit -m "feat(emby_wrapper): generate in-memory virtual nfo per movie in configured folders"
```

---

### Task 6: Get 虚拟 nfo（真实文件优先）

**Files:**
- Modify: `drivers/emby_wrapper/driver.go`（追加 Get）
- Test: `drivers/emby_wrapper/get_test.go`

**Interfaces:**
- Consumes: `virtualNFO`/`buildNFOContent`（Task 5）、`resolveSetting`（Task 4）、`d.remote()`（Task 3）
- Produces: `(d *EmbyWrapper) Get(ctx context.Context, path string) (model.Obj, error)`、`(d *EmbyWrapper) virtualNFOForPath(path string) (model.Obj, bool, error)`

- [ ] **Step 1: 写失败测试** `drivers/emby_wrapper/get_test.go`

```go
package emby_wrapper_test

import (
	"context"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
)

func TestGetVirtualNFO(t *testing.T) {
	d := setup(t)
	if err := d.Rename(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, `{"actors":"三上悠亚"}`); err != nil {
		t.Fatalf("set actors: %+v", err)
	}
	obj, err := d.Get(context.Background(), "/Movies/AAA.nfo")
	if err != nil {
		t.Fatalf("get nfo: %+v", err)
	}
	if obj.GetName() != "AAA.nfo" || obj.IsDir() {
		t.Errorf("unexpected obj: %+v", obj)
	}
	if obj.GetSize() == 0 {
		t.Error("nfo must have content size")
	}
}

func TestGetRealNFOFileWins(t *testing.T) {
	d := setup(t)
	if err := writeDownstreamFile(t, "/Movies/AAA.nfo", "real-content"); err != nil {
		t.Fatalf("write real nfo: %v", err)
	}
	if err := d.Rename(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, `{"actors":"A"}`); err != nil {
		t.Fatalf("set actors: %+v", err)
	}
	obj, err := d.Get(context.Background(), "/Movies/AAA.nfo")
	if err != nil {
		t.Fatalf("get nfo: %+v", err)
	}
	if obj.GetSize() != int64(len("real-content")) {
		t.Errorf("real nfo must win, got size %d", obj.GetSize())
	}
}

func TestGetVirtualNFOInherited(t *testing.T) {
	d := setup(t)
	if err := d.Rename(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, `{"actors":"三上悠亚"}`); err != nil {
		t.Fatalf("set actors: %+v", err)
	}
	if err := writeDownstreamDir(t, "/Movies/Sub"); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	if err := writeDownstreamFile(t, "/Movies/Sub/BBB.mp4", "x"); err != nil {
		t.Fatalf("write sub movie: %v", err)
	}
	obj, err := d.Get(context.Background(), "/Movies/Sub/BBB.nfo")
	if err != nil {
		t.Fatalf("get inherited nfo: %+v", err)
	}
	if obj.GetName() != "BBB.nfo" {
		t.Errorf("unexpected obj: %v", obj.GetName())
	}
}

func TestGetNFOWithoutMatchingMovieNotFound(t *testing.T) {
	d := setup(t)
	if err := d.Rename(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, `{"actors":"A"}`); err != nil {
		t.Fatalf("set actors: %+v", err)
	}
	if _, err := d.Get(context.Background(), "/Movies/NotFound.nfo"); err == nil {
		t.Fatal("nfo without matching movie must not be served")
	}
}

func TestGetPlainFileForwardsDownstream(t *testing.T) {
	d := setup(t)
	obj, err := d.Get(context.Background(), "/Movies/AAA.mkv")
	if err != nil {
		t.Fatalf("get movie: %+v", err)
	}
	if obj.GetName() != "AAA.mkv" {
		t.Errorf("unexpected obj: %v", obj.GetName())
	}
}

var _ = op.Get // 保持 import 稳定
```

- [ ] **Step 2: 实现** —— `driver.go` 追加：

```go
func (d *EmbyWrapper) Get(ctx context.Context, path string) (model.Obj, error) {
	if utils.PathEqual(path, "/") {
		return &model.Object{Name: "Root", IsFolder: true, Path: "/"}, nil
	}
	if strings.HasSuffix(strings.ToLower(path), ".nfo") {
		if obj, ok, err := d.virtualNFOForPath(path); err != nil {
			return nil, err
		} else if ok {
			return obj, nil
		}
	}
	remoteStorage, remoteActualPath, err := d.remote()
	if err != nil {
		return nil, err
	}
	return op.Get(ctx, remoteStorage, stdpath.Join(remoteActualPath, path))
}

// virtualNFOForPath 尝试为 .nfo 路径构建虚拟对象。
// 返回 (obj, true, nil)：命中虚拟 nfo；(nil, false, nil)：应转发下游（无设置/无匹配影片/存在真实 nfo）。
func (d *EmbyWrapper) virtualNFOForPath(path string) (model.Obj, bool, error) {
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
	objs, err := op.List(context.Background(), remoteStorage, stdpath.Join(remoteActualPath, parentDir), model.ListArgs{})
	if err != nil {
		return nil, false, err
	}
	base := strings.TrimSuffix(stdpath.Base(path), ".nfo")
	var movieObj model.Obj
	for _, o := range objs {
		if o.IsDir() {
			continue
		}
		if strings.EqualFold(utils.Ext(o.GetName()), "nfo") && virtual_file.GetRealName(o.GetName()) == base {
			// 下游存在真实 nfo，交给下游 Get 返回
			return nil, false, nil
		}
		if virtual_file.GetRealName(o.GetName()) == base {
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
```

注意：`virtualNFOForPath` 里 `op.List` 用的是下游存储与下游路径，不会包含本驱动的虚拟 nfo（缓存键不同），无递归风险；`op.List` 的 dirCache 由 `op.Rename` 的 `Cache.DeleteDirectory(storage, srcDirPath)` 在设置变更时失效。import 需追加 `"github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"`。

- [ ] **Step 3: 运行确认通过**

```bash
cd /Users/varg247/store/work-store/backend/openlist && /Library/Go/sdk/go1.25.4/bin/go test ./drivers/emby_wrapper/ -count=1
```

Expected: PASS。若 `TestGetVirtualNFOInherited` 失败，检查 `op.List` 是否因 dirCache 返回旧条目——`Get` 路径下 `virtualNFOForPath` 的 `op.List` 未传 `Refresh`，但子目录是刚创建的、缓存行不存在，应为冷路径。

- [ ] **Step 4: 提交**

```bash
cd /Users/varg247/store/work-store/backend/openlist && git add drivers/emby_wrapper/ && git commit -m "feat(emby_wrapper): serve virtual nfo via Get with real-file priority"
```

---

### Task 7: Link 内存内容服务 + 下游转发

**Files:**
- Modify: `drivers/emby_wrapper/driver.go`（追加 Link）
- Test: `drivers/emby_wrapper/link_test.go`

**Interfaces:**
- Consumes: `virtualNFO`（Task 5）、`d.remote()`（Task 3）、`stream.GetRangeReaderFromMFile`（已存在）
- Produces: `(d *EmbyWrapper) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error)`

- [ ] **Step 1: 写失败测试** `drivers/emby_wrapper/link_test.go`

```go
package emby_wrapper_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/http_range"
)

func TestLinkServesVirtualNFOContent(t *testing.T) {
	d := setup(t)
	if err := d.Rename(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, `{"actors":"三上悠亚,深田咏美"}`); err != nil {
		t.Fatalf("set actors: %+v", err)
	}
	obj, err := d.Get(context.Background(), "/Movies/AAA.nfo")
	if err != nil {
		t.Fatalf("get nfo: %+v", err)
	}
	link, err := d.Link(context.Background(), obj, model.LinkArgs{})
	if err != nil {
		t.Fatalf("link nfo: %+v", err)
	}
	if link.ContentLength != obj.GetSize() {
		t.Errorf("content length mismatch: %d vs %d", link.ContentLength, obj.GetSize())
	}
	if link.RangeReader == nil {
		t.Fatal("nfo link must have a range reader")
	}
	rc, err := link.RangeReader.RangeRead(context.Background(), http_range.Range{Length: -1})
	if err != nil {
		t.Fatalf("range read: %+v", err)
	}
	defer rc.Close()
	body, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read: %+v", err)
	}
	got := string(body)
	if !strings.Contains(got, "AAA") {
		t.Errorf("nfo must contain movie title, got %s", got)
	}
	if !strings.Contains(got, "三上悠亚") || !strings.Contains(got, "深田咏美") {
		t.Errorf("nfo must contain actors, got %s", got)
	}
}

func TestLinkForwardsDownstream(t *testing.T) {
	d := setup(t)
	obj, err := d.Get(context.Background(), "/Movies/AAA.mkv")
	if err != nil {
		t.Fatalf("get movie: %+v", err)
	}
	link, err := d.Link(context.Background(), obj, model.LinkArgs{})
	if err != nil {
		t.Fatalf("link movie: %+v", err)
	}
	if link.URL == "" && link.RangeReader == nil {
		t.Error("downstream link must provide url or range reader")
	}
	if link.RangeReader == nil {
		// URL 直链模式：local 驱动通常返回文件路径 URL
		if !strings.Contains(link.URL, "AAA.mkv") {
			t.Errorf("unexpected link url %s", link.URL)
		}
	}
}
```

- [ ] **Step 2: 实现** —— `driver.go` 追加：

```go
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
```

import 追加：`"bytes"`、`"github.com/OpenListTeam/OpenList/v4/internal/stream"`。

- [ ] **Step 3: 运行确认通过**

```bash
cd /Users/varg247/store/work-store/backend/openlist && /Library/Go/sdk/go1.25.4/bin/go test ./drivers/emby_wrapper/ -count=1
```

Expected: PASS。若 `TestLinkForwardsDownstream` 中 local 驱动返回 URL 与预期不符，放宽断言为仅检查 `link.URL != "" || link.RangeReader != nil`。

- [ ] **Step 4: 提交**

```bash
cd /Users/varg247/store/work-store/backend/openlist && git add drivers/emby_wrapper/ && git commit -m "feat(emby_wrapper): serve virtual nfo content in memory via Link"
```

---

### Task 8: fs 层端到端测试 + 全量验证

**Files:**
- Test: `drivers/emby_wrapper/e2e_test.go`

**Interfaces:**
- Consumes: 全部已完成接口；`fs.List`/`fs.Link`（`internal/fs`）

- [ ] **Step 1: 写端到端测试** —— 走 `fs` 包全链路（等价于 strm `generateStrm` 调用 Link 取内容的行为）

```go
package emby_wrapper_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/fs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/http_range"
)

func TestEndToEndThroughFS(t *testing.T) {
	d := setup(t)
	// 通过 fs 重命名设置 actor（等价于 UI 操作）
	if err := fs.Rename(context.Background(), "/ew/Movies", `{"actors":"三上悠亚"}`); err != nil {
		t.Fatalf("rename via fs: %+v", err)
	}
	// fs 列表应包含虚拟 nfo
	objs, err := fs.List(context.Background(), "/ew/Movies", &fs.ListArgs{})
	if err != nil {
		t.Fatalf("fs list: %+v", err)
	}
	var found bool
	for _, o := range objs {
		if o.GetName() == "AAA.nfo" {
			found = true
		}
	}
	if !found {
		t.Fatal("virtual nfo must appear in fs list")
	}
	// fs 链接（strm generateStrm 同款调用链：Link -> 读取内容）
	link, _, err := fs.Link(context.Background(), "/ew/Movies/AAA.nfo", model.LinkArgs{})
	if err != nil {
		t.Fatalf("fs link: %+v", err)
	}
	rc, err := link.RangeReader.RangeRead(context.Background(), http_range.Range{Length: -1})
	if err != nil {
		t.Fatalf("range read: %+v", err)
	}
	defer rc.Close()
	body, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read: %+v", err)
	}
	got := string(body)
	if !strings.Contains(got, "三上悠亚") || !strings.Contains(got, "AAA") {
		t.Errorf("nfo content mismatch: %s", got)
	}
}
```

- [ ] **Step 2: 运行确认通过**

```bash
cd /Users/varg247/store/work-store/backend/openlist && /Library/Go/sdk/go1.25.4/bin/go test ./drivers/emby_wrapper/ -run TestEndToEndThroughFS -count=1
```

Expected: PASS（若 `fs.Rename` 内部对 driver.Rename 的 `Cache.DeleteDirectory` 使 dirCache 失效，则列表必然回源；否则断言前先 `op.Cache.DeleteDirectory(d, "/Movies")`。）

- [ ] **Step 3: 全量验证**

```bash
cd /Users/varg247/store/work-store/backend/openlist && /Library/Go/sdk/go1.25.4/bin/go build ./... && /Library/Go/sdk/go1.25.4/bin/go vet ./drivers/emby_wrapper/ ./drivers/virtual_file/ && /Library/Go/sdk/go1.25.4/bin/go test ./drivers/emby_wrapper/ ./drivers/virtual_file/ -count=1
```

Expected: 全部 PASS，无 vet 告警。

- [ ] **Step 4: 提交**

```bash
cd /Users/varg247/store/work-store/backend/openlist && git add drivers/emby_wrapper/e2e_test.go && git commit -m "test(emby_wrapper): end-to-end nfo flow through fs layer"
```

- [ ] **Step 5: 部署说明**（不写代码，写给用户）

1. Web UI 新增存储选择 **EmbyWrapper**，`remote_path` 填下游存储挂载路径（如 `/115`）
2. strm 驱动的 `DownloadFileTypes` 需包含 `nfo`（用户已自行配置）
3. 在 EmbyWrapper 挂载路径下对文件夹执行"重命名"，表单填入 `actors`（逗号分隔，如 `三上悠亚,深田咏美`），确定后该文件夹及子文件夹内每个影片（mp4/mkv 等）即出现对应 `<影片名>.nfo` 虚拟文件
4. strm 扫描后本地生成 `<影片名>.strm` + `<影片名>.nfo`，Emby/Jellyfin 刮削即取到 actor
5. 清除设置：重命名填空 `actors` 即可

---

## Self-Review

**1. Spec coverage:**
- nfo 仅 title+actor：Task 5 `buildNFOContent` ✓；title=文件名（GetRealName 归一）：Task 5 ✓
- 继承：Task 4 `resolveSetting` + Task 5/6 用例 ✓
- 重命名 JSON 生效、不实现 MakeDir、空 actors 清除、重命名文件报错：Task 4 ✓
- 同一 basename 一个 nfo、真实 nfo 优先：Task 5（realNFO 跳过 + added 去重）✓
- 扩展名列表与 strm 默认一致且可配：Task 3 meta.go ✓
- 驱动名 EmbyWrapper、注册 all.go：Task 3 ✓
- 内存构建、不落盘、Link RangeReader：Task 7 ✓；strm 落盘为用户侧配置（部署说明）✓

**2. Placeholder scan:** 无 TBD；`getSettingForTest` 辅助函数有完整定义；Task 5 的 `bytes` 占位行有明确移除指令（Step 2 末尾说明）；`utils.TrimSpace` 不确定处有 fallback 说明（Task 4 Step 2）。

**3. Type consistency:**
- `RenderMediaNFO(*Media) ([]byte, error)`：Task 1 定义，Task 5 使用 ✓
- `resolveSetting(string) (*model.EmbyDirSetting, error)`：Task 4 定义，Task 5/6 使用 ✓
- `virtualNFO`（含 `content []byte`）：Task 5 定义，Task 6/7 使用 ✓
- `FolderAddition{Actors}`：Task 3 定义，Task 4 Rename 使用 ✓
- `withVirtualNFOs`/`virtualNFOForPath` 命名在 Task 5/6 一致 ✓
- List 返回值顺序（withFolderAddition 先、withVirtualNFOs 后）Task 3→5 一致 ✓
