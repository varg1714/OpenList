package bilibili

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-resty/resty/v2"
	"golang.org/x/time/rate"
)

// roundTripFunc 是 fc2 同款 transport mock 缝
type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newMockServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(handler)
	t.Cleanup(s.Close)
	return s
}

func newTestDriver() *Bilibili {
	d := &Bilibili{}
	d.Addition.Cookie = "SESSDATA=abc;DedeUserID=1"
	d.cookieStr = d.Addition.Cookie
	d.limiter = rate.NewLimiter(rate.Inf, 0) // 测试不限速
	return d
}

// mockRoundTrip 把任意请求（含硬编码的 apiBase/passportBase URL）转发到 srv：
// httptest server 的 client transport 是标准 transport，会 dial req.URL.Host，
// 所以必须先改写 scheme+host
func mockRoundTrip(srv *httptest.Server) roundTripFunc {
	return roundTripFunc(func(req *http.Request) (*http.Response, error) {
		req2 := req.Clone(req.Context())
		req2.URL.Scheme = "http"
		req2.URL.Host = strings.TrimPrefix(srv.URL, "http://")
		return srv.Client().Transport.RoundTrip(req2)
	})
}

func TestDoGetCodeZeroReturnsData(t *testing.T) {
	srv := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Set-Cookie", "buvid3=xyz; path=/")
		w.Write([]byte(`{"code":0,"message":"0","data":{"mid":42}}`))
	})
	d := newTestDriver()
	d.client = resty.New().SetTransport(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		// cookie 手动头已带上种子
		if c := req.Header.Get("Cookie"); !strings.Contains(c, "SESSDATA=abc") {
			t.Errorf("cookie header = %q, want SESSDATA=abc", c)
		}
		return srv.Client().Transport.RoundTrip(req)
	}))
	data, err := d.doGet(context.Background(), srv.URL+"/x/web-interface/nav", nil, false)
	if err != nil {
		t.Fatalf("doGet: %v", err)
	}
	var mid struct {
		Mid int64 `json:"mid"`
	}
	if err := json.Unmarshal(data, &mid); err != nil || mid.Mid != 42 {
		t.Fatalf("data = %s, err = %v", data, err)
	}
	// Set-Cookie 被合并
	if !strings.Contains(d.cookieStr, "buvid3=xyz") {
		t.Fatalf("cookieStr not merged: %q", d.cookieStr)
	}
}

func TestFetchMixinKeyMalformedWbiImgErrors(t *testing.T) {
	// 空 wbi_img：path.Base("") 返回 "."，历史上会漏过 == "" 守卫并在
	// getMixinKey("..") 越界 panic——现在应返回错误而非 panic。
	srv := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":0,"data":{"wbi_img":{}}}`))
	})
	d := newTestDriver()
	d.client = resty.New().SetTransport(mockRoundTrip(srv))
	if _, err := d.fetchMixinKey(context.Background()); err == nil {
		t.Fatal("fetchMixinKey: want error for malformed wbi_img")
	}
}

func TestDoGetNonZeroCode(t *testing.T) {
	srv := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":-101,"message":"账号未登录"}`))
	})
	d := newTestDriver()
	d.client = resty.New().SetTransport(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return srv.Client().Transport.RoundTrip(req)
	}))
	_, err := d.doGet(context.Background(), srv.URL+"/x", nil, false)
	if err == nil || !strings.Contains(err.Error(), "未登录") {
		t.Fatalf("err = %v, want 未登录 hint", err)
	}
}

func TestDoGetSignedAddsWRid(t *testing.T) {
	var gotQuery string
	srv := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Write([]byte(`{"code":0,"data":{}}`))
	})
	d := newTestDriver()
	d.mixinKey = "ea1db124af3c7062474693fa704f4ff8" // 直接注入，跳过 nav
	d.client = resty.New().SetTransport(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return srv.Client().Transport.RoundTrip(req)
	}))
	if _, err := d.doGet(context.Background(), srv.URL+"/x/space/wbi/arc/search",
		map[string]string{"mid": "2", "pn": "1"}, true); err != nil {
		t.Fatalf("doGet: %v", err)
	}
	if !strings.Contains(gotQuery, "w_rid=") || !strings.Contains(gotQuery, "wts=") {
		t.Fatalf("signed query missing w_rid/wts: %q", gotQuery)
	}
}

