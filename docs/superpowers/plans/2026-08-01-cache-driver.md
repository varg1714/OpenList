# Cache 驱动（挂载型列表持久化缓存）实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 新增 `Cache` 驱动，挂载其他存储（remote_path），将下游文件列表快照持久化到数据库，浏览时零下游访问，Link 恒转发下游。

**Architecture:** 参照 `drivers/chunk` 的转发模式（`op.GetStorageAndActualPath` + `op.List/op.Get/op.Link`）。每目录一行 `CacheList`（JSON 快照，整行 upsert 覆盖）；`List` 命中 DB 即返回（不判 TTL），miss 回源并写 DB；`Get` 实现 `Getter` 查父目录快照；`Link` 纯转发；`pkg/cron` 定时任务按 TTL 刷新过期目录。

**Tech Stack:** Go 1.25（GOROOT: `/Library/Go/sdk/go1.25.4`）、GORM（sqlite 内存测试）、`pkg/cron`、`internal/op`、`internal/db`、`pkg/utils`

## Global Constraints

- 所有 Go 命令用 `/Library/Go/sdk/go1.25.4/bin/go`（不在 PATH 上）
- 只缓存文件列表，不缓存文件内容；Link 恒转发下游
- 快照只含基础字段（`CachedObj`），不暴露驱动特有类型
- `Drop()` 不删除 DB 记录，仅 `cron.Stop()`
- 删除 CacheList 行的唯一时机：定时任务回源失败/目录已消失
- 代码不添加注释（除非必要），遵循项目现有风格（chunk 驱动模式）
- TDD：每任务先写失败测试（红），再实现（绿），提交

---

### Task 1: CacheList 模型 + AutoMigrate 注册

**Files:**
- Create: `internal/model/cache.go`
- Modify: `internal/db/db.go:14`
- Test: `internal/model/cache_test.go`

**Interfaces:**
- Consumes: `internal/db.AutoMigrate(dst ...interface{}) error`（db.go:21）
- Produces:
  - `model.CachedObj` struct（字段：`ID string`、`Path string`、`Name string`、`Size int64`、`Modified/Ctime time.Time`、`IsFolder bool`、`HashInfo map[string]string`（hash 类型名→值，序列化安全）、`Thumbnail string`）
  - `model.CacheList` struct（字段：`ID uint`、`StorageID uint`、`DirPath string`、`Data []CachedObj`、`UpdatedAt time.Time`；`Data` 用 `gorm:"type:json;serializer:json"`，DB 直接存 JSON 对象——项目先例 internal/model/film.go:89 `gorm:"type:json;serializer:json"`）

- [ ] **Step 1: Write the failing test**

`internal/model/cache_test.go`（参照 `internal/op/storage_test.go:17-24` 的 init 模式）：

```go
package model_test

import (
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
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
	conf.Conf = conf.DefaultConfig("data")
	db.Init(dB)
}

func TestCacheListModel(t *testing.T) {
	item := model.CacheList{
		StorageID: 1,
		DirPath:   "/dir",
		Data: []model.CachedObj{
			{Name: "a.txt", Size: 10, HashInfo: map[string]string{"sha1": "abc"}},
			{Name: "b", IsFolder: true, Thumbnail: "https://example.com/t.jpg"},
		},
		UpdatedAt: time.Now(),
	}
	if err := db.GetDb().Create(&item).Error; err != nil {
		t.Fatalf("failed to create: %+v", err)
	}
	var got model.CacheList
	if err := db.GetDb().Where("storage_id = ? AND dir_path = ?", 1, "/dir").First(&got).Error; err != nil {
		t.Fatalf("failed to find: %+v", err)
	}
	if len(got.Data) != 2 {
		t.Fatalf("expected 2 data entries, got %d", len(got.Data))
	}
	if got.Data[0].Name != "a.txt" || got.Data[0].HashInfo["sha1"] != "abc" {
		t.Errorf("data[0] mismatch: %+v", got.Data[0])
	}
	if !got.Data[1].IsFolder || got.Data[1].Thumbnail != "https://example.com/t.jpg" {
		t.Errorf("data[1] mismatch: %+v", got.Data[1])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./internal/model/ -run TestCacheListModel -v`
