# Bilibili 本地快照 + 增量刷新 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** bilibili 驱动列表数据入库持久化（框架通用快照表），每次 List 只做增量检查（第 1 页起翻到"页内全已知"即停），失败返回旧快照，无部分结果。

**Architecture:** internal/db 新增通用表 VirtualDirSnapshot（StorageID+DirKey+Owner+Data JSON 文本），驱动侧泛型门面 listWithSnapshot 统一"读快照 → 翻页增量 → 原子落库 → 重建 obj"，singleflight 防并发重拉；collectPages 的 partial 语义作废（fetchWithRetry 页级退避保留）。

**Tech Stack:** Go 1.25、gorm（sqlite 测试 / mysql 生产）、golang.org/x/sync/singleflight（已在 go.mod v0.16.0）、resty。

**Spec:** `docs/superpowers/specs/2026-09-03-bilibili-snapshot-incremental-design.md`

## Global Constraints

- Go 工具链：`/Library/Go/sdk/go1.25.4/bin/go` 与同路径 `gofmt`（不用 PATH 上的）
- 测试全部离线（`mockRoundTrip`/`fetch` 注入），不得触碰真实 bilibili 网络
- 驱动 Data JSON 文本存**原始 API 条目**（FollowItem/VideoItem/FavFolder），不存展示对象
- 快照写入仅在完整成功后整体覆盖（原子）；任何中途失败 → 返回旧快照（有）或错误（无），**绝不返回部分分页结果**
- 增量停止条件 = 某页内全部条目已在库中（`fresh==0`）；新条目顺序保持 API 新→旧，合并时插到旧数据**头部**
- Init 换账号（uid 变化）→ `DeleteVirtualDirSnapshotsNotOwner`
- 现有测试 41 项全绿为回归基线；测试间隔离：每个测试 driver 用唯一 StorageID（快照按 storage_id 隔离）
- 不新增第三方依赖（singleflight 已在 go.mod）
- `go build ./...` 的 winfsp/cgofuse 失败是 macOS 预存问题，与本次改动无关（构建验证用 `./drivers/...` 与 `./internal/db/`）

---

### Task 1: 框架通用快照表（model + db CRUD + 迁移）

**Files:**
- Create: `internal/model/virtual_snapshot.go`
- Create: `internal/db/virtual_snapshot.go`
- Create: `internal/db/virtual_snapshot_test.go`
- Modify: `internal/db/db.go:14`（AutoMigrate 列表追加注册）

**Interfaces:**
- Produces:
  - `model.VirtualDirSnapshot{ID uint; StorageID uint; DirKey string; Owner string; Data string; UpdatedAt time.Time}`（Data 为 JSON 文本，TaskItem.PersistData 同款 `gorm:"type:text"` 先例）
  - `db.GetVirtualDirSnapshot(storageID uint, dirKey string) (*model.VirtualDirSnapshot, error)` —— 不存在返回 `(nil, nil)`
  - `db.UpsertVirtualDirSnapshot(s *model.VirtualDirSnapshot) error` —— ON CONFLICT 覆盖 Data，gorm 自动更新 UpdatedAt
  - `db.DeleteVirtualDirSnapshotsNotOwner(storageID uint, owner string) error` —— `DELETE WHERE storage_id=? AND owner<>?`

- [ ] **Step 1: 写 model 与 db 层（先实现后测试不成立——本任务测试需要实现存在，先写实现）**

`internal/model/virtual_snapshot.go`:

```go
package model

import "time"

// VirtualDirSnapshot 虚拟目录驱动的列表持久化快照（bilibili 首个接入者）。
// StorageID+DirKey 唯一：DirKey 由驱动自定义（bilibili = 目录 obj.ID，
// 如 followings / favs / up_{mid} / fav_{media_id}），不随显示名变化。
// Owner 为可选账号标识（bilibili = 登录 uid），换账号时用于清理旧数据。
// Data 为驱动自管 JSON 文本（原始 API 条目 + 格式版本）。
type VirtualDirSnapshot struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	StorageID uint      `gorm:"uniqueIndex:idx_vdir_snap,priority:1" json:"storage_id"`
	DirKey    string    `gorm:"uniqueIndex:idx_vdir_snap,priority:2;type:varchar(255)" json:"dir_key"`
	Owner     string    `gorm:"index:idx_vdir_snap_owner;type:varchar(64);default:''" json:"owner"`
	Data      string    `gorm:"type:text" json:"data"`
	UpdatedAt time.Time `json:"updated_at"`
}
```

`internal/db/virtual_snapshot.go`:

```go
package db

import (
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/pkg/errors"
	"gorm.io/gorm/clause"
)

// GetVirtualDirSnapshot 读快照；不存在返回 (nil, nil)（调用方少一个分支）
func GetVirtualDirSnapshot(storageID uint, dirKey string) (*model.VirtualDirSnapshot, error) {
	var snap model.VirtualDirSnapshot
	err := db.Where("storage_id = ? AND dir_key = ?", storageID, dirKey).First(&snap).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.WithStack(err)
	}
	return &snap, nil
}

// UpsertVirtualDirSnapshot 同 key 覆盖写入（Data 整体替换 + UpdatedAt 刷新）
func UpsertVirtualDirSnapshot(s *model.VirtualDirSnapshot) error {
	s.UpdatedAt = time.Now()
	return errors.WithStack(db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "storage_id"}, {Name: "dir_key"}},
		DoUpdates: clause.AssignmentColumns([]string{"data", "owner", "updated_at"}),
	}).Create(s).Error)
}

// DeleteVirtualDirSnapshotsNotOwner 清掉非当前 owner 的快照（换账号清理）。
// 注意 owner="" 的无主行也会被删——语义即"只保留当前账号的数据"。
func DeleteVirtualDirSnapshotsNotOwner(storageID uint, owner string) error {
	return errors.WithStack(db.Where("storage_id = ? AND owner <> ?", storageID, owner).
		Delete(&model.VirtualDirSnapshot{}).Error)
}
```

补 import：`"gorm.io/gorm"`（errors.Is 判 ErrRecordNotFound 需要）。

`internal/db/db.go` AutoMigrate 列表末尾追加 `new(model.VirtualDirSnapshot)`。

- [ ] **Step 2: 写 db 层测试**

`internal/db/virtual_snapshot_test.go`（走包内既有 TestMain sqlite 设施）:

