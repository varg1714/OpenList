# ScheduledSync Driver Extraction Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extract the Cache driver's scheduled-scan responsibility into a new reusable ScheduledSync driver, leaving Cache with only caching + whitelist display filtering.

**Architecture:** ScheduledSync is a dumb cron trigger: on each fire it resolves its downstream storage via `op.GetStorageAndActualPath`, then BFS-walks whitelisted directories calling `op.List(ctx, downstream, path, ListArgs{Refresh: d.Refresh})`. Whitelist containment is enforced by the downstream's own `List` (Cache returns only whitelisted dirs) plus the sync driver's own `WithinSyncPaths` guard. Cache keeps `sync_paths` for browse filtering only; cron/interval/TTL fields and `syncAll` are deleted.

**Tech Stack:** Go 1.25, robfig/cron via `pkg/cron`, GORM + in-memory SQLite for tests, logrus, `pkg/generic.NewQueue`.

**Spec:** `docs/superpowers/specs/2026-08-02-scheduled-sync-driver-design.md`

## Global Constraints

- Run all Go commands with `/Library/Go/sdk/go1.25.4/bin/go` (and `/Library/Go/sdk/go1.25.4/bin/gofmt`) — Go is not on PATH by default.
- No new external dependencies; only reuse existing packages (`internal/syncpaths` is new but self-contained).
- Follow existing driver conventions: embed `model.Storage`, `Addition` struct + `config` + `op.RegisterDriver` in `meta.go`; comments in Chinese matching the codebase style.
- Conventional commit messages: `feat:` / `refactor:` / `test:` / `docs:`.
- Unknown JSON fields in stored `Addition` are silently ignored on unmarshal (legacy configs with removed fields keep working).
- Do NOT modify: `pkg/cron`, `internal/op`, `internal/model`, `drivers/cache/db.go` (except removing `ListCacheLists`), cache's display-filter behavior.

---

### Task 1: Shared `internal/syncpaths` package + tests

Extracts the whitelist utilities currently living in `drivers/cache/sync.go` into a shared package so both Cache and ScheduledSync use identical semantics. Pure addition — Cache still uses its own copies until Task 3.

**Files:**
- Create: `internal/syncpaths/syncpaths.go`
- Create: `internal/syncpaths/syncpaths_test.go`

**Interfaces:**
- Consumes: `pkg/utils` (`FixAndCleanPath`, `IsSubPath`), `github.com/sirupsen/logrus`
- Produces (used by Task 2 and Task 3):
  - `func ParseSyncPaths(raw string) []string`
  - `func WithinSyncPaths(relPath string, entries []string) bool`
  - `func DirDepth(dirPath string) int`
  - `func ToRelEntries(actualPath, raw string) ([]string, bool)`

- [ ] **Step 1: Write the failing tests**

`internal/syncpaths/syncpaths_test.go`:

```go
package syncpaths

import (
	"slices"
	"testing"
)

func TestParseSyncPaths(t *testing.T) {
	cases := []struct {
		raw  string
		want []string
	}{
		{"", nil},
		{"   \n  ", nil},
		{"..", []string{"/"}},
		{"/", []string{"/"}},
		{"/sub", []string{"/sub"}},
		{"sub", []string{"/sub"}},
		{"/sub\n/sub2", []string{"/sub", "/sub2"}},
		{"/a,/b\n/c", []string{"/a", "/b", "/c"}},
		{"/a\n/a,/b", []string{"/a", "/b"}},
	}
	for _, c := range cases {
		if got := ParseSyncPaths(c.raw); !slices.Equal(got, c.want) {
			t.Errorf("ParseSyncPaths(%q) = %v, want %v", c.raw, got, c.want)
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
		if got := WithinSyncPaths(c.path, entries); got != c.want {
			t.Errorf("WithinSyncPaths(%q) = %v, want %v", c.path, got, c.want)
		}
	}
	if !WithinSyncPaths("/a/b", []string{"/"}) {
		t.Errorf("root entry must match everything")
	}
}

func TestToRelEntries(t *testing.T) {
	cases := []struct {
		raw     string
		actual  string
		want    []string
		enabled bool
	}{
		{"", "/", nil, false},
		{"/sub", "/", []string{"/sub"}, true},
		{"/sub\n/missing", "/", []string{"/sub", "/missing"}, true},
		{"..", "/", []string{"/"}, true},
		{"/sub", "/other", nil, true},
		{"/local/sub", "/local", []string{"/sub"}, true},
	}
	for _, c := range cases {
		got, enabled := ToRelEntries(c.actual, c.raw)
		if enabled != c.enabled || !slices.Equal(got, c.want) {
			t.Errorf("ToRelEntries(%q, %q) = (%v, %v), want (%v, %v)", c.raw, c.actual, got, enabled, c.want, c.enabled)
		}
	}
}

func TestDirDepth(t *testing.T) {
	cases := map[string]int{"/": 0, "/a": 1, "/a/b": 2, "/a/b/c": 3}
	for path, want := range cases {
		if got := DirDepth(path); got != want {
			t.Errorf("DirDepth(%q) = %d, want %d", path, got, want)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./internal/syncpaths/...`
