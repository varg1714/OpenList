package bilibili

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/go-resty/resty/v2"
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

// TestListConcurrentDifferentDirs：不同目录并发互不阻塞（各拉一次）
func TestListConcurrentDifferentDirs(t *testing.T) {
	var calls int64
	d := listDriver(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&calls, 1)
		switch r.URL.Path {
		case "/x/relation/followings":
			w.Write([]byte(`{"code":0,"data":{"list":[{"mid":1,"uname":"UP1"}],"total":1}}`))
		case "/x/v3/fav/folder/created/list-all":
			w.Write([]byte(`{"code":0,"data":{"list":[{"id":7,"title":"收藏夹7"}]}}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	})
	dirA := dirObj(t, "/我的关注", dirFollowID)
	dirB := dirObj(t, "/我的收藏", dirFavID)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, dir := range []model.Obj{dirA, dirB} {
		wg.Add(1)
		go func(i int, dir model.Obj) {
			defer wg.Done()
			_, errs[i] = d.List(context.Background(), dir, model.ListArgs{})
		}(i, dir)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("list %d: %v", i, err)
		}
	}
	if n := atomic.LoadInt64(&calls); n != 2 {
		t.Fatalf("api calls = %d, want 2 (one per dir)", n)
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
	d.uid = 1 // 模拟换账号前的旧缓存 uid
	srv := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/x/web-interface/nav" {
			w.Write([]byte(`{"code":0,"data":{"isLogin":true,"mid":1,"uname":"u"}}`))
			return
		}
		t.Fatalf("unexpected path %s", r.URL.Path)
	})
	d.client = resty.New().SetTransport(mockRoundTrip(srv))
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