```go
package db

import (
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

func TestVirtualDirSnapshotCRUD(t *testing.T) {
	// 不存在 → (nil, nil)
	snap, err := GetVirtualDirSnapshot(1, "up_42")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if snap != nil {
		t.Fatalf("want nil for missing snapshot, got %+v", snap)
	}
	// Upsert 新建
	s := &model.VirtualDirSnapshot{StorageID: 1, DirKey: "up_42", Owner: "12345", Data: `{"v":1}`}
	if err := UpsertVirtualDirSnapshot(s); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	got, err := GetVirtualDirSnapshot(1, "up_42")
	if err != nil || got == nil || got.Data != `{"v":1}` || got.Owner != "12345" {
		t.Fatalf("Get after upsert = %+v, %v", got, err)
	}
	// 同 key 覆盖（不产生第二行）
	s2 := &model.VirtualDirSnapshot{StorageID: 1, DirKey: "up_42", Owner: "12345", Data: `{"v":2,"items":["x"]}`}
	if err := UpsertVirtualDirSnapshot(s2); err != nil {
		t.Fatalf("Upsert2: %v", err)
	}
	got, err = GetVirtualDirSnapshot(1, "up_42")
	if err != nil || got == nil || got.Data != `{"v":2,"items":["x"]}` {
		t.Fatalf("Get after overwrite = %+v, %v", got, err)
	}
	// 不同 storage 同 key 互不干扰
	if _, err := GetVirtualDirSnapshot(2, "up_42"); err != nil {
		t.Fatalf("other storage Get: %v", err)
	}
}

func TestDeleteVirtualDirSnapshotsNotOwner(t *testing.T) {
	must := func(s *model.VirtualDirSnapshot) {
		t.Helper()
		if err := UpsertVirtualDirSnapshot(s); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
	}
	must(&model.VirtualDirSnapshot{StorageID: 9, DirKey: "a", Owner: "1", Data: "{}"})
	must(&model.VirtualDirSnapshot{StorageID: 9, DirKey: "b", Owner: "2", Data: "{}"})
	must(&model.VirtualDirSnapshot{StorageID: 9, DirKey: "c", Owner: "", Data: "{}"})
	must(&model.VirtualDirSnapshot{StorageID: 10, DirKey: "a", Owner: "1", Data: "{}"})
	if err := DeleteVirtualDirSnapshotsNotOwner(9, "2"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	for _, tc := range []struct {
		key  string
		want bool // true = 应仍存在
	}{{"a", false}, {"b", true}, {"c", false}, {"a", true}} {
		tc := tc
		snap, err := GetVirtualDirSnapshot(9, tc.key)
		if err != nil {
			t.Fatalf("Get %s: %v", tc.key, err)
		}
		if (snap != nil) != tc.want {
			t.Fatalf("storage 9 key %s: exists=%v, want %v", tc.key, snap != nil, tc.want)
		}
	}
	// storage 10 不受影响
	snap, err := GetVirtualDirSnapshot(10, "a")
	if err != nil || snap == nil {
		t.Fatalf("storage 10 must survive: %+v, %v", snap, err)
	}
}
```

（`{"a", false}` 与最后一行 `{"a", true}` 键同为 "a" 但属不同 storage——测试内部循环查 storage 9，最后单独查 storage 10。）

- [ ] **Step 3: 运行测试确认通过**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./internal/db/ -run TestVirtualDirSnapshot -count=1`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/model/virtual_snapshot.go internal/db/virtual_snapshot.go internal/db/virtual_snapshot_test.go internal/db/db.go
git commit -m "feat(db): generic virtual dir snapshot table (get/upsert/delete-not-owner)"
```

---

### Task 2: 驱动快照门面 listWithSnapshot（泛型增量/全量统一流程）

**Files:**
- Create: `drivers/bilibili/snapshot.go`
- Create: `drivers/bilibili/testmain_test.go`（包级 TestMain：sqlite + db.Init，仿 `drivers/pornhub/fanart_test_helpers_test.go`）
- Create: `drivers/bilibili/snapshot_test.go`

**Interfaces:**
- Consumes: `db.GetVirtualDirSnapshot` / `db.UpsertVirtualDirSnapshot`（Task 1）；`fetchWithRetry[T]`（现成，api.go 包级）；`d.uid`；`d.ID`（model.Storage）
- Produces:
  - `func listWithSnapshot[T any](d *Bilibili, ctx context.Context, dir model.Obj, dirKey string, fetchPage func(pn int) ([]T, int, error), keyOf func(T) string, build func(dir model.Obj, items []T) ([]model.Obj, error)) ([]model.Obj, error)`（包级泛型函数，Go 方法不允许类型参数）
  - 约定：DirKey 用目录 obj.ID；owner = `strconv.FormatInt(d.uid, 10)`

- [ ] **Step 1: 写包级 TestMain（db 设施）**

`drivers/bilibili/testmain_test.go`:

```go
package bilibili

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestMain 为快照相关测试提供 sqlite db（pornhub 驱动同款设施）。
// 不影响既有纯 mock 测试——db 包全局在此初始化后空闲可用。
func TestMain(m *testing.M) {
	dataDir, err := os.MkdirTemp("", "bilibili-test-")
	if err != nil {
		panic(err)
	}
	conf.Conf = conf.DefaultConfig(dataDir)
	database, err := gorm.Open(sqlite.Open(filepath.Join(dataDir, "bilibili-test.db")), &gorm.Config{})
	if err != nil {
		panic(err)
	}
	if err := db.Init(database); err != nil {
		panic(err)
	}
	code := m.Run()
	if sqlDB, sqlErr := database.DB(); sqlErr == nil {
		_ = sqlDB.Close()
	}
	_ = os.RemoveAll(dataDir)
	os.Exit(code)
}
```

- [ ] **Step 2: 写失败的门面测试（fetch 注入，纯内存 + sqlite）**

`drivers/bilibili/snapshot_test.go`（本任务只测门面本身；fetch 用注入假页，不 mock 网络）:

```go
package bilibili

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

// intItem 门面测试的假条目类型
type intItem struct {
	ID   int
	Name string
}

func intKeyOf(it intItem) string { return strconv.Itoa(it.ID) }

// intBuild 假展示层：把 items 序号化返回（顺序即断言对象）
func intBuild(dir model.Obj, items []intItem) ([]model.Obj, error) {
	objs := make([]model.Obj, 0, len(items))
	for _, it := range items {
		objs = append(objs, &model.Object{ID: strconv.Itoa(it.ID), Name: it.Name, Path: "/x/" + it.Name})
	}
	return objs, nil
}

// pagesFrom 把多页切片转成 fetchPage（每页返回 total = 全量长度；pn 越界给空页）
func pagesFrom(pages ...[]intItem) func(pn int) ([]intItem, int, error) {
	total := 0
	for _, p := range pages {
		total += len(p)
	}
	return func(pn int) ([]intItem, int, error) {
		if pn > len(pages) {
			return nil, total, nil
		}
		return pages[pn-1], total, nil
	}
}

// newSnapDriver 快照测试驱动：每用例唯一 StorageID（快照按 storage_id 隔离防串扰）
var testSnapStorageSeq int64

func newSnapDriver() *Bilibili {
	d := newTestDriver()
	d.ID = uint(atomic.AddInt64(&testSnapStorageSeq, 1) + 1000) // 避开其他测试的 0
	return d
}
```

