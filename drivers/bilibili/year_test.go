package bilibili

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

// 中旬时间戳：任何本地时区（UTC-12..+14）年份都不漂移
const (
	ts2022 = 1655251200 // 2022-06-15T00:00:00Z
	ts2023 = 1686787200 // 2023-06-15T00:00:00Z
	ts2024 = 1718323200 // 2024-06-15T00:00:00Z
)

// y 时间戳的本地年份（断言随环境时区自动正确）
func y(t *testing.T, ts int64) int {
	t.Helper()
	return time.Unix(ts, 0).Year()
}

func TestParseYearSeg(t *testing.T) {
	cases := []struct {
		seg  string
		want int
		ok   bool
	}{
		{"2023", 2023, true}, {"2099", 2099, true},
		{"1999", 0, false}, {"202", 0, false}, {"2023x", 0, false},
		{"abc", 0, false}, {"", 0, false},
	}
	for _, c := range cases {
		got, ok := parseYearSeg(c.seg)
		if got != c.want || ok != c.ok {
			t.Errorf("parseYearSeg(%q) = (%d,%v), want (%d,%v)", c.seg, got, ok, c.want, c.ok)
		}
	}
}

func TestParseYearDirID(t *testing.T) {
	if mid, year, ok := parseYearDirID("up_42_2023", upFolderPrefix); mid != 42 || year != 2023 || !ok {
		t.Fatalf("parseYearDirID up = (%d,%d,%v)", mid, year, ok)
	}
	if mid, year, ok := parseYearDirID("fav_999_2024", favFolderPrefix); mid != 999 || year != 2024 || !ok {
		t.Fatalf("parseYearDirID fav = (%d,%d,%v)", mid, year, ok)
	}
	for _, bad := range []struct{ id, prefix string }{
		{"up_42", upFolderPrefix}, {"up_42_", upFolderPrefix}, {"up_42_x", upFolderPrefix},
		{"up_42_202", upFolderPrefix}, {"up__2023", upFolderPrefix}, {"up_42_2023", favFolderPrefix},
	} {
		if _, _, ok := parseYearDirID(bad.id, bad.prefix); ok {
			t.Fatalf("parseYearDirID(%q,%q) should fail", bad.id, bad.prefix)
		}
	}
}

func TestGroupVideosByYear(t *testing.T) {
	items := []VideoItem{
		{Bvid: "BV1a", Pubdate: ts2023},
		{Bvid: "BV1b", Pubdate: ts2024},
		{Bvid: "BV1c", Pubdate: 0}, // 无年份：不进任何组（旧 3 段路径仍可达）
		{Bvid: "BV1d", Pubdate: ts2023},
		{Bvid: "BV1e", Pubdate: ts2022},
	}
	groups := groupVideosByYear(items)
	if len(groups) != 3 {
		t.Fatalf("groups = %d, want 3", len(groups))
	}
	wantYears := []int{y(t, ts2024), y(t, ts2023), y(t, ts2022)} // 年份降序
	for i, g := range groups {
		if g.Year != wantYears[i] {
			t.Fatalf("groups[%d].Year = %d, want %d", i, g.Year, wantYears[i])
		}
	}
	// 组内保持入序；BV1c 被排除
	if len(groups[1].Items) != 2 || groups[1].Items[0].Bvid != "BV1a" || groups[1].Items[1].Bvid != "BV1d" {
		t.Fatalf("2023 items = %+v", groups[1].Items)
	}
	if !hasVideoYear(items, y(t, ts2023)) || hasVideoYear(items, 2029) {
		t.Fatal("hasVideoYear wrong")
	}
}

