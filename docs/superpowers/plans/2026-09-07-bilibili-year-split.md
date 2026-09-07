# Bilibili 年份目录拆分 Implementation Plan（Part A：驱动 Go 改动）

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** bilibili 驱动目录结构按年份拆分（`UP/{年份}/视频`，收藏夹同构），快照层零改动、年份目录为纯内存投影（M1）。

**Architecture:** Go 侧新增 `drivers/bilibili/year.go` 纯函数层（年份分组/年份目录对象/叶段解析），driver.go 只改 List 分发与 Get 深路径接线；master 快照行（key=UP/收藏夹目录展示路径）不变，增量同步逻辑不动，旧 3 段路径 Get 纯查库兼容。

**Tech Stack:** Go 1.25.4（`/Library/Go/sdk/go1.25.4/bin/go`）。

**Spec:** `docs/superpowers/specs/2026-09-07-bilibili-year-split-design.md`（本计划从 spec 论证，执行者先读 spec 再读本计划）

**Part B（迁移脚本，不入 git）:** 按任务维度分段维护在 worktree 的 `bilibili-year-migration/PLAN.md`（untracked），执行到 Task 4 验收后再展开编写/执行（Task 5-8：扫描解析 → plan_show 映射 → 执行落盘 → 回归与交付说明）。

## Global Constraints

- Go 工具链一律 `/Library/Go/sdk/go1.25.4/bin/go` 与 `/Library/Go/sdk/go1.25.4/bin/gofmt`
- 验证命令：`/Library/Go/sdk/go1.25.4/bin/go test ./drivers/bilibili/`；回归 `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/emby_wrapper/`；`/Library/Go/sdk/go1.25.4/bin/go vet ./drivers/bilibili/`
- TDD：先失败测试再实现；Go 任务各自独立提交，提交信息 `feat(bilibili): ...` 风格
- 快照表/快照门面（internal/db、snapshot.go、listWithSnapshot）**零改动**；emby_wrapper/strm **零改动**
- 实现前先按 superpowers:using-git-worktrees 单开 worktree（用户指示），本计划在 worktree 中执行
- Part B（迁移脚本）约束：脚本+测试永不 git add / 永不提交（用户指示：只提交 openlist 项目相关内容）

---

### Task 1: year.go 纯函数层（分组/年份目录/解析）

**Files:**
- Create: `drivers/bilibili/year.go`
- Test: `drivers/bilibili/year_test.go`

**Interfaces:**
- Produces（后续 Task 2/3 依赖的签名）：
  - `videoYearOf(v VideoItem) int`（Pubdate<=0 → 0）
  - `parseYearSeg(seg string) (int, bool)`（"20xx"）
  - `parseYearDirID(id, prefix string) (int64, int, bool)`（`up_42_2023` → (42, 2023, true)）
  - `type videoYearGroup struct{ Year int; Items []VideoItem }`
  - `groupVideosByYear(items []VideoItem) []videoYearGroup`（年份降序，year<=0 排除，组内保持入序）
  - `hasVideoYear(items []VideoItem, year int) bool`
  - `yearFolderObj(parent model.Obj, name, id string, year int) model.Obj`（Ctime = `time.Date(year,1,1,0,0,0,0,time.Local)`）
  - `buildYearDirs(parent model.Obj, idPrefix string, items []VideoItem) ([]model.Obj, error)`
  - `buildVideoObjs(dir model.Obj, items []VideoItem) ([]model.Obj, error)`（= 现 buildUpVideos 函数体迁移）
  - `resolveVideoLeaf(dir model.Obj, idPrefix, seg string, items []VideoItem) (model.Obj, error)`
  - `resolveYearVideo(dir model.Obj, idPrefix, yearSeg, videoSeg string, items []VideoItem) (model.Obj, error)`

- [ ] **Step 1: 写失败测试 `drivers/bilibili/year_test.go`**

```go
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
```

- [ ] **Step 2: 跑测试确认失败（undefined）**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/bilibili/ -run 'TestParseYearSeg|TestParseYearDirID|TestGroupVideosByYear|TestYearFolderObjDeterministic|TestBuildYearDirs|TestBuildVideoObjsKeepsOrder'`
Expected: FAIL（undefined 符号）

- [ ] **Step 3: 写最小实现 `drivers/bilibili/year.go`**

```go
package bilibili

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