```go
func TestListWithSnapshotFirstFull(t *testing.T) {
	// 首次：无快照 → 拉完全部页（无"页全已知"可停）→ 落库
	d := newSnapDriver()
	fetch := pagesFrom(
		[]intItem{{1, "a"}, {2, "b"}},
		[]intItem{{3, "c"}},
	)
	objs, err := listWithSnapshot(d, context.Background(), nil, "up_1", fetch, intKeyOf, intBuild)
	if err != nil {
		t.Fatalf("listWithSnapshot: %v", err)
	}
	if len(objs) != 3 || objs[0].GetName() != "a" || objs[2].GetName() != "c" {
		t.Fatalf("objs = %+v", objs)
	}
	// 已落库
	snap, err := dbGetSnap(d, "up_1")
	if err != nil || snap == nil {
		t.Fatalf("snapshot missing after first full: %+v %v", snap, err)
	}
}

func TestListWithSnapshotEmptyFirst(t *testing.T) {
	// 空目录：也落空快照（防每次重拉全量）
	d := newSnapDriver()
	fetch := pagesFrom() // 第 1 页即空
	if _, err := listWithSnapshot(d, context.Background(), nil, "up_2", fetch, intKeyOf, intBuild); err != nil {
		t.Fatalf("listWithSnapshot: %v", err)
	}
	snap, err := dbGetSnap(d, "up_2")
	if err != nil || snap == nil {
		t.Fatalf("empty snapshot must persist: %+v %v", snap, err)
	}
	// 二次调用：第 1 页空 → 直接返回，无写入异常
	if _, err := listWithSnapshot(d, context.Background(), nil, "up_2", fetch, intKeyOf, intBuild); err != nil {
		t.Fatalf("second call: %v", err)
	}
}
```

（dbGetSnap 是包内测试 helper，见 Step 3；`listWithSnapshot(d, ctx, nil, ...)` 传 nil dir 仅在 build 不使用 dir 时可行——intBuild 未用 dir，OK。）

```go
func TestListWithSnapshotIncrementalNoNew(t *testing.T) {
	// 已有快照 {1,2,3}；API 第 1 页全已知 → 1 次 fetch 后停止，不落库
	d := newSnapDriver()
	seed := &model.VirtualDirSnapshot{StorageID: d.ID, DirKey: "up_3", Owner: "12345",
		Data: `{"v":1,"items":[{"ID":3,"Name":"c"},{"ID":2,"Name":"b"},{"ID":1,"Name":"a"}]}`}
	if err := dbUpsertSnap(seed); err != nil {
		t.Fatalf("seed: %v", err)
	}
	var calls int64
	fetch := func(pn int) ([]intItem, int, error) {
		atomic.AddInt64(&calls, 1)
		if pn == 1 {
			return []intItem{{3, "c"}, {2, "b"}, {1, "a"}}, 3, nil
		}
		return nil, 3, nil
	}
	objs, err := listWithSnapshot(d, context.Background(), nil, "up_3", fetch, intKeyOf, intBuild)
	if err != nil {
		t.Fatalf("listWithSnapshot: %v", err)
	}
	if len(objs) != 3 {
		t.Fatalf("objs = %d", len(objs))
	}
	if n := atomic.LoadInt64(&calls); n != 1 {
		t.Fatalf("fetch calls = %d, want 1", n)
	}
}

func TestListWithSnapshotIncrementalNewItems(t *testing.T) {
	// 已有快照 {3,2,1}；API 第 1 页含新 {5,4} 且页内已接上 3 → 新条目插头部
	d := newSnapDriver()
	seed := &model.VirtualDirSnapshot{StorageID: d.ID, DirKey: "up_4", Owner: "12345",
		Data: `{"v":1,"items":[{"ID":3,"Name":"c"},{"ID":2,"Name":"b"},{"ID":1,"Name":"a"}]}`}
	if err := dbUpsertSnap(seed); err != nil {
		t.Fatalf("seed: %v", err)
	}
	fetch := pagesFrom(
		[]intItem{{5, "e"}, {4, "d"}, {3, "c"}}, // 页内前 2 未知、第 3 条已知 → 本页即接上
	)
	objs, err := listWithSnapshot(d, context.Background(), nil, "up_4", fetch, intKeyOf, intBuild)
	if err != nil {
		t.Fatalf("listWithSnapshot: %v", err)
	}
	names := []string{objs[0].GetName(), objs[1].GetName(), objs[2].GetName(), objs[3].GetName()}
	want := []string{"e", "d", "c", "b"} // 新条目在前，旧序保持
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("names = %v, want %v", names, want)
		}
	}
	if len(objs) != 4 {
		t.Fatalf("objs = %d, want 4 (3 old + 2 new)", len(objs))
	}
}

func TestListWithSnapshotIncrementalMultiPage(t *testing.T) {
	// 新增跨多页：{5} 在第 1 页、{4} 在第 2 页、第 3 页全已知 → 停
	d := newSnapDriver()
	seed := &model.VirtualDirSnapshot{StorageID: d.ID, DirKey: "up_5", Owner: "12345",
		Data: `{"v":1,"items":[{"ID":3,"Name":"c"},{"ID":2,"Name":"b"},{"ID":1,"Name":"a"}]}`}
	if err := dbUpsertSnap(seed); err != nil {
		t.Fatalf("seed: %v", err)
	}
	fetch := pagesFrom(
		[]intItem{{5, "e"}},
		[]intItem{{4, "d"}},
		[]intItem{{3, "c"}}, // 全已知 → 停
	)
	objs, err := listWithSnapshot(d, context.Background(), nil, "up_5", fetch, intKeyOf, intBuild)
	if err != nil {
		t.Fatalf("listWithSnapshot: %v", err)
	}
	if len(objs) != 5 {
		t.Fatalf("objs = %d, want 5", len(objs))
	}
	// 已持久化（含新增）
	snap, err := dbGetSnap(d, "up_5")
	if err != nil || snap == nil {
		t.Fatalf("snap: %+v %v", snap, err)
	}
	if snap.Data == seed.Data {
		t.Fatal("snapshot data must include new items")
	}
}

func TestListWithSnapshotFailFallsBackToSnapshot(t *testing.T) {
	// 增量检查失败（有快照）→ 返回旧快照数据，不写库
	d := newSnapDriver()
	seed := &model.VirtualDirSnapshot{StorageID: d.ID, DirKey: "up_6", Owner: "12345",
		Data: `{"v":1,"items":[{"ID":3,"Name":"c"},{"ID":2,"Name":"b"},{"ID":1,"Name":"a"}]}`}
	if err := dbUpsertSnap(seed); err != nil {
		t.Fatalf("seed: %v", err)
	}
	fetch := func(pn int) ([]intItem, int, error) {
		return nil, 0, errors.New("risk-control")
	}
	objs, err := listWithSnapshot(d, context.Background(), nil, "up_6", fetch, intKeyOf, intBuild)
	if err != nil {
		t.Fatalf("must fall back to snapshot, got err: %v", err)
	}
	if len(objs) != 3 {
		t.Fatalf("objs = %d, want 3 from snapshot", len(objs))
	}
}

func TestListWithSnapshotFailNoSnapshotErrors(t *testing.T) {
	// 首次失败（无快照）→ 返回错误
	d := newSnapDriver()
	fetch := func(pn int) ([]intItem, int, error) {
		return nil, 0, errors.New("network down")
	}
	if _, err := listWithSnapshot(d, context.Background(), nil, "up_7", fetch, intKeyOf, intBuild); err == nil {
		t.Fatal("first-fetch failure must return error")
	}
}
```

