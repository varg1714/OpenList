package bilibili

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/go-resty/resty/v2"
)

func playDriver(t *testing.T, handler http.HandlerFunc) *Bilibili {
	t.Helper()
	d := newTestDriver()
	srv := newMockServer(t, handler)
	d.client = resty.New().SetTransport(mockRoundTrip(srv))
	return d
}

func TestLinkWithCid(t *testing.T) {
	d := playDriver(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/x/player/wbi/playurl" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		w.Write([]byte(`{"code":0,"data":{"durl":[{"url":"https://upos.example/v.mp4?sig=1","size":2048}]}}`))
	})
	d.mixinKey = "ea1db124af3c7062474693fa704f4ff8" // playurl 走 wbi 签名，注入避免 nav 前置请求
	file := &videoObj{}
	file.Name = "测试.mp4"
	file.bvid = "BV1xx"
	file.cid = 555
	link, err := d.Link(context.Background(), file, model.LinkArgs{})
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if link.URL != "https://upos.example/v.mp4?sig=1" {
		t.Fatalf("url = %q", link.URL)
	}
	if link.Header.Get("Referer") != "https://www.bilibili.com/" {
		t.Fatalf("referer = %q", link.Header.Get("Referer"))
	}
	if link.ContentLength != 2048 {
		t.Fatalf("content length = %d", link.ContentLength)
	}
	if link.Expiration == nil {
		t.Fatal("expiration should be set")
	}
}

func TestLinkFetchesCidViaView(t *testing.T) {
	d := playDriver(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/x/web-interface/view":
			w.Write([]byte(`{"code":0,"data":{"cid":555}}`))
		case "/x/player/wbi/playurl":
			w.Write([]byte(`{"code":0,"data":{"durl":[{"url":"https://upos.example/v.mp4"}]}}`))
		}
	})
	d.mixinKey = "ea1db124af3c7062474693fa704f4ff8" // playurl 走 wbi 签名，注入避免 nav 前置请求
	file := &videoObj{}
	file.Name = "测试.mp4"
	file.bvid = "BV1xx" // cid=0 → 触发 view
	link, err := d.Link(context.Background(), file, model.LinkArgs{})
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if !strings.Contains(link.URL, "upos.example") {
		t.Fatalf("url = %q", link.URL)
	}
}

func TestLinkWrongType(t *testing.T) {
	d := newTestDriver()
	_, err := d.Link(context.Background(), &model.Object{Name: "x"}, model.LinkArgs{})
	if !errs.IsNotSupportError(err) {
		t.Fatalf("err = %v, want NotSupport", err)
	}
}

func TestLinkDurlEmptyError(t *testing.T) {
	d := playDriver(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":0,"data":{"dash":{"video":[]}}}`))
	})
	d.mixinKey = "ea1db124af3c7062474693fa704f4ff8" // playurl 走 wbi 签名，注入避免 nav 前置请求
	file := &videoObj{}
	file.Name = "x.mp4"
	file.bvid = "BV1xx"
	file.cid = 1
	if _, err := d.Link(context.Background(), file, model.LinkArgs{}); err == nil {
		t.Fatal("want error when durl empty")
	} else if !strings.Contains(err.Error(), "durl") {
		t.Fatalf("err = %v, want message mentioning durl", err)
	}
}