// ---- 年份拆分（spec 2026-09-07：M1 内存投影，快照层零改动）----

// videoYearOf 条目年份（关注 = 发布时间；收藏夹 = FavTime——Pubdate 字段即收藏时间）。
// Pubdate<=0（防御，真实数据不存在）→ 0（不进年份目录，旧 3 段路径仍可解析）。
func videoYearOf(v VideoItem) int {
	if v.Pubdate <= 0 {
		return 0
	}
	return time.Unix(v.Pubdate, 0).Year()
}

// yearSegRe 年份段："20xx" 四位
var yearSegRe = regexp.MustCompile(`^20\d\d$`)

// parseYearSeg 段解析为年份（段必须形如 "20xx"）
func parseYearSeg(seg string) (int, bool) {
	if !yearSegRe.MatchString(seg) {
		return 0, false
	}
	year, err := strconv.Atoi(seg)
	if err != nil {
		return 0, false
	}
	return year, true
}

// parseYearDirID 解析 "{prefix}{数字}_{年份}" 目录 ID（up_{mid}_{year} / fav_{id}_{year}）；
// 纯 "{prefix}{数字}"（父目录 ID）与无关前缀不匹配。
func parseYearDirID(id, prefix string) (int64, int, bool) {
	rest, ok := strings.CutPrefix(id, prefix)
	if !ok || rest == "" {
		return 0, 0, false
	}
	i := strings.LastIndex(rest, "_")
	if i <= 0 || i == len(rest)-1 {
		return 0, 0, false
	}
	base, err := strconv.ParseInt(rest[:i], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	year, err := strconv.Atoi(rest[i+1:])
	if err != nil {
		return 0, 0, false
	}
	return base, year, true
}

// videoYearGroup 单年份分组
type videoYearGroup struct {
	Year  int
	Items []VideoItem
}

// groupVideosByYear 按年份分组（年份降序输出；year<=0 排除；组内保持入序 = API pubdate 倒序）
func groupVideosByYear(items []VideoItem) []videoYearGroup {
	byYear := make(map[int][]VideoItem)
	for _, it := range items {
		if yy := videoYearOf(it); yy > 0 {
			byYear[yy] = append(byYear[yy], it)
		}
	}
	years := make([]int, 0, len(byYear))
	for yy := range byYear {
		years = append(years, yy)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(years)))
	groups := make([]videoYearGroup, 0, len(years))
	for _, yy := range years {
		groups = append(groups, videoYearGroup{Year: yy, Items: byYear[yy]})
	}
	return groups
}

// hasVideoYear 是否存在某年份的视频（年份目录存在性判定）
func hasVideoYear(items []VideoItem, year int) bool {
	for _, it := range items {
		if videoYearOf(it) == year {
			return true
		}
	}
	return false
}

// yearFolderObj 年份目录对象：Ctime = 当年 1 月 1 日（确定性——emby_wrapper 按 Ctime
// 升序分配季号，S01 = 最早年份；跨刷新稳定，迁移脚本可复算）
func yearFolderObj(parent model.Obj, name, id string, year int) model.Obj {
	o := folderObj(parent, name, id).(*model.Object)
	o.Ctime = time.Date(year, 1, 1, 0, 0, 0, 0, time.Local)
	return o
}

// buildYearDirs 父目录（UP/收藏夹）展示层：条目按年份分组 → 年份目录列表（年份降序）。
// idPrefix = "up_{mid}" / "fav_{media_id}"；年份目录 ID = idPrefix + "_{year}"
func buildYearDirs(parent model.Obj, idPrefix string, items []VideoItem) ([]model.Obj, error) {
	groups := groupVideosByYear(items)
	objs := make([]model.Obj, 0, len(groups))
	for _, g := range groups {
		ys := strconv.Itoa(g.Year)
		objs = append(objs, yearFolderObj(parent, ys, idPrefix+"_"+ys, g.Year))
	}
	return objs, nil
}