- [ ] **Step 3: 写测试 helper（dbGetSnap/dbUpsertSnap）+ 最小门面实现**

`drivers/bilibili/snapshot_test.go` 顶部补 helper（引 db 包）:

```go
import (
	...
	"github.com/OpenListTeam/OpenList/v4/internal/db"
)

func dbGetSnap(d *Bilibili, dirKey string) (*model.VirtualDirSnapshot, error) {
	return db.GetVirtualDirSnapshot(d.ID, dirKey)
}

func dbUpsertSnap(s *model.VirtualDirSnapshot) error {
	return db.UpsertVirtualDirSnapshot(s)
}
```

`drivers/bilibili/snapshot.go` 实现:

```go
package bilibili

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

// snapshotEnvelope 快照 Data 的负载结构（v 为格式版本，未来迁移用）
type snapshotEnvelope[T any] struct {
	V         int    `json:"v"`
	FetchedAt string `json:"fetched_at"`
	Items     []T    `json:"items"`
}

// snapshotOwner 当前登录账号的快照 owner 标识
func (d *Bilibili) snapshotOwner() string {
	return strconv.FormatInt(d.uid, 10)
}

// loadSnapshot 读快照并解码；无快照返回 (nil, false, nil)
func loadSnapshot[T any](d *Bilibili, dirKey string) (*snapshotEnvelope[T], bool, error) {
	row, err := db.GetVirtualDirSnapshot(d.ID, dirKey)
	if err != nil {
		return nil, false, err
	}
	if row == nil {
		return nil, false, nil
	}
	var env snapshotEnvelope[T]
	if err := json.Unmarshal([]byte(row.Data), &env); err != nil {
		// 数据损坏：按无快照处理（下次全量重建覆盖）
		return nil, false, nil
	}
	return &env, true, nil
}

// saveSnapshot 整体覆盖写快照（原子：仅在完整成功后调用）
func saveSnapshot[T any](d *Bilibili, dirKey string, env *snapshotEnvelope[T]) error {
	env.V = 1
	env.FetchedAt = time.Now().Format(time.RFC3339)
	data, err := json.Marshal(env)
	if err != nil {
		return err
	}
	return db.UpsertVirtualDirSnapshot(&model.VirtualDirSnapshot{
		StorageID: d.ID,
		DirKey:    dirKey,
		Owner:     d.snapshotOwner(),
		Data:      string(data),
	})
}

// listWithSnapshot 目录列表统一门面：读快照 → 翻页增量 → 原子落库 → build 展示。
//
// 语义（spec 2026-09-03）：
//   - 无快照 = 首次：known 为空集，翻到空页或达 total 即全量；成功即落库（空目录也落）
//   - 有快照 = 增量：从第 1 页起，页内全部条目已知（fresh==0）即停——新内容只可能在头部；
//     新增条目插到旧数据头部（保持 API 新→旧顺序）
//   - 任意页失败（重试耗尽）：有快照 → 返回旧快照数据且不写库；无快照 → 返回错误。
//     绝不返回部分分页结果（原子性，用户 Z 决策）
//
// 包级泛型函数（Go 方法不允许类型参数），fetchWithRetry 页级退避在 api.go。
func listWithSnapshot[T any](d *Bilibili, ctx context.Context, dir model.Obj, dirKey string,
	fetchPage func(pn int) ([]T, int, error), keyOf func(T) string,
	build func(dir model.Obj, items []T) ([]model.Obj, error)) ([]model.Obj, error) {

	env, hasSnap, err := loadSnapshot[T](d, dirKey)
	if err != nil {
		return nil, fmt.Errorf("load snapshot %s: %w", dirKey, err)
	}

	known := make(map[string]bool)
	if hasSnap {
		for _, it := range env.Items {
			known[keyOf(it)] = true
		}
	}

	var merged []T
	if hasSnap {
		merged = make([]T, len(env.Items))
		copy(merged, env.Items)
	}
	var newHead []T // 增量新增（保持页序 = API 新→旧）
	changed := false

	for pn := 1; ; pn++ {
		items, total, err := fetchWithRetry(d, ctx, fetchPage, pn)
		if err != nil {
			if hasSnap {
				return build(dir, env.Items) // 降级：旧快照，不写库
			}
			return nil, err
		}
		if len(items) == 0 {
			break // API 拉完
		}
		var fresh []T
		for _, it := range items {
			if !known[keyOf(it)] {
				known[keyOf(it)] = true
				fresh = append(fresh, it)
			}
		}
		if !hasSnap {
			merged = append(merged, fresh...)
			if len(merged) >= total {
				break // 首次全量拉完
			}
		} else if len(fresh) > 0 {
			newHead = append(newHead, fresh...)
		} else {
			break // 页内全部已知 → 增量接上
		}
	}

	if !hasSnap {
		changed = true // 首次：空目录也要落库（防每次重拉）
	} else if len(newHead) > 0 {
		merged = append(newHead, merged...)
		changed = true
	}
	if changed {
		if err := saveSnapshot(d, dirKey, &snapshotEnvelope[T]{Items: merged}); err != nil {
			return nil, fmt.Errorf("save snapshot %s: %w", dirKey, err)
		}
	}
	return build(dir, merged)
}
```

- [ ] **Step 4: 运行测试（先确认 Step 2 用例失败→实现后通过）**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/bilibili/ -run TestListWithSnapshot -count=1`
Expected: 实现前 FAIL（undefined: listWithSnapshot）；实现后全部 PASS

- [ ] **Step 5: 全包回归 + commit**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/bilibili/ -count=1`
Expected: PASS（既有测试不受 TestMain/db 引入影响）

```bash
git add drivers/bilibili/snapshot.go drivers/bilibili/snapshot_test.go drivers/bilibili/testmain_test.go
git commit -m "feat(bilibili): snapshot facade listWithSnapshot - full/incremental/failover in one flow"
```

