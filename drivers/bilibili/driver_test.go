package bilibili

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/go-resty/resty/v2"
)

func listDriver(t *testing.T, handler http.HandlerFunc) *Bilibili {
	t.Helper()
	d := newTestDriver()
	d.uid = 12345
	srv := newMockServer(t, handler)
	d.client = resty.New().SetTransport(mockRoundTrip(srv))
	return d
}

func dirObj(t *testing.T, path, id string) model.Obj {
	t.Helper()
	return &model.Object{ID: id, Name: pathName(path), Path: path, IsFolder: true}
}

func pathName(p string) string {
	if p == "/" {
		return "root"
	}
	parts := strings.Split(strings.Trim(p, "/"), "/")
	return parts[len(parts)-1]
}

func TestListRoot(t *testing.T) {
	d := listDriver(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("root list must not call api")
	})
	objs, err := d.List(context.Background(), dirObj(t, "/", ""), model.ListArgs{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(objs) != 2 || !objs[0].IsDir() || objs[0].GetName() != "我的关注" || objs[1].GetName() != "我的收藏" {
		t.Fatalf("root objs = %+v", objs)
	}
}

func TestListFollowings(t *testing.T) {
	d := listDriver(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/x/relation/followings" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		w.Write([]byte(`{"code":0,"data":{"list":[{"mid":42,"uname":"测试UP"},{"mid":7,"uname":"带_下划线"}],"total":2}}`))
	})
	objs, err := d.List(context.Background(), dirObj(t, "/我的关注", dirFollowID), model.ListArgs{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(objs) != 2 {
		t.Fatalf("objs = %d", len(objs))
	}
	f0 := objs[0]
	if !f0.IsDir() || f0.GetName() != "测试UP" {
		t.Fatalf("folder0 = %q dir=%v", f0.GetName(), f0.IsDir())
	}
	if f0.GetID() != "up_42" {
		t.Fatalf("folder0 id = %q, want up_42 (invisible, used for dispatch)", f0.GetID())
	}
	if objs[1].GetName() != "带_下划线" || objs[1].GetID() != "up_7" {
		t.Fatalf("folder1 = %q id=%q", objs[1].GetName(), objs[1].GetID())
	}
}

func TestListFollowingsDupNames(t *testing.T) {
	// 同名 UP：全部追加 _mid 消歧（不依赖顺序）；唯一名保持干净
	d := listDriver(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":0,"data":{"list":[
			{"mid":1,"uname":"同名UP"},{"mid":2,"uname":"同名UP"},{"mid":3,"uname":"唯一UP"}],"total":3}}`))
	})
	objs, err := d.List(context.Background(), dirObj(t, "/我的关注", dirFollowID), model.ListArgs{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(objs) != 3 {
		t.Fatalf("objs = %d", len(objs))
	}
	want := map[string]string{"同名UP_1": "up_1", "同名UP_2": "up_2", "唯一UP": "up_3"}
	got := map[string]string{}
	for _, o := range objs {
		got[o.GetName()] = o.GetID()
	}
	for name, id := range want {
		if got[name] != id {
			t.Fatalf("objs = %v, want %v", got, want)
		}
	}
}

func TestListUpperVideos(t *testing.T) {
	d := listDriver(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/x/space/wbi/arc/search" {
			w.Write([]byte(`{"code":0,"data":{"list":{"vlist":[
				{"bvid":"BV1a","title":"最新视频","pic":"http://i0.hdslb.com/p.jpg","created":1700000100}
			]},"page":{"count":1}}}`))
			return
		}
		t.Fatalf("unexpected path %s", r.URL.Path)
	})
	d.mixinKey = "ea1db124af3c7062474693fa704f4ff8" // arc/search 走 wbi 签名，注入避免 nav 前置请求
	objs, err := d.List(context.Background(), dirObj(t, "/我的关注/测试UP", upFolderPrefix+"42"), model.ListArgs{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("objs = %d", len(objs))
	}
	vo, ok := objs[0].(*videoObj)
	if !ok {
		t.Fatalf("obj type = %T, want *videoObj", objs[0])
	}
	if vo.GetName() != "最新视频.mp4" || vo.bvid != "BV1a" || vo.cid != 0 || vo.GetID() != "BV1a" {
		t.Fatalf("video = %q bvid=%s cid=%d id=%s", vo.GetName(), vo.bvid, vo.cid, vo.GetID())
	}
	if vo.Thumbnail.Thumbnail != "https://i0.hdslb.com/p.jpg" {
		t.Fatalf("thumb = %q", vo.Thumbnail.Thumbnail)
	}
	if !vo.ModTime().Equal(time.Unix(1700000100, 0)) {
		t.Fatalf("modtime = %s, want 1700000100 (vlist created)", vo.ModTime())
	}
}

func TestListFavFolders(t *testing.T) {
	d := listDriver(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":0,"data":{"list":[{"id":999,"title":"我的收藏夹"}]}}`))
	})
	objs, err := d.List(context.Background(), dirObj(t, "/我的收藏", dirFavID), model.ListArgs{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(objs) != 1 || objs[0].GetName() != "我的收藏夹" || objs[0].GetID() != "fav_999" {
		t.Fatalf("objs = %+v", objs)
	}
}

func TestListFavFoldersDupNames(t *testing.T) {
	// 同名收藏夹：全部追加 _id 消歧
	d := listDriver(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":0,"data":{"list":[{"id":1,"title":"追番"},{"id":2,"title":"追番"}]}}`))
	})
	objs, err := d.List(context.Background(), dirObj(t, "/我的收藏", dirFavID), model.ListArgs{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(objs) != 2 || objs[0].GetName() != "追番_1" || objs[1].GetName() != "追番_2" {
		t.Fatalf("objs = %+v", objs)
	}
}

func TestListFavVideos(t *testing.T) {
	d := listDriver(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":0,"data":{"info":{"media_count":1},"medias":[
			{"bvid":"BV1b","title":"收藏的教程","cover":"http://i0.hdslb.com/c.jpg","fav_time":1700000200,"ugc":{"first_cid":555}}]}}`))
	})
	objs, err := d.List(context.Background(), dirObj(t, "/我的收藏/我的收藏夹", favFolderPrefix+"999"), model.ListArgs{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	vo, ok := objs[0].(*videoObj)
	if !ok {
		t.Fatalf("obj type = %T", objs[0])
	}
	if vo.cid != 555 || vo.bvid != "BV1b" {
		t.Fatalf("video bvid=%s cid=%d", vo.bvid, vo.cid)
	}
}

func TestListVideosDupTitles(t *testing.T) {
	// 同 UP 下重复标题：追加 bvid 消歧
	d := listDriver(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":0,"data":{"list":{"vlist":[
			{"bvid":"BV1a","title":"第1集","created":1700000100},
			{"bvid":"BV1b","title":"第1集","created":1700000101},
			{"bvid":"BV1c","title":"第2集","created":1700000102}
		]},"page":{"count":3}}}`))
	})
	d.mixinKey = "ea1db124af3c7062474693fa704f4ff8"
	objs, err := d.List(context.Background(), dirObj(t, "/我的关注/某UP", upFolderPrefix+"42"), model.ListArgs{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(objs) != 3 {
		t.Fatalf("objs = %d", len(objs))
	}
	names := []string{objs[0].GetName(), objs[1].GetName(), objs[2].GetName()}
	want := []string{"第1集_BV1a.mp4", "第1集_BV1b.mp4", "第2集.mp4"}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("names = %v, want %v", names, want)
		}
	}
}