// buildVideoObjs 目录内视频展示层：sanitize + 目录内重名消歧 + videoObj，保持 items 顺序。
// 拆分后消歧作用域 = 年份目录内（跨年同名不再互消歧，spec 2026-09-07）。
func buildVideoObjs(dir model.Obj, items []VideoItem) ([]model.Obj, error) {
	displays := make([]string, len(items))
	suffixes := make([]string, len(items))
	for i, v := range items {
		displays[i] = sanitizeName(v.Title, 150)
		suffixes[i] = v.Bvid
	}
	names := disambiguate(displays, suffixes)
	objs := make([]model.Obj, 0, len(items))
	for i := range items {
		objs = append(objs, newVideoObj(dir, items[i], names[i]))
	}
	return objs, nil
}

// resolveVideoLeaf 深路径叶段（{UP}/{收藏夹} 之后仅 1 段）：年份目录（该年有内容）优先；
// 否则按视频名匹配（带或不带 .mp4，旧格式兼容）。dir = UP/收藏夹目录 obj。
func resolveVideoLeaf(dir model.Obj, idPrefix, seg string, items []VideoItem) (model.Obj, error) {
	if year, ok := parseYearSeg(seg); ok && hasVideoYear(items, year) {
		return yearFolderObj(dir, seg, idPrefix+"_"+seg, year), nil
	}
	item := matchVideoItem(items, seg)
	if item == nil {
		return nil, errs.ObjectNotFound
	}
	return newVideoObj(dir, *item, strings.TrimSuffix(seg, ".mp4")), nil
}

