package bilibili

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-resty/resty/v2"
)

// apiDriver：单 handler mock（按 path/query 自行分发）
func apiDriver(t *testing.T, handler http.HandlerFunc) *Bilibili {
	t.Helper()
	d := newTestDriver()
	srv := newMockServer(t, handler)
	d.client = resty.New().SetTransport(mockRoundTrip(srv))
	return d
}

func jsonResp(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(body))
}

func TestNavInfo(t *testing.T) {
	d := apiDriver(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/x/web-interface/nav" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		jsonResp(w, `{"code":0,"data":{"isLogin":true,"mid":12345,"uname":"测试君"}}`)
	})
	uid, uname, err := d.navInfo(context.Background())
	if err != nil || uid != 12345 || uname != "测试君" {
		t.Fatalf("navInfo = %d %q %v", uid, uname, err)
	}
}

func TestFollowingsPagination(t *testing.T) {
	// 2 页 × 每页 2 条 = 4 人
	page1 := `{"code":0,"data":{"list":[{"mid":1,"uname":"A"},{"mid":2,"uname":"B"}],"total":4}}`
	page2 := `{"code":0,"data":{"list":[{"mid":3,"uname":"C"},{"mid":4,"uname":"D"}],"total":4}}`
	d := apiDriver(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/x/relation/followings" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if r.URL.Query().Get("pn") == "1" {
			jsonResp(w, page1)
		} else {
			jsonResp(w, page2)
		}
	})
	items, err := d.followings(context.Background())
	if err != nil {
		t.Fatalf("followings: %v", err)
	}
	if len(items) != 4 || items[0].Uname != "A" || items[3].Mid != 4 {
		t.Fatalf("followings = %+v", items)
	}
}

func TestUpVideosSignedAndParsed(t *testing.T) {
	var query string
	d := apiDriver(t, func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		// vlist 字段与真实接口一致：无 cid
		jsonResp(w, `{"code":0,"data":{"list":{"vlist":[
			{"bvid":"BV1xx","title":"测试视频","pic":"http://i0.hdslb.com/1.jpg","created":1700000000,"length":"10:00"}
		]},"page":{"count":1}}}`)
	})
	d.mixinKey = "ea1db124af3c7062474693fa704f4ff8" // 注入，避免走 nav
	items, err := d.upVideos(context.Background(), 2)
	if err != nil {
		t.Fatalf("upVideos: %v", err)
	}
	if !strings.Contains(query, "w_rid=") {
		t.Fatalf("arc/search not signed: %q", query)
	}
	if len(items) != 1 || items[0].Bvid != "BV1xx" || items[0].Cid != 0 {
		t.Fatalf("upVideos = %+v", items)
	}
	if items[0].Pubdate != 1700000000 {
		t.Fatalf("pubdate = %d, want 1700000000 (vlist created)", items[0].Pubdate)
	}
}

