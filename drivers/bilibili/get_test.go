package bilibili

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/go-resty/resty/v2"
)

// getReadyDriver：先 List 落库（followings + UP 投稿 + 收藏夹 + 收藏视频），
// 返回计数 driver 与累计 API 请求数——Get 纯查库不得再发请求
func getReadyDriver(t *testing.T) (*Bilibili, *int64) {
	t.Helper()
	var calls int64
	d := listDriver(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&calls, 1)
		switch r.URL.Path {
		case "/x/relation/followings":
			w.Write([]byte(`{"code":0,"data":{"list":[
				{"mid":42,"uname":"测试UP"},{"mid":7,"uname":"同名UP"}],"total":2}}`))
		case "/x/v3/fav/folder/created/list-all":
			w.Write([]byte(`{"code":0,"data":{"list":[{"id":999,"title":"我的收藏夹"}]}}`))
		case "/x/space/wbi/arc/search":
			w.Write([]byte(`{"code":0,"data":{"list":{"vlist":[
				{"bvid":"BV1a","title":"最新视频","created":1700000100},
				{"bvid":"BV1b","title":"第1集","created":1700000100}]},"page":{"count":2}}}`))
		case "/x/v3/fav/resource/list":
			w.Write([]byte(`{"code":0,"data":{"info":{"media_count":1},"medias":[
				{"bvid":"BV1c","title":"收藏视频","cover":"","fav_time":1700000200,"ugc":{"first_cid":555}}]}}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	})
	d.mixinKey = "ea1db124af3c7062474693fa704f4ff8"

	dirs := []model.Obj{
		dirObj(t, "/我的关注", dirFollowID),
		dirObj(t, "/我的关注/测试UP", upFolderPrefix+"42"),
		dirObj(t, "/我的收藏", dirFavID),
		dirObj(t, "/我的收藏/我的收藏夹", favFolderPrefix+"999"),
	}
	for _, dir := range dirs {
		if _, err := d.List(context.Background(), dir, model.ListArgs{}); err != nil {
			t.Fatalf("seed List %s: %v", dir.GetPath(), err)
		}
	}
	return d, &calls
}

func TestGetDeepFollowingsFromSnapshot(t *testing.T) {
	d, calls := getReadyDriver(t)

	// UP 目录：命中快照（folder obj 带内部 ID）
	obj, err := d.Get(context.Background(), "/我的关注/测试UP")
	if err != nil {
		t.Fatalf("Get up dir: %v", err)
	}
	if !obj.IsDir() || obj.GetID() != "up_42" || obj.GetName() != "测试UP" {
		t.Fatalf("up obj = %+v (id=%q)", obj, obj.GetID())
	}

	// 视频文件：命中快照 → *videoObj 带 bvid
	vo, err := d.Get(context.Background(), "/我的关注/测试UP/最新视频.mp4")
	if err != nil {
		t.Fatalf("Get video: %v", err)
	}
	v, ok := vo.(*videoObj)
	if !ok || v.bvid != "BV1a" {
		t.Fatalf("video obj = %T %+v", vo, vo)
	}

	// 收藏夹目录 + 收藏视频
	fo, err := d.Get(context.Background(), "/我的收藏/我的收藏夹")
	if err != nil || fo.GetID() != "fav_999" {
		t.Fatalf("Get fav folder = %+v %v", fo, err)
	}
	fv, err := d.Get(context.Background(), "/我的收藏/我的收藏夹/收藏视频.mp4")
	if err != nil {
		t.Fatalf("Get fav video: %v", err)
	}
	if f, ok := fv.(*videoObj); !ok || f.cid != 555 {
		t.Fatalf("fav video obj = %T %+v", fv, fv)
	}

	// 全部命中 = 0 新增 API 请求
	if n := atomic.LoadInt64(calls); n != 4 {
		t.Fatalf("api calls = %d, want 4 (only the 4 seed lists)", n)
	}
}

func TestGetDeepSuffixedSegments(t *testing.T) {
	// 重名消歧后缀段（UP "同名UP_7"）与 bvid 后缀视频段
	d := newTestDriver()
	d.ID = 70001
	d.uid = 12345
	srv := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/x/relation/followings":
			w.Write([]byte(`{"code":0,"data":{"list":[
				{"mid":7,"uname":"同名UP"},{"mid":8,"uname":"同名UP"}],"total":2}}`))
		case "/x/space/wbi/arc/search":
			w.Write([]byte(`{"code":0,"data":{"list":{"vlist":[
				{"bvid":"BV1a","title":"第1集","created":1700000100},
				{"bvid":"BV1b","title":"第1集","created":1700000100}]},"page":{"count":2}}}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	})
	d.client = resty.New().SetTransport(mockRoundTrip(srv))
	d.mixinKey = "ea1db124af3c7062474693fa704f4ff8"

	// 落库：followings（两个同名 → 消歧 "同名UP_7"/"同名UP_8"）+ 投稿（同名标题 → bvid 后缀）
	dir := dirObj(t, "/我的关注", dirFollowID)
	if _, err := d.List(context.Background(), dir, model.ListArgs{}); err != nil {
		t.Fatalf("seed followings: %v", err)
	}
	for _, up := range []model.Obj{
		dirObj(t, "/我的关注/同名UP_7", upFolderPrefix+"7"),
		dirObj(t, "/我的关注/同名UP_8", upFolderPrefix+"8"),
	} {
		if _, err := d.List(context.Background(), up, model.ListArgs{}); err != nil {
			t.Fatalf("seed %s: %v", up.GetPath(), err)
		}
	}

	// 后缀段命中
	obj, err := d.Get(context.Background(), "/我的关注/同名UP_7/第1集_BV1a.mp4")
	if err != nil {
		t.Fatalf("Get suffixed: %v", err)
	}
	v, ok := obj.(*videoObj)
	if !ok || v.bvid != "BV1a" {
		t.Fatalf("obj = %T %+v", obj, obj)
	}
	// 干净名段仍可命中（无重名时才存在——此处标题重名，干净名应 miss）
	if _, err := d.Get(context.Background(), "/我的关注/同名UP_7/第1集.mp4"); !errs.IsObjectNotFound(err) {
		t.Fatalf("clean dup title should miss: %v", err)
	}
}

func TestGetDeepMissIsObjectNotFound(t *testing.T) {
	// 纯查库：快照无该条目 / 无快照 → ObjectNotFound，零 API 请求（决策 A）
	d, calls := getReadyDriver(t)
	if _, err := d.Get(context.Background(), "/我的关注/不存在的UP"); !errs.IsObjectNotFound(err) {
		t.Fatalf("missing up should be ObjectNotFound: %v", err)
	}
	if _, err := d.Get(context.Background(), "/我的关注/测试UP/不存在的视频.mp4"); !errs.IsObjectNotFound(err) {
		t.Fatalf("missing video should be ObjectNotFound: %v", err)
	}
	// 未同步目录（无快照）：同样 404，不触发 List
	if _, err := d.Get(context.Background(), "/我的关注/从未同步的UP"); !errs.IsObjectNotFound(err) {
		t.Fatalf("unsynced dir should be ObjectNotFound: %v", err)
	}
	if n := atomic.LoadInt64(calls); n != 4 {
		t.Fatalf("api calls = %d, want 4 (Get must not trigger any)", n)
	}
	if _, err := d.Get(context.Background(), "/无关目录/某UP"); !errs.IsObjectNotFound(err) {
		t.Fatalf("foreign root should be ObjectNotFound: %v", err)
	}
	// 太深路径
	if _, err := d.Get(context.Background(), "/我的关注/测试UP/视频.mp4/多余"); !errs.IsObjectNotFound(err) {
		t.Fatalf("too deep should be ObjectNotFound: %v", err)
	}
}