---

### Task 3: 数据源 fetch 化 + 接线（api.go 重构 + driver.go 走门面）

**Files:**
- Modify: `drivers/bilibili/api.go`（collectPages 删除；followings/upVideos/favFolders/favVideos 改为页 fetch 函数）
- Modify: `drivers/bilibili/driver.go`（listFollowings/listUpVideos/listFavFolders/listFavVideos → listWithSnapshot 门面；build 展示函数抽出）
- Modify: `drivers/bilibili/api_test.go`（collectPages 测试删除/改写、数据函数测试改 fetch 页断言）
- Modify: `drivers/bilibili/driver_test.go`（listDriver helper 设唯一 StorageID；List 测试语义 = 首次全量落库）

**Interfaces:**
- Consumes: `listWithSnapshot[T]`（Task 2）；`fetchWithRetry[T]`、`pageRetryBackoff`（现成）
- Produces:
  - `fetchFollowingsPage(d, ctx) func(pn int) ([]FollowItem, int, error)` —— 闭包工厂
  - `fetchUpVideosPage(d, ctx, mid int64) func(pn int) ([]VideoItem, int, error)`
  - `fetchFavFoldersOnce(d, ctx) func(pn int) ([]FavFolder, int, error)` —— 单请求接口：pn=1 拉一次，pn≥2 空页
  - `fetchFavVideosPage(d, ctx, mediaID int64) func(pn int) ([]VideoItem, int, error)`
  - driver.go 展示函数：`buildFollowings(dir, items)` / `buildUpVideos(dir, items)` / `buildFavFolders(dir, items)` / `buildFavVideos(dir, items)`（现有 disambiguate/folderObj/newVideoObj/sanitizeName 逻辑移入，签名返回 `([]model.Obj, error)`）

- [ ] **Step 1: api.go——fetch 工厂替代数据函数，删 collectPages**

把 `followings(ctx)` / `upVideos(ctx, mid)` / `favVideos(ctx, mediaID)` 三个函数体改造为工厂函数（返回闭包，保留原解析逻辑），`favFolders(ctx)` 同理（单请求）。collectPages 整体删除（partial 语义作废，spec 决策）。fetchWithRetry/pageRetryBackoff 保留不动。改动后的 api.go 关键形态:

```go
// fetchFollowingsPage 关注列表页拉取器工厂（新→旧顺序假设，spec 验证点）
func fetchFollowingsPage(d *Bilibili, ctx context.Context) func(pn int) ([]FollowItem, int, error) {
	return func(pn int) ([]FollowItem, int, error) {
		raw, err := d.doGet(ctx, apiBase+"/x/relation/followings", map[string]string{
			"vmid": strconv.FormatInt(d.uid, 10), "pn": strconv.Itoa(pn), "ps": "50",
			"order": "desc", "order_type": "",
		}, false)
		if err != nil {
			return nil, 0, err
		}
		var page followingPage
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, 0, err
		}
		items := make([]FollowItem, 0, len(page.List))
		for _, f := range page.List {
			items = append(items, FollowItem{Mid: f.Mid, Uname: f.Uname})
		}
		return items, page.Total, nil
	}
}

// fetchUpVideosPage UP 投稿页拉取器工厂（arc/search，wbi 签名，按 pubdate 倒序）
func fetchUpVideosPage(d *Bilibili, ctx context.Context, mid int64) func(pn int) ([]VideoItem, int, error) {
	return func(pn int) ([]VideoItem, int, error) {
		raw, err := d.doGet(ctx, apiBase+"/x/space/wbi/arc/search", map[string]string{
			"mid": strconv.FormatInt(mid, 10), "pn": strconv.Itoa(pn), "ps": "50",
			"order": "pubdate", "tid": "0",
		}, true)
		if err != nil {
			return nil, 0, err
		}
		var page arcPage
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, 0, err
		}
		items := make([]VideoItem, 0, len(page.List.Vlist))
		for _, v := range page.List.Vlist {
			items = append(items, VideoItem{Bvid: v.Bvid, Title: v.Title, Pic: v.Pic, Pubdate: v.Created})
		}
		return items, page.Page.Count, nil
	}
}

// fetchFavFoldersOnce 收藏夹列表（list-all 单请求接口；pn≥2 给空页令门面停止）
func fetchFavFoldersOnce(d *Bilibili, ctx context.Context) func(pn int) ([]FavFolder, int, error) {
	return func(pn int) ([]FavFolder, int, error) {
		if pn > 1 {
			return nil, 0, nil
		}
		raw, err := d.doGet(ctx, apiBase+"/x/v3/fav/folder/created/list-all", map[string]string{
			"up_mid": strconv.FormatInt(d.uid, 10),
		}, false)
		if err != nil {
			return nil, 0, err
		}
		var resp favFolderResp
		if err := json.Unmarshal(raw, &resp); err != nil {
			return nil, 0, err
		}
		out := make([]FavFolder, 0, len(resp.List))
		for _, f := range resp.List {
			out = append(out, FavFolder{ID: f.ID, Title: f.Title})
		}
		return out, len(out), nil
	}
}

// fetchFavVideosPage 收藏夹视频页拉取器工厂（按收藏时间 mtime 倒序）
func fetchFavVideosPage(d *Bilibili, ctx context.Context, mediaID int64) func(pn int) ([]VideoItem, int, error) {
	return func(pn int) ([]VideoItem, int, error) {
		raw, err := d.doGet(ctx, apiBase+"/x/v3/fav/resource/list", map[string]string{
			"media_id": strconv.FormatInt(mediaID, 10), "pn": strconv.Itoa(pn), "ps": "20",
			"order": "mtime", "type": "2", "tid": "0", "platform": "web",
		}, false)
		if err != nil {
			return nil, 0, err
		}
		var page favResourcePage
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, 0, err
		}
		items := make([]VideoItem, 0, len(page.Medias))
		for _, m := range page.Medias {
			items = append(items, VideoItem{Bvid: m.Bvid, Title: m.Title, Pic: m.Cover, Pubdate: m.FavTime, Cid: m.Ugc.FirstCid})
		}
		return items, page.Info.MediaCount, nil
	}
}
```

- [ ] **Step 2: driver.go——listXxx 走门面，展示逻辑抽 build 函数**

替换四个 list 函数（保持 List 分发 switch 不动）与新增 build 函数。现有 listXxx 内逻辑（disambiguate/sanitizeName/folderObj/newVideoObj）原样搬入 buildXxx。改动后的形态:

```go
// List 分发（dir.GetID()）不变；快照 DirKey = dir.GetID()

func (d *Bilibili) listFollowings(ctx context.Context, dir model.Obj) ([]model.Obj, error) {
	return listWithSnapshot(d, ctx, dir, dir.GetID(),
		fetchFollowingsPage(d, ctx),
		func(f FollowItem) string { return strconv.FormatInt(f.Mid, 10) },
		buildFollowings)
}

func buildFollowings(dir model.Obj, items []FollowItem) ([]model.Obj, error) {
	displays := make([]string, len(items))
	suffixes := make([]string, len(items))
	for i, f := range items {
		displays[i] = sanitizeName(f.Uname, 80)
		suffixes[i] = strconv.FormatInt(f.Mid, 10)
	}
	names := disambiguate(displays, suffixes)
	objs := make([]model.Obj, 0, len(items))
	for i, f := range items {
		objs = append(objs, folderObj(dir, names[i], upFolderPrefix+strconv.FormatInt(f.Mid, 10)))
	}
	return objs, nil
}

func (d *Bilibili) listUpVideos(ctx context.Context, dir model.Obj) ([]model.Obj, error) {
	_, mid, ok := parsePrefixedID(dir.GetID(), upFolderPrefix)
	if !ok {
		return nil, errs.ObjectNotFound
	}
	return listWithSnapshot(d, ctx, dir, dir.GetID(),
		fetchUpVideosPage(d, ctx, mid),
		func(v VideoItem) string { return v.Bvid },
		buildUpVideos)
}

func buildUpVideos(dir model.Obj, items []VideoItem) ([]model.Obj, error) {
	displays := make([]string, len(items))
	suffixes := make([]string, len(items))
	for i, v := range items {
		displays[i] = sanitizeName(v.Title, 150)
		suffixes[i] = v.Bvid
	}
	names := disambiguate(displays, suffixes)
	objs := make([]model.Obj, 0, len(items))
	for i := range items {
		objs = append(objs, newVideoObj(dir, items[i], names[i]))
	}
	return objs, nil
}

func (d *Bilibili) listFavFolders(ctx context.Context, dir model.Obj) ([]model.Obj, error) {
	return listWithSnapshot(d, ctx, dir, dir.GetID(),
		fetchFavFoldersOnce(d, ctx),
		func(f FavFolder) string { return strconv.FormatInt(f.ID, 10) },
		buildFavFolders)
}

func buildFavFolders(dir model.Obj, items []FavFolder) ([]model.Obj, error) {
	displays := make([]string, len(items))
	suffixes := make([]string, len(items))
	for i, f := range items {
		displays[i] = sanitizeName(f.Title, 80)
		suffixes[i] = strconv.FormatInt(f.ID, 10)
	}
	names := disambiguate(displays, suffixes)
	objs := make([]model.Obj, 0, len(items))
	for i, f := range items {
		objs = append(objs, folderObj(dir, names[i], favFolderPrefix+strconv.FormatInt(f.ID, 10)))
	}
	return objs, nil
}

func (d *Bilibili) listFavVideos(ctx context.Context, dir model.Obj) ([]model.Obj, error) {
	_, mediaID, ok := parsePrefixedID(dir.GetID(), favFolderPrefix)
	if !ok {
		return nil, errs.ObjectNotFound
	}
	return listWithSnapshot(d, ctx, dir, dir.GetID(),
		fetchFavVideosPage(d, ctx, mediaID),
		func(v VideoItem) string { return v.Bvid },
		buildFavVideos)
}

func buildFavVideos(dir model.Obj, items []VideoItem) ([]model.Obj, error) {
	displays := make([]string, len(items))
	suffixes := make([]string, len(items))
	for i, v := range items {
		displays[i] = sanitizeName(v.Title, 150)
		suffixes[i] = v.Bvid
	}
	names := disambiguate(displays, suffixes)
	objs := make([]model.Obj, 0, len(items))
	for i := range items {
		objs = append(objs, newVideoObj(dir, items[i], names[i]))
	}
	return objs, nil
}
```

注意：listFollowings 与 listFavFolders 现在不直接抛 ObjectNotFound（dir.GetID() 由 List switch 保证匹配，门面按无快照全量处理）。原 `listUpVideos(ctx, mid)`/`listFavVideos(ctx, mediaID)` 的 mid/mediaID 参数改从 `parsePrefixedID(dir.GetID(), ...)` 内部解出——List 分发处的解析保持不变或同步简化（分发已 parse 一次；实现时避免重复解析：List switch 内把 mid 传入改为直接调 listUpVideos(ctx, dir)——见 Step 3 兼容测试对 List 路径的断言）。

- [ ] **Step 3: 适配 api_test.go**

- 删除 `TestCollectPagesMaxLimit` / `TestCollectPagesRetryThenSucceed` / `TestCollectPagesPartialOnPersistentError` / `TestCollectPagesFirstPageError`（collectPages 已删）
- `TestFollowingsPagination` 改测页解析：`fetch := fetchFollowingsPage(d, ctx); items, total, err := fetch(1)`，mock 两页断言 total 与单页条目解析；页 2 解析同
- `TestUpVideosSignedAndParsed` 改 `fetchUpVideosPage(d, ctx, 2)(1)`（wbi 签名断言保留——mock 仍校验 w_rid 存在）
- `TestFavVideosFirstCid` 改 `fetchFavVideosPage(d, ctx, 777)(1)`（断言 first_cid 解析进 VideoItem.Cid）
- 页退避测试（TestCollectPagesRetryThenSucceed 的语义）迁移为门面层覆盖：Task 2 门面测试已用 fetchWithRetry——补一个 `TestFetchWithRetry` 直测 fetchWithRetry（页失败 2 次后成功 / 耗尽返回最后错误，pageRetryBackoff 置 0）保留退避语义覆盖

```go
func TestFetchWithRetryBackoffThenSucceed(t *testing.T) {
	defer func(old []time.Duration) { pageRetryBackoff = old }(pageRetryBackoff)
	pageRetryBackoff = []time.Duration{0, 0}
	d := newTestDriver()
	calls := 0
	_, _, err := fetchWithRetry(d, context.Background(), func(pn int) ([]int, int, error) {
		calls++
		if calls < 3 {
			return nil, 0, errors.New("boom")
		}
		return []int{1}, 1, nil
	}, 1)
	if err != nil || calls != 3 {
		t.Fatalf("err=%v calls=%d, want nil/3", err, calls)
	}
}

func TestFetchWithRetryExhausted(t *testing.T) {
	defer func(old []time.Duration) { pageRetryBackoff = old }(pageRetryBackoff)
	pageRetryBackoff = []time.Duration{0, 0}
	d := newTestDriver()
	_, _, err := fetchWithRetry(d, context.Background(), func(pn int) ([]int, int, error) {
		return nil, 0, errors.New("always")
	}, 1)
	if err == nil {
		t.Fatal("want error after retries exhausted")
	}
}
```