Expected: FAIL — package does not exist (build error).

- [ ] **Step 3: Implement the package**

`internal/syncpaths/syncpaths.go`:

```go
package syncpaths

import (
	"strings"

	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	log "github.com/sirupsen/logrus"
)

// DirDepth returns the depth of a driver-relative path; "/" has depth 0.
func DirDepth(dirPath string) int {
	if dirPath == "/" {
		return 0
	}
	return strings.Count(strings.Trim(dirPath, "/"), "/") + 1
}

// ParseSyncPaths parses a whitelist string (newline/comma separated) into a
// cleaned, de-duplicated path list. Returns nil when there are no valid entries.
func ParseSyncPaths(raw string) []string {
	raw = strings.ReplaceAll(raw, ",", "\n")
	seen := make(map[string]bool)
	var res []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || seen[utils.FixAndCleanPath(line)] {
			continue
		}
		p := utils.FixAndCleanPath(line)
		seen[p] = true
		res = append(res, p)
	}
	return res
}

// WithinSyncPaths reports whether relPath (driver-relative) lies inside any
// whitelist entry's subtree.
func WithinSyncPaths(relPath string, entries []string) bool {
	for _, e := range entries {
		if utils.IsSubPath(e, relPath) {
			return true
		}
	}
	return false
}

// ToRelEntries parses the whitelist (downstream actual-path coordinates) and
// converts it to driver-relative coordinates under actualPath. Entries not
// under actualPath are logged and dropped. Returns relEntries and whether the
// whitelist is enabled (raw contains any non-blank content).
func ToRelEntries(actualPath, raw string) ([]string, bool) {
	if strings.TrimSpace(raw) == "" {
		return nil, false
	}
	var rel []string
	for _, w := range ParseSyncPaths(raw) {
		if !utils.IsSubPath(actualPath, w) {
			log.Warnf("syncpaths: sync path %s is not under actual path %s, ignored", w, actualPath)
			continue
		}
		rel = append(rel, utils.FixAndCleanPath(strings.TrimPrefix(w, actualPath)))
	}
	return rel, true
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./internal/syncpaths/... -v`
Expected: PASS, 4 tests.

- [ ] **Step 5: Commit**

```bash
git add internal/syncpaths/
git commit -m "feat(syncpaths): extract shared whitelist parsing and path utilities"
```

---

### Task 2: ScheduledSync driver

The new driver: config + no-op data plane + cron lifecycle + BFS walk + tests (fake downstream + integration with a real Cache downstream).

**Files:**
- Create: `drivers/scheduled_sync/meta.go`
- Create: `drivers/scheduled_sync/walk.go`
- Create: `drivers/scheduled_sync/driver.go`
- Create: `drivers/scheduled_sync/driver_test.go` (bootstrap + fake downstream + walk/integration tests)
- Create: `drivers/scheduled_sync/lifecycle_test.go` (Init/Drop validation)

**Interfaces:**
- Consumes: `internal/syncpaths` from Task 1 (`ToRelEntries`, `WithinSyncPaths`, `DirDepth`); `pkg/cron` (`NewCronExpr`, `Cron.Do`, `Cron.Stop`); `internal/op` (`GetStorageAndActualPath`, `List`); `pkg/generic.NewQueue[string]`; `internal/errs` (`NotImplement`)
- Produces: driver named `"ScheduledSync"` registered via `op.RegisterDriver`; `ScheduledSync.scan()` (unexported, called by cron and directly in tests); struct fields `RemotePath`/`SyncCronExpr`/`SyncPaths`/`Refresh` from Addition

