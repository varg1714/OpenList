package bilibili

import (
	"context"
	"errors"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/db"
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

func dbGetSnap(d *Bilibili, dirKey string) (*model.VirtualDirSnapshot, error) {
	return db.GetVirtualDirSnapshot(d.ID, dirKey)
}

func dbUpsertSnap(s *model.VirtualDirSnapshot) error {
	return db.UpsertVirtualDirSnapshot(s)
}

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
		[]intItem{{5, "e"}, {4, "d"}, {3, "c"}}, // 页内前 2 未知、第 3 条已知 → 本页即接上（不再翻页）
	)
	objs, err := listWithSnapshot(d, context.Background(), nil, "up_4", fetch, intKeyOf, intBuild)
	if err != nil {
		t.Fatalf("listWithSnapshot: %v", err)
	}
	// 永久存储：API 外的旧条目（2,1）保留 → 5 条；新条目插头部
	if len(objs) != 5 {
		t.Fatalf("objs = %d, want 5 (2 new + 3 old kept forever)", len(objs))
	}
	names := []string{objs[0].GetName(), objs[1].GetName(), objs[2].GetName(), objs[3].GetName(), objs[4].GetName()}
	want := []string{"e", "d", "c", "b", "a"} // 新条目在前，旧序保持
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("names = %v, want %v", names, want)
		}
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
	defer func(old []time.Duration) { pageRetryBackoff = old }(pageRetryBackoff)
	pageRetryBackoff = []time.Duration{0, 0}
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
	// 库未被写
	snap, err := dbGetSnap(d, "up_6")
	if err != nil || snap == nil || snap.Data != seed.Data {
		t.Fatalf("snapshot must be untouched: %+v %v", snap, err)
	}
}

func TestListWithSnapshotFailNoSnapshotErrors(t *testing.T) {
	// 首次失败（无快照）→ 返回错误
	defer func(old []time.Duration) { pageRetryBackoff = old }(pageRetryBackoff)
	pageRetryBackoff = []time.Duration{0, 0}
	d := newSnapDriver()
	fetch := func(pn int) ([]intItem, int, error) {
		return nil, 0, errors.New("network down")
	}
	if _, err := listWithSnapshot(d, context.Background(), nil, "up_7", fetch, intKeyOf, intBuild); err == nil {
		t.Fatal("first-fetch failure must return error")
	}
}

func TestListWithSnapshotCorruptDataRebuilds(t *testing.T) {
	// 快照 Data 损坏 → 按无快照全量重建覆盖
	d := newSnapDriver()
	bad := &model.VirtualDirSnapshot{StorageID: d.ID, DirKey: "up_8", Owner: "12345",
		Data: `{not json`}
	if err := dbUpsertSnap(bad); err != nil {
		t.Fatalf("seed: %v", err)
	}
	fetch := pagesFrom([]intItem{{1, "a"}})
	objs, err := listWithSnapshot(d, context.Background(), nil, "up_8", fetch, intKeyOf, intBuild)
	if err != nil {
		t.Fatalf("listWithSnapshot: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("objs = %d", len(objs))
	}
	snap, err := dbGetSnap(d, "up_8")
	if err != nil || snap == nil || snap.Data == bad.Data {
		t.Fatalf("snapshot must be rebuilt: %+v %v", snap, err)
	}
}

func TestListWithSnapshotVersionMismatchRebuilds(t *testing.T) {
	// 快照格式版本非 1 → 按无快照全量重建（V 是迁移钩子）
	d := newSnapDriver()
	old := &model.VirtualDirSnapshot{StorageID: d.ID, DirKey: "up_9", Owner: "12345",
		Data: `{"v":2,"items":[{"ID":99,"Name":"future-schema"}]}`}
	if err := dbUpsertSnap(old); err != nil {
		t.Fatalf("seed: %v", err)
	}
	fetch := pagesFrom([]intItem{{1, "a"}})
	objs, err := listWithSnapshot(d, context.Background(), nil, "up_9", fetch, intKeyOf, intBuild)
	if err != nil {
		t.Fatalf("listWithSnapshot: %v", err)
	}
	if len(objs) != 1 || objs[0].GetName() != "a" {
		t.Fatalf("objs = %+v, want rebuilt [a]", objs)
	}
	snap, err := dbGetSnap(d, "up_9")
	if err != nil || snap == nil || snap.Data == old.Data {
		t.Fatalf("snapshot must be rebuilt with v1: %+v %v", snap, err)
	}
}

func TestListWithSnapshotDupInPageDoesNotStopEarly(t *testing.T) {
	// 回归钉死：页内重复条目不得误触"接上"停止。
	// 场景：seed {5,4,3,2,1}；API 页1 {7,7}（重复新 7，无旧条目）、页2 {6}、页3 {5}（旧）。
	// 旧实现把页内重复计入"已见旧条目" → 页1 即停 → 漏 6（merged 6 条）；新实现 7 条。
	d := newSnapDriver()
	seed := &model.VirtualDirSnapshot{StorageID: d.ID, DirKey: "up_10", Owner: "12345",
		Data: `{"v":1,"items":[{"ID":5,"Name":"e"},{"ID":4,"Name":"d"},{"ID":3,"Name":"c"},{"ID":2,"Name":"b"},{"ID":1,"Name":"a"}]}`}
	if err := dbUpsertSnap(seed); err != nil {
		t.Fatalf("seed: %v", err)
	}
	fetch := pagesFrom(
		[]intItem{{7, "g"}, {7, "g"}}, // 重复新条目，无旧条目 → 不得停
		[]intItem{{6, "f"}},           // 继续翻页发现更多新条目
		[]intItem{{5, "e"}},           // 遇旧条目 → 停
	)
	objs, err := listWithSnapshot(d, context.Background(), nil, "up_10", fetch, intKeyOf, intBuild)
	if err != nil {
		t.Fatalf("listWithSnapshot: %v", err)
	}
	if len(objs) != 7 {
		t.Fatalf("objs = %d, want 7 ({7,6} new + {5,4,3,2,1} old), item 6 must not be lost", len(objs))
	}
	names := []string{objs[0].GetName(), objs[1].GetName(), objs[2].GetName(), objs[3].GetName(),
		objs[4].GetName(), objs[5].GetName(), objs[6].GetName()}
	want := []string{"g", "f", "e", "d", "c", "b", "a"}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("names = %v, want %v", names, want)
		}
	}
}

func TestListWithSnapshotFirstFullDedup(t *testing.T) {
	// 首拉页内重复：merged 去重（{1,1,2} → {1,2}）
	d := newSnapDriver()
	fetch := pagesFrom([]intItem{{1, "a"}, {1, "a"}, {2, "b"}})
	objs, err := listWithSnapshot(d, context.Background(), nil, "up_11", fetch, intKeyOf, intBuild)
	if err != nil {
		t.Fatalf("listWithSnapshot: %v", err)
	}
	if len(objs) != 2 {
		t.Fatalf("objs = %d, want 2 (dedup)", len(objs))
	}
}
