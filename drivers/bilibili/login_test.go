package bilibili

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/go-resty/resty/v2"
)

func TestQRLoginHTMLContainsImage(t *testing.T) {
	html, err := qrLoginHTML("等待扫码", "https://passport.bilibili.com/x/passport-login/web/qrcode/generate?foo=1")
	if err != nil {
		t.Fatalf("qrLoginHTML: %v", err)
	}
	if !strings.Contains(html, "data:image/png;base64,") {
		t.Fatal("html missing base64 png")
	}
	if !strings.Contains(html, "等待扫码") {
		t.Fatal("html missing state text")
	}
}

func TestLoginByQRCodeGenerateThenPollPending(t *testing.T) {
	// 第一次：无 key → generate；poll 返回 86101 → HTML 错误
	var hits int
	srv := newMockServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		switch r.URL.Path {
		case "/x/passport-login/web/qrcode/generate":
			w.Write([]byte(`{"code":0,"data":{"url":"https://passport.bilibili.com/x/passport-login/web/qrcode/generate?qrcode_key=k123","qrcode_key":"k123"}}`))
		case "/x/passport-login/web/qrcode/poll":
			w.Write([]byte(`{"code":0,"data":{"code":86101,"message":"未扫码"}}`))
		}
	}))
	d := newTestDriver()
	d.client = resty.New().SetTransport(mockRoundTrip(srv))
	err := d.loginByQRCode(context.Background())
	if err == nil {
		t.Fatal("want HTML error when not scanned")
	}
	if !strings.Contains(err.Error(), "need verify:") || !strings.Contains(err.Error(), "base64") {
		t.Fatalf("err not HTML qr: %v", err)
	}
	if d.qrcodeKey != "k123" {
		t.Fatalf("qrcodeKey = %q, want k123", d.qrcodeKey)
	}
}

func TestLoginByQRCodeSuccessFillsCookie(t *testing.T) {
	srv := newMockServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/x/passport-login/web/qrcode/generate":
			w.Write([]byte(`{"code":0,"data":{"url":"https://passport.bilibili.com/x/passport-login/web/qrcode/generate?qrcode_key=k1","qrcode_key":"k1"}}`))
		case "/x/passport-login/web/qrcode/poll":
			w.Write([]byte(`{"code":0,"data":{"code":0,"url":"https://passport.bilibili.com/login?SESSDATA=abc123&DedeUserID=42&bili_jct=csrf"}}`))
		}
	}))
	d := newTestDriver()
	d.client = resty.New().SetTransport(mockRoundTrip(srv))
	err := d.loginByQRCode(context.Background())
	if err != nil {
		t.Fatalf("loginByQRCode: %v", err)
	}
	if !strings.Contains(d.Addition.Cookie, "SESSDATA=abc123") {
		t.Fatalf("cookie not filled: %q", d.Addition.Cookie)
	}
	if d.qrcodeKey != "" {
		t.Fatalf("qrcodeKey should be cleared after success")
	}
}

func TestLoginByQRCodeExpired(t *testing.T) {
	srv := newMockServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/x/passport-login/web/qrcode/generate":
			w.Write([]byte(`{"code":0,"data":{"url":"https://passport.bilibili.com/x/passport-login/web/qrcode/generate?qrcode_key=k2","qrcode_key":"k2"}}`))
		case "/x/passport-login/web/qrcode/poll":
			w.Write([]byte(`{"code":0,"data":{"code":86038,"message":"二维码已失效"}}`))
		}
	}))
	d := newTestDriver()
	d.client = resty.New().SetTransport(mockRoundTrip(srv))
	err := d.loginByQRCode(context.Background())
	if err == nil || !strings.Contains(err.Error(), "过期") {
		t.Fatalf("err = %v, want expired hint", err)
	}
	if d.qrcodeKey != "" {
		t.Fatal("qrcodeKey should reset on expire")
	}
}

func TestQRLoginStateDistinct(t *testing.T) {
	// 86101 与 86090 文案不同（用于确认）
	srv := newMockServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/x/passport-login/web/qrcode/generate":
			w.Write([]byte(`{"code":0,"data":{"url":"https://p/x?qrcode_key=k","qrcode_key":"k"}}`))
		case "/x/passport-login/web/qrcode/poll":
			w.Write([]byte(`{"code":0,"data":{"code":86090}}`))
		}
	}))
	d := newTestDriver()
	d.client = resty.New().SetTransport(mockRoundTrip(srv))
	err := d.loginByQRCode(context.Background())
	if err == nil || !strings.Contains(err.Error(), "确认") {
		t.Fatalf("err = %v, want confirm hint for 86090", err)
	}
}

func TestCookiesFromLoginURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "full core cookies keep order",
			in:   "https://passport.bilibili.com/login?SESSDATA=abc&DedeUserID=42&DedeUserID__ckMd5=md5&bili_jct=csrf&other=ignored",
			want: "SESSDATA=abc; DedeUserID=42; DedeUserID__ckMd5=md5; bili_jct=csrf",
		},
		{
			name: "missing ckMd5 dropped",
			in:   "https://passport.bilibili.com/login?SESSDATA=abc&DedeUserID=42",
			want: "SESSDATA=abc; DedeUserID=42",
		},
		{
			name: "no SESSDATA yields empty",
			in:   "https://passport.bilibili.com/login?bili_jct=csrf",
			want: "",
		},
		{
			name: "unparseable URL yields empty",
			in:   "https://passport.bilibili.com/login?%zz",
			want: "",
		},
	}
	for _, c := range cases {
		if got := cookiesFromLoginURL(c.in); got != c.want {
			t.Errorf("%s: cookiesFromLoginURL(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

func TestLoginByQRCodeSuccessViaSetCookie(t *testing.T) {
	// poll 成功但回调 URL 无 SESSDATA（2026 实际行为：cookie 经响应头 Set-Cookie 下发）
	srv := newMockServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/x/passport-login/web/qrcode/generate":
			jsonResp(w, `{"code":0,"data":{"url":"https://passport.bilibili.com/x/passport-login/web/qrcode/generate?qrcode_key=k1","qrcode_key":"k1"}}`)
		case "/x/passport-login/web/qrcode/poll":
			w.Header().Set("Set-Cookie", "SESSDATA=fromheader%2Cvalue; Path=/; Domain=bilibili.com; HttpOnly")
			w.Header().Add("Set-Cookie", "DedeUserID=42; Path=/; Domain=bilibili.com")
			w.Header().Add("Set-Cookie", "bili_jct=csrf; Path=/; Domain=bilibili.com")
			jsonResp(w, `{"code":0,"data":{"code":0,"url":"","refresh_token":"rt","timestamp":1}}`)
		}
	}))
	d := newTestDriver()
	d.client = resty.New().SetTransport(mockRoundTrip(srv))
	err := d.loginByQRCode(context.Background())
	if err != nil {
		t.Fatalf("loginByQRCode: %v", err)
	}
	if !strings.Contains(d.Addition.Cookie, "SESSDATA=fromheader%2Cvalue") {
		t.Fatalf("cookie not filled from Set-Cookie header: %q", d.Addition.Cookie)
	}
	if !strings.Contains(d.Addition.Cookie, "DedeUserID=42") || !strings.Contains(d.Addition.Cookie, "bili_jct=csrf") {
		t.Fatalf("cookie missing fields: %q", d.Addition.Cookie)
	}
	if d.qrcodeKey != "" {
		t.Fatalf("qrcodeKey should be cleared after success")
	}
}

func TestLoginByQRCodeSuccessNoSESSDATAError(t *testing.T) {
	// 成功但两个通道都没有 SESSDATA → 明确报错而非静默保存垃圾 cookie
	srv := newMockServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/x/passport-login/web/qrcode/generate":
			jsonResp(w, `{"code":0,"data":{"url":"https://passport.bilibili.com/x/passport-login/web/qrcode/generate?qrcode_key=k1","qrcode_key":"k1"}}`)
		case "/x/passport-login/web/qrcode/poll":
			jsonResp(w, `{"code":0,"data":{"code":0,"url":"https://passport.bilibili.com/crossDomain?DedeUserID=42","refresh_token":"rt"}}`)
		}
	}))
	d := newTestDriver()
	d.Addition.Cookie = "" // newTestDriver 预置了 SESSDATA，本用例需空 cookie
	d.cookieStr = ""
	d.client = resty.New().SetTransport(mockRoundTrip(srv))
	err := d.loginByQRCode(context.Background())
	if err == nil || !strings.Contains(err.Error(), "SESSDATA") {
		t.Fatalf("err = %v, want SESSDATA-missing error", err)
	}
	if d.Addition.Cookie != "" {
		t.Fatalf("Addition.Cookie should stay empty on failure: %q", d.Addition.Cookie)
	}
}