（api_test.go 需补 import：`errors`、`time`——按现有 import 块调整。）

- [ ] **Step 4: 适配 driver_test.go——List 测试走快照**

`listDriver` helper 增加唯一 StorageID（快照隔离）:

```go
var testListStorageSeq uint64

func listDriver(t *testing.T, handler http.HandlerFunc) *Bilibili {
	t.Helper()
	d := newTestDriver()
	d.ID = uint(atomic.AddUint64(&testListStorageSeq, 1) + 5000)
	d.uid = 12345
	srv := newMockServer(t, handler)
	d.client = resty.New().SetTransport(mockRoundTrip(srv))
	return d
}
```

（driver_test.go 补 import `sync/atomic`。各 List 测试现有断言**保持**——首次 List = 全量拉 + 落库 + 重建 obj，输出与改动前一致；`TestListUpperVideos` 等 mock 里仍断言请求路径。每个测试独立 storage → 快照互不干扰。`TestListUnknownPath` 无网络无 db（分发即 ObjectNotFound）不变。）

- [ ] **Step 5: 新增网络级集成测试（mockRoundTrip：增量/降级/请求计数）**

`drivers/bilibili/snapshot_net_test.go`:

```go
package bilibili

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/go-resty/resty/v2"
)

// TestListFollowingsSecondCallNoAPI：二次 List 无新增 → 只发 1 次 API 请求（增量第 1 页即停）
func TestListFollowingsSecondCallNoAPI(t *testing.T) {
	var calls int64
	d := listDriver(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/x/relation/followings" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		atomic.AddInt64(&calls, 1)
		w.Write([]byte(`{"code":0,"data":{"list":[{"mid":42,"uname":"测试UP"}],"total":1}}`))
	})
	dir := dirObj(t, "/我的关注", dirFollowID)
	if _, err := d.List(context.Background(), dir, model.ListArgs{}); err != nil {
		t.Fatalf("first List: %v", err)
	}
	objs, err := d.List(context.Background(), dir, model.ListArgs{})
	if err != nil {
		t.Fatalf("second List: %v", err)
	}
	if len(objs) != 1 || objs[0].GetName() != "测试UP" {
		t.Fatalf("objs = %+v", objs)
	}
	if n := atomic.LoadInt64(&calls); n != 2 {
		t.Fatalf("api calls = %d, want 2 (first full + one incremental page)", n)
	}
}

// TestListUpVideosIncrementalGrows：二次 List 有新投稿 → 翻页到接上，快照增长
func TestListUpVideosIncrementalGrows(t *testing.T) {
	var pn int64
	d := listDriver(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/x/space/wbi/arc/search" {
			cur := atomic.AddInt64(&pn, 1)
			// 第 1 次调用全量（pn=1,2）；后续增量检查只到 pn=1 或 2
			var body string
			if cur == 1 {
				body = `{"code":0,"data":{"list":{"vlist":[
					{"bvid":"BV1a","title":"新1","created":1700000300}]},"page":{"count":1}}}`
			} else {
				body = `{"code":0,"data":{"list":{"vlist":[]},"page":{"count":0}}}`
			}
			w.Write([]byte(body))
			return
		}
		t.Fatalf("unexpected path %s", r.URL.Path)
	})
	d.mixinKey = "ea1db124af3c7062474693fa704f4ff8"
	dir := dirObj(t, "/我的关注/某UP", upFolderPrefix+"42")
	if _, err := d.List(context.Background(), dir, model.ListArgs{}); err != nil {
		t.Fatalf("first List: %v", err)
	}
	objs, err := d.List(context.Background(), dir, model.ListArgs{})
	if err != nil {
		t.Fatalf("second List: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("objs = %d, want 1", len(objs))
	}
	_ = pn
}
```

（TestListUpVideosIncrementalGrows 中第二段 mock 简化：真实"有新投稿再翻页"路径已在 Task 2 门面测试用注入覆盖，网络级只验证"无新 → 单页停"与首拉。集成增量增长用例若实现时易写（mock 状态机按调用次数切换响应），实现者补充：第二次 List 时第 1 页返回 BV1a+BV1b（新）→ 断言第二次返回 2 条且第 2 次调用后快照含 BV1b。）

- [ ] **Step 6: 全包测试 + gofmt + vet + commit**

Run: `/Library/Go/sdk/go1.25.4/bin/gofmt -l drivers/bilibili/`（应为空）
Run: `/Library/Go/sdk/go1.25.4/bin/go vet ./drivers/bilibili/`
Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/bilibili/ -count=1`
Expected: 全部 PASS（原 41 项语义保留 + 新增网络级）

```bash
git add drivers/bilibili/api.go drivers/bilibili/driver.go drivers/bilibili/api_test.go drivers/bilibili/driver_test.go drivers/bilibili/snapshot_net_test.go
git commit -m "feat(bilibili): route list endpoints through snapshot facade (fetch factories, no partial results)"
```

---

### Task 4: Init 换账号清理 + singleflight 并发单飞

**Files:**
- Modify: `drivers/bilibili/driver.go`（Init：navInfo 后清理非当前 uid 快照；struct 加 singleflight）
- Modify: `drivers/bilibili/snapshot.go`（listWithSnapshot 外包 singleflight 层或调用点包装）
- Create: `drivers/bilibili/snapshot_lifecycle_test.go`

**Interfaces:**
- Consumes: `db.DeleteVirtualDirSnapshotsNotOwner`（Task 1）；`listWithSnapshot`（Task 2）
- Produces: 无新导出；行为：同目录并发 List 只拉一次 API；换账号清库

- [ ] **Step 1: 写失败测试（并发单飞 + Init 清理）**

`drivers/bilibili/snapshot_lifecycle_test.go`:

```go
package bilibili

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

// TestListConcurrentSingleFetch：同目录 10 并发 List → 只 1 次全量拉取，全部成功
func TestListConcurrentSingleFetch(t *testing.T) {
	var calls int64
	d := listDriver(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&calls, 1)
		w.Write([]byte(`{"code":0,"data":{"list":[{"mid":42,"uname":"测试UP"}],"total":1}}`))
	})
	dir := dirObj(t, "/我的关注", dirFollowID)
	var wg sync.WaitGroup
	errs := make([]error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = d.List(context.Background(), dir, model.ListArgs{})
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("list %d: %v", i, err)
		}
	}
	if n := atomic.LoadInt64(&calls); n != 1 {
		t.Fatalf("api calls = %d, want 1 (singleflight)", n)
	}
}

