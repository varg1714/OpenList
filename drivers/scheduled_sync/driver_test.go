package scheduled_sync

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	_ "github.com/OpenListTeam/OpenList/v4/drivers/alias"
	"github.com/OpenListTeam/OpenList/v4/drivers/cache"
	_ "github.com/OpenListTeam/OpenList/v4/drivers/local"
	_ "github.com/OpenListTeam/OpenList/v4/drivers/strm"

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
	op.RegisterDriver(func() driver.Driver {
		return &fakeDownstream{}
	})
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
	calls = append(calls, listCall{path: dir.GetPath(), refresh: args.Refresh, scheduleScan: args.ScheduleScan})
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
	path         string
	refresh      bool
	scheduleScan bool
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
	d.SetStorage(model.Storage{MountPath: "/sched"})
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
		"/":      {"sub", "other"},
		"/sub":   {"x"},
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
		if !c.scheduleScan {
			t.Errorf("expected ScheduleScan=true (background scan marker) for %s", c.path)
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

func TestScanNestedWhitelistVisitsOnce(t *testing.T) {
	resetFake()
	registerFake(t)
	tree = map[string][]string{
		"/":              {"movies"},
		"/movies":        {"2024", "x"},
		"/movies/2024":   {"y"},
		"/movies/x":      nil,
		"/movies/2024/y": nil,
	}
	d := schedWith(Addition{RemotePath: "/fake", SyncCronExpr: "0 3 * * *", SyncPaths: "/movies\n/movies/2024"})
	d.scan()
	want := []string{"/movies", "/movies/2024", "/movies/x", "/movies/2024/y"}
	var got []string
	mu.Lock()
	for _, c := range calls {
		got = append(got, c.path)
	}
	mu.Unlock()
	if !slices.Equal(got, want) {
		t.Errorf("nested whitelist walk = %v, want %v (each dir must be listed once)", got, want)
	}
}

func TestScanSkipsDangerousChildNames(t *testing.T) {
	resetFake()
	registerFake(t)
	tree = map[string][]string{
		"/":      {"sub"},
		"/sub":   {"..", "x"},
		"/sub/x": nil,
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
		t.Errorf("dangerous-name walk = %v, want %v (.. child must not revisit /)", got, want)
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

	// 把缓存行老化到超过 TTL（默认 24h）：定时扫描只回源刷新过期行
	if err := db.GetDb().Model(&model.CacheList{}).
		Where("storage_id = ?", cacheDriver.GetStorage().ID).
		Update("updated_at", time.Now().Add(-25*time.Hour)).Error; err != nil {
		t.Fatalf("age cache rows: %v", err)
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

// 定时扫描穿过转发层（定时→别名→缓存→本地）时 ScheduleScan 必须透传，
// 缓存才能按 TTL 门控：行新鲜时扫描不回源，new.txt 不应出现在缓存行中。
// 若别名重建参数时丢掉 ScheduleScan，这里会退化为手动刷新语义全量回源。
func TestScanThroughAliasRespectsCacheTTL(t *testing.T) {
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
	aliasID, err := op.CreateStorage(context.Background(), model.Storage{
		Driver:    "Alias",
		MountPath: "/alias",
		Addition:  `{"paths":"/cache"}`,
	})
	if err != nil {
		t.Fatalf("create alias storage: %+v", err)
	}
	schedID, err := op.CreateStorage(context.Background(), model.Storage{
		Driver:    "ScheduledSync",
		MountPath: "/sched",
		Addition:  `{"remote_path":"/alias","sync_cron_expr":"0 3 * * *","refresh":true}`,
	})
	if err != nil {
		t.Fatalf("create sched storage: %+v", err)
	}
	t.Cleanup(func() {
		_ = op.DeleteStorageById(context.Background(), schedID)
		_ = op.DeleteStorageById(context.Background(), aliasID)
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

	// 行保持新鲜（未超过 TTL），扫描穿过别名层后必须仍被 TTL 门控
	_ = os.WriteFile(filepath.Join(tmp, "new.txt"), []byte("x"), 0o644)

	schedDriver, err := op.GetStorageByMountPath("/sched")
	if err != nil {
		t.Fatalf("get sched storage: %+v", err)
	}
	schedDriver.(*ScheduledSync).scan()

	objs, err := cd.List(context.Background(), root, model.ListArgs{})
	if err != nil {
		t.Fatalf("list cache after scan: %+v", err)
	}
	var names []string
	for _, o := range objs {
		names = append(names, o.GetName())
	}
	if slices.Contains(names, "new.txt") {
		t.Errorf("fresh row must be served from cache through alias chain, got %v", names)
	}
}

// 复现完整链路 定时→strm→别名(多根)→缓存→网盘：所有缓存行都新鲜时，
// 定时扫描不得触达任何网盘——ScheduleScan 必须穿透 strm 与别名两层转发，
// 缓存才能按 TTL 门控（行新鲜 = serve 缓存，不回源）。
func TestScanFullChainHitsNoNetdiskWhenRowsFresh(t *testing.T) {
	resetFake()
	registerFake(t)
	tree = map[string][]string{
		"/":     {"a", "b"},
		"/a":    {"f1"},
		"/b":    {"f2"},
		"/a/f1": nil,
		"/b/f2": nil,
	}
	cacheID1, err := op.CreateStorage(context.Background(), model.Storage{
		Driver:    "Cache",
		MountPath: "/cache1",
		Addition:  `{"remote_path":"/fake"}`,
	})
	if err != nil {
		t.Fatalf("create cache1: %+v", err)
	}
	cacheID2, err := op.CreateStorage(context.Background(), model.Storage{
		Driver:    "Cache",
		MountPath: "/cache2",
		Addition:  `{"remote_path":"/fake"}`,
	})
	if err != nil {
		t.Fatalf("create cache2: %+v", err)
	}
	aliasID, err := op.CreateStorage(context.Background(), model.Storage{
		Driver:    "Alias",
		MountPath: "/alias",
		Addition:  "{\"paths\":\"电视:/cache1\\n电影:/cache2\"}",
	})
	if err != nil {
		t.Fatalf("create alias: %+v", err)
	}
	strmID, err := op.CreateStorage(context.Background(), model.Storage{
		Driver:    "Strm",
		MountPath: "/strm",
		Addition:  `{"paths":"/alias"}`,
	})
	if err != nil {
		t.Fatalf("create strm: %+v", err)
	}
	schedID, err := op.CreateStorage(context.Background(), model.Storage{
		Driver:    "ScheduledSync",
		MountPath: "/sched",
		Addition:  `{"remote_path":"/strm","sync_cron_expr":"0 3 * * *","refresh":true}`,
	})
	if err != nil {
		t.Fatalf("create sched: %+v", err)
	}
	t.Cleanup(func() {
		_ = op.DeleteStorageById(context.Background(), schedID)
		_ = op.DeleteStorageById(context.Background(), strmID)
		_ = op.DeleteStorageById(context.Background(), aliasID)
		_ = op.DeleteStorageById(context.Background(), cacheID2)
		_ = op.DeleteStorageById(context.Background(), cacheID1)
	})
	cd1 := mustCache(t, "/cache1")
	cd2 := mustCache(t, "/cache2")

	// 两个缓存各自补齐全部目录行（新鲜，未超过 TTL）
	ctx := context.Background()
	for _, p := range []string{"/", "/a", "/b", "/a/f1", "/b/f2"} {
		dir := &model.Object{Path: p, Name: p, IsFolder: true}
		if _, err := cd1.List(ctx, dir, model.ListArgs{}); err != nil {
			t.Fatalf("prime cd1 %s: %+v", p, err)
		}
		if _, err := cd2.List(ctx, dir, model.ListArgs{}); err != nil {
			t.Fatalf("prime cd2 %s: %+v", p, err)
		}
	}

	// 清空网盘访问记录后触发扫描
	mu.Lock()
	calls = nil
	mu.Unlock()
	sched := mustSched(t)
	sched.scan()

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 0 {
		t.Errorf("scan must not touch netdisks when all cache rows fresh, got %d calls: %v", len(calls), calls)
	}
}

func mustCache(t *testing.T, mount string) *cache.Cache {
	t.Helper()
	d, err := op.GetStorageByMountPath(mount)
	if err != nil {
		t.Fatalf("get %s: %+v", mount, err)
	}
	return d.(*cache.Cache)
}

func mustSched(t *testing.T) *ScheduledSync {
	t.Helper()
	d, err := op.GetStorageByMountPath("/sched")
	if err != nil {
		t.Fatalf("get sched: %+v", err)
	}
	return d.(*ScheduledSync)
}