// resolveYearVideo 4 段路径 {年份}/{视频}：年份段必须存在且与条目 pubdate 年份一致；
// 视频名在该年份范围内匹配（跨年同名互不干扰，spec §2 年份目录语义）。
func resolveYearVideo(dir model.Obj, idPrefix, yearSeg, videoSeg string, items []VideoItem) (model.Obj, error) {
	year, ok := parseYearSeg(yearSeg)
	if !ok {
		return nil, errs.ObjectNotFound
	}
	scoped := make([]VideoItem, 0, len(items))
	for _, it := range items {
		if videoYearOf(it) == year {
			scoped = append(scoped, it)
		}
	}
	item := matchVideoItem(scoped, videoSeg)
	if item == nil {
		return nil, errs.ObjectNotFound
	}
	yearDir := yearFolderObj(dir, yearSeg, idPrefix+"_"+yearSeg, year)
	return newVideoObj(yearDir, *item, strings.TrimSuffix(videoSeg, ".mp4")), nil
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/bilibili/ -run 'TestParseYearSeg|TestParseYearDirID|TestGroupVideosByYear|TestYearFolderObjDeterministic|TestBuildYearDirs|TestBuildVideoObjsKeepsOrder'`
Expected: PASS

- [ ] **Step 5: 全包编译**

Run: `/Library/Go/sdk/go1.25.4/bin/go build ./drivers/bilibili/ && /Library/Go/sdk/go1.25.4/bin/gofmt -l drivers/bilibili/year.go drivers/bilibili/year_test.go`
Expected: 编译通过；gofmt 无输出

- [ ] **Step 6: Commit**

```bash
git add drivers/bilibili/year.go drivers/bilibili/year_test.go
git commit -m "feat(bilibili): year-split pure helpers (year grouping, deterministic year-folder Ctime, leaf/4-seg resolvers)"
```

---

### Task 2: List 接线 —— 父目录输出年份目录 + 年份目录投影 List

**Files:**
- Modify: `drivers/bilibili/driver.go`（List 分发、listUpVideos/listFavVideos、删除 buildUpVideos/buildFavVideos）
- Test: `drivers/bilibili/year_test.go`（追加）
- Modify: `drivers/bilibili/driver_test.go`（TestListUpperVideos / TestListFavVideos / TestListVideosDupTitles 改造，补 import `strconv`）
- Modify: `drivers/bilibili/snapshot_net_test.go`（TestListUpVideosIncrementalGrows / TestListUpVideosFailKeepsSnapshot 改造，补 import `strconv`）

**Interfaces:**
- Consumes: Task 1 的 `buildYearDirs` / `buildVideoObjs` / `parseYearDirID`；snapshot.go 现成 `loadSnapshot`
- Produces: `d.listUpVideos(ctx, dir, mid int64)`（签名加 mid）、`d.listFavVideos(ctx, dir, mediaID int64)`、`d.listYearVideos(ctx, dir, year int) ([]model.Obj, error)`（纯投影，无 API 无写库）

- [ ] **Step 1: 先改造既有测试（红）**

`driver_test.go` 中整体替换下面三个测试函数（年份断言用 `y(t, ts)` 保持时区无关；`1700000100` 与 `1700000200` 为 2023-11-14 时间戳）：

```go
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
	d.mixinKey = "ea1db124af3c7062474693fa704f4ff8"
	wantYear := y(t, 1700000100)
	objs, err := d.List(context.Background(), dirObj(t, "/我的关注/测试UP", upFolderPrefix+"42"), model.ListArgs{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// 父目录 List → 年份目录（不再是视频文件）
	if len(objs) != 1 {
		t.Fatalf("objs = %d, want 1 year folder", len(objs))
	}
	yf := objs[0]
	if !yf.IsDir() || yf.GetName() != strconv.Itoa(wantYear) || yf.GetID() != "up_42_"+strconv.Itoa(wantYear) {
		t.Fatalf("year folder = %q id=%q", yf.GetName(), yf.GetID())
	}
	if !yf.CreateTime().Equal(time.Date(wantYear, 1, 1, 0, 0, 0, 0, time.Local)) {
		t.Fatalf("year Ctime = %v", yf.CreateTime())
	}
	// 年份目录：纯投影 List（该 mock 对额外 API 请求会 t.Fatal → 零 API 由测试结构保证）
	objs, err = d.List(context.Background(),
		dirObj(t, "/我的关注/测试UP/"+strconv.Itoa(wantYear), "up_42_"+strconv.Itoa(wantYear)), model.ListArgs{})
	if err != nil {
		t.Fatalf("List year dir: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("year objs = %d", len(objs))
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

func TestListFavVideos(t *testing.T) {
	d := listDriver(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":0,"data":{"info":{"media_count":1},"medias":[
			{"bvid":"BV1b","title":"收藏的教程","cover":"http://i0.hdslb.com/c.jpg","fav_time":1700000200,"ugc":{"first_cid":555}}]}}`))
	})
	wantYear := y(t, 1700000200) // 收藏夹年份 = FavTime
	objs, err := d.List(context.Background(), dirObj(t, "/我的收藏/我的收藏夹", favFolderPrefix+"999"), model.ListArgs{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(objs) != 1 || objs[0].GetName() != strconv.Itoa(wantYear) || objs[0].GetID() != "fav_999_"+strconv.Itoa(wantYear) {
		t.Fatalf("year folder objs = %+v", objs)
	}
	objs, err = d.List(context.Background(),
		dirObj(t, "/我的收藏/我的收藏夹/"+strconv.Itoa(wantYear), "fav_999_"+strconv.Itoa(wantYear)), model.ListArgs{})
	if err != nil {
		t.Fatalf("List year dir: %v", err)
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
	// 同 UP 同年重复标题：年份目录内追加 bvid 消歧
	d := listDriver(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":0,"data":{"list":{"vlist":[
			{"bvid":"BV1a","title":"第1集","created":1700000100},
			{"bvid":"BV1b","title":"第1集","created":1700000101},
			{"bvid":"BV1c","title":"第2集","created":1700000102}
		]},"page":{"count":3}}}`))
	})
	d.mixinKey = "ea1db124af3c7062474693fa704f4ff8"
	wantYear := y(t, 1700000100)
	objs, err := d.List(context.Background(), dirObj(t, "/我的关注/某UP", upFolderPrefix+"42"), model.ListArgs{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(objs) != 1 || objs[0].GetID() != "up_42_"+strconv.Itoa(wantYear) {
		t.Fatalf("objs = %+v", objs)
	}
	objs, err = d.List(context.Background(), dirObj(t, "/我的关注/某UP/"+strconv.Itoa(wantYear), objs[0].GetID()), model.ListArgs{})
	if err != nil {
		t.Fatalf("List year dir: %v", err)
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
```

`snapshot_net_test.go` 中整体替换下面两个测试函数：

```go
// TestListUpVideosIncrementalGrows：状态机 mock——
// 第一次 List（全量）：第 1 页 {BV1a,BV1b}；之后（增量）：第 1 页 {BV1c,BV1a}（新 1 条 BV1c 插头）
// 断言：父目录两次 List 各 1 次 API（首次全量/增量接上各一页）；年份目录 List 0 API（纯投影）。
func TestListUpVideosIncrementalGrows(t *testing.T) {
	var fullCall atomic.Bool
	var calls int64
	d := listDriver(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/x/space/wbi/arc/search" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		atomic.AddInt64(&calls, 1)
		var body string
		if !fullCall.Load() && r.URL.Query().Get("pn") == "1" {
			fullCall.Store(true)
			body = `{"code":0,"data":{"list":{"vlist":[
				{"bvid":"BV1a","title":"旧1","created":1700000001},
				{"bvid":"BV1b","title":"旧2","created":1700000000}]},"page":{"count":2}}}`
		} else if fullCall.Load() && r.URL.Query().Get("pn") == "1" {
			body = `{"code":0,"data":{"list":{"vlist":[
				{"bvid":"BV1c","title":"新1","created":1700000002},
				{"bvid":"BV1a","title":"旧1","created":1700000001}]},"page":{"count":3}}}`
		} else {
			body = `{"code":0,"data":{"list":{"vlist":[]},"page":{"count":0}}}`
		}
		w.Write([]byte(body))
	})
	d.mixinKey = "ea1db124af3c7062474693fa704f4ff8"
	wantYear := time.Unix(1700000002, 0).Year()
	dir := dirObj(t, "/我的关注/某UP", upFolderPrefix+"42")

	objs, err := d.List(context.Background(), dir, model.ListArgs{})
	if err != nil {
		t.Fatalf("first List: %v", err)
	}
	if len(objs) != 1 || objs[0].GetName() != strconv.Itoa(wantYear) {
		t.Fatalf("first objs = %+v, want single %d year folder", objs, wantYear)
	}
	objs, err = d.List(context.Background(), dir, model.ListArgs{})
	if err != nil {
		t.Fatalf("second List: %v", err)
	}
	if len(objs) != 1 || objs[0].GetID() != "up_42_"+strconv.Itoa(wantYear) {
		t.Fatalf("second objs = %+v", objs)
	}
	if n := atomic.LoadInt64(&calls); n != 2 {
		t.Fatalf("api calls = %d, want 2 (first full + one incremental page)", n)
	}
	// 年份目录投影：3 条、新 1 在前；0 新增 API
	yobjs, err := d.List(context.Background(),
		dirObj(t, "/我的关注/某UP/"+strconv.Itoa(wantYear), objs[0].GetID()), model.ListArgs{})
	if err != nil {
		t.Fatalf("List year dir: %v", err)
	}
	if len(yobjs) != 3 || yobjs[0].GetName() != "新1.mp4" {
		t.Fatalf("year objs = %+v, want 3 with 新1 first", yobjs)
	}
	if n := atomic.LoadInt64(&calls); n != 2 {
		t.Fatalf("api calls = %d, want 2 (year-dir List must not fetch)", n)
	}
	snap, err := db.GetVirtualDirSnapshot(d.ID, dir.GetPath())
	if err != nil || snap == nil {
		t.Fatalf("snap: %+v %v", snap, err)
	}
	if !strings.Contains(snap.Data, "BV1c") {
		t.Fatalf("snapshot must contain BV1c: %s", snap.Data)
	}
}

// TestListUpVideosFailKeepsSnapshot：目录已有快照后 API 失败 → List 返回旧快照数据（不报错）；
// 年份目录投影同样回退旧快照且零 API。
func TestListUpVideosFailKeepsSnapshot(t *testing.T) {
	defer func(old []time.Duration) { pageRetryBackoff = old }(pageRetryBackoff)
	pageRetryBackoff = []time.Duration{0, 0}
	var calls int64
	d := listDriver(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/x/space/wbi/arc/search" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if atomic.AddInt64(&calls, 1) == 1 {
			w.Write([]byte(`{"code":0,"data":{"list":{"vlist":[
				{"bvid":"BV1a","title":"唯一视频","created":1700000000}]},"page":{"count":1}}}`))
			return
		}
		http.Error(w, "<html>verify</html>", http.StatusOK)
	})
	d.mixinKey = "ea1db124af3c7062474693fa704f4ff8"
	wantYear := time.Unix(1700000000, 0).Year()
	dir := dirObj(t, "/我的关注/某UP", upFolderPrefix+"42")
	if _, err := d.List(context.Background(), dir, model.ListArgs{}); err != nil {
		t.Fatalf("first List: %v", err)
	}
	objs, err := d.List(context.Background(), dir, model.ListArgs{})
	if err != nil {
		t.Fatalf("second List must fall back to snapshot, got err: %v", err)
	}
	if len(objs) != 1 || objs[0].GetName() != strconv.Itoa(wantYear) {
		t.Fatalf("objs = %+v, want snapshot-derived year folder", objs)
	}
	yobjs, err := d.List(context.Background(),
		dirObj(t, "/我的关注/某UP/"+strconv.Itoa(wantYear), objs[0].GetID()), model.ListArgs{})
	if err != nil {
		t.Fatalf("year dir List: %v", err)
	}
	if len(yobjs) != 1 || yobjs[0].GetName() != "唯一视频.mp4" {
		t.Fatalf("year objs = %+v", yobjs)
	}
	if n := atomic.LoadInt64(&calls); n != 2 {
		t.Fatalf("api calls = %d, want 2 (year-dir projection must not fetch)", n)
	}
}
```

- [ ] **Step 2: 跑测试确认红**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/bilibili/ -run 'TestListUpperVideos|TestListFavVideos|TestListVideosDupTitles|TestListUpVideosIncrementalGrows|TestListUpVideosFailKeepsSnapshot'`
Expected: FAIL（父目录 List 仍平铺返回视频文件，断言不匹配）