- [ ] **Step 1: Create `meta.go`**

```go
package scheduled_sync

import (
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
)

type Addition struct {
	RemotePath   string `json:"remote_path" required:"true" help:"the mount path of the downstream storage"`
	SyncCronExpr string `json:"sync_cron_expr" required:"true" help:"cron expression for scheduled scan, e.g. 0 3 * * *"`
	SyncPaths    string `json:"sync_paths" help:"directories to scan (downstream actual paths, one per line or comma separated); empty = walk from downstream root"`
	Refresh      bool   `json:"refresh" default:"true" help:"pass Refresh=true to downstream List calls; for Cache downstream this force-refreshes cache rows"`
}

var config = driver.Config{
	Name:        "ScheduledSync",
	NoUpload:    true,
	DefaultRoot: "/",
}

func init() {
	op.RegisterDriver(func() driver.Driver {
		return &ScheduledSync{
			Addition: Addition{Refresh: true},
		}
	})
}
```

- [ ] **Step 2: Create `walk.go`**

```go
package scheduled_sync

import (
	"context"
	stdpath "path"
	"sort"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/internal/syncpaths"
	"github.com/OpenListTeam/OpenList/v4/pkg/generic"
	log "github.com/sirupsen/logrus"
)

// scan 触发一次定时遍历：白名单条目（空白名单为下游根）按深度排序后入
// BFS 队列，每个目录通过下游自己的 List 获取（Refresh 由配置决定）。
// 白名单之外的目录不会出现在下游 List 的返回中（Cache 场景），
// 即使出现（普通驱动场景）也由 WithinSyncPaths 拦截，不会入队。
// 单目录失败仅记日志继续——保留下游已产生的数据，不删除。
func (d *ScheduledSync) scan() {
	remoteStorage, actualPath, err := op.GetStorageAndActualPath(d.RemotePath)
	if err != nil {
		log.Errorf("scheduled_sync: resolve remote %s: %+v", d.RemotePath, err)
		return
	}
	entries, whitelisted := syncpaths.ToRelEntries(actualPath, d.SyncPaths)
	seeds := make([]string, 0)
	if whitelisted {
		seeds = append(seeds, entries...)
	} else {
		seeds = append(seeds, "/")
	}
	if len(seeds) == 0 {
		return
	}
	sort.Slice(seeds, func(i, j int) bool {
		return syncpaths.DirDepth(seeds[i]) < syncpaths.DirDepth(seeds[j])
	})
	queue := generic.NewQueue[string]()
	for _, s := range seeds {
		queue.Push(s)
	}
	ctx := context.Background()
	for !queue.IsEmpty() {
		dirPath := queue.Pop()
		objs, err := op.List(ctx, remoteStorage, stdpath.Join(actualPath, dirPath), model.ListArgs{Refresh: d.Refresh})
		if err != nil {
			log.Errorf("scheduled_sync: list %s: %+v", dirPath, err)
			continue
		}
		for _, o := range objs {
			if !o.IsDir() {
				continue
			}
			child := stdpath.Join(dirPath, o.GetName())
			if !whitelisted || syncpaths.WithinSyncPaths(child, entries) {
				queue.Push(child)
			}
		}
	}
}
```

- [ ] **Step 3: Create `driver.go`**

```go
package scheduled_sync

import (
	"context"
	"strings"

	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/cron"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/pkg/errors"
)

type ScheduledSync struct {
	model.Storage
	Addition
	cron *cron.Cron
}

func (d *ScheduledSync) Config() driver.Config { return config }

func (d *ScheduledSync) GetAddition() driver.Additional { return &d.Addition }

func (d *ScheduledSync) Init(ctx context.Context) error {
	if strings.TrimSpace(d.RemotePath) == "" {
		return errors.New("remote path must not be empty")
	}
	d.RemotePath = utils.FixAndCleanPath(d.RemotePath)
	expr := strings.TrimSpace(d.SyncCronExpr)
	if expr == "" {
		return errors.New("sync_cron_expr must not be empty")
	}
	if d.cron != nil {
		d.cron.Stop()
		d.cron = nil
	}
	c, err := cron.NewCronExpr(expr)
	if err != nil {
		return errors.Wrapf(err, "scheduled_sync: invalid sync_cron_expr %q", utils.SanitizeHTML(expr))
	}
	d.cron = c
	d.cron.Do(d.scan)
	return nil
}

func (d *ScheduledSync) Drop(ctx context.Context) error {
	if d.cron != nil {
		d.cron.Stop()
		d.cron = nil
	}
	return nil
}

func (d *ScheduledSync) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {
	return nil, nil
}

func (d *ScheduledSync) Get(ctx context.Context, path string) (model.Obj, error) {
	return nil, errs.NotImplement
}

func (d *ScheduledSync) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
	return nil, errs.NotImplement
}

var _ driver.Driver = (*ScheduledSync)(nil)
```