Expected: FAIL — 编译错误 `undefined: model.CacheList`

- [ ] **Step 3: Write minimal implementation**

`internal/model/cache.go`：

```go
package model

import "time"

type CachedObj struct {
	ID        string
	Path      string
	Name      string
	Size      int64
	Modified  time.Time
	Ctime     time.Time
	IsFolder  bool
	HashInfo  map[string]string // hash type name -> hash value
	Thumbnail string
}

type CacheList struct {
	ID        uint        `gorm:"primaryKey"`
	StorageID uint        `gorm:"uniqueIndex:idx_cache_storage_dir"`
	DirPath   string      `gorm:"uniqueIndex:idx_cache_storage_dir"`
	Data      []CachedObj `gorm:"type:json;serializer:json"`
	UpdatedAt time.Time
}
```

`internal/db/db.go:14` 的 `AutoMigrate` 参数列表末尾追加 `new(model.CacheList)`：

```go
err := AutoMigrate(new(model.Storage), new(model.User), new(model.Meta), new(model.SettingItem), new(model.SearchNode), new(model.Film), new(model.MissedFilm), new(model.MagnetCache), new(model.Actor), new(model.VirtualFile), new(model.Replacement), new(model.TaskItem), new(model.SSHPublicKey), new(model.MovedItem), new(model.SharingDB), new(model.FilmWork), new(model.FilmFile), new(model.SourceMagnet), new(model.CacheList))
```

- [ ] **Step 4: Run test to verify it passes**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./internal/model/ -run TestCacheListModel -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/model/cache.go internal/model/cache_test.go internal/db/db.go
git commit -m "feat(model): add CacheList model for cache driver"
```

---

### Task 2: 快照转换（CachedObj ↔ model.Obj）

**Files:**
- Create: `drivers/cache/snapshot.go`
- Test: `drivers/cache/snapshot_test.go`

**Interfaces:**
- Consumes: `model.Obj`（obj.go:22）、`model.Object`、`model.ObjThumb`、`model.GetThumb`（obj.go:162）、`model.CachedObj`（Task 1）、`utils.HashInfo`、`utils.HashType`、`utils.GetHashByName`（hash.go:60）、`utils.NewHashInfoByMap`（hash.go:193）
- Produces:
  - `func toCachedObj(dirPath string, obj model.Obj) model.CachedObj` — Path 统一为 `stdpath.Join(dirPath, obj.GetName())`；HashInfo 通过 `obj.GetHash().Export()` 转为 `map[string]string`（hash 类型名→值；`utils.HashInfo` 的 JSON 序列化输出 `{}`，不可直接持久化，必须在快照层转换）
  - `func fromCachedObj(c model.CachedObj) model.Obj` — Thumbnail 非空 → `*model.ObjThumb`，否则 → `*model.Object`；HashInfo 通过 `utils.GetHashByName` 逐个恢复为 `utils.NewHashInfoByMap`

- [ ] **Step 1: Write the failing test**

`drivers/cache/snapshot_test.go`（`package cache`，与实现同包以便测试未导出函数；`utils.HashInfo` 用法见 `drivers/115_open/driver.go:223`，用 `utils.NewHashInfoByMap` 构造）：

```go
package cache

