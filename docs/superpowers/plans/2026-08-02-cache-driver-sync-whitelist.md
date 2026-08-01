# cache 驱动同步白名单（SyncPaths）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 cache 驱动新增 `SyncPaths` 白名单配置，定时任务 `syncAll` 只扫描白名单子树（含主动种子同步），浏览行为不变。

**Architecture:** `Addition` 新增 `SyncPaths string`（`type:"text"`，换行/逗号分隔，填写下游实际路径）。`syncAll` 运行时把白名单条目从下游坐标转换为驱动相对坐标（去掉 actualPath 前缀），队列种子 = 白名单子树内过期缓存行 + 不在 DB 的白名单目录，BFS 下钻限制在白名单子树内；留空保持现有行为。

**Tech Stack:** Go 1.25（GOROOT `/Library/Go/sdk/go1.25.4`）、GORM + SQLite（测试内存库）、`pkg/cron`、`internal/op`、`pkg/utils`。

## Global Constraints

- 白名单**只影响定时任务同步**；`List`/`Get`/`Link` 浏览行为完全不变
- 白名单条目坐标系 = 下游存储实际路径（与 `GetStorageAndActualPath` 返回的 actualPath 相同）
- 条目语义：整个子树递归同步 + 未浏览过的白名单目录由定时任务首次拉取（种子同步）
- 白名单外缓存行：不刷新、不删除
- 覆盖语义不变：`UpsertCacheList` 取回什么覆盖什么；`op.List` 出错保留旧行
- 白名单留空 = 完全现有行为（向后兼容）
- 无效条目（不在 actualPath 之下）：记日志忽略，不影响其余条目
- 用 `/Library/Go/sdk/go1.25.4/bin/go` 和 `/Library/Go/sdk/go1.25.4/bin/gofmt`
- 测试跟随现有 `drivers/cache/sync_test.go` 惯例（package `cache` 白盒、`setup(t)` 建 Local+C ache 双存储）

---

### Task 1: SyncPaths 配置字段 + 解析/匹配纯函数

**Files:**
- Modify: `drivers/cache/meta.go:8-12`
- Modify: `drivers/cache/sync.go:1-20`
- Test: `drivers/cache/sync_test.go`

**Interfaces:**
- Consumes: 无（纯新增）
- Produces:
  - `Addition.SyncPaths string`（`json:"sync_paths" type:"text"`）
  - `parseSyncPaths(raw string) []string` — 换行/逗号分隔 → FixAndCleanPath 清理、去重、丢弃空项与 `/`；无有效项返回 nil
  - `withinSyncPaths(relPath string, entries []string) bool` — relPath 位于任一条目子树内（`utils.IsSubPath(entry, relPath)`）

- [ ] **Step 1: 写失败测试**（追加到 `drivers/cache/sync_test.go` 末尾）

```go
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
```

（`slices` 已由 Go 1.21+ 标准库提供，需在 sync_test.go 的 import 中加入 `"slices"`）

- [ ] **Step 2: 运行确认失败**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/cache/ -run 'TestParseSyncPaths|TestWithinSyncPaths' -v`
Expected: FAIL — undefined: parseSyncPaths / withinSyncPaths

- [ ] **Step 3: 实现**（`drivers/cache/meta.go` Addition 加字段；`drivers/cache/sync.go` 加两个函数）

meta.go:

```go
type Addition struct {
	RemotePath        string `json:"remote_path" required:"true" help:"the mount path of the downstream storage"`
	TTLHours          int    `json:"ttl_hours" required:"true" type:"number" default:"24" help:"cache validity period in hours"`
	SyncIntervalHours int    `json:"sync_interval_hours" required:"true" type:"number" default:"1" help:"background sync interval in hours, 0 to disable"`
	SyncPaths         string `json:"sync_paths" type:"text" help:"directories to sync (downstream actual paths, one per line or comma separated); empty = sync all cached"`
}
```

sync.go（import 增加 `"strings"` 与 `"github.com/OpenListTeam/OpenList/v4/pkg/utils"`；`"sort"` 已存在）：

```go
// parseSyncPaths 解析白名单字符串（换行/逗号分隔），返回清理后的下游实际路径列表。
// 无有效条目时返回 nil。
func parseSyncPaths(raw string) []string {
	raw = strings.ReplaceAll(raw, ",", "\n")
	seen := make(map[string]bool)
	var res []string
	for _, line := range strings.Split(raw, "\n") {
		p := utils.FixAndCleanPath(strings.TrimSpace(line))
		if p == "/" || seen[p] {
			continue
		}
		seen[p] = true
		res = append(res, p)
	}
	return res
}