- [ ] **Step 4: Run `go build` to verify the driver compiles**

Run: `/Library/Go/sdk/go1.25.4/bin/go build ./drivers/scheduled_sync/...`
Expected: success.

- [ ] **Step 5: Create `driver_test.go`** — bootstrap, fake downstream, walk tests, cache integration test

Package is internal (`package scheduled_sync`) so tests can call `d.scan()` directly.

```go
package scheduled_sync

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/drivers/cache"
	_ "github.com/OpenListTeam/OpenList/v4/drivers/local"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
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

// fakeDownstream 是可编程的下游驱动：tree 决定 List 返回，errPaths 决定哪些
// 目录 List 报错，calls 记录每次 List 调用（路径 + Refresh）。
type fakeDownstream struct {
	model.Storage
}

func (d *fakeDownstream) Config() driver.Config {
	return driver.Config{Name: "FakeDownstream", NoCache: true}
}

func (d *fakeDownstream) GetAddition() driver.Additional { return &struct{}{} }

func (d *fakeDownstream) Init(ctx context.Context) error { return nil }

func (d *fakeDownstream) Drop(ctx context.Context) error { return nil }

func (d *fakeDownstream) Get(ctx context.Context, path string) (model.Obj, error) {
	mu.Lock()
	defer mu.Unlock()
	if _, ok := tree[path]; !ok {
		return nil, errs.ObjectNotFound
	}
	return &model.Object{Path: path, Name: "Root", IsFolder: true}, nil
}

func (d *fakeDownstream) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {
	mu.Lock()
	defer mu.Unlock()
	calls = append(calls, listCall{path: dir.GetPath(), refresh: args.Refresh})
	if errPaths[dir.GetPath()] {
		return nil, fmt.Errorf("scripted error for %s", dir.GetPath())
	}
	var res []model.Obj
	for _, name := range tree[dir.GetPath()] {
		res = append(res, &model.Object{Name: name, IsFolder: true})
	}
	return res, nil
}

func (d *fakeDownstream) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
	return nil, errs.NotImplement
}

type listCall struct {
	path    string
	refresh bool
}

var (
	mu       sync.Mutex
	calls    []listCall
	tree     map[string][]string
	errPaths map[string]bool
)

func resetFake() {
	calls = nil
	tree = make(map[string][]string)
	errPaths = make(map[string]bool)
}

func registerFake(t *testing.T) uint {
	t.Helper()
	id, err := op.CreateStorage(context.Background(), model.Storage{
		Driver:    "FakeDownstream",
		MountPath: "/fake",
		Addition:  "{}",
	})
	if err != nil {
		t.Fatalf("create fake storage: %+v", err)
	}
	t.Cleanup(func() { _ = op.DeleteStorageById(context.Background(), id) })
	return id
}

func schedWith(addition Addition) *ScheduledSync {
	d := &ScheduledSync{}
	_ = d.SetStorage(model.Storage{MountPath: "/sched"})
	d.Addition = addition
	return d
}

func TestScanWalksFullTreeFromRoot(t *testing.T) {
	resetFake()
	registerFake(t)
	tree = map[string][]string{
		"/":    {"a", "b"},
		"/a":   {"c"},
		"/a/c": nil,
		"/b":   nil,
	}
	d := schedWith(Addition{RemotePath: "/fake", SyncCronExpr: "0 3 * * *", Refresh: false})
	d.scan()
	want := []string{"/", "/a", "/b", "/a/c"}
	var got []string
	mu.Lock()
	for _, c := range calls {
		got = append(got, c.path)
	}
	mu.Unlock()
	if !slices.Equal(got, want) {
		t.Errorf("walk order = %v, want %v", got, want)
	}
}

func TestScanRespectsWhitelist(t *testing.T) {
	resetFake()
	registerFake(t)
	tree = map[string][]string{
		"/":     {"sub", "other"},
		"/sub":  {"x"},
		"/sub/x": nil,
		"/other": {"y"},
	}
	d := schedWith(Addition{RemotePath: "/fake", SyncCronExpr: "0 3 * * *", SyncPaths: "/sub"})
	d.scan()
	want := []string{"/sub", "/sub/x"}
	var got []string
	mu.Lock()
	for _, c := range calls {
		got = append(got, c.path)
	}
	mu.Unlock()
	if !slices.Equal(got, want) {
		t.Errorf("whitelist walk = %v, want %v (siblings must not be listed)", got, want)
	}
}

func TestScanPassesRefreshFlag(t *testing.T) {
	resetFake()
	registerFake(t)
	tree = map[string][]string{"/": {"a"}}
	d := schedWith(Addition{RemotePath: "/fake", SyncCronExpr: "0 3 * * *", Refresh: true})
	d.scan()
	mu.Lock()
	defer mu.Unlock()
	if len(calls) == 0 {
		t.Fatal("expected list calls")
	}
	for _, c := range calls {
		if !c.refresh {
			t.Errorf("expected Refresh=true for %s", c.path)
		}
	}
}

func TestScanContinuesOnListError(t *testing.T) {
	resetFake()
	registerFake(t)
	tree = map[string][]string{
		"/":  {"a", "b"},
		"/a": nil,
		"/b": nil,
	}
	errPaths = map[string]bool{"/a": true}
	d := schedWith(Addition{RemotePath: "/fake", SyncCronExpr: "0 3 * * *", Refresh: false})
	d.scan()
	want := []string{"/", "/a", "/b"}
	var got []string
	mu.Lock()
	for _, c := range calls {
		got = append(got, c.path)
	}
	mu.Unlock()
	if !slices.Equal(got, want) {
		t.Errorf("error-continuation walk = %v, want %v", got, want)
	}
}

func TestScanSkipsWhenDownstreamMissing(t *testing.T) {
	resetFake()
	d := schedWith(Addition{RemotePath: "/ghost", SyncCronExpr: "0 3 * * *"})
	d.scan() // must not panic
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 0 {
		t.Errorf("expected no list calls, got %v", calls)
	}
}

func TestScanOnCacheDownstreamRefreshesRows(t *testing.T) {
	tmp := t.TempDir()
	_ = os.WriteFile(filepath.Join(tmp, "a.txt"), []byte("hello"), 0o644)
	_ = os.MkdirAll(filepath.Join(tmp, "sub"), 0o755)
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
		Addition:  `{"remote_path":"/local"}`,
	})
	if err != nil {
		t.Fatalf("create cache storage: %+v", err)
	}
	schedID, err := op.CreateStorage(context.Background(), model.Storage{
		Driver:    "ScheduledSync",
		MountPath: "/sched",
		Addition:  `{"remote_path":"/cache","sync_cron_expr":"0 3 * * *","refresh":true}`,
	})
	if err != nil {
		t.Fatalf("create sched storage: %+v", err)
	}
	t.Cleanup(func() {
		_ = op.DeleteStorageById(context.Background(), schedID)
		_ = op.DeleteStorageById(context.Background(), cacheID)
		_ = op.DeleteStorageById(context.Background(), localID)
	})
	cacheDriver, err := op.GetStorageByMountPath("/cache")
	if err != nil {
		t.Fatalf("get cache storage: %+v", err)
	}
	cd := cacheDriver.(*cache.Cache)
	root := &model.Object{Path: "/", Name: "Root", IsFolder: true}
	if _, err := cd.List(context.Background(), root, model.ListArgs{}); err != nil {
		t.Fatalf("prime cache root: %+v", err)
	}

	// 修改下游文件系统：新增 new.txt、删除 a.txt
	_ = os.WriteFile(filepath.Join(tmp, "new.txt"), []byte("x"), 0o644)
	_ = os.Remove(filepath.Join(tmp, "a.txt"))

	schedDriver, err := op.GetStorageByMountPath("/sched")
	if err != nil {
		t.Fatalf("get sched storage: %+v", err)
	}
	schedDriver.(*ScheduledSync).scan()

	// 直接调 Cache 驱动的 List（不走 op.dirCache）断言缓存行已被 scan 刷新
	objs, err := cd.List(context.Background(), root, model.ListArgs{})
	if err != nil {
		t.Fatalf("list cache after scan: %+v", err)
	}
	var names []string
	for _, o := range objs {
		names = append(names, o.GetName())
	}
	if !slices.Contains(names, "new.txt") {
		t.Errorf("cache row missing new.txt after scan: %v", names)
	}
	if slices.Contains(names, "a.txt") {
		t.Errorf("cache row still has a.txt after scan: %v", names)
	}
}
```

