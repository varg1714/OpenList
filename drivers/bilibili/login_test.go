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