func TestSanitizeName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"正常标题", "正常标题"},
		{`a/b\c:d*e?f"g<h>i|j`, "a_b_c_d_e_f_g_h_i_j"},
		{"  前后空格  ", "前后空格"},
		// 150 rune 截断："很长标题" 4 runes + 146 x
		{"很长标题" + strings.Repeat("x", 200), "很长标题" + strings.Repeat("x", 146)},
	}
	for _, c := range cases {
		if got := sanitizeName(c.in, 150); got != c.want {
			t.Errorf("sanitizeName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := sanitizeName("///", 10); got == "" {
		t.Fatal("sanitize of all-illegal should not be empty")
	}
}

func TestGetShallowPaths(t *testing.T) {
	d := newTestDriver()
	obj, err := d.Get(context.Background(), "/我的关注")
	if err != nil || !obj.IsDir() || obj.GetID() != dirFollowID {
		t.Fatalf("Get /我的关注 = %v %v (id=%q)", obj, err, obj.GetID())
	}
	if _, err := d.Get(context.Background(), "/我的关注/某某_1/BV1xx.mp4"); !errs.IsNotSupportError(err) {
		t.Fatalf("deep Get err = %v, want NotSupport", err)
	}
}

func TestGetRootAndShallowDirs(t *testing.T) {
	d := newTestDriver()
	obj, err := d.Get(context.Background(), "/")
	if err != nil || !obj.IsDir() || obj.GetName() != "root" {
		t.Fatalf("Get / = %v %v", obj, err)
	}
	obj, err = d.Get(context.Background(), "/我的收藏")
	if err != nil || !obj.IsDir() || obj.GetName() != "我的收藏" || obj.GetID() != dirFavID {
		t.Fatalf("Get /我的收藏 = %v %v (id=%q)", obj, err, obj.GetID())
	}
	if _, err := d.Get(context.Background(), "/我的收藏/不存在_1/x.mp4"); !errs.IsNotSupportError(err) {
		t.Fatalf("deep Get err = %v, want NotSupport", err)
	}
}

func TestListUnknownPath(t *testing.T) {
	d := listDriver(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("unknown list path must not call api")
	})
	if _, err := d.List(context.Background(), dirObj(t, "/无关目录", ""), model.ListArgs{}); !errs.IsObjectNotFound(err) {
		t.Fatalf("List err = %v, want ObjectNotFound", err)
	}
}

func TestDropKeepsQRCodeState(t *testing.T) {
	d := newTestDriver()
	d.qrcodeKey = "k123"
	d.qrURL = "https://passport.bilibili.com/x/passport-login/web/qrcode/generate?qrcode_key=k123"
	d.mixinKey = "mk"
	d.mixinKeyDay = "20260903"
	d.cookieStr = "SESSDATA=x"
	d.uid = 42
	d.uname = "u"
	if err := d.Drop(context.Background()); err != nil {
		t.Fatalf("Drop: %v", err)
	}
	// qrcode 状态必须跨保存请求存活（UpdateStorage 先 Drop 再 Init，
	// 清了会导致每次保存重新生成二维码、用户扫的码永远失效）
	if d.qrcodeKey != "k123" || d.qrURL == "" {
		t.Fatal("Drop must preserve qrcodeKey/qrURL for the QR login flow")
	}
	if d.mixinKey != "" || d.mixinKeyDay != "" || d.cookieStr != "" || d.uid != 0 || d.uname != "" {
		t.Fatal("Drop must clear runtime state (mixinKey/cookieStr/uid/uname)")
	}
}