- [ ] **Step 6: Create `lifecycle_test.go`**

```go
package scheduled_sync

import (
	"context"
	"strings"
	"testing"
)

func TestInitRejectsEmptyRemotePath(t *testing.T) {
	d := schedWith(Addition{SyncCronExpr: "0 3 * * *"})
	if err := d.Init(context.Background()); err == nil {
		t.Fatal("expected error for empty remote path")
	}
}

func TestInitRejectsEmptyCronExpr(t *testing.T) {
	d := schedWith(Addition{RemotePath: "/fake"})
	if err := d.Init(context.Background()); err == nil {
		t.Fatal("expected error for empty cron expr")
	}
}

func TestInitRejectsInvalidCronExpr(t *testing.T) {
	d := schedWith(Addition{RemotePath: "/fake", SyncCronExpr: "60 * * * *"})
	err := d.Init(context.Background())
	if err == nil || !strings.Contains(err.Error(), "sync_cron_expr") {
		t.Fatalf("expected error mentioning sync_cron_expr, got %v", err)
	}
}

func TestInitStartsCronDropStops(t *testing.T) {
	d := schedWith(Addition{RemotePath: "/fake", SyncCronExpr: " 0 3 * * * "})
	if err := d.Init(context.Background()); err != nil {
		t.Fatalf("init: %+v", err)
	}
	if d.cron == nil {
		t.Fatal("expected cron started after init")
	}
	if err := d.Drop(context.Background()); err != nil {
		t.Fatalf("drop: %+v", err)
	}
	if d.cron != nil {
		t.Fatal("expected cron stopped after drop")
	}
}

func TestReInitRestartsCron(t *testing.T) {
	d := schedWith(Addition{RemotePath: "/fake", SyncCronExpr: "0 3 * * *"})
	if err := d.Init(context.Background()); err != nil {
		t.Fatalf("first init: %+v", err)
	}
	if err := d.Init(context.Background()); err != nil {
		t.Fatalf("second init: %+v", err)
	}
	if d.cron == nil {
		t.Fatal("expected cron rebuilt after re-init")
	}
}
```