- [ ] **Step 3: 实现 —— driver.go 改动**

(a) `List` 的 default 分发替换为（mid/mediaID 预解析 + 年份目录分支）：

```go
		default:
			if mid, ok := parsePrefixedID(key, upFolderPrefix); ok {
				return d.listUpVideos(ctx, dir, mid)
			}
			if _, year, ok := parseYearDirID(key, upFolderPrefix); ok {
				return d.listYearVideos(ctx, dir, year)
			}
			if mediaID, ok := parsePrefixedID(key, favFolderPrefix); ok {
				return d.listFavVideos(ctx, dir, mediaID)
			}
			if _, year, ok := parseYearDirID(key, favFolderPrefix); ok {
				return d.listYearVideos(ctx, dir, year)
			}
```

(b) 替换 `listUpVideos`/`listFavVideos` 并新增 `listYearVideos`（同时删除原 `buildUpVideos`/`buildFavVideos`——其函数体已迁移为 year.go 的 `buildVideoObjs`）：

```go
func (d *Bilibili) listUpVideos(ctx context.Context, dir model.Obj, mid int64) ([]model.Obj, error) {
	// key 不变：UP 目录展示路径（master 行唯一事实源；年份目录为内存投影）
	return listWithSnapshot(d, ctx, dir, dir.GetPath(),
		fetchUpVideosPage(d, ctx, mid),
		func(v VideoItem) string { return v.Bvid },
		func(parent model.Obj, items []VideoItem) ([]model.Obj, error) {
			return buildYearDirs(parent, upFolderPrefix+strconv.FormatInt(mid, 10), items)
		})
}

func (d *Bilibili) listFavVideos(ctx context.Context, dir model.Obj, mediaID int64) ([]model.Obj, error) {
	return listWithSnapshot(d, ctx, dir, dir.GetPath(),
		fetchFavVideosPage(d, ctx, mediaID),
		func(v VideoItem) string { return v.Bvid },
		func(parent model.Obj, items []VideoItem) ([]model.Obj, error) {
			return buildYearDirs(parent, favFolderPrefix+strconv.FormatInt(mediaID, 10), items)
		})
}

// listYearVideos 年份目录 List：纯内存投影（M1，spec 2026-09-07）——
// 父 master 行 key = 本目录路径去末段；无 API、无写库；行缺失 → ObjectNotFound
// （数据同步只由父目录 List 负责，decision A）。
func (d *Bilibili) listYearVideos(ctx context.Context, dir model.Obj, year int) ([]model.Obj, error) {
	parentKey := filepath.Dir(dir.GetPath())
	env, ok, err := loadSnapshot[VideoItem](d, parentKey)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errs.ObjectNotFound
	}
	items := make([]VideoItem, 0, len(env.Items))
	for _, it := range env.Items {
		if videoYearOf(it) == year {
			items = append(items, it)
		}
	}
	return buildVideoObjs(dir, items)
}
```

