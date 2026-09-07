package bilibili

import (
	"strconv"
	"testing"
	"time"

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
		{"up__2023", upFolderPrefix}, {"up_42_2023", favFolderPrefix},
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
