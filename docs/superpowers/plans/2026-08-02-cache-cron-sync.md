# Cache 驱动 Cron 表达式同步调度实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Cache 驱动的后台同步调度支持 cron 表达式（如 `0 3 * * *`），通过新增 Addition 字段 `SyncCronExpr` 实现，`SyncIntervalHours` 原字段类型与行为完全不变。

**Architecture:** 在 `drivers/cache/Addition` 中新增 `SyncCronExpr string` 文本字段（旧 JSON 无此 key → 零值，向后兼容）。Init 中提取 `buildSyncCron(expr, intervalHours) (*cron.Cron, error)` 纯函数做分支选择：cron 非空即用 cron（非法则报错），否则 interval > 0 用 interval，否则禁用。用 `pkg/cron.NewCronExpr`（上一轮已基于 robfig 实现，含表达式解析与定点触发测试）。

**Tech Stack:** Go 1.23.4（go.mod）、`pkg/cron`（robfig 封装）、gorm/sqlite 测试基建（已有）。

## Global Constraints

- 不改 `SyncIntervalHours` 字段类型、不做数据迁移（Addition 持久化为 JSON 字符串，改类型会使现有存储 unmarshal 失败）
- 优先级（spec 原文）：cron 非空 → cron；否则 interval > 0 → interval；否则禁用
- 非法表达式必须让 Init 失败并暴露错误（不静默回退到 interval）
- `SyncCronExpr` 字段 help 文案（verbatim）：`cron expression for background sync, e.g. 0 3 * * * or @every 12h; empty = use sync_interval_hours above`
- 用户输入需 `strings.TrimSpace` 后再解析
- 前端表单由 struct tag 自动生成，不改前端仓库
- 只改 Cache 驱动（drivers/cache/），不动其他驱动与 pkg/cron
- Go 工具链：`/Library/Go/sdk/go1.25.4/bin/go`、gofmt 同理
- 测试文件包名：新增内部单测 `schedule_test.go` 用 `package cache`（需访问未导出 `buildSyncCron`）；集成测试追加到现有 `driver_test.go`（`package cache_test`，复用其 DB init）

---

### Task 1: SyncCronExpr 字段 + buildSyncCron 选择逻辑 + Init 接入（TDD）

**Files:**
- Modify: `drivers/cache/meta.go`（Addition 新增字段）
- Modify: `drivers/cache/driver.go:49-61`（Init 调度分支改造 + 新增 buildSyncCron）
- Create: `drivers/cache/schedule_test.go`（内部单测，`package cache`）
- Modify: `drivers/cache/driver_test.go`（追加集成测试，`package cache_test`）

**Interfaces:**
- Produces:
  ```go
  // drivers/cache/driver.go 新增
  func buildSyncCron(expr string, intervalHours int) (*cron.Cron, error)
  // 语义：expr != "" → cron.NewCronExpr(expr)（非法返回 error）
  //      否则 intervalHours > 0 → cron.NewCron(time.Duration(intervalHours)*time.Hour), nil
  //      否则 → nil, nil
  ```
- Addition 新增字段：`SyncCronExpr string \`json:"sync_cron_expr" type:"text" help:"cron expression for background sync, e.g. 0 3 * * * or @every 12h; empty = use sync_interval_hours above"\``

- [ ] **Step 1: meta.go 新增字段**

在 `drivers/cache/meta.go` 的 `SyncIntervalHours` 字段后、`SyncPaths` 前插入：

```go
	SyncCronExpr      string `json:"sync_cron_expr" type:"text" help:"cron expression for background sync, e.g. 0 3 * * * or @every 12h; empty = use sync_interval_hours above"`
```

`SyncIntervalHours`、`TTLHours`、`RemotePath`、`SyncPaths` 字段及 init() 默认值一律不动。

- [ ] **Step 2: 写内部单测（TDD 红）**

新建 `drivers/cache/schedule_test.go`：

```go
package cache

import "testing"

func TestBuildSyncCronPrecedence(t *testing.T) {
	c, err := buildSyncCron("0 3 * * *", 5)
	if err != nil || c == nil {
		t.Fatalf("expr should win over interval: cron=%v err=%v", c, err)
	}
	c, err = buildSyncCron("", 3)
	if err != nil || c == nil {
		t.Fatalf("interval fallback: cron=%v err=%v", c, err)
	}
	c, err = buildSyncCron("", 0)
	if err != nil || c != nil {
		t.Fatalf("disabled when both empty: cron=%v err=%v", c, err)
	}
}

func TestBuildSyncCronRejectsInvalidExprEvenWithInterval(t *testing.T) {
	if c, err := buildSyncCron("60 * * * *", 5); err == nil {
		t.Fatalf("invalid expr must error even when interval is set (no silent fallback), got cron=%v", c)
	}
}
```