- [ ] **Step 4: 跑测试确认绿**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/bilibili/ -run 'TestListUpperVideos|TestListFavVideos|TestListVideosDupTitles|TestListUpVideosIncrementalGrows|TestListUpVideosFailKeepsSnapshot'`
Expected: PASS

- [ ] **Step 5: 补投影边界测试（追加到 year_test.go）**

```go
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
```

（year_test.go 需补 import：`context`、`net/http`、`sync/atomic`、`errs`、`github.com/go-resty/resty/v2`——与 driver_test.go 同款工具函数 `listDriver`/`dirObj` 同包复用。）

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/bilibili/ -run 'TestListYearDirProjectionAndMiss|TestListYearDirNoParentSnapshot|TestListUpperVideos|TestListFavVideos|TestListVideosDupTitles|TestListUpVideosIncrementalGrows|TestListUpVideosFailKeepsSnapshot'`
Expected: PASS

- [ ] **Step 6: 全包测试 + fmt**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/bilibili/ && /Library/Go/sdk/go1.25.4/bin/gofmt -l drivers/bilibili/`
Expected: 全绿；gofmt 无输出

- [ ] **Step 7: Commit**

```bash
git add drivers/bilibili/driver.go drivers/bilibili/year_test.go drivers/bilibili/driver_test.go drivers/bilibili/snapshot_net_test.go
git commit -m "feat(bilibili): year-dir listing - parent dirs group into year folders, year dirs are pure DB projections (M1)"
```

---

### Task 3: Get 深路径 2~4 段矩阵（旧 3 段兼容 + 年份目录 + 年份校验）

**Files:**
- Modify: `drivers/bilibili/driver.go`（Get 段数上限、getFollowPath/getFavPath 重构）
- Test: `drivers/bilibili/year_test.go`（追加 Get 矩阵测试）

**Interfaces:**
- Consumes: Task 1 的 `resolveVideoLeaf` / `resolveYearVideo` / `yearFolderObj`；现有 `matchVideoItem`/`matchUname`/`matchFavTitle`/`splitFolderName`/`hasFollowByID`/`hasFavByID` 保留不动
- Produces: Get 行为矩阵（spec §2 表）——3 段旧视频路径任意年份解析、3 段年份目录（`.mp4` 后缀消歧）、4 段年份校验 404

- [ ] **Step 1: 写失败测试（追加到 year_test.go）**

```go
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
```

（year_test.go 需补 import：`strings`、`errs`。）

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/bilibili/ -run 'TestGetYearDirAndYearScopedVideo|TestGetLegacyThreeSegCompat|TestGetYearVsVideoNameAmbiguity|TestGetFavYearDirAndMismatch'`
Expected: FAIL（旧 Get 只认 2~3 段、无年份语义）

