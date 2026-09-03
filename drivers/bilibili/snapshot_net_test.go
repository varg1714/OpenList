package bilibili

import (
	"context"
	"net/http"
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
// 断言第二次 List 返回 3 条且快照已含 BV1c。
func TestListUpVideosIncrementalGrows(t *testing.T) {
	var fullCall atomic.Bool // 首次全量已发生
	d := listDriver(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/x/space/wbi/arc/search" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		var body string
		if !fullCall.Load() && r.URL.Query().Get("pn") == "1" {
			// 首次全量第 1 页（2 条 + total=2 → 拉完即止）
			fullCall.Store(true)
			body = `{"code":0,"data":{"list":{"vlist":[
				{"bvid":"BV1a","title":"旧1","created":1700000001},
				{"bvid":"BV1b","title":"旧2","created":1700000000}]},"page":{"count":2}}}`
		} else if fullCall.Load() && r.URL.Query().Get("pn") == "1" {
			// 增量第 1 页：新 BV1c + 已知 BV1a → 页内出现已知 → 接上停
			body = `{"code":0,"data":{"list":{"vlist":[
				{"bvid":"BV1c","title":"新1","created":1700000002},
				{"bvid":"BV1a","title":"旧1","created":1700000001}]},"page":{"count":3}}}`
		} else {
			body = `{"code":0,"data":{"list":{"vlist":[]},"page":{"count":0}}}`
		}
		w.Write([]byte(body))
	})
	d.mixinKey = "ea1db124af3c7062474693fa704f4ff8" // 注入，避免走 nav
	dir := dirObj(t, "/我的关注/某UP", upFolderPrefix+"42")

	objs, err := d.List(context.Background(), dir, model.ListArgs{})
	if err != nil {
		t.Fatalf("first List: %v", err)
	}
	if len(objs) != 2 {
		t.Fatalf("first objs = %d, want 2", len(objs))
	}

	objs, err = d.List(context.Background(), dir, model.ListArgs{})
	if err != nil {
		t.Fatalf("second List: %v", err)
	}
	if len(objs) != 3 || objs[0].GetName() != "新1.mp4" {
		t.Fatalf("second objs = %+v, want 3 with 新1 first", objs)
	}
	// 快照已持久化新增
	snap, err := db.GetVirtualDirSnapshot(d.ID, dir.GetPath())
	if err != nil || snap == nil {
		t.Fatalf("snap: %+v %v", snap, err)
	}
	if !strings.Contains(snap.Data, "BV1c") {
		t.Fatalf("snapshot must contain BV1c: %s", snap.Data)
	}
}

// TestListUpVideosFailKeepsSnapshot：目录已有快照后 API 失败 → List 返回旧快照数据（不报错）
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
	dir := dirObj(t, "/我的关注/某UP", upFolderPrefix+"42")
	if _, err := d.List(context.Background(), dir, model.ListArgs{}); err != nil {
		t.Fatalf("first List: %v", err)
	}
	objs, err := d.List(context.Background(), dir, model.ListArgs{})
	if err != nil {
		t.Fatalf("second List must fall back to snapshot, got err: %v", err)
	}
	if len(objs) != 1 || objs[0].GetName() != "唯一视频.mp4" {
		t.Fatalf("objs = %+v, want snapshot content", objs)
	}
}