func TestFavVideosFirstCid(t *testing.T) {
	d := apiDriver(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/x/v3/fav/resource/list" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		jsonResp(w, `{"code":0,"data":{"info":{"media_count":1},"medias":[
			{"id":100,"bvid":"BV1yy","title":"收藏视频","cover":"http://i0.hdslb.com/2.jpg",
			 "fav_time":1700000001,"ugc":{"first_cid":888}}]}}`)
	})
	items, err := d.favVideos(context.Background(), 777)
	if err != nil {
		t.Fatalf("favVideos: %v", err)
	}
	if len(items) != 1 || items[0].Bvid != "BV1yy" || items[0].Cid != 888 {
		t.Fatalf("favVideos = %+v", items)
	}
	if items[0].Pubdate != 1700000001 {
		t.Fatalf("pubdate = %d, want 1700000001 (fav_time)", items[0].Pubdate)
	}
	if items[0].Pic != "http://i0.hdslb.com/2.jpg" {
		t.Fatalf("pic = %q, want http://i0.hdslb.com/2.jpg (cover)", items[0].Pic)
	}
}

func TestVideoCidViaView(t *testing.T) {
	d := apiDriver(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/x/web-interface/view" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		jsonResp(w, `{"code":0,"data":{"bvid":"BV1xx","cid":555,"title":"测试"}}`)
	})
	cid, err := d.videoCid(context.Background(), "BV1xx")
	if err != nil || cid != 555 {
		t.Fatalf("videoCid = %d, %v", cid, err)
	}
}

func TestPlayURLDurl(t *testing.T) {
	d := apiDriver(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/x/player/wbi/playurl" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		jsonResp(w, `{"code":0,"data":{"durl":[{"url":"https://upos.example/video.mp4?t=1","size":1024000}]}}`)
	})
	d.mixinKey = "ea1db124af3c7062474693fa704f4ff8" // 注入，避免走 nav
	u, size, err := d.playURLDurl(context.Background(), "BV1xx", 555)
	if err != nil || u == "" || size != 1024000 {
		t.Fatalf("playURLDurl = %q %d %v", u, size, err)
	}
}

func TestPlayURLDurlEmpty(t *testing.T) {
	d := apiDriver(t, func(w http.ResponseWriter, r *http.Request) {
		jsonResp(w, `{"code":0,"data":{"dash":{"video":[]},"durl":null}}`)
	})
	d.mixinKey = "ea1db124af3c7062474693fa704f4ff8" // 注入，避免走 nav
	_, _, err := d.playURLDurl(context.Background(), "BV1xx", 555)
	if err == nil {
		t.Fatal("want error when durl empty (dash-only video)")
	}
}

func TestCollectPagesMaxLimit(t *testing.T) {
	d := newTestDriver()
	d.Addition.MaxListItems = 3
	var got []int
	err := collectPages(d, context.Background(), 50, func(pn int) ([]int, int, error) {
		start := (pn - 1) * 2
		return []int{start + 1, start + 2}, 100, nil
	}, &got)
	if err != nil {
		t.Fatalf("collectPages: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d items, want 3 (MaxListItems cut)", len(got))
	}
}

func TestCollectPagesRetryThenSucceed(t *testing.T) {
	// 页失败 → 按退避重试 → 成功
	defer func(old []time.Duration) { pageRetryBackoff = old }(pageRetryBackoff)
	pageRetryBackoff = []time.Duration{0, 0}
	d := newTestDriver()
	var calls int
	var got []int
	err := collectPages(d, context.Background(), 50, func(pn int) ([]int, int, error) {
		calls++
		if calls == 1 {
			return nil, 0, errors.New("risk-control html")
		}
		return []int{1, 2}, 2, nil
	}, &got)
	if err != nil {
		t.Fatalf("collectPages: %v", err)
	}
	if calls != 2 || len(got) != 2 {
		t.Fatalf("calls = %d, got = %d items, want 2 calls / 2 items", calls, len(got))
	}
}

func TestCollectPagesPartialOnPersistentError(t *testing.T) {
	// 首页成功、后续页重试耗尽 → 保留部分结果返回 nil（部分可见 > 全不可见）
	defer func(old []time.Duration) { pageRetryBackoff = old }(pageRetryBackoff)
	pageRetryBackoff = []time.Duration{0, 0}
	d := newTestDriver()
	var got []int
	err := collectPages(d, context.Background(), 50, func(pn int) ([]int, int, error) {
		if pn == 1 {
			return []int{1, 2}, 100, nil
		}
		return nil, 0, errors.New("risk-control html")
	}, &got)
	if err != nil {
		t.Fatalf("collectPages: want nil err (partial result), got %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d items, want 2 (partial page 1)", len(got))
	}
}

func TestCollectPagesFirstPageError(t *testing.T) {
	// 首页即失败（重试耗尽，无任何收集）→ 返回错误，调用方明确失败
	defer func(old []time.Duration) { pageRetryBackoff = old }(pageRetryBackoff)
	pageRetryBackoff = []time.Duration{0, 0}
	d := newTestDriver()
	var got []int
	err := collectPages(d, context.Background(), 50, func(pn int) ([]int, int, error) {
		return nil, 0, errors.New("boom")
	}, &got)
	if err == nil {
		t.Fatal("want error when first page fails")
	}
	if len(got) != 0 {
		t.Fatalf("got %d items, want 0", len(got))
	}
}