- [ ] **Step 3: 运行确认失败（红）**

Run:
```bash
/Library/Go/sdk/go1.25.4/bin/go test ./drivers/cache/ -run 'TestBuildSyncCron' -count=1
```
Expected: FAIL，编译错误 `undefined: buildSyncCron`。

- [ ] **Step 4: 实现 buildSyncCron（绿）**

在 `drivers/cache/driver.go` 中（`Init` 方法之前）新增：

```go
// buildSyncCron selects the background sync schedule: a non-empty cron
// expression wins over the legacy interval; both empty disables sync.
func buildSyncCron(expr string, intervalHours int) (*cron.Cron, error) {
	if expr != "" {
		return cron.NewCronExpr(expr)
	}
	if intervalHours > 0 {
		return cron.NewCron(time.Duration(intervalHours) * time.Hour), nil
	}
	return nil, nil
}
```

- [ ] **Step 5: 运行确认通过（绿）**

Run:
```bash
/Library/Go/sdk/go1.25.4/bin/go test ./drivers/cache/ -run 'TestBuildSyncCron' -count=1
```
Expected: PASS。

- [ ] **Step 6: 写集成测试（TDD 红）**

在 `drivers/cache/driver_test.go` 末尾追加（imports 新增 `"strings"` 与 `"github.com/OpenListTeam/OpenList/v4/pkg/utils"`）：

```go
// 旧版 Addition JSON（仅 interval，无 cron 字段）必须无错误反序列化，
// 且新字段取零值——字段类型未改动，这是向后兼容的回归保护。
func TestLegacyAdditionUnmarshal(t *testing.T) {
	var a cache.Addition
	if err := utils.Json.UnmarshalFromString(`{"remote_path":"/local","ttl_hours":24,"sync_interval_hours":2}`, &a); err != nil {
		t.Fatalf("legacy addition unmarshal: %+v", err)
	}
	if a.SyncIntervalHours != 2 {
		t.Errorf("expected interval 2, got %d", a.SyncIntervalHours)
	}
	if a.SyncCronExpr != "" {
		t.Errorf("expected empty cron expr, got %q", a.SyncCronExpr)
	}
}

// cron 表达式（含前后空格）配置的存储必须初始化成功并可用。
func TestCronExprScheduleInit(t *testing.T) {
	tmp := t.TempDir()
	localID, err := op.CreateStorage(context.Background(), model.Storage{
		Driver:    "Local",
		MountPath: "/local3",
		Addition:  fmt.Sprintf(`{"root_folder_path":%q}`, tmp),
	})
	if err != nil {
		t.Fatalf("create local storage: %+v", err)
	}
	cacheID, err := op.CreateStorage(context.Background(), model.Storage{
		Driver:    "Cache",
		MountPath: "/cache3",
		Addition:  `{"remote_path":"/local3","ttl_hours":24,"sync_interval_hours":0,"sync_cron_expr":" 0 3 * * * "}`,
	})
	if err != nil {
		t.Fatalf("create cache storage with cron expr: %+v", err)
	}
	t.Cleanup(func() {
		_ = op.DeleteStorageById(context.Background(), localID)
		_ = op.DeleteStorageById(context.Background(), cacheID)
	})
	d, err := op.GetStorageByMountPath("/cache3")
	if err != nil {
		t.Fatalf("get cache storage: %+v", err)
	}
	cd := d.(*cache.Cache)
	if cd.SyncCronExpr != " 0 3 * * * " {
		t.Fatalf("raw field should be preserved (trim only at parse), got %q", cd.SyncCronExpr)
	}
	if _, err := cd.List(context.Background(), rootDir(), model.ListArgs{}); err != nil {
		t.Fatalf("list with cron schedule: %+v", err)
	}
	if err := cd.Drop(context.Background()); err != nil {
		t.Fatalf("drop with cron schedule: %+v", err)
	}
}

// 非法表达式必须在创建/加载时失败并指明字段名——不能静默回退到 interval。
func TestInvalidCronExprRejected(t *testing.T) {
	tmp := t.TempDir()
	localID, err := op.CreateStorage(context.Background(), model.Storage{
		Driver:    "Local",
		MountPath: "/local4",
		Addition:  fmt.Sprintf(`{"root_folder_path":%q}`, tmp),
	})
	if err != nil {
		t.Fatalf("create local storage: %+v", err)
	}
	t.Cleanup(func() {
		if d, err := op.GetStorageByMountPath("/cache4"); err == nil {
			_ = op.DeleteStorageById(context.Background(), d.GetStorage().ID)
		}
		_ = op.DeleteStorageById(context.Background(), localID)
	})
	_, err = op.CreateStorage(context.Background(), model.Storage{
		Driver:    "Cache",
		MountPath: "/cache4",
		Addition:  `{"remote_path":"/local4","ttl_hours":24,"sync_interval_hours":0,"sync_cron_expr":"60 * * * *"}`,
	})
	if err == nil || !strings.Contains(err.Error(), "sync_cron_expr") {
		t.Fatalf("expected error mentioning sync_cron_expr, got %v", err)
	}
}
```