import (
	"strings"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

func TestToCachedObjPlain(t *testing.T) {
	obj := &model.Object{
		ID:       "id1",
		Name:     "a.txt",
		Size:     10,
		Modified: time.Unix(100, 0),
		IsFolder: false,
	}
	c := toCachedObj("/dir", obj)
	if c.Path != "/dir/a.txt" {
		t.Errorf("expected path /dir/a.txt, got %s", c.Path)
	}
	if c.Name != "a.txt" || c.Size != 10 || c.IsFolder {
		t.Errorf("bad snapshot: %+v", c)
	}
	if c.Thumbnail != "" {
		t.Errorf("expected empty thumbnail, got %s", c.Thumbnail)
	}
}

func TestToCachedObjWithThumb(t *testing.T) {
	obj := &model.ObjThumb{
		Object:    model.Object{Name: "b.jpg", Size: 5},
		Thumbnail: model.Thumbnail{Thumbnail: "https://example.com/thumb.jpg"},
	}
	c := toCachedObj("/", obj)
	if c.Thumbnail != "https://example.com/thumb.jpg" {
		t.Errorf("expected thumbnail, got %s", c.Thumbnail)
	}
}

func TestFromCachedObjRoundTrip(t *testing.T) {
	c := model.CachedObj{
		ID:        "id1",
		Path:      "/dir/a.txt",
		Name:      "a.txt",
		Size:      10,
		Modified:  time.Unix(100, 0),
		Ctime:     time.Unix(50, 0),
		IsFolder:  false,
		HashInfo:  map[string]string{"sha1": "abc"},
		Thumbnail: "https://example.com/thumb.jpg",
	}
	obj := fromCachedObj(c)
	if _, ok := obj.(*model.ObjThumb); !ok {
		t.Fatalf("expected *model.ObjThumb, got %T", obj)
	}
	if obj.GetName() != "a.txt" || obj.GetSize() != 10 || obj.GetPath() != "/dir/a.txt" {
		t.Errorf("round trip mismatch: %+v", obj)
	}
	if obj.GetHash().GetHash(utils.SHA1) != "abc" {
		t.Errorf("hash mismatch")
	}
	if obj.CreateTime().Unix() != 50 {
		t.Errorf("ctime mismatch")
	}
}

func TestFromCachedObjNoThumb(t *testing.T) {
	obj := fromCachedObj(model.CachedObj{Name: "x.txt", IsFolder: true})
	if _, ok := obj.(*model.Object); !ok {
		t.Fatalf("expected *model.Object, got %T", obj)
	}
	if !obj.IsDir() {
		t.Errorf("expected folder")
	}
}

func TestHashRoundTrip(t *testing.T) {
	obj := &model.Object{
		Name:     "h.txt",
		HashInfo: utils.NewHashInfoByMap(map[*utils.HashType]string{utils.SHA1: "abc", utils.MD5: "def"}),
	}
	c := toCachedObj("/", obj)
	if c.HashInfo["sha1"] != "abc" || c.HashInfo["md5"] != "def" {
		t.Errorf("hash not exported: %+v", c.HashInfo)
	}
	obj2 := fromCachedObj(c)
	if obj2.GetHash().GetHash(utils.SHA1) != "abc" || obj2.GetHash().GetHash(utils.MD5) != "def" {
		t.Errorf("hash not restored: %s %s", obj2.GetHash().GetHash(utils.SHA1), obj2.GetHash().GetHash(utils.MD5))
	}
}

func TestSpecialCharsName(t *testing.T) {
	name := strings.Repeat("很", 50) + ".txt"
	c := toCachedObj("/", &model.Object{Name: name})
	if c.Name != name {
		t.Errorf("special chars corrupted: %q", c.Name)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/cache/ -run 'TestToCachedObj|TestFromCachedObj|TestHashRoundTrip|TestSpecialCharsName' -v`
Expected: FAIL — `drivers/cache` 目录不存在，`no such file or directory`

- [ ] **Step 3: Write minimal implementation**

`drivers/cache/snapshot.go`：

```go
package cache

import (
	stdpath "path"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

func toCachedObj(dirPath string, obj model.Obj) model.CachedObj {
	c := model.CachedObj{
		ID:       obj.GetID(),
		Path:     stdpath.Join(dirPath, obj.GetName()),
		Name:     obj.GetName(),
		Size:     obj.GetSize(),
		Modified: obj.ModTime(),
		Ctime:    obj.CreateTime(),
		IsFolder: obj.IsDir(),
	}
	if hi := obj.GetHash().Export(); len(hi) > 0 {
		hashMap := make(map[string]string, len(hi))
		for ht, v := range hi {
			hashMap[ht.Name] = v
		}
		c.HashInfo = hashMap
	}
	if thumb, ok := model.GetThumb(obj); ok {
		c.Thumbnail = thumb
	}
	return c
}

func fromCachedObj(c model.CachedObj) model.Obj {
	obj := model.Object{
		ID:       c.ID,
		Path:     c.Path,
		Name:     c.Name,
		Size:     c.Size,
		Modified: c.Modified,
		Ctime:    c.Ctime,
		IsFolder: c.IsFolder,
	}
	if len(c.HashInfo) > 0 {
		hi := make(map[*utils.HashType]string, len(c.HashInfo))
		for name, v := range c.HashInfo {
			if ht, ok := utils.GetHashByName(name); ok {
				hi[ht] = v
			}
		}
		obj.HashInfo = utils.NewHashInfoByMap(hi)
	}
	if c.Thumbnail != "" {
		return &model.ObjThumb{
			Object:    obj,
			Thumbnail: model.Thumbnail{Thumbnail: c.Thumbnail},
		}
	}
	return &obj
}
```

注意：`utils.HashInfo.Export()` 返回 `map[*utils.HashType]string`（hash.go:232），遍历取 `ht.Name` 作键；`utils.GetHashByName` 按名字恢复（hash.go:60）。本任务不再需要 marshal/unmarshal 函数——DB 层直接存取 `[]model.CachedObj`（GORM serializer:json）。

- [ ] **Step 4: Run test to verify it passes**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/cache/ -run 'TestToCachedObj|TestFromCachedObj|TestMarshal|TestUnmarshal' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add drivers/cache/snapshot.go drivers/cache/snapshot_test.go
git commit -m "feat(cache): add CachedObj snapshot conversion"
```

---

### Task 3: CacheList 数据库读写

**Files:**
- Create: `drivers/cache/db.go`
- Test: `drivers/cache/db_test.go`

**Interfaces:**
- Consumes: `db.GetDb() *gorm.DB`（internal/db/db.go:31）、`model.CacheList`（Task 1）、`gorm.io/gorm.ErrRecordNotFound`
- Produces:
  - `func GetCacheList(storageID uint, dirPath string) (*model.CacheList, error)` — 未找到返回 `(nil, nil)`
  - `func UpsertCacheList(storageID uint, dirPath string, data []model.CachedObj) error` — 存在则更新 Data/UpdatedAt，不存在则创建
  - `func DeleteCacheList(storageID uint, dirPath string) error`
  - `func ListCacheLists(storageID uint) ([]model.CacheList, error)`

- [ ] **Step 1: Write the failing test**

`drivers/cache/db_test.go`（`package cache`；init 复用 Task 1 的 sqlite 模式）：

```go
package cache

import (
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
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

func TestGetCacheListNotFound(t *testing.T) {
	item, err := GetCacheList(99, "/nope")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if item != nil {
		t.Errorf("expected nil, got %+v", item)
	}
}

func TestUpsertCreateThenUpdate(t *testing.T) {
	if err := UpsertCacheList(1, "/dir", []model.CachedObj{{Name: "a"}}); err != nil {
		t.Fatalf("create: %v", err)
	}
	item, err := GetCacheList(1, "/dir")
	if err != nil || item == nil {
		t.Fatalf("get after create: %v %+v", err, item)
	}
	if len(item.Data) != 1 || item.Data[0].Name != "a" {
		t.Errorf("expected a, got %+v", item.Data)
	}
	first := item.UpdatedAt

	time.Sleep(2 * time.Millisecond)
	if err := UpsertCacheList(1, "/dir", []model.CachedObj{{Name: "b"}}); err != nil {
		t.Fatalf("update: %v", err)
	}
	item, err = GetCacheList(1, "/dir")
	if err != nil || item == nil {
		t.Fatalf("get after update: %v %+v", err, item)
	}
	if len(item.Data) != 1 || item.Data[0].Name != "b" {
		t.Errorf("expected b, got %+v", item.Data)
	}
	if !item.UpdatedAt.After(first) {
		t.Errorf("expected UpdatedAt refreshed")
	}
	var count int64
	if err := db.GetDb().Model(&model.CacheList{}).Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 row after upsert, got %d", count)
	}
}

func TestStorageIsolation(t *testing.T) {
	_ = UpsertCacheList(1, "/dir", "[1]")
	_ = UpsertCacheList(2, "/dir", []model.CachedObj{{Name: "2"}})
	item, err := GetCacheList(1, "/dir")
	if err != nil || item == nil {
		t.Fatalf("storage1: %v %+v", err, item)
	}
	if len(item.Data) != 1 || item.Data[0].Name != "1" {
		t.Errorf("storage1 polluted: %+v", item.Data)
	}
}

func TestDeleteCacheList(t *testing.T) {
	_ = UpsertCacheList(3, "/dir", []model.CachedObj{{Name: "a"}})
	if err := DeleteCacheList(3, "/dir"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	item, err := GetCacheList(3, "/dir")
	if err != nil || item != nil {
		t.Errorf("expected nil after delete, got %v %v", item, err)
	}
}

func TestListCacheLists(t *testing.T) {
	_ = UpsertCacheList(4, "/a", []model.CachedObj{{Name: "1"}})
	_ = UpsertCacheList(4, "/b", []model.CachedObj{{Name: "2"}})
	_ = UpsertCacheList(5, "/c", []model.CachedObj{{Name: "3"}})
	rows, err := ListCacheLists(4)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("expected 2 rows for storage 4, got %d", len(rows))
	}
}
```

注意：db_test.go 需 `import "github.com/OpenListTeam/OpenList/v4/internal/model"`（上面代码块中 `model.CacheList` 使用处）。

- [ ] **Step 2: Run test to verify it fails**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/cache/ -run 'TestGetCacheList|TestUpsert|TestStorageIsolation|TestDeleteCacheList|TestListCacheLists' -v`
Expected: FAIL — 编译错误 `undefined: GetCacheList`

- [ ] **Step 3: Write minimal implementation**

`drivers/cache/db.go`：

```go
package cache

import (
	"errors"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"gorm.io/gorm"
)

func GetCacheList(storageID uint, dirPath string) (*model.CacheList, error) {
	var item model.CacheList
	err := db.GetDb().Where("storage_id = ? AND dir_path = ?", storageID, dirPath).First(&item).Error
	if err == nil {
		return &item, nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return nil, err
}

func UpsertCacheList(storageID uint, dirPath string, data []model.CachedObj) error {
	item, err := GetCacheList(storageID, dirPath)
	if err != nil {
		return err
	}
	if item != nil {
		return db.GetDb().Model(&model.CacheList{}).Where("id = ?", item.ID).Updates(map[string]any{
			"data":       data,
			"updated_at": time.Now(),
		}).Error
	}
	return db.GetDb().Create(&model.CacheList{
		StorageID: storageID,
		DirPath:   dirPath,
		Data:      data,
		UpdatedAt: time.Now(),
	}).Error
}

func DeleteCacheList(storageID uint, dirPath string) error {
	return db.GetDb().Where("storage_id = ? AND dir_path = ?", storageID, dirPath).Delete(&model.CacheList{}).Error
}

func ListCacheLists(storageID uint) ([]model.CacheList, error) {
	var rows []model.CacheList
	err := db.GetDb().Where("storage_id = ?", storageID).Find(&rows).Error
	return rows, err
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/cache/ -v`
Expected: PASS（Task 2 的测试也一起通过）

- [ ] **Step 5: Commit**

```bash
git add drivers/cache/db.go drivers/cache/db_test.go
git commit -m "feat(cache): add CacheList db read/write"
```

---

### Task 4: 驱动主体（meta.go + driver.go：Init/Drop/List/Get/Link）

**Files:**
- Create: `drivers/cache/meta.go`
- Create: `drivers/cache/driver.go`
- Test: `drivers/cache/driver_test.go`

**Interfaces:**
- Consumes: `op.GetStorageAndActualPath(rawPath)`（op/path.go:16）、`op.List(ctx, storage, path, args)`、`op.Get(ctx, storage, path)`、`op.Link(ctx, storage, path, args) (*model.Link, model.Obj, error)`、`op.RegisterDriver`（op/driver.go:18）、`utils.NewSyncClosers`、Task 2/3 的快照与 db 函数
- Produces: `Cache` struct（embed `model.Storage` + `Addition`）、`Addition{RemotePath string; TTLHours int; SyncIntervalHours int}`、驱动名 `"Cache"`（meta.go 中 `op.RegisterDriver`）
- `Cache` 方法签名：`Config() driver.Config`、`GetAddition() driver.Additional`、`Init(ctx) error`、`Drop(ctx) error`、`Get(ctx, path string) (model.Obj, error)`、`List(ctx, dir model.Obj, args model.ListArgs) ([]model.Obj, error)`、`Link(ctx, file model.Obj, args model.LinkArgs) (*model.Link, error)`
- config: `driver.Config{Name: "Cache", LocalSort: true, NoUpload: true, DefaultRoot: "/"}`

- [ ] **Step 1: Write the failing test**

`drivers/cache/driver_test.go`（`package cache_test`；用 `Local` 驱动作下游 mock：`op.CreateStorage` + `root_folder_path=t.TempDir()`；初始化模式同 storage_test.go）：

```go
package cache_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/drivers/cache"
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

func setup(t *testing.T) *cache.Cache {
	t.Helper()
	tmp := t.TempDir()
	_ = os.MkdirAll(filepath.Join(tmp, "sub"), 0o755)
	_ = os.WriteFile(filepath.Join(tmp, "a.txt"), []byte("hello"), 0o644)
	_ = os.WriteFile(filepath.Join(tmp, "sub", "b.txt"), []byte("world"), 0o644)
	_, err := op.CreateStorage(context.Background(), model.Storage{
		Driver:   "Local",
		MountPath: "/local",
		Addition: fmt.Sprintf(`{"root_folder_path":%q}`, tmp),
	})
	if err != nil {
		t.Fatalf("create local storage: %+v", err)
	}
	d, err := op.CreateStorage(context.Background(), model.Storage{
		Driver:   "Cache",
		MountPath: "/cache",
		Addition: `{"remote_path":"/local","ttl_hours":24,"sync_interval_hours":0}`,
	})
	if err != nil {
		t.Fatalf("create cache storage: %+v", err)
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
	item, err := db.GetCacheList(d.ID, "/")
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
	objs, err = d.List(context.Background(), rootDir(), model.ListArgs{})
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
	if l.URL == "" {
		t.Errorf("expected link url, got empty")
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
	_, actual, err := op.GetStorageAndActualPath(d.RemotePath)
	if err != nil {
		panic(err)
	}
	return actual
}

func mustLocalStorageID(d *cache.Cache) uint {
	storage, _, err := op.GetStorageAndActualPath(d.RemotePath)
	if err != nil {
		panic(err)
	}
	return storage.GetStorage().ID
}
```

注意：
- `mustRootPath(d)` 返回下游实际根路径（Local 的 `root_folder_path`），文件操作直接基于它
- 测试文件需注册 `Local` 驱动：**测试文件顶部追加** `_ "github.com/OpenListTeam/OpenList/v4/drivers/local"`。cache 驱动自身通过 `"github.com/OpenListTeam/OpenList/v4/drivers/cache"` 导入触发注册。

- [ ] **Step 2: Run test to verify it fails**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/cache/ -run 'TestList|TestGet|TestLink' -v`
Expected: FAIL — 编译错误 `undefined: cache.Cache`（driver.go 不存在）

- [ ] **Step 3: Write minimal implementation**

`drivers/cache/meta.go`：

```go
package cache

import (
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
)

type Addition struct {
	RemotePath        string `json:"remote_path" required:"true" help:"the mount path of the downstream storage"`
	TTLHours          int    `json:"ttl_hours" required:"true" type:"number" default:"24" help:"cache validity period in hours"`
	SyncIntervalHours int    `json:"sync_interval_hours" required:"true" type:"number" default:"1" help:"background sync interval in hours, 0 to disable"`
}

var config = driver.Config{
	Name:        "Cache",
	LocalSort:   true,
	NoUpload:    true,
	DefaultRoot: "/",
}

func init() {
	op.RegisterDriver(func() driver.Driver {
		return &Cache{
			Addition: Addition{
				TTLHours:          24,
				SyncIntervalHours: 1,
			},
		}
	})
}
```

`drivers/cache/driver.go`：

```go
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
	return config
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

func (d *Cache) remote() (driver.Driver, string, error) {
	return op.GetStorageAndActualPath(d.RemotePath)
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
```

提示：`op.DeleteStorageById`（internal/op/storage.go:260）已确认存在，测试中直接使用。

- [ ] **Step 4: Run test to verify it passes**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/cache/ -v`
Expected: PASS（含 Task 2/3 测试）

- [ ] **Step 5: Commit**

```bash
git add drivers/cache/meta.go drivers/cache/driver.go drivers/cache/driver_test.go
git commit -m "feat(cache): add Cache driver with list cache and link forwarding"
```

---

### Task 5: 定时同步（sync.go）

**Files:**
- Create: `drivers/cache/sync.go`
- Test: `drivers/cache/sync_test.go`

**Interfaces:**
- Consumes: `op.List`、`op.GetStorageAndActualPath`、Task 2/3 快照与 db 函数、Task 4 的 `d.ID`/`d.RemotePath`/`d.TTLHours`
- Produces: `func (d *Cache) syncAll()` — 供 Task 4 `Init` 的 `cron.Do(d.syncAll)` 调用

- [ ] **Step 1: Write the failing test**

`drivers/cache/sync_test.go`（`package cache_test`，复用 driver_test.go 的 init/setup/helpers；导入 model、db、time）：

```go
package cache_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

func TestSyncAllRefreshesExpired(t *testing.T) {
	d := setup(t)
	_, _ = d.List(context.Background(), rootDir(), model.ListArgs{})

	root := mustRootPath(d)
	_ = os.WriteFile(filepath.Join(root, "new.txt"), []byte("x"), 0o644)
	_ = os.Remove(filepath.Join(root, "a.txt"))
	_ = os.MkdirAll(filepath.Join(root, "newdir"), 0o755)

	item, err := db.GetCacheList(d.ID, "/")
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

func TestSyncAllDeletesRowOnFailure(t *testing.T) {
	d := setup(t)
	_, _ = d.List(context.Background(), rootDir(), model.ListArgs{})
	_ = op.DeleteStorageById(mustLocalStorageID(d))

	item, err := db.GetCacheList(d.ID, "/")
	if err != nil || item == nil {
		t.Fatalf("get cache row: %v %v", item, err)
	}
	if err := db.GetDb().Model(&model.CacheList{}).Where("id = ?", item.ID).Update("updated_at", time.Now().Add(-48*time.Hour)).Error; err != nil {
		t.Fatalf("age row: %v", err)
	}

	d.syncAll()

	row, err := db.GetCacheList(d.ID, "/")
	if err != nil || row != nil {
		t.Errorf("expected row deleted after sync failure, got %v %v", row, err)
	}
}
```

注意：`TestSyncAllDeletesRowOnFailure` 需要 import `op`（已在 driver_test.go 中，但 sync_test.go 独立文件需自行 import `"github.com/OpenListTeam/OpenList/v4/internal/op"`）。

- [ ] **Step 2: Run test to verify it fails**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/cache/ -run TestSyncAll -v`
Expected: FAIL — 编译错误 `d.syncAll undefined`

- [ ] **Step 3: Write minimal implementation**

`drivers/cache/sync.go`：

```go
package cache

import (
	"context"
	stdpath "path"
	"sort"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	log "github.com/sirupsen/logrus"
)

func dirDepth(dirPath string) int {
	if dirPath == "/" {
		return 0
	}
	return strings.Count(strings.Trim(dirPath, "/"), "/") + 1
}

func (d *Cache) syncAll() {
	rows, err := ListCacheLists(d.ID)
	if err != nil {
		log.Errorf("cache: list rows: %+v", err)
		return
	}
	if len(rows) == 0 {
		return
	}
	sort.Slice(rows, func(i, j int) bool {
		return dirDepth(rows[i].DirPath) < dirDepth(rows[j].DirPath)
	})
	ttl := time.Duration(d.TTLHours) * time.Hour
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	known := make(map[string]bool, len(rows)*2)
	for i := range rows {
		known[rows[i].DirPath] = true
	}
	queue := make([]string, 0)
	for i := range rows {
		if time.Since(rows[i].UpdatedAt) >= ttl {
			queue = append(queue, rows[i].DirPath)
		}
	}
	ctx := context.Background()
	for len(queue) > 0 {
		dirPath := queue[0]
		queue = queue[1:]
		remoteStorage, remoteActualPath, err := op.GetStorageAndActualPath(d.RemotePath)
		if err != nil {
			continue
		}
		objs, err := op.List(ctx, remoteStorage, stdpath.Join(remoteActualPath, dirPath), model.ListArgs{})
		if err != nil {
			log.Errorf("cache: sync %s: %+v, drop row", dirPath, err)
			_ = DeleteCacheList(d.ID, dirPath)
			continue
		}
		snaps := make([]model.CachedObj, 0, len(objs))
		for _, o := range objs {
			snaps = append(snaps, toCachedObj(dirPath, o))
		}
		if err := UpsertCacheList(d.ID, dirPath, snaps); err != nil {
			log.Errorf("cache: sync upsert %s: %+v", dirPath, err)
		}
		for i := range snaps {
			if snaps[i].IsFolder {
				child := stdpath.Join(dirPath, snaps[i].Name)
				if !known[child] {
					known[child] = true
					queue = append(queue, child)
				}
			}
		}
	}
}
```

注意：`sync.go` 不使用 `utils`，最终 import 集为：`context`、`stdpath "path"`、`sort`、`strings`、`time`、`model`、`op`、`log`（上面代码块已含）。

- [ ] **Step 4: Run test to verify it passes**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/cache/ -v`
Expected: PASS（全部测试）

- [ ] **Step 5: Commit**

```bash
git add drivers/cache/sync.go drivers/cache/sync_test.go
git commit -m "feat(cache): add background sync task"
```

---

### Task 6: 注册驱动 + 全量验证

**Files:**
- Modify: `drivers/all.go`（在 chunk 行 `_ "github.com/OpenListTeam/OpenList/v4/drivers/chunk"` 后追加）

- [ ] **Step 1: 注册驱动**

`drivers/all.go` 第 25 行 `_ "github.com/OpenListTeam/OpenList/v4/drivers/chunk"` 后追加：

```go
	_ "github.com/OpenListTeam/OpenList/v4/drivers/cache"
```

- [ ] **Step 2: 全量测试 + 构建**

Run:
```bash
/Library/Go/sdk/go1.25.4/bin/go build ./...
/Library/Go/sdk/go1.25.4/bin/go vet ./drivers/cache/ ./internal/model/
/Library/Go/sdk/go1.25.4/bin/go test ./drivers/cache/ ./internal/model/ -v
```
Expected: build 无错误；vet 无输出；测试全 PASS

- [ ] **Step 3: 验证注册与全量测试**

Run:
```bash
/Library/Go/sdk/go1.25.4/bin/go test ./internal/op/ -run TestCreateStorage -v
/Library/Go/sdk/go1.25.4/bin/go test ./drivers/cache/ -v
```
Expected: 全部 PASS（`TestCreateStorage` 验证驱动注册链路无破坏；cache 测试验证 Cache 驱动可实例化）

- [ ] **Step 4: Commit**

```bash
git add drivers/all.go
git commit -m "feat(cache): register cache driver"
```

---

## 验证清单

完成后整体验证：

```bash
/Library/Go/sdk/go1.25.4/bin/go build ./...
/Library/Go/sdk/go1.25.4/bin/go vet ./drivers/cache/
/Library/Go/sdk/go1.25.4/bin/go test ./drivers/cache/ -v
/Library/Go/sdk/go1.25.4/bin/go test ./internal/model/ -run TestCacheListModel -v
```

对照 spec 的检查项：
- [ ] List 命中缓存（下游零调用）— `TestListHitServesCache`
- [ ] List miss 回源写 DB — `TestListMissFillsCache`
- [ ] Refresh 强制回源并更新 — `TestListRefresh`
- [ ] Get 命中父目录快照 / miss 回源 — `TestGetFromCache` / `TestGetMissFetchesDownstream`
- [ ] Link 转发下游 — `TestLinkForwards`
- [ ] 定时任务刷新过期目录（覆盖增删）、跳过新鲜目录、失败删行 — `TestSyncAll*`
- [ ] 快照往返、缩略图、特殊字符 — `TestToCachedObj*` / `TestFromCachedObj*` / `TestMarshal*`
- [ ] DB upsert/隔离/删除/列表 — `TestUpsert*` / `TestStorageIsolation` / `TestDeleteCacheList` / `TestListCacheLists`
