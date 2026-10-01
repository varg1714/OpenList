package emby_wrapper

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestThumbContentDownloadAndCache：thumb 内容下载 + 按 URL 键控缓存（方案 B）。
func TestThumbContentDownloadAndCache(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write([]byte("thumb-bytes"))
	}))
	defer srv.Close()
	d := &EmbyWrapper{}
	got, err := d.thumbContent(srv.URL)
	if err != nil || string(got) != "thumb-bytes" {
		t.Fatalf("download thumb: %q %v", got, err)
	}
	got, err = d.thumbContent(srv.URL)
	if err != nil || string(got) != "thumb-bytes" {
		t.Fatalf("cached thumb: %q %v", got, err)
	}
	if hits != 1 {
		t.Errorf("thumb must be cached by URL, server hits = %d", hits)
	}
	// 下载失败返回错误（不缓存）
	fail := httptest.NewServer(http.NotFoundHandler())
	defer fail.Close()
	if _, err := d.thumbContent(fail.URL); err == nil {
		t.Error("failed download must return error")
	} else if !strings.Contains(err.Error(), "404") {
		t.Errorf("error must carry status, got %v", err)
	}
}

// TestShowImageNameIndex：剧根封面占位命名（pornhub fanart 同款：poster.jpg +
// fanart1..N.jpg）与 Get 反查的序号解析互为逆运算。
func TestShowImageNameIndex(t *testing.T) {
	cases := []struct {
		i    int
		name string
	}{
		{0, "poster.jpg"},
		{1, "fanart1.jpg"},
		{2, "fanart2.jpg"},
		{10, "fanart10.jpg"},
	}
	for _, c := range cases {
		if got := showImageName(c.i); got != c.name {
			t.Errorf("showImageName(%d) = %q, want %q", c.i, got, c.name)
		}
		if got, ok := showImageIndex(c.name); !ok || got != c.i {
			t.Errorf("showImageIndex(%q) = %d,%v want %d,true", c.name, got, ok, c.i)
		}
	}
	for _, bad := range []string{"poster.png", "fanart0.jpg", "fanart.jpg", "folder.jpg", "backdrop1.jpg", "thumb.jpg", "fanart1.png"} {
		if _, ok := showImageIndex(bad); ok {
			t.Errorf("showImageIndex(%q) must not match", bad)
		}
	}
}