// withinSyncPaths 判断 relPath（驱动相对坐标）是否位于任一白名单条目的子树内。
func withinSyncPaths(relPath string, entries []string) bool {
	for _, e := range entries {
		if utils.IsSubPath(e, relPath) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: 运行确认通过**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/cache/ -run 'TestParseSyncPaths|TestWithinSyncPaths' -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add drivers/cache/meta.go drivers/cache/sync.go drivers/cache/sync_test.go
git commit -m "feat(cache): add sync paths whitelist config with parse helpers"
```

---

### Task 2: 坐标转换 syncPathEntries

**Files:**
- Modify: `drivers/cache/sync.go`
- Test: `drivers/cache/sync_test.go`

**Interfaces:**
- Consumes: `parseSyncPaths`（Task 1）、`d.RemotePath`、`utils.IsSubPath`/`FixAndCleanPath`、`op.GetStorageAndActualPath`
- Produces: `func (d *Cache) syncPathEntries(actualPath string) ([]string, bool)` — 返回（驱动相对坐标条目列表, 是否启用白名单）。`enabled=false` 表示未配置；条目不在 actualPath 之下 → 记 `log.Warnf` 并忽略
- 转换规则：`rel = FixAndCleanPath(strings.TrimPrefix(w, actualPath))`（`/` 前缀 → 根 `/`；条目==actualPath → rel `/`，即全量）

- [ ] **Step 1: 写失败测试**（追加到 sync_test.go）

```go
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
```

（`".."` 经 FixAndCleanPath 变 `/` 被 parse 丢弃 → 空列表但 enabled=true：白名单模式开启、无条目 → syncAll 无事可做，不能退化为全量）

- [ ] **Step 2: 运行确认失败**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/cache/ -run TestSyncPathEntries -v`
Expected: FAIL — undefined: syncPathEntries

- [ ] **Step 3: 实现**（sync.go）

```go
// syncPathEntries 解析白名单（下游实际路径坐标）并转换为驱动相对坐标。
// enabled=false 表示未配置白名单（保持全量同步行为）。
func (d *Cache) syncPathEntries(actualPath string) ([]string, bool) {
	if strings.TrimSpace(d.SyncPaths) == "" {
		return nil, false
	}
	var rel []string
	for _, w := range parseSyncPaths(d.SyncPaths) {
		if !utils.IsSubPath(actualPath, w) {
			log.Warnf("cache: sync path %s is not under actual path %s, ignored", w, actualPath)
			continue
		}
		rel = append(rel, utils.FixAndCleanPath(strings.TrimPrefix(w, actualPath)))
	}
	return rel, true
}
```

- [ ] **Step 4: 运行确认通过**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/cache/ -run TestSyncPathEntries -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add drivers/cache/sync.go drivers/cache/sync_test.go
git commit -m "feat(cache): convert sync paths whitelist to driver-relative coords"
```

---

### Task 3: syncAll 白名单约束 + Init 校验

**Files:**
- Modify: `drivers/cache/sync.go:22-81`（syncAll 重构）
- Modify: `drivers/cache/driver.go:38-56`（Init 日志校验）
- Test: `drivers/cache/sync_test.go`

**Interfaces:**
- Consumes: `syncPathEntries`、`withinSyncPaths`（Task 1/2）、`ListCacheLists`、`UpsertCacheList`、`op.GetStorageAndActualPath`、`op.List`、`dirDepth`、`toCachedObj`
- Produces: 重构后的 `syncAll()` — 种子队列/下钻均受白名单约束；无白名单时行为与现状一致
- 行为变化点：
  1. `GetStorageAndActualPath` 从循环内提升到循环外（解析失败直接 return）
  2. `len(rows)==0` 不再直接 return（白名单模式下种子仍需执行）
  3. 白名单模式：种子 = 白名单内过期行 + 不在 DB 的白名单条目；白名单外行不刷新；下钻需 `withinSyncPaths`

- [ ] **Step 1: 写失败测试**（追加到 sync_test.go）

```go
func TestSyncAllSeedsWhitelistedDirs(t *testing.T) {
	d := setup(t)
	d.SyncPaths = "/sub"
	d.syncAll()

	item, err := GetCacheList(d.ID, "/sub")
	if err != nil || item == nil {
		t.Fatalf("expected seeded /sub row, got %v %v", item, err)
	}
	if !contains(names(fromCachedObjs(item.Data)), "b.txt") {
		t.Errorf("expected b.txt seeded, got %v", names(fromCachedObjs(item.Data)))
	}
	root, err := GetCacheList(d.ID, "/")
	if err != nil || root != nil {
		t.Errorf("expected no root row seeded, got %v %v", root, err)
	}
}

func TestSyncAllWhitelistRefreshesDescendants(t *testing.T) {
	d := setup(t)
	root := mustRootPath(d)
	_ = os.MkdirAll(filepath.Join(root, "sub", "deep"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "sub", "deep", "e.txt"), []byte("e"), 0o644)
	d.SyncPaths = "/sub"
	d.syncAll()

	deepRow, err := GetCacheList(d.ID, "/sub/deep")
	if err != nil || deepRow == nil {
		t.Fatalf("expected seeded descendant /sub/deep, got %v %v", deepRow, err)
	}
	if !contains(names(fromCachedObjs(deepRow.Data)), "e.txt") {
		t.Errorf("expected e.txt in /sub/deep, got %v", names(fromCachedObjs(deepRow.Data)))
	}
}

func TestSyncAllSkipsNonWhitelistedRows(t *testing.T) {
	d := setup(t)
	d.SyncPaths = "/sub"
	_, _ = d.List(context.Background(), rootDir(), model.ListArgs{})
	_, _ = d.List(context.Background(), &model.Object{Path: "/sub", Name: "sub", IsFolder: true}, model.ListArgs{})

	root := mustRootPath(d)
	_ = os.WriteFile(filepath.Join(root, "new.txt"), []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "sub", "c.txt"), []byte("z"), 0o644)
	aged := time.Now().Add(-48 * time.Hour)
	for _, p := range []string{"/", "/sub"} {
		item, err := GetCacheList(d.ID, p)
		if err != nil || item == nil {
			t.Fatalf("get row %s: %v %v", p, item, err)
		}
		if err := db.GetDb().Model(&model.CacheList{}).Where("id = ?", item.ID).Update("updated_at", aged).Error; err != nil {
			t.Fatalf("age row: %v", err)
		}
	}

	d.syncAll()

	rootRow, err := GetCacheList(d.ID, "/")
	if err != nil {
		t.Fatalf("get root row: %v", err)
	}
	if contains(names(fromCachedObjs(rootRow.Data)), "new.txt") {
		t.Errorf("non-whitelisted row must not be refreshed")
	}
	subRow, err := GetCacheList(d.ID, "/sub")
	if err != nil {
		t.Fatalf("get sub row: %v", err)
	}
	if !contains(names(fromCachedObjs(subRow.Data)), "c.txt") {
		t.Errorf("whitelisted row must be refreshed, got %v", names(fromCachedObjs(subRow.Data)))
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/cache/ -run 'TestSyncAllSeedsWhitelistedDirs|TestSyncAllWhitelistRefreshesDescendants|TestSyncAllSkipsNonWhitelistedRows' -v`
Expected: FAIL — 种子行为未实现（`/sub` 行不存在或没有 deep 行；`/` 行被刷新）

- [ ] **Step 3: 实现 syncAll 重构**（`drivers/cache/sync.go:22-81` 整体替换；`"sort"` 继续使用）

```go
func (d *Cache) syncAll() {
	rows, err := ListCacheLists(d.ID)
	if err != nil {
		log.Errorf("cache: list rows: %+v", err)
		return
	}
	remoteStorage, remoteActualPath, err := op.GetStorageAndActualPath(d.RemotePath)
	if err != nil {
		log.Errorf("cache: sync resolve remote %s: %+v", d.RemotePath, err)
		return
	}
	ttl := time.Duration(d.TTLHours) * time.Hour
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	entries, whitelisted := d.syncPathEntries(remoteActualPath)
	rowsByDir := make(map[string]model.CacheList, len(rows))
	known := make(map[string]bool, len(rows)*2)
	for i := range rows {
		rowsByDir[rows[i].DirPath] = rows[i]
		known[rows[i].DirPath] = true
	}
	stale := func(dirPath string) bool {
		return time.Since(rowsByDir[dirPath].UpdatedAt) >= ttl
	}
	queue := make([]string, 0)
	if !whitelisted {
		for i := range rows {
			if stale(rows[i].DirPath) {
				queue = append(queue, rows[i].DirPath)
			}
		}
	} else {
		for i := range rows {
			if withinSyncPaths(rows[i].DirPath, entries) && stale(rows[i].DirPath) {
				queue = append(queue, rows[i].DirPath)
			}
		}
		for _, e := range entries {
			if row, ok := rowsByDir[e]; !ok || time.Since(row.UpdatedAt) >= ttl {
				if !known[e] {
					known[e] = true
					queue = append(queue, e)
				}
			}
		}
	}
	if len(queue) == 0 {
		return
	}
	sort.Slice(queue, func(i, j int) bool {
		return dirDepth(queue[i]) < dirDepth(queue[j])
	})
	ctx := context.Background()
	for len(queue) > 0 {
		dirPath := queue[0]
		queue = queue[1:]
		objs, err := op.List(ctx, remoteStorage, stdpath.Join(remoteActualPath, dirPath), model.ListArgs{})
		if err != nil {
			// 保留既有缓存行，不删除：下游错误可能是暂时性故障（超时/5xx），
			// 删行会导致浏览 miss 回源雪崩；待日志足够后按错误细节决定保留/删除策略
			log.Errorf("cache: sync %s: %+v, keep stale row", dirPath, err)
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
				if !known[child] && (!whitelisted || withinSyncPaths(child, entries)) {
					known[child] = true
					queue = append(queue, child)
				}
			}
		}
	}
}
```

- [ ] **Step 4: Init 日志校验**（`drivers/cache/driver.go` Init 中 `d.syncProxy()` 之后、`d.cron` 启动之前插入）

```go
	if _, actualPath, err := op.GetStorageAndActualPath(d.RemotePath); err == nil {
		d.syncPathEntries(actualPath)
	} else {
		log.Warnf("cache: resolve remote for sync paths %s: %+v", d.RemotePath, err)
	}
```

（仅触发 `syncPathEntries` 的无效条目日志校验，不阻断初始化；`"github.com/OpenListTeam/OpenList/v4/internal/op"` 已 import）

- [ ] **Step 5: 运行全量包测试确认通过 + 无回归**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/cache/ -v`
Expected: PASS — 新增 3 个测试 + 既有 `TestSyncAllRefreshesExpired`/`TestSyncAllSkipsFresh`/`TestSyncAllKeepsRowOnFailure` 全部通过

- [ ] **Step 6: gofmt + 提交**

```bash
/Library/Go/sdk/go1.25.4/bin/gofmt -l drivers/cache/
git add drivers/cache/sync.go drivers/cache/driver.go drivers/cache/sync_test.go
git commit -m "feat(cache): constrain background sync to whitelisted subtrees"
```

---

## 自审记录

- **Spec 覆盖**：配置字段（Task 1）、坐标转换（Task 2）、种子/跳过/下钻受限（Task 3）、Init 校验（Task 3）、覆盖语义保留（Task 3 保留原 Upsert/keep-stale 逻辑）、留空兼容（syncAll `!whitelisted` 分支）、无效条目忽略（Task 2）。
- **无占位符**：所有代码块为最终实现内容。
- **类型一致性**：`parseSyncPaths(string) []string`、`withinSyncPaths(string, []string) bool`、`syncPathEntries(string) ([]string, bool)` 在三个任务间签名一致；测试中 `fromCachedObjs`/`names`/`contains`/`mustRootPath`/`setup` 均为 sync_test.go 既有工具，`slices` 在 Task 1 加入 import。
