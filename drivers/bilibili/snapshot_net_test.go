package bilibili

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
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

// TestListUpVideosIncrementalGrows：状态机 mock——
// 第一次 List（全量）：第 1 页 {BV1a,BV1b}；之后（增量）：第 1 页 {BV1c,BV1a}（新 1 条 BV1c 插头）
// 断言：父目录两次 List 各 1 次 API（首次全量/增量接上各一页）；年份目录 List 0 API（纯投影）。
func TestListUpVideosIncrementalGrows(t *testing.T) {
	var fullCall atomic.Bool
	var calls int64
	d := listDriver(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/x/space/wbi/arc/search" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		atomic.AddInt64(&calls, 1)
		var body string
		if !fullCall.Load() && r.URL.Query().Get("pn") == "1" {
			fullCall.Store(true)
			body = `{"code":0,"data":{"list":{"vlist":[
				{"bvid":"BV1a","title":"旧1","created":1700000001},
				{"bvid":"BV1b","title":"旧2","created":1700000000}]},"page":{"count":2}}}`
		} else if fullCall.Load() && r.URL.Query().Get("pn") == "1" {
			body = `{"code":0,"data":{"list":{"vlist":[
				{"bvid":"BV1c","title":"新1","created":1700000002},
				{"bvid":"BV1a","title":"旧1","created":1700000001}]},"page":{"count":3}}}`
		} else {
			body = `{"code":0,"data":{"list":{"vlist":[]},"page":{"count":0}}}`
		}
		w.Write([]byte(body))
	})
	d.mixinKey = "ea1db124af3c7062474693fa704f4ff8"
	wantYear := time.Unix(1700000002, 0).Year()
	dir := dirObj(t, "/我的关注/某UP", upFolderPrefix+"42")

	objs, err := d.List(context.Background(), dir, model.ListArgs{})
	if err != nil {
		t.Fatalf("first List: %v", err)
	}
	if len(objs) != 1 || objs[0].GetName() != strconv.Itoa(wantYear) {
		t.Fatalf("first objs = %+v, want single %d year folder", objs, wantYear)
	}
	objs, err = d.List(context.Background(), dir, model.ListArgs{})
	if err != nil {
		t.Fatalf("second List: %v", err)
	}
	if len(objs) != 1 || objs[0].GetID() != "up_42_"+strconv.Itoa(wantYear) {
		t.Fatalf("second objs = %+v", objs)
	}
	if n := atomic.LoadInt64(&calls); n != 2 {
		t.Fatalf("api calls = %d, want 2 (first full + one incremental page)", n)
	}
	// 年份目录投影：3 条、新 1 在前；0 新增 API
	yobjs, err := d.List(context.Background(),
		dirObj(t, "/我的关注/某UP/"+strconv.Itoa(wantYear), objs[0].GetID()), model.ListArgs{})
	if err != nil {
		t.Fatalf("List year dir: %v", err)
	}
	if len(yobjs) != 3 || yobjs[0].GetName() != "新1.mp4" {
		t.Fatalf("year objs = %+v, want 3 with 新1 first", yobjs)
	}
	if n := atomic.LoadInt64(&calls); n != 2 {
		t.Fatalf("api calls = %d, want 2 (year-dir List must not fetch)", n)
	}
	snap, err := db.GetVirtualDirSnapshot(d.ID, dir.GetPath())
	if err != nil || snap == nil {
		t.Fatalf("snap: %+v %v", snap, err)
	}
	if !strings.Contains(snap.Data, "BV1c") {
		t.Fatalf("snapshot must contain BV1c: %s", snap.Data)
	}
}

// TestListUpVideosFailKeepsSnapshot：目录已有快照后 API 失败 → List 返回旧快照数据（不报错）；
// 年份目录投影同样回退旧快照且零 API。
func TestListUpVideosFailKeepsSnapshot(t *testing.T) {
	defer func(old []time.Duration) { pageRetryBackoff = old }(pageRetryBackoff)
	pageRetryBackoff = []time.Duration{0, 0}
	var calls int64
	d := listDriver(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/x/space/wbi/arc/search" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if atomic.AddInt64(&calls, 1) == 1 {
			w.Write([]byte(`{"code":0,"data":{"list":{"vlist":[
				{"bvid":"BV1a","title":"唯一视频","created":1700000000}]},"page":{"count":1}}}`))
			return
		}
		// 后续请求全部风控（HTML 验证页）
		http.Error(w, "<html>verify</html>", http.StatusOK)
	})
	d.mixinKey = "ea1db124af3c7062474693fa704f4ff8"
	wantYear := time.Unix(1700000000, 0).Year()
	dir := dirObj(t, "/我的关注/某UP", upFolderPrefix+"42")
	if _, err := d.List(context.Background(), dir, model.ListArgs{}); err != nil {
		t.Fatalf("first List: %v", err)
	}
	objs, err := d.List(context.Background(), dir, model.ListArgs{})
	if err != nil {
		t.Fatalf("second List must fall back to snapshot, got err: %v", err)
	}
	if len(objs) != 1 || objs[0].GetName() != strconv.Itoa(wantYear) {
		t.Fatalf("objs = %+v, want snapshot-derived year folder", objs)
	}
	// 相对计数断言：年份目录 List 前后必须一致（纯投影零 API）。
	// 不做绝对计数：失败的增量第 1 页按 pageRetryBackoff{0,0} 重试共 3 次尝试
	// （首次全量 1 + 重试 3 = 4），与增量测试的“2”口径不同；此处只关心投影不新增请求。
	before := atomic.LoadInt64(&calls)
	yobjs, err := d.List(context.Background(),
		dirObj(t, "/我的关注/某UP/"+strconv.Itoa(wantYear), objs[0].GetID()), model.ListArgs{})
	if err != nil {
		t.Fatalf("year dir List: %v", err)
	}
	if len(yobjs) != 1 || yobjs[0].GetName() != "唯一视频.mp4" {
		t.Fatalf("year objs = %+v", yobjs)
	}
	if n := atomic.LoadInt64(&calls); n != before {
		t.Fatalf("api calls grew %d -> %d (year-dir projection must not fetch)", before, n)
	}
}