Note: `TestInitRejectsInvalidCronExpr` and the others reference `schedWith` from Step 5 — implement it there.

- [ ] **Step 7: Run the scheduled_sync tests**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/scheduled_sync/... -v`
Expected: PASS — 11 tests (6 walk/integration + 5 lifecycle).
Troubleshoot if `op.CreateStorage` for the fake fails: the fake's `GetAddition` returns `&struct{}{}`; if `IsUseOnlineAPI` or storage details code requires a `WithDetails` method, it does not — `GetDetails` is optional.

- [ ] **Step 8: Commit**

```bash
git add drivers/scheduled_sync/
git commit -m "feat(scheduled_sync): add cron-triggered scan driver for arbitrary downstreams"
```

---

### Task 3: Cache driver refactor — remove scheduling, keep display filtering

Removes cron/interval/TTL/syncAll from Cache, swaps its whitelist helpers for `internal/syncpaths`, and updates/deletes tests accordingly. This is the user-visible behavior change: scheduled sync now lives in ScheduledSync storages.

**Files:**
- Modify: `drivers/cache/meta.go` (Addition fields, init default)
- Modify: `drivers/cache/driver.go` (remove cron/TTL/buildSyncCron; use syncpaths)
- Modify: `drivers/cache/sync.go` (delete syncAll + moved helpers, keep visibleInSyncPaths)
- Modify: `drivers/cache/db.go` (remove `ListCacheLists`)
- Modify: `drivers/cache/sync_test.go` (delete syncAll tests, keep whitelist display tests)
- Delete: `drivers/cache/schedule_test.go`
- Modify: `drivers/cache/driver_test.go` (delete cron tests, rewrite TestLegacyAdditionUnmarshal)
- Modify: `drivers/cache/db_test.go` (delete TestListCacheLists)

**Interfaces:**
- Consumes: `internal/syncpaths` (`ToRelEntries`)
- Produces: Cache `Addition` = `{RemotePath, SyncPaths}` only; `syncpaths.ToRelEntries` replaces `d.syncPathEntries`; `visibleInSyncPaths` stays in cache

- [ ] **Step 1: Update `meta.go`**

Replace the `Addition` struct and `init()` default:

```go
type Addition struct {
	RemotePath string `json:"remote_path" required:"true" help:"the mount path of the downstream storage"`
	SyncPaths  string `json:"sync_paths" help:"directories to show when browsing (downstream actual paths, one per line or comma separated); empty = show all cached; scheduled scanning is handled by a ScheduledSync storage pointing at this one"`
}
```

and `init()`:

```go
func init() {
	op.RegisterDriver(func() driver.Driver {
		return &Cache{}
	})
}
```

- [ ] **Step 2: Update `driver.go`**

- Remove the `cron *cron.Cron` field from `Cache` (line 21).
- Remove `buildSyncCron` (lines 38-50).
- Rewrite `Init` (lines 52-79):

```go
func (d *Cache) Init(ctx context.Context) error {
	if strings.TrimSpace(d.RemotePath) == "" {
		return errors.New("remote path must not be empty")
	}
	d.RemotePath = utils.FixAndCleanPath(d.RemotePath)
	d.syncProxy()
	if _, actualPath, err := op.GetStorageAndActualPath(d.RemotePath); err == nil {
		syncpaths.ToRelEntries(actualPath, d.SyncPaths)
	} else {
		log.Warnf("cache: resolve remote for sync paths %s: %+v", d.RemotePath, err)
	}
	return nil
}
```

- Rewrite `Drop` (lines 81-87):

```go
func (d *Cache) Drop(ctx context.Context) error {
	return nil
}
```

- In `List` (line 117), replace `entries, whitelisted := d.syncPathEntries(remoteActualPath)` with:

```go
entries, whitelisted := syncpaths.ToRelEntries(remoteActualPath, d.SyncPaths)
```

- Add import `"github.com/OpenListTeam/OpenList/v4/internal/syncpaths"` and remove the now-unused `"github.com/OpenListTeam/OpenList/v4/pkg/cron"` import.
- `filterCachedObjs` (lines 145-156) is unchanged.

- [ ] **Step 3: Update `sync.go`**

Delete `dirDepth` (lines 17-22), `parseSyncPaths` (lines 24-40), `withinSyncPaths` (lines 42-50), `syncPathEntries` (lines 65-80), and `syncAll` (lines 82-166). Keep only:

```go
package cache