注意：`TestInvalidCronExprRejected` 的清理逻辑在创建失败后仍通过 mount path 查找并删除残留存储（initStorage 在失败时也会把驱动放入 storagesMap）。

- [ ] **Step 7: 运行集成测试确认失败（红）**

Run:
```bash
/Library/Go/sdk/go1.25.4/bin/go test ./drivers/cache/ -run 'TestLegacyAdditionUnmarshal|TestCronExprScheduleInit|TestInvalidCronExprRejected' -count=1
```
Expected: FAIL——`TestCronExprScheduleInit` 与 `TestInvalidCronExprRejected` 失败（Init 尚未解析 SyncCronExpr，非法表达式被忽略直接创建成功）。`TestLegacyAdditionUnmarshal` 此时已通过（字段未动）。

- [ ] **Step 8: 接入 Init（绿）**

将 `drivers/cache/driver.go` 的 Init 中现有调度段（当前为）：

```go
	if d.cron != nil {
		d.cron.Stop()
		d.cron = nil
	}
	if d.SyncIntervalHours > 0 {
		d.cron = cron.NewCron(time.Duration(d.SyncIntervalHours) * time.Hour)
		d.cron.Do(d.syncAll)
	}
```

替换为：

```go
	if d.cron != nil {
		d.cron.Stop()
		d.cron = nil
	}
	c, err := buildSyncCron(strings.TrimSpace(d.SyncCronExpr), d.SyncIntervalHours)
	if err != nil {
		return errors.Wrapf(err, "cache: invalid sync_cron_expr %q", d.SyncCronExpr)
	}
	if c != nil {
		d.cron = c
		d.cron.Do(d.syncAll)
	}
```

- [ ] **Step 9: 运行全部 cache 测试确认通过（绿）**

Run:
```bash
/Library/Go/sdk/go1.25.4/bin/go test ./drivers/cache/ -count=1
```
Expected: 全部 PASS（含既有测试：`TestProxyInheritanceFromDownstream`、`TestListMissFillsCache` 等，setup 中 `sync_interval_hours:0` 依旧表示禁用）。

- [ ] **Step 10: vet / gofmt / 全仓编译**

Run:
```bash
/Library/Go/sdk/go1.25.4/bin/go vet ./drivers/cache/ && /Library/Go/sdk/go1.25.4/bin/gofmt -l drivers/cache/ && /Library/Go/sdk/go1.25.4/bin/go build ./drivers/... ./internal/...
```
Expected: vet 无输出；gofmt 无输出；build 仅允许预存在的 `github.com/winfsp/cgofuse/fuse`（fuse.h 缺失，macOS 环境限制，与本次改动无关）。

- [ ] **Step 11: 提交**

```bash
git add drivers/cache/meta.go drivers/cache/driver.go drivers/cache/schedule_test.go drivers/cache/driver_test.go
git commit -m "feat(cache): support cron expression for background sync"
```

---

### Task 2: 回归验证与计划文档提交

**Files:**
- 无源码改动（只跑测试）
- Commit: `docs/superpowers/plans/2026-08-02-cache-cron-sync.md`

**Interfaces:**
- Consumes: Task 1 的 Cache 驱动改动
- Produces: 回归通过结论

- [ ] **Step 1: 回归相关包**

Run:
```bash
/Library/Go/sdk/go1.25.4/bin/go test ./drivers/cache/ ./internal/cache/ ./pkg/cron/ -count=1
```
Expected: 全部 PASS（pkg/cron 确认调度库层未受影响）。

- [ ] **Step 2: 提交计划文档**

```bash
git add docs/superpowers/plans/2026-08-02-cache-cron-sync.md
git commit -m "docs(cache): add cron expression sync implementation plan"
```

- [ ] **Step 3: 确认工作区干净**

Run:
```bash
git status --short
```
Expected: 无未提交改动。
