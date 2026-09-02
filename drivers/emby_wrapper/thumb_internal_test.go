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