func TestYearFolderObjDeterministic(t *testing.T) {
	parent := dirObj(t, "/我的关注/测试UP", upFolderPrefix+"42")
	o1 := yearFolderObj(parent, "2023", "up_42_2023", 2023)
	o2 := yearFolderObj(parent, "2023", "up_42_2023", 2023)
	if o1.GetName() != "2023" || o1.GetID() != "up_42_2023" || o1.GetPath() != "/我的关注/测试UP/2023" || !o1.IsDir() {
		t.Fatalf("year folder = %+v", o1)
	}
	if _, ok := o1.(*model.Object); !ok {
		t.Fatalf("year folder type = %T, want *model.Object (Ctime 可写)", o1)
	}
	want := time.Date(2023, 1, 1, 0, 0, 0, 0, time.Local)
	if !o1.CreateTime().Equal(want) || !o2.CreateTime().Equal(want) {
		t.Fatalf("Ctime = %v / %v, want %v (deterministic)", o1.CreateTime(), o2.CreateTime(), want)
	}
}

func TestBuildYearDirs(t *testing.T) {
	parent := dirObj(t, "/我的关注/测试UP", upFolderPrefix+"42")
	objs, err := buildYearDirs(parent, "up_42", []VideoItem{
		{Bvid: "BV1a", Pubdate: ts2023}, {Bvid: "BV1b", Pubdate: ts2024},
	})
	if err != nil {
		t.Fatalf("buildYearDirs: %v", err)
	}
	if len(objs) != 2 {
		t.Fatalf("objs = %d", len(objs))
	}
	wantYear := y(t, ts2024)
	if objs[0].GetName() != strconv.Itoa(wantYear) || objs[0].GetID() != "up_42_"+strconv.Itoa(wantYear) {
		t.Fatalf("objs[0] = %+v (newest year first)", objs[0])
	}
}

func TestBuildVideoObjsKeepsOrder(t *testing.T) {
	dir := dirObj(t, "/我的关注/测试UP/2023", "up_42_2023")
	objs, err := buildVideoObjs(dir, []VideoItem{
		{Bvid: "BV1b", Title: "第1集", Pubdate: ts2023},
		{Bvid: "BV1a", Title: "第1集", Pubdate: ts2023},
		{Bvid: "BV1c", Title: "唯一", Pubdate: ts2023},
	})
	if err != nil {
		t.Fatalf("buildVideoObjs: %v", err)
	}
	// 目录内（年内）重名消歧；顺序保持
	names := []string{objs[0].GetName(), objs[1].GetName(), objs[2].GetName()}
	want := []string{"第1集_BV1b.mp4", "第1集_BV1a.mp4", "唯一.mp4"}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("names = %v, want %v", names, want)
		}
	}
}