- [ ] **Step 2: 实现 —— driver.go Get 区重构**

(a) `Get` 的段数检查由 `len(segs) < 2 || len(segs) > 3` 改为 `len(segs) < 2 || len(segs) > 4`，注释同步为"结构固定 4 层以内，顶层只能是关注/收藏"。

(b) 整体替换 `getFollowPath` 与 `getFavPath` 的实现（原内联的视频解析/消歧匹配逻辑删除，改调 Task 1 的 `resolveVideoLeaf`/`resolveYearVideo`；UP/收藏夹名匹配段不变）：

```go
// getFollowPath 在关注子树内定位：{UP}（1 段）| {UP}/{年份|旧视频名}（2 段）|
// {UP}/{年份}/{视频}（3 段，年份与条目 pubdate 校验；spec 2026-09-07）
func (d *Bilibili) getFollowPath(ctx context.Context, parent model.Obj, segs []string) (model.Obj, error) {
	items, ok, err := loadSnapshot[FollowItem](d, parent.GetPath())
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errs.ObjectNotFound // 未同步：由 List（浏览/定时任务）负责
	}
	upName := segs[0]
	mid := matchUname(items.Items, upName)
	if mid == 0 {
		// 消歧名 "名_{mid}"：解出 id 并校验前缀（防伪造段误命中）
		if prefix, id, ok := splitFolderName(upName); ok && hasFollowByID(items.Items, id, prefix) {
			mid = id
		}
	}
	if mid == 0 {
		return nil, errs.ObjectNotFound
	}
	upDir := folderObj(parent, upName, upFolderPrefix+strconv.FormatInt(mid, 10))
	if len(segs) == 1 { // segs 已去掉顶层：仅 UP 目录段
		return upDir, nil
	}
	// 视频/年份层：读该 UP master 行（key = UP 展示路径；年份为内存投影）
	vitems, ok, err := loadSnapshot[VideoItem](d, upDir.GetPath())
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errs.ObjectNotFound
	}
	if len(segs) == 2 { // 叶段：年份目录或旧式视频路径
		return resolveVideoLeaf(upDir, upFolderPrefix+strconv.FormatInt(mid, 10), segs[1], vitems.Items)
	}
	return resolveYearVideo(upDir, upFolderPrefix+strconv.FormatInt(mid, 10), segs[1], segs[2], vitems.Items)
}

// getFavPath 在收藏子树内定位（与 getFollowPath 同构；收藏夹年份 = FavTime）
func (d *Bilibili) getFavPath(ctx context.Context, parent model.Obj, segs []string) (model.Obj, error) {
	items, ok, err := loadSnapshot[FavFolder](d, parent.GetPath())
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errs.ObjectNotFound
	}
	favName := segs[0]
	mediaID := matchFavTitle(items.Items, favName)
	if mediaID == 0 {
		if prefix, id, ok := splitFolderName(favName); ok && hasFavByID(items.Items, id, prefix) {
			mediaID = id
		}
	}
	if mediaID == 0 {
		return nil, errs.ObjectNotFound
	}
	favDir := folderObj(parent, favName, favFolderPrefix+strconv.FormatInt(mediaID, 10))
	if len(segs) == 1 { // segs 已去掉顶层：仅收藏夹目录段
		return favDir, nil
	}
	vitems, ok, err := loadSnapshot[VideoItem](d, favDir.GetPath())
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errs.ObjectNotFound
	}
	if len(segs) == 2 {
		return resolveVideoLeaf(favDir, favFolderPrefix+strconv.FormatInt(mediaID, 10), segs[1], vitems.Items)
	}
	return resolveYearVideo(favDir, favFolderPrefix+strconv.FormatInt(mediaID, 10), segs[1], segs[2], vitems.Items)
}
```