import (
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

// visibleInSyncPaths 判断 relPath 是否可见：与任一白名单条目同链
// （relPath 是条目的祖先/等于，或条目是 relPath 的祖先/等于）。
// 祖先目录作为导航路径展示（如条目 /电影/邻居 的祖先 /电影），
// 但定时扫描范围由 ScheduledSync 驱动经 syncpaths.WithinSyncPaths 限定。
func visibleInSyncPaths(relPath string, entries []string) bool {
	for _, e := range entries {
		if utils.IsSubPath(e, relPath) || utils.IsSubPath(relPath, e) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Update `db.go`**

Delete `ListCacheLists` (lines 46-49). Keep `GetCacheList`, `UpsertCacheList`, `DeleteCacheList`.

- [ ] **Step 5: Update `sync_test.go`**

- Delete these syncAll tests (lines 105-218, 297-369, 485-506): `TestSyncAllRefreshesExpired`, `TestSyncAllFullRefreshOnRootPath`, `TestSyncAllSkipsFresh`, `TestSyncAllKeepsRowOnFailure`, `TestSyncAllSeedsWhitelistedDirs`, `TestSyncAllWhitelistRefreshesDescendants`, `TestSyncAllSkipsNonWhitelistedRows`, `TestSyncAllSeedsDeepWhitelistEntry`.
- Delete the migrated helpers tests `TestParseSyncPaths` (219-239), `TestWithinSyncPaths` (241-263), `TestSyncPathEntries` (265-295) — they now live in `internal/syncpaths/syncpaths_test.go`.
- Keep the display-filter tests unchanged: `TestListWhitelistFiltersRoot`, `TestListWhitelistShowsSubtree`, `TestListWhitelistOutsideDirEmpty`, `TestListWhitelistShowsAncestorDirs`, `TestVisibleInSyncPaths`, `TestListWhitelistRefreshStillFilters`.
- Keep `mustRootPath` (86-95) — still used by the kept `TestListWhitelistShowsAncestorDirs` (line 426), so the `driver` import stays too. Delete the orphaned `mustLocalStorageID` (97-103).

- [ ] **Step 6: Delete `schedule_test.go`**

`git rm drivers/cache/schedule_test.go` (contains only `buildSyncCron` tests).

- [ ] **Step 7: Update `driver_test.go`**

- Delete the cron tests `TestCronExprScheduleInit` (300-352), `TestInvalidCronExprRejected` (355-401), `TestReInitKeepsSchedule` (405-441, end of file).
- Simplify the cache `Addition` strings in `setup()` (line 51) and `TestProxyInheritanceFromDownstream` (line 97) from `{"remote_path":"...","ttl_hours":24,"sync_interval_hours":0}` to `{"remote_path":"..."}` (the old fields no longer exist; unknown keys are ignored but keep tests honest).
- Rewrite `TestLegacyAdditionUnmarshal` (284-297):

```go
// 旧版 Addition JSON（含已移除的 sync/ttl 字段）必须无错误反序列化，
// 未知字段被忽略，保留字段仍可读——向后兼容的回归保护。
func TestLegacyAdditionUnmarshal(t *testing.T) {
	var a cache.Addition
	if err := utils.Json.UnmarshalFromString(`{"remote_path":"/local","ttl_hours":24,"sync_interval_hours":2,"sync_cron_expr":"0 3 * * *","sync_paths":"/sub"}`, &a); err != nil {
		t.Fatalf("legacy addition unmarshal: %+v", err)
	}
	if a.RemotePath != "/local" {
		t.Errorf("expected remote_path /local, got %q", a.RemotePath)
	}
	if a.SyncPaths != "/sub" {
		t.Errorf("expected sync_paths /sub, got %q", a.SyncPaths)
	}
}
```

- [ ] **Step 8: Update `db_test.go`**

Delete `TestListCacheLists` (92-end); keep `TestGetCacheListNotFound`, `TestUpsertCreateThenUpdate`, `TestStorageIsolation`, `TestDeleteCacheList`.

- [ ] **Step 9: Run all affected tests**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/cache/... ./internal/syncpaths/... ./drivers/scheduled_sync/...`
Expected: PASS. Fix any unused imports/variables the deletions leave behind (run `/Library/Go/sdk/go1.25.4/bin/go vet ./drivers/cache/...` to surface them).

- [ ] **Step 10: Commit**

```bash
git add -A drivers/cache/
git commit -m "refactor(cache): move scheduled scanning to ScheduledSync driver"
```

---

### Task 4: Final verification

- [ ] **Step 1: Build the whole module**

Run: `/Library/Go/sdk/go1.25.4/bin/go build ./...`
Expected: success.

- [ ] **Step 2: Run the full test suite for affected packages**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/cache/... ./internal/syncpaths/... ./drivers/scheduled_sync/...`
Expected: all PASS.

- [ ] **Step 3: Confirm no stale references**

Run: `rg "sync_interval_hours|sync_cron_expr|ttl_hours|buildSyncCron|syncAll|ListCacheLists|syncPathEntries" drivers/cache internal/syncpaths drivers/scheduled_sync`
Expected: no matches in Go files (legacy JSON strings in test fixtures for the unmarshal test are the only acceptable match — keep that one occurrence in `driver_test.go`).

- [ ] **Step 4: Commit any stragglers**

```bash
git status --short
```

If anything is left (e.g. gofmt reformat), commit it with an appropriate message. Otherwise no commit needed.