func TestListYearDirProjectionAndMiss(t *testing.T) {
	// 跨年同名：各年份目录内名字干净（不互消歧）
	d := listDriver(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/x/space/wbi/arc/search" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		w.Write([]byte(`{"code":0,"data":{"list":{"vlist":[
			{"bvid":"BV1a","title":"第1集","created":` + strconv.FormatInt(ts2023, 10) + `},
			{"bvid":"BV1b","title":"第1集","created":` + strconv.FormatInt(ts2024, 10) + `}
		]},"page":{"count":2}}}`))
	})
	d.mixinKey = "ea1db124af3c7062474693fa704f4ff8"
	if _, err := d.List(context.Background(), dirObj(t, "/我的关注/某UP", upFolderPrefix+"42"), model.ListArgs{}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	y23, y24 := y(t, ts2023), y(t, ts2024)
	for _, yy := range []int{y23, y24} {
		objs, err := d.List(context.Background(),
			dirObj(t, "/我的关注/某UP/"+strconv.Itoa(yy), "up_42_"+strconv.Itoa(yy)), model.ListArgs{})
		if err != nil {
			t.Fatalf("List %d: %v", yy, err)
		}
		if len(objs) != 1 || objs[0].GetName() != "第1集.mp4" {
			t.Fatalf("year %d objs = %+v", yy, objs)
		}
	}
	// 无该年份 → 空列表（父目录不会展示的目录；不报错）
	objs, err := d.List(context.Background(), dirObj(t, "/我的关注/某UP/2029", "up_42_2029"), model.ListArgs{})
	if err != nil || len(objs) != 0 {
		t.Fatalf("empty year List = %v %v", objs, err)
	}
}

func TestListYearDirNoParentSnapshot(t *testing.T) {
	// 父行缺失（未同步/被清）：ObjectNotFound，零 API
	d := listDriver(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("year-dir List must not call api")
	})
	if _, err := d.List(context.Background(), dirObj(t, "/我的关注/某UP/2023", "up_42_2023"), model.ListArgs{}); !errs.IsObjectNotFound(err) {
		t.Fatalf("err = %v, want ObjectNotFound", err)
	}
}

// seedUpWithYears：关注 1 个 UP、投稿跨两年含跨年同名；返回已落库 driver
func seedUpWithYears(t *testing.T, vlist string) *Bilibili {
	t.Helper()
	d := listDriver(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/x/relation/followings":
			w.Write([]byte(`{"code":0,"data":{"list":[{"mid":42,"uname":"测试UP"}],"total":1}}`))
		case "/x/space/wbi/arc/search":
			w.Write([]byte(vlist))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	})
	d.mixinKey = "ea1db124af3c7062474693fa704f4ff8"
	for _, dir := range []model.Obj{
		dirObj(t, "/我的关注", dirFollowID),
		dirObj(t, "/我的关注/测试UP", upFolderPrefix+"42"),
	} {
		if _, err := d.List(context.Background(), dir, model.ListArgs{}); err != nil {
			t.Fatalf("seed List %s: %v", dir.GetPath(), err)
		}
	}
	return d
}

func TestGetYearDirAndYearScopedVideo(t *testing.T) {
	d := seedUpWithYears(t, `{"code":0,"data":{"list":{"vlist":[
		{"bvid":"BV1c","title":"最新视频","created":`+strconv.FormatInt(ts2024, 10)+`},
		{"bvid":"BV1a","title":"第1集","created":`+strconv.FormatInt(ts2023, 10)+`},
		{"bvid":"BV1b","title":"第1集","created":`+strconv.FormatInt(ts2024, 10)+`}
	]},"page":{"count":3}}}`)
	y23, y24 := y(t, ts2023), y(t, ts2024)

	// 3 段年份目录（纯查库；obj 与 List 输出同构）
	obj, err := d.Get(context.Background(), "/我的关注/测试UP/"+strconv.Itoa(y23))
	if err != nil {
		t.Fatalf("Get year dir: %v", err)
	}
	if !obj.IsDir() || obj.GetID() != "up_42_"+strconv.Itoa(y23) || obj.GetName() != strconv.Itoa(y23) {
		t.Fatalf("year dir obj = %+v", obj)
	}
	if !obj.CreateTime().Equal(time.Date(y23, 1, 1, 0, 0, 0, 0, time.Local)) {
		t.Fatalf("year Ctime = %v", obj.CreateTime())
	}
	// 4 段：跨年同名各自命中（年份范围匹配）
	v, err := d.Get(context.Background(), "/我的关注/测试UP/"+strconv.Itoa(y23)+"/第1集.mp4")
	if err != nil {
		t.Fatalf("Get y23 video: %v", err)
	}
	if vo, ok := v.(*videoObj); !ok || vo.bvid != "BV1a" {
		t.Fatalf("y23 video = %T %+v", v, v)
	}
	v, err = d.Get(context.Background(), "/我的关注/测试UP/"+strconv.Itoa(y24)+"/第1集.mp4")
	if err != nil {
		t.Fatalf("Get y24 video: %v", err)
	}
	if vo, ok := v.(*videoObj); !ok || vo.bvid != "BV1b" {
		t.Fatalf("y24 video = %T %+v", v, v)
	}
	// 4 段 obj.Path 含年份段
	if !strings.HasSuffix(v.GetPath(), "/"+strconv.Itoa(y24)+"/第1集.mp4") {
		t.Fatalf("video path = %s", v.GetPath())
	}
	// 年份校验：最新视频(2024) 挂在 2023 下 → 404
	if _, err := d.Get(context.Background(), "/我的关注/测试UP/"+strconv.Itoa(y23)+"/最新视频.mp4"); !errs.IsObjectNotFound(err) {
		t.Fatalf("wrong-year Get should 404: %v", err)
	}
	// 不存在的年份 → 404
	if _, err := d.Get(context.Background(), "/我的关注/测试UP/2029/第1集.mp4"); !errs.IsObjectNotFound(err) {
		t.Fatalf("missing year Get should 404: %v", err)
	}
}

func TestGetLegacyThreeSegCompat(t *testing.T) {
	d := seedUpWithYears(t, `{"code":0,"data":{"list":{"vlist":[
		{"bvid":"BV1c","title":"最新视频","created":`+strconv.FormatInt(ts2024, 10)+`},
		{"bvid":"BV1a","title":"第1集","created":`+strconv.FormatInt(ts2023, 10)+`},
		{"bvid":"BV1b","title":"第1集","created":`+strconv.FormatInt(ts2024, 10)+`}
	]},"page":{"count":3}}}`)
	// 旧 3 段：消歧后缀名（跨年同名平铺时代必有后缀）→ 任意年份命中
	v, err := d.Get(context.Background(), "/我的关注/测试UP/第1集_BV1b.mp4")
	if err != nil {
		t.Fatalf("legacy suffixed Get: %v", err)
	}
	if vo, ok := v.(*videoObj); !ok || vo.bvid != "BV1b" {
		t.Fatalf("obj = %T %+v", v, v)
	}
	// 旧 3 段：跨年同名干净名 → 行级歧义 404（与旧平铺语义一致）
	if _, err := d.Get(context.Background(), "/我的关注/测试UP/第1集.mp4"); !errs.IsObjectNotFound(err) {
		t.Fatalf("row-ambiguous clean name should 404: %v", err)
	}
	// 旧 3 段：无扩展名回退（唯一名）
	v, err = d.Get(context.Background(), "/我的关注/测试UP/最新视频")
	if err != nil {
		t.Fatalf("extensionless legacy Get: %v", err)
	}
	if vo, ok := v.(*videoObj); !ok || vo.bvid != "BV1c" {
		t.Fatalf("obj = %T %+v", v, v)
	}
}

func TestGetYearVsVideoNameAmbiguity(t *testing.T) {
	// 视频标题恰好是 "2024"（2023 年发布）：Get(…/2024) → 年份目录（该年有内容）；
	// Get(…/2024.mp4) → 视频文件（.mp4 消歧规则，spec §2）
	d := seedUpWithYears(t, `{"code":0,"data":{"list":{"vlist":[
		{"bvid":"BV1a","title":"2024","created":`+strconv.FormatInt(ts2023, 10)+`},
		{"bvid":"BV1b","title":"别的视频","created":`+strconv.FormatInt(ts2024, 10)+`}
	]},"page":{"count":2}}}`)
	y24 := y(t, ts2024)
	obj, err := d.Get(context.Background(), "/我的关注/测试UP/"+strconv.Itoa(y24))
	if err != nil || !obj.IsDir() || obj.GetID() != "up_42_"+strconv.Itoa(y24) {
		t.Fatalf("year seg should win: %v %+v", err, obj)
	}
	v, err := d.Get(context.Background(), "/我的关注/测试UP/"+strconv.Itoa(y24)+".mp4")
	if err != nil {
		t.Fatalf("video name Get: %v", err)
	}
	if vo, ok := v.(*videoObj); !ok || vo.bvid != "BV1a" {
		t.Fatalf("obj = %T %+v", v, v)
	}
}

func TestGetYearShapedEmptySegFallsBackToVideo(t *testing.T) {
	// 纯 "20xx" 形状段但该年份无内容（视频标题恰为 "2012"、2023 年发布）：
	// 不返回年份目录，回退旧式无扩展名视频名匹配（spec §2 顺序：年份目录优先且该年有内容）
	d := seedUpWithYears(t, `{"code":0,"data":{"list":{"vlist":[
		{"bvid":"BV1a","title":"2012","created":`+strconv.FormatInt(ts2023, 10)+`},
		{"bvid":"BV1b","title":"别的视频","created":`+strconv.FormatInt(ts2024, 10)+`}
	]},"page":{"count":2}}}`)
	v, err := d.Get(context.Background(), "/我的关注/测试UP/2012")
	if err != nil {
		t.Fatalf("legacy extensionless Get: %v", err)
	}
	if vo, ok := v.(*videoObj); !ok || vo.bvid != "BV1a" {
		t.Fatalf("obj = %T %+v, want video BV1a", v, v)
	}
	// 4 段年份路径：2012 年无内容 → 404（非年份目录，年份下无视频）
	if _, err := d.Get(context.Background(), "/我的关注/测试UP/2012/2012.mp4"); !errs.IsObjectNotFound(err) {
		t.Fatalf("year-2012 4-seg Get should 404: %v", err)
	}
}

func TestGetFavYearDirAndMismatch(t *testing.T) {
	// 收藏夹年份 = FavTime
	d := listDriver(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/x/v3/fav/folder/created/list-all":
			w.Write([]byte(`{"code":0,"data":{"list":[{"id":999,"title":"我的收藏夹"}]}}`))
		case "/x/v3/fav/resource/list":
			w.Write([]byte(`{"code":0,"data":{"info":{"media_count":2},"medias":[
				{"bvid":"BV1a","title":"旧收藏","cover":"","fav_time":` + strconv.FormatInt(ts2023, 10) + `,"ugc":{"first_cid":1}},
				{"bvid":"BV1b","title":"新收藏","cover":"","fav_time":` + strconv.FormatInt(ts2024, 10) + `,"ugc":{"first_cid":2}}]}}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	})
	for _, dir := range []model.Obj{
		dirObj(t, "/我的收藏", dirFavID),
		dirObj(t, "/我的收藏/我的收藏夹", favFolderPrefix+"999"),
	} {
		if _, err := d.List(context.Background(), dir, model.ListArgs{}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	y23, y24 := y(t, ts2023), y(t, ts2024)
	obj, err := d.Get(context.Background(), "/我的收藏/我的收藏夹/"+strconv.Itoa(y23))
	if err != nil || !obj.IsDir() || obj.GetID() != "fav_999_"+strconv.Itoa(y23) {
		t.Fatalf("fav year dir = %v %+v", err, obj)
	}
	v, err := d.Get(context.Background(), "/我的收藏/我的收藏夹/"+strconv.Itoa(y23)+"/旧收藏.mp4")
	if err != nil {
		t.Fatalf("fav year video: %v", err)
	}
	if vo, ok := v.(*videoObj); !ok || vo.bvid != "BV1a" || vo.cid != 1 {
		t.Fatalf("obj = %T %+v", v, v)
	}
	// FavTime 年份校验不匹配 → 404
	if _, err := d.Get(context.Background(), "/我的收藏/我的收藏夹/"+strconv.Itoa(y23)+"/新收藏.mp4"); !errs.IsObjectNotFound(err) {
		t.Fatalf("wrong-year fav Get should 404: %v", err)
	}
	if _, err := d.Get(context.Background(), "/我的收藏/我的收藏夹/"+strconv.Itoa(y24)+"/旧收藏.mp4"); !errs.IsObjectNotFound(err) {
		t.Fatalf("wrong-year fav Get should 404: %v", err)
	}
}