- [ ] **Step 3: 跑测试确认绿**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/bilibili/ -run 'TestGetYearDirAndYearScopedVideo|TestGetLegacyThreeSegCompat|TestGetYearVsVideoNameAmbiguity|TestGetFavYearDirAndMismatch|TestGetDeep'`
Expected: PASS

- [ ] **Step 4: 全包测试 + fmt**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/bilibili/ && /Library/Go/sdk/go1.25.4/bin/gofmt -l drivers/bilibili/`
Expected: 全绿；gofmt 无输出

- [ ] **Step 5: Commit**

```bash
git add drivers/bilibili/driver.go drivers/bilibili/year_test.go
git commit -m "feat(bilibili): Get 2-4 segment matrix - legacy no-year paths via pure DB, year dirs with pubdate verification"
```

---

### Task 4: 全量回归 + emby_wrapper 零改动确认

- [ ] **Step 1: 全量相关包测试**

Run:
```bash
/Library/Go/sdk/go1.25.4/bin/go test ./drivers/bilibili/ ./drivers/emby_wrapper/
/Library/Go/sdk/go1.25.4/bin/go vet ./drivers/bilibili/
```
Expected: 全绿（emby_wrapper 零改动——其季映射/编号测试基于 local 下游驱动，不受影响）

- [ ] **Step 2: diff 完整性检查**

Run: `git status --short && git log --oneline -4`
Expected: 工作区干净；日志 = Task 1-3 的 3 个 feat commit（+ 之前的 spec/plan docs commit）

---