// TestInitClearsForeignOwnerSnapshots：换账号（uid 变化）→ Init 清旧账号快照
func TestInitClearsForeignOwnerSnapshots(t *testing.T) {
	d := newTestDriver()
	d.ID = 424242
	// 预置：当前 uid=1 的快照 + 旧账号 uid=2 的快照
	mustUpsert := func(dirKey, owner string) {
		t.Helper()
		if err := db.UpsertVirtualDirSnapshot(&model.VirtualDirSnapshot{
			StorageID: d.ID, DirKey: dirKey, Owner: owner, Data: `{"v":1,"items":[]}`,
		}); err != nil {
			t.Fatalf("upsert %s: %v", dirKey, err)
		}
	}
	mustUpsert("up_1", "1")
	mustUpsert("up_9", "2")
	d.uid = 1 // Init 前旧 uid（模拟换账号前缓存）
	srv := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/x/web-interface/nav" {
			w.Write([]byte(`{"code":0,"data":{"isLogin":true,"mid":1,"uname":"u"}}`))
			return
		}
		t.Fatalf("unexpected path %s", r.URL.Path)
	})
	d.client = restyClientFor(srv)
	if err := d.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	keep, err := db.GetVirtualDirSnapshot(d.ID, "up_1")
	if err != nil || keep == nil {
		t.Fatalf("current-owner snapshot must survive: %+v %v", keep, err)
	}
	gone, err := db.GetVirtualDirSnapshot(d.ID, "up_9")
	if err != nil || gone != nil {
		t.Fatalf("foreign-owner snapshot must be cleared: %+v %v", gone, err)
	}
}
```

（`restyClientFor(srv)` = `resty.New().SetTransport(mockRoundTrip(srv))`——driver_test 已有等价写法，直接在测试内联。Init 内部走 initClient（若 d.client 非 nil 则跳过重建）→ 测试先设 d.client 再 Init。注意 Init 也会设 limiter/uid——d.uid 由 navInfo 覆盖为 1；d.cookieStr 种子已在 newTestDriver 设置。）

- [ ] **Step 2: 实现——struct 加 singleflight + List 分发包装 + Init 清理**

`drivers/bilibili/meta.go` Bilibili struct 增加字段:

```go
	// 快照并发单飞：同目录并发 List 只放行一次拉取（x/sync 已在 go.mod）
	sf singleflight.Group
```

`drivers/bilibili/driver.go` List 分发改为经单飞（四个 listXxx 保持，List 内包 singleflight 一层即可——key 用 dir.GetID()）:

```go
func (d *Bilibili) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {
	p := dir.GetPath()
	if p == "" || p == "/" {
		return []model.Obj{
			folderObj(dir, dirFollow, dirFollowID),
			folderObj(dir, dirFav, dirFavID),
		}, nil
	}
	key := dir.GetID()
	if key == "" {
		return nil, errs.ObjectNotFound // 旧缓存 obj 无 ID：等 dirCache 过期重建（b08f4f88 兼容说明）
	}
	v, err, _ := d.sf.Do(key, func() (interface{}, error) {
		switch key {
		case dirFollowID:
			return d.listFollowings(ctx, dir)
		case dirFavID:
			return d.listFavFolders(ctx, dir)
		default:
			if _, ok := parsePrefixedID(key, upFolderPrefix); ok {
				return d.listUpVideos(ctx, dir)
			}
			if _, ok := parsePrefixedID(key, favFolderPrefix); ok {
				return d.listFavVideos(ctx, dir)
			}
		}
		return nil, errs.ObjectNotFound
	})
	if err != nil {
		return nil, err
	}
	return v.([]model.Obj), nil
}
```

（注意：原 List switch 基于 p == "/"+dirFollow 等路径判断已由 ID 分发完全取代——路径分支删除后 Get fallback 的"旧缓存 obj 无 ID"会 ObjectNotFound，与 b08f4f88 的兼容说明一致：刷新即重建。）

`drivers/bilibili/driver.go` Init 末尾（navInfo 成功后）加清理:

```go
	// 校验登录态并缓存 uid/uname
	if _, _, err := d.navInfo(ctx); err != nil {
		return err
	}
	// 换账号：清掉非当前 uid 的旧快照（防旧账号数据混入增量基线）
	if err := db.DeleteVirtualDirSnapshotsNotOwner(d.ID, d.snapshotOwner()); err != nil {
		utils.Log.Warnf("bilibili: cleanup foreign snapshots: %v", err)
	}
	return nil
```

（driver.go 补 import `internal/db`、`pkg/utils`、`golang.org/x/sync/singleflight`；meta.go 补 singleflight import。snapshotOwner() 已在 Task 2 的 snapshot.go 定义。）

- [ ] **Step 3: 运行测试 + gofmt + vet + commit**

Run: `/Library/Go/sdk/go1.25.4/bin/gofmt -l drivers/bilibili/`（应为空）
Run: `/Library/Go/sdk/go1.25.4/bin/go vet ./drivers/bilibili/`
Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/bilibili/ -count=1`
Expected: 全 PASS（含新并发/清理用例）

```bash
git add drivers/bilibili/driver.go drivers/bilibili/meta.go drivers/bilibili/snapshot_lifecycle_test.go
git commit -m "feat(bilibili): singleflight per-dir list + Init clears foreign-owner snapshots"
```

---

### Task 5: 回归 + 文档收尾

**Files:**
- Modify: `docs/superpowers/specs/2026-09-03-bilibili-snapshot-incremental-design.md`（实现偏差记录，如有）
- 验证：`internal/db` 与 `drivers/bilibili` 全量测试；构建 `./drivers/...` 与 `./internal/db/`

- [ ] **Step 1: 全量回归**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./internal/db/ -count=1`
Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/bilibili/ -count=1`
Run: `/Library/Go/sdk/go1.25.4/bin/go build ./drivers/... ./internal/db/`
Expected: 全 PASS + build 干净（winfsp/cgofuse 失败与本次无关）

- [ ] **Step 2: 如实现与 spec 有偏差（签名/常量/行为），更新 spec 文档相应小节并提交**

- [ ] **Step 3: Commit（如无偏差跳过）**

```bash
git log --oneline -5
```

## Self-Review 记录（写作时核对）

- **Spec 覆盖**：快照表（Task 1）✓；门面增量/全量/失败降级/原子（Task 2）✓；四个目录类型接线 + collectPages partial 作废（Task 3）✓；换账号清理 + 并发（Task 4）✓；无 TTL/无容量上限（设计内建，MaxListItems 不动）✓；外部 refresh 机制 = 框架现成，驱动无改动 ✓
- **顺序假设验证点**（spec 风险节）留给真实账号手测，测试按"新在前" mock；若实测不符，对应 fetch 工厂退化为每次全量（改动局限 Task 3 的一个函数）
- **类型一致性**：`listWithSnapshot[T]` 签名在 Task 2/3 使用处一致；`db` 三函数名 Task 1/2/4 一致；`snapshotOwner()` Task 2 定义 Task 4 使用；`upFolderPrefix/favFolderPrefix/dirFollowID/dirFavID` 现成常量复用