func TestDoGetSignedFetchesMixinKeyFromNav(t *testing.T) {
	// signed=true 且 mixinKey 为空 → 先请求 /x/web-interface/nav 拿 wbi_img
	var navHits int
	srv := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/x/web-interface/nav":
			navHits++
			w.Write([]byte(`{"code":0,"data":{"wbi_img":{
				"img_url":"https://i0.hdslb.com/bfs/wbi/7cd084941338484aae1ad9425b84077c.png",
				"sub_url":"https://i0.hdslb.com/bfs/wbi/4932caff0ff746eab6f01bf08b70ac45.png"}}}`))
		default:
			w.Write([]byte(`{"code":0,"data":{}}`))
		}
	})
	d := newTestDriver()
	d.client = resty.New().SetTransport(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		// nav 抓取走包级 apiBase（真实 bilibili 主机）；测试里把外部主机请求重写到 mock
		if req.URL.Host != srv.Listener.Addr().String() {
			u := *req.URL
			u.Scheme = "http"
			u.Host = srv.Listener.Addr().String()
			req.URL = &u
		}
		return srv.Client().Transport.RoundTrip(req)
	}))
	if _, err := d.doGet(context.Background(), srv.URL+"/x/space/wbi/arc/search",
		map[string]string{"mid": "2"}, true); err != nil {
		t.Fatalf("doGet: %v", err)
	}
	if navHits != 1 {
		t.Fatalf("nav hits = %d, want 1", navHits)
	}
	if d.mixinKey != "ea1db124af3c7062474693fa704f4ff8" {
		t.Fatalf("mixinKey = %q", d.mixinKey)
	}
}

func TestDoGetHTMLRiskControlError(t *testing.T) {
	// bilibili 风控/验证页返回 HTML（真实事故：arc/search pn=2 返回 <html>）：
	// 必须报清晰错误（含状态码与响应前缀），而非含糊的 "bad json"
	srv := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html><body>访问过于频繁，请稍后重试</body></html>"))
	})
	d := newTestDriver()
	d.client = resty.New().SetTransport(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return srv.Client().Transport.RoundTrip(req)
	}))
	_, err := d.doGet(context.Background(), srv.URL+"/x/space/wbi/arc/search", nil, false)
	if err == nil {
		t.Fatal("want error for HTML response")
	}
	for _, want := range []string{"HTML", "risk-control", "<html>"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %q, want it to mention %q", err, want)
		}
	}
}

func TestDoGetNon200Status(t *testing.T) {
	// 412 = bilibili 风控状态码；必须带状态码报错
	srv := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPreconditionFailed)
		w.Write([]byte("rate limited"))
	})
	d := newTestDriver()
	d.client = resty.New().SetTransport(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return srv.Client().Transport.RoundTrip(req)
	}))
	_, err := d.doGet(context.Background(), srv.URL+"/x", nil, false)
	if err == nil || !strings.Contains(err.Error(), "412") {
		t.Fatalf("err = %v, want 412 status hint", err)
	}
}

func TestEffectiveRateFallback(t *testing.T) {
	cases := []struct {
		set  float64
		want float64
	}{
		{0, defaultRateLimit},  // 老存储未设置 → 回落默认
		{-1, defaultRateLimit}, // 非法负值 → 回落默认
		{2.5, 2.5},             // 显式配置生效
	}
	for _, c := range cases {
		d := newTestDriver()
		d.Addition.LimitRate = c.set
		if got := d.effectiveRate(); got != c.want {
			t.Errorf("effectiveRate(%v) = %v, want %v", c.set, got, c.want)
		}
	}
}

func TestInitClientLimiterFromAddition(t *testing.T) {
	// 限流器按 Addition.LimitRate 创建，burst=1（严格均匀间隔）
	d := newTestDriver()
	d.Addition.LimitRate = 1.5
	d.initClient()
	if d.client == nil {
		t.Fatal("initClient must create client")
	}
	if got := d.limiter.Limit(); got != 1.5 {
		t.Fatalf("limiter rate = %v, want 1.5", got)
	}
	if got := d.limiter.Burst(); got != 1 {
		t.Fatalf("limiter burst = %d, want 1 (uniform spacing)", got)
	}
	// 未设置（老存储）→ 回落默认 2
	d2 := newTestDriver()
	d2.initClient()
	if got := d2.limiter.Limit(); got != 2 {
		t.Fatalf("default limiter rate = %v, want 2", got)
	}
}
