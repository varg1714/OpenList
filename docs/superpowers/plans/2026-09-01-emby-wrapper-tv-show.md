# EmbyWrapper 电视剧模式（TV Show）实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 文件夹可标记为电视剧（`tv_show` + 自定义剧名 `tv_show_name`）：根目录直接文件 = 第 1 季，直接子文件夹按"创建时间+名称"排序分配季号并**虚拟映射为 `S{季}` 目录**（原文件夹名不再展示），季内文件**递归收集并扁平化**到季目录，视频按"创建时间+名称"编号为 `S{NN}E{MM}.mp4`（纯编号，原文件名经剧集 nfo title 保留）、非视频保留原名；生成剧集 nfo（`<episodedetails>`：title=原名、actors、无 plot）、`tvshow.nfo`（`<tvshow>`：剧名+简介+actors）与 `season.nfo`（`<season>`：seasonnumber，双保险）；文件不物理改名，下游 Get/Link 还原真实路径。

**Architecture:** 沿用现有虚拟 nfo 模式：`EmbyDirSetting` 新增 `TvShow bool` 与 `TvShowName string`（本地生效不继承）；新增 `episode.go` 构建整棵剧集树索引（`tvIndex`：根文件=季 1 + 直接子文件夹按创建时间+名称分配连续季号 + 季内递归收集按时间编号），一次构建供 List 展示与 Get 反查共用；`virtual_file.RenderNFO` 根元素参数化支持 `tvshow`/`episodedetails`（`season.nfo` 结构简单，直接手写 XML）；`Get` 是虚拟→真实路径的唯一映射点（`virtualEpisode.GetPath()` 返回真实路径，Link 现有代码零改动）；任一目录的 List/Get 先查"最近的电视剧祖先"再决定走 TV 分支还是影片分支。

**Tech Stack:** Go 1.25.4（`/Library/Go/sdk/go1.25.4/bin/go`）、gorm、复用 `virtual_file.RenderNFO`（CDATA）与既有测试设施（`setup(t)`/`writeDownstreamFile`/`readNFOLink`/`names`）。

**Spec:** 用户需求（2026-09-01 逐项确认）：
1. 文件夹新增设置 `tv_show`（bool，标记为电视剧）与 `tv_show_name`（string，自定义剧名，为空回退文件夹名）；**本地生效，不继承**（子文件夹是否为电视剧由自身标记决定，嵌套电视剧独立成剧、被父剧索引跳过）
2. **排序**：文件与文件夹一律按 **`CreateTime()`（创建时间，驱动未提供时自动回退 `ModTime()`）+ 名称升序** 联合排序（用户确认：同一时间多个文件不能单靠时间，联合排序保证确定性、不随整理动作漂移）
3. **结构**：根目录直接文件 = 第 1 季（`S01E<季内时间序>`）；**直接子文件夹 = 季**——不解析文件夹名，按创建时间+名称排序分配**连续季号**（旧的季号更小；根目录存在直接视频时子文件夹从第 2 季起，否则从第 1 季起），**2026-09-02 修订：虚拟映射为 `S{季号}` 别名目录**（原文件夹名不再展示，Emby 原生识别 S{NN} 目录为季；展示层/Get 路径统一走别名，映射到真实文件夹）；季内文件**递归收集**（嵌套子文件夹如 `季/专题/` 中的文件**扁平化提取**到季目录展示；递归时跳过自身标记为 TV 的子文件夹），按创建时间+名称编号 `S{NN}E{MM}.mp4`（纯编号），非视频文件保留原名
4. **已含 `SxxExx`/`NxNN` 编号的文件跳过**、保持原名（含其同名 nfo 生成），不消耗序号
5. **剧集 nfo**：每个剧集文件生成虚拟 nfo（`S{NN}E{MM}.nfo`）：根元素 `<episodedetails>`，title=原文件名去扩展名，actors=现有继承解析结果（`resolveSetting`），**无 plot**；`-cd` 多段不合并（一文件一集）
6. **tvshow.nfo**（剧集根目录）：根元素 `<tvshow>`，title=自定义剧名（空→文件夹名），plot=该目录解析出的 plot（剧集介绍），actors 同
7. **season.nfo**（每个季，虚拟）：根元素 `<season>`，含 `<seasonnumber>NN</seasonnumber>`；**2026-09-02 修订：省略 `<seasonname>`**（季目录已虚拟映射为 `S{NN}`，Emby 原生识别；season.nfo 仅作双保险，真实目录下存在同名真实 season.nfo 时优先转发下游）
8. **不物理改名**：虚拟名只存在于展示层（List/Get 返回的对象名）；`virtualEpisode.GetPath()` 返回真实路径，Link 直接转发真实文件（`op.Link` 前必经 Get，Get 是唯一映射点）；真实同名文件/nfo 优先（同目录存在同名真实文件或 nfo 时跳过虚拟生成）
9. 剧集命名格式用 `S{NN}E{MM}` 纯编号（Emby 官方文档+解析器确认 `-E01` 不被识别；`anything_s01e02` 是受支持模式）。**2026-09-02 修订**：早期版本为 `原基础名-S{NN}E{MM}`，实测 Emby 的 EpisodePathParser 会把 `SxxExx` 前的任意前缀捕获为 seriesname（剧集名），前缀与剧名不符时剧集归属错乱（`xx1-S01E01.mp4` 被识别为名为 xx1 的剧）；改为纯编号后文件名不含可解析的剧名，归属完全来自文件夹层级，原文件名经剧集 nfo 的 `<title>`（=原文件名去扩展名）保留
10. **不需要** TV 边界阻断 plot/append 继承（早期设计的遗留需求，方案 B 下 TV 树内任何目录都不再走影片模式 nfo 生成，无泄漏场景）

## Global Constraints

- Go 工具链一律用 `/Library/Go/sdk/go1.25.4/bin/go`
- TDD：先失败测试再实现；每个任务独立提交
- 提交信息沿用仓库风格：`feat(emby_wrapper): ...`
- `UpsertEmbyDirSetting` 签名变化影响全部既有调用点（db_test.go / setting_test.go 共约 40 处，Task 1 用 sed 批量更新并编译验证）
- 全量 `go build ./...` 会因环境缺失 fuse.h 失败（与本改动无关）；验证使用受控包集：`drivers/emby_wrapper`、`drivers/virtual_file`、`internal/op`、`internal/fs`、`internal/db`、`internal/model`
- 不修改 `text_types` 默认值
- 既有测试语义不回归：`buildNFOContent` 重构为内部调用 `buildNFOWithRoot("movie", ...)`，行为保持不变
- **测试时间控制**：local 驱动用文件系统 birth time 填充 Ctime（`times.Stat`），`os.Chtimes` 无法控制；集成测试用 `writeEpisodeFile`/`writeDirOrdered` 辅助函数（写后 sleep 15ms）保证创建时间递增

---

### Task 1: 模型字段 + Upsert 扩展 + FolderAddition + Rename 接线

**Files:**
- Modify: `internal/model/emby.go`
- Modify: `drivers/emby_wrapper/db.go`
- Modify: `drivers/emby_wrapper/folder.go`
- Modify: `drivers/emby_wrapper/driver.go`（Rename 调用）
- Modify: `drivers/emby_wrapper/db_test.go`、`drivers/emby_wrapper/setting_test.go`（既有调用点签名同步）
- Test: `drivers/emby_wrapper/db_test.go`（追加合并语义测试）

**Interfaces:**
- Consumes: 现有 EmbyDirSetting / GetEmbyDirSetting
- Produces: `EmbyDirSetting{..., TvShow bool, TvShowName string}`、`UpsertEmbyDirSetting(storageID uint, dirPath, actors, plot, tvShowName string, useNameAsActor, appendFileNameToPlot *bool, tvShow *bool) error`（tvShowName 空串清除；tvShow nil 保持原值）、`FolderAddition{..., TvShow *bool, TvShowName string}` —— 后续任务依赖

- [ ] **Step 1: 模型加字段** `internal/model/emby.go`

```go
// EmbyDirSetting 某个目录的 Emby 元数据设置。各字段独立生效（分维度继承）：
// Actors 为空且 UseNameAsActor 为 false 表示未配置演员；Plot 为空表示未配置简介；
// AppendFileNameToPlot 为 nil 表示未配置（false 为显式关闭，阻断上层继承）。
// TvShow 标记该目录为电视剧（本地生效，不继承）；TvShowName 为自定义剧名（空回退文件夹名）。
// 全部字段未配置（append 为 nil 或 false）时该行会被删除，删除后恢复上层继承。
type EmbyDirSetting struct {
	ID                   uint   `gorm:"primaryKey"`
	StorageID            uint   `gorm:"uniqueIndex:idx_emby_dir_setting"`
	DirPath              string `gorm:"uniqueIndex:idx_emby_dir_setting"`
	Actors               string
	UseNameAsActor       bool
	Plot                 string
	AppendFileNameToPlot *bool
	TvShow               bool
	TvShowName           string
}
```

- [ ] **Step 2: 更新 CRUD** `drivers/emby_wrapper/db.go` —— `UpsertEmbyDirSetting` 整体替换：

```go
// UpsertEmbyDirSetting 保存目录设置。各字段独立合并：
// actors/plot/tvShowName 去空格后为空表示清除对应字段；useNameAsActor/appendFileNameToPlot/tvShow 为 nil 表示未提供（保持原值）。
// 所有字段均未配置时删除该目录的设置行。
func UpsertEmbyDirSetting(storageID uint, dirPath, actors, plot, tvShowName string, useNameAsActor, appendFileNameToPlot *bool, tvShow *bool) error {
	actors = strings.TrimSpace(actors)
	plot = strings.TrimSpace(plot)
	tvShowName = strings.TrimSpace(tvShowName)
	item, err := GetEmbyDirSetting(storageID, dirPath)
	if err != nil {
		return err
	}
	use := false
	var appendFlag *bool
	tv := false
	if item != nil {
		use = item.UseNameAsActor
		appendFlag = item.AppendFileNameToPlot
		tv = item.TvShow
	}
	if useNameAsActor != nil {
		use = *useNameAsActor
	}
	if appendFileNameToPlot != nil {
		appendFlag = appendFileNameToPlot
	}
	if tvShow != nil {
		tv = *tvShow
	}
	if actors == "" && plot == "" && tvShowName == "" && !use && !tv && (appendFlag == nil || !*appendFlag) {
		return db.GetDb().Where("storage_id = ? AND dir_path = ?", storageID, dirPath).Delete(&model.EmbyDirSetting{}).Error
	}
	if item != nil {
		item.Actors = actors
		item.Plot = plot
		item.TvShowName = tvShowName
		item.UseNameAsActor = use
		item.AppendFileNameToPlot = appendFlag
		item.TvShow = tv
		return db.GetDb().Save(item).Error
	}
	return db.GetDb().Create(&model.EmbyDirSetting{
		StorageID:            storageID,
		DirPath:              dirPath,
		Actors:               actors,
		Plot:                 plot,
		TvShowName:           tvShowName,
		UseNameAsActor:       use,
		AppendFileNameToPlot: appendFlag,
		TvShow:               tv,
	}).Error
}
```

- [ ] **Step 3: FolderAddition 加字段** `drivers/emby_wrapper/folder.go`

```go
type FolderAddition struct {
	Actors               string `json:"actors"`
	UseNameAsActor       *bool  `json:"use_name_as_actor"`
	Plot                 string `json:"plot"`
	AppendFileNameToPlot *bool  `json:"append_file_name_to_plot"`
	TvShow               *bool  `json:"tv_show"`
	TvShowName           string `json:"tv_show_name"`
}
```

- [ ] **Step 4: Rename 传新参数** `drivers/emby_wrapper/driver.go`

```go
	return UpsertEmbyDirSetting(d.ID, srcObj.GetPath(), req.Actors, req.Plot, req.TvShowName, req.UseNameAsActor, req.AppendFileNameToPlot, req.TvShow)
```

- [ ] **Step 5: sed 批量更新既有测试调用点**（db_test.go、setting_test.go 中全部 6 参调用改为 8 参：plot 后插入 `""`，末尾追加 `nil`）

```bash
cd /Users/varg247/store/work-store/backend/openlist && sed -i '' -E 's/UpsertEmbyDirSetting\(([^,]+), ([^,]+), ("[^"]*"|nil), ("[^"]*"|nil), (nil|boolPtr\(true\)|&[a-z]+), (nil|&[a-z]+)\)/UpsertEmbyDirSetting(\1, \2, \3, \4, "", \5, \6, nil)/' drivers/emby_wrapper/db_test.go drivers/emby_wrapper/setting_test.go
```

Run 验证（若有未匹配的行会编译报错，手动补 `""` 与末尾 `nil` 即可）：

```bash
cd /Users/varg247/store/work-store/backend/openlist && /Library/Go/sdk/go1.25.4/bin/go build ./drivers/emby_wrapper/ ./internal/model/ && /Library/Go/sdk/go1.25.4/bin/go vet ./drivers/emby_wrapper/
```

Expected: 编译通过

- [ ] **Step 6: 追加合并语义测试** `drivers/emby_wrapper/db_test.go`

```go
// TestUpsertTVShowMergeSemantics：tv_show/tv_show_name 独立合并；显式关闭保行；全清删行。
func TestUpsertTVShowMergeSemantics(t *testing.T) {
	tv, ff := true, false
	// 标记电视剧 + 剧名 + actors 并存
	if err := UpsertEmbyDirSetting(99, "/TV", "演员X", "", "剧名X", nil, nil, &tv); err != nil {
		t.Fatalf("mark tv show: %v", err)
	}
	item, err := GetEmbyDirSetting(99, "/TV")
	if err != nil || item == nil {
		t.Fatalf("get: %v %v", item, err)
	}
	if !item.TvShow || item.TvShowName != "剧名X" || item.Actors != "演员X" {
		t.Errorf("expected tv_show=true name=剧名X actors=演员X, got %+v", item)
	}
	// 写 actors 不影响 tv 字段
	if err := UpsertEmbyDirSetting(99, "/TV", "演员Y", "", "", nil, nil, nil); err != nil {
		t.Fatalf("set actors: %v", err)
	}
	item, err = GetEmbyDirSetting(99, "/TV")
	if err != nil || item == nil {
		t.Fatalf("get: %v %v", item, err)
	}
	if !item.TvShow || item.TvShowName != "剧名X" || item.Actors != "演员Y" {
		t.Errorf("tv fields must survive actors write, got %+v", item)
	}
	// 显式关闭 tv_show：行保留（阻断语义与 append 一致）
	if err := UpsertEmbyDirSetting(99, "/TV", "", "", "", nil, nil, &ff); err != nil {
		t.Fatalf("disable tv show: %v", err)
	}
	item, err = GetEmbyDirSetting(99, "/TV")
	if err != nil || item == nil {
		t.Fatalf("row must survive explicit disable, got %v %v", item, err)
	}
	if item.TvShow {
		t.Errorf("expected tv_show disabled, got %+v", item)
	}
	// 全部清空：删行
	if err := UpsertEmbyDirSetting(99, "/TV", "", "", "", nil, nil, nil); err != nil {
		t.Fatalf("clear all: %v", err)
	}
	item, err = GetEmbyDirSetting(99, "/TV")
	if err != nil || item != nil {
		t.Errorf("expected row deleted, got %v %v", item, err)
	}
}

// TestUpsertTVShowNameClear：清剧名不影响 tv_show 标记。
func TestUpsertTVShowNameClear(t *testing.T) {
	tv := true
	if err := UpsertEmbyDirSetting(100, "/TV2", "", "", "剧名", nil, nil, &tv); err != nil {
		t.Fatalf("mark: %v", err)
	}
	if err := UpsertEmbyDirSetting(100, "/TV2", "", "", "", nil, nil, nil); err != nil {
		t.Fatalf("clear name: %v", err)
	}
	item, err := GetEmbyDirSetting(100, "/TV2")
	if err != nil || item == nil {
		t.Fatalf("get: %v %v", item, err)
	}
	if item.TvShowName != "" || !item.TvShow {
		t.Errorf("name cleared but tv_show kept, got %+v", item)
	}
}
```

- [ ] **Step 7: 运行确认通过**

```bash
cd /Users/varg247/store/work-store/backend/openlist && /Library/Go/sdk/go1.25.4/bin/go test ./drivers/emby_wrapper/ -count=1
```

Expected: PASS（既有测试不回归；新增合并语义测试通过）

- [ ] **Step 8: 提交**

```bash
cd /Users/varg247/store/work-store/backend/openlist && git add internal/model/emby.go drivers/emby_wrapper/db.go drivers/emby_wrapper/db_test.go drivers/emby_wrapper/folder.go drivers/emby_wrapper/driver.go drivers/emby_wrapper/setting_test.go && git commit -m "feat(emby_wrapper): add tv_show and tv_show_name directory settings"
```

---

### Task 2: episode.go 剧集树索引（纯函数部分）

**Files:**
- Create: `drivers/emby_wrapper/episode.go`
- Test: `drivers/emby_wrapper/episode_internal_test.go`

**Interfaces:**
- Consumes: 无（纯函数；`utils.Ext` 返回无点小写扩展名，与 `Init` 的 supportSuffix 键一致；`model.Obj.CreateTime()` 存在且 Ctime 为零时回退 ModTime）
- Produces: `isNumberedEpisode(fileName string) bool`、`episodeVirtualName(fileName string, seasonNo, epNo int) string`（`S%02dE%02d+扩展名`，纯编号不带前缀）、`byCreateTimeName(a, b model.Obj) bool`（创建时间升序、名称升序）、`tvIndex{root string; byVirtual map[string]model.Obj; titles/names/nfoBases/byReal map[string]string; seasonNo map[string]int; last model.Obj}`、`(*tvIndex).addEpisode(real model.Obj, canonicalPath, virtualName string)`、`(*tvIndex).resolve(virtualName string) model.Obj`、`(*tvIndex).episodeName(realObj model.Obj) (string, bool)` —— Task 4 依赖

- [ ] **Step 1: 写失败测试** `drivers/emby_wrapper/episode_internal_test.go`（package emby_wrapper，内部测试，与 nfo_internal_test.go 同模式）：

```go
package emby_wrapper

import (
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

func TestIsNumberedEpisode(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"AAA.mkv", false},
		{"A-S01E01.mp4", true},
		{"A.S01E01.mp4", true},
		{"Show.s01.e02.mkv", true},
		{"Show.s01_e02.mkv", true},
		{"Show.1x02.mkv", true},
		{"S01E01.mkv", true},
		{"A-1080p.mp4", false},
		{"A-B.mp4", false},
	}
	for _, c := range cases {
		if got := isNumberedEpisode(c.name); got != c.want {
			t.Errorf("isNumberedEpisode(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestEpisodeVirtualName(t *testing.T) {
	cases := []struct {
		fileName string
		seasonNo int
		epNo     int
		want     string
	}{
		{"AAA.mkv", 1, 1, "AAA-S01E01.mkv"},
		{"B.mp4", 2, 5, "B-S02E05.mp4"},
		{"C", 12, 3, "C-S12E03"},
		{"D.mkv", 1, 100, "D-S01E100.mkv"},
	}
	for _, c := range cases {
		if got := episodeVirtualName(c.fileName, c.seasonNo, c.epNo); got != c.want {
			t.Errorf("episodeVirtualName(%q, %d, %d) = %q, want %q", c.fileName, c.seasonNo, c.epNo, got, c.want)
		}
	}
}

func TestByCreateTimeName(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	obj := func(name string, ctime, mtime time.Time) model.Obj {
		return &model.Object{Name: name, Path: "/dir/" + name, Modified: mtime, Ctime: ctime}
	}
	// 创建时间优先
	if !byCreateTimeName(obj("A.mp4", base, base), obj("B.mp4", base.Add(time.Hour), base.Add(2*time.Hour))) {
		t.Error("older ctime must sort first")
	}
	// Ctime 为零时回退修改时间
	if !byCreateTimeName(obj("A.mp4", time.Time{}, base), obj("B.mp4", time.Time{}, base.Add(time.Hour))) {
		t.Error("zero ctime must fall back to mtime")
	}
	// 时间相同按名称升序
	if !byCreateTimeName(obj("A.mp4", base, base), obj("B.mp4", base, base)) {
		t.Error("same time must tie-break by name asc")
	}
	if byCreateTimeName(obj("B.mp4", base, base), obj("A.mp4", base, base)) {
		t.Error("name tie-break must be asc")
	}
}

func TestTVIndexAddAndResolve(t *testing.T) {
	idx := newTVIndexForTest("/R")
	real := &model.Object{Name: "A.mp4", Path: "/R/A.mp4", Modified: time.Now()}
	idx.addEpisode(real, "/R/A.mp4", "A-S01E01.mp4")
	if got := idx.resolve("A-S01E01.mp4"); got != real {
		t.Errorf("resolve must return the real object, got %v", got)
	}
	if got := idx.resolve("a-s01e01.mp4"); got != real {
		t.Errorf("resolve must be case-insensitive, got %v", got)
	}
	if got := idx.resolve("A.mp4"); got != nil {
		t.Errorf("original name must not resolve, got %v", got)
	}
	if got := idx.titles["a-s01e01.mp4"]; got != "A" {
		t.Errorf("title must be original base name, got %q", got)
	}
	if got := idx.nfoBases["a-s01e01"]; got != "A-S01E01.mp4" {
		t.Errorf("nfo base must map to virtual name, got %q", got)
	}
	if got, ok := idx.episodeName(real); !ok || got != "A-S01E01.mp4" {
		t.Errorf("episodeName by real path must work, got %q %v", got, ok)
	}
}

// newTVIndexForTest 构造空索引（测试辅助，Task 4 的 buildTVIndex 是驱动方法）。
func newTVIndexForTest(root string) *tvIndex {
	return &tvIndex{
		root:      root,
		byVirtual: map[string]model.Obj{},
		titles:    map[string]string{},
		names:     map[string]string{},
		nfoBases:  map[string]string{},
		byReal:    map[string]string{},
		seasonNo:  map[string]int{},
	}
}
```

- [ ] **Step 2: 运行确认失败**

```bash
cd /Users/varg247/store/work-store/backend/openlist && /Library/Go/sdk/go1.25.4/bin/go test ./drivers/emby_wrapper/ -run 'TestIsNumberedEpisode|TestEpisodeVirtualName|TestByCreateTimeName|TestTVIndexAddAndResolve' -count=1
```

Expected: FAIL（编译错误：isNumberedEpisode 等未定义）

- [ ] **Step 3: 实现** `drivers/emby_wrapper/episode.go`

```go
package emby_wrapper

import (
	"fmt"
	stdpath "path"
	"regexp"
	"strings"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

// episodePattern 匹配已含 Emby 剧集编号的文件名（SxxExx 及其变体、NxNN）。
// 命中视为已编号，保持原名、跳过时间排序编号。
var episodePattern = regexp.MustCompile(`(?i)(?:^|[._ -])(?:s\d{1,4}[. _-]*e\d{1,3}|\d{1,4}x\d{1,3})(?:[._ -]|$)`)

// isNumberedEpisode 判断文件名是否已含剧集编号。
func isNumberedEpisode(fileName string) bool {
	return episodePattern.MatchString(fileName)
}

// episodeVirtualName 为未编号文件生成虚拟剧集名：S{季号}E{集号}+原扩展名（纯编号，见 Spec 9 修订说明）。
func episodeVirtualName(fileName string, seasonNo, epNo int) string {
	ext := stdpath.Ext(fileName)
	return fmt.Sprintf("%s-S%02dE%02d%s", strings.TrimSuffix(fileName, ext), seasonNo, epNo, ext)
}

// byCreateTimeName 比较两个对象的排序键：创建时间升序（CreateTime 为零时回退
// ModTime，model.Object.CreateTime 已内置该回退），时间相同按名称升序。
// 保证任何输入集都产生确定性顺序。
func byCreateTimeName(a, b model.Obj) bool {
	at, bt := a.CreateTime(), b.CreateTime()
	if !at.Equal(bt) {
		return at.Before(bt)
	}
	return a.GetName() < b.GetName()
}

// tvIndex 一部电视剧的完整索引：根目录直接文件 = 第 1 季；直接子文件夹 = 季
// （按创建时间+名称排序分配连续季号，保留原名）。一次构建，List 展示与 Get 反查共用。
type tvIndex struct {
	root      string              // 剧集根目录规范路径
	byVirtual map[string]model.Obj // 小写虚拟名（含扩展名）→ 真实对象
	titles    map[string]string    // 小写虚拟名 → 集名（原文件名去扩展名）
	names     map[string]string    // 小写虚拟名 → 虚拟名（保留原样）
	nfoBases  map[string]string    // 小写虚拟名去扩展名 → 虚拟名
	byReal    map[string]string    // 规范真实路径 → 虚拟名
	seasonNo  map[string]int       // 直接子文件夹规范路径 → 季号
	last      model.Obj            // 排序后最新的视频对象（tvshow.nfo 时间戳用，无视频时为 nil）
}

// addEpisode 将真实对象登记为一个剧集条目（虚拟名 → 真实对象及各派生映射）。
// canonicalPath 为 wrapper 命名空间下的真实路径。
func (idx *tvIndex) addEpisode(real model.Obj, canonicalPath, virtualName string) {
	key := strings.ToLower(virtualName)
	idx.byVirtual[key] = real
	idx.names[key] = virtualName
	ext := stdpath.Ext(real.GetName())
	idx.titles[key] = strings.TrimSuffix(real.GetName(), ext)
	idx.nfoBases[strings.ToLower(strings.TrimSuffix(virtualName, stdpath.Ext(virtualName)))] = virtualName
	idx.byReal[canonicalPath] = virtualName
	idx.last = real
}

// resolve 按虚拟名（含扩展名，大小写不敏感）反查真实对象；未命中返回 nil。
func (idx *tvIndex) resolve(virtualName string) model.Obj {
	return idx.byVirtual[strings.ToLower(virtualName)]
}

// episodeName 返回真实对象（规范路径）对应的虚拟名。
func (idx *tvIndex) episodeName(realObj model.Obj) (string, bool) {
	name, ok := idx.byReal[realObj.GetPath()]
	return name, ok
}
```

- [ ] **Step 4: 运行确认通过**

```bash
cd /Users/varg247/store/work-store/backend/openlist && /Library/Go/sdk/go1.25.4/bin/go test ./drivers/emby_wrapper/ -run 'TestIsNumberedEpisode|TestEpisodeVirtualName|TestByCreateTimeName|TestTVIndexAddAndResolve' -count=1
```

Expected: PASS

- [ ] **Step 5: 提交**

```bash
cd /Users/varg247/store/work-store/backend/openlist && git add drivers/emby_wrapper/episode.go drivers/emby_wrapper/episode_internal_test.go && git commit -m "feat(emby_wrapper): build tv-show index primitives with time-ordered naming"
```

---

### Task 3: RenderNFO 根元素参数化 + 剧集/剧集级/季 nfo 构建

**Files:**
- Modify: `drivers/virtual_file/util.go`
- Modify: `drivers/emby_wrapper/nfo.go`
- Test: `drivers/virtual_file/render_nfo_test.go`、`drivers/emby_wrapper/nfo_internal_test.go`

**Interfaces:**
- Consumes: 现有 `virtual_file.Media`/`Inner`/`Actor`、`buildPlot`/`plotFileName`/`splitActors`
- Produces: `virtual_file.RenderNFO(root string, m *Media) ([]byte, error)`（拷贝语义，不突变入参；`RenderMediaNFO` 签名不变委托之）、`buildNFOWithRoot(root, title, plot string, setting *model.EmbyDirSetting) ([]byte, error)`、`buildEpisodeNFO(title string, setting *model.EmbyDirSetting) ([]byte, error)`、`buildTVShowNFO(showName, plot string, setting *model.EmbyDirSetting) ([]byte, error)`、`buildSeasonNFO(seasonNo int, name string) []byte`（手写 XML，无失败路径）—— Task 4 依赖

- [ ] **Step 1: 写失败测试**（追加到 `drivers/virtual_file/render_nfo_test.go`）：

```go
// TestRenderNFOWithRoot：根元素参数化，且不突变入参 Media。
func TestRenderNFOWithRoot(t *testing.T) {
	m := &Media{
		Title: Inner{Inner: "<![CDATA[测试标题]]>"},
		Actor: []Actor{{Name: "三上悠亚"}},
	}
	out, err := RenderNFO("tvshow", m)
	if err != nil {
		t.Fatalf("render tvshow: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "<tvshow>") || !strings.Contains(got, "</tvshow>") {
		t.Errorf("missing tvshow root, got %s", got)
	}
	if strings.Contains(got, "<movie>") {
		t.Errorf("tvshow must not use movie root, got %s", got)
	}
	out2, err := RenderNFO("episodedetails", m)
	if err != nil {
		t.Fatalf("render episodedetails: %v", err)
	}
	if !strings.Contains(string(out2), "<episodedetails>") {
		t.Errorf("missing episodedetails root, got %s", string(out2))
	}
	// 入参不被突变：仍保持 movie 根
	if m.XMLName.Local != "movie" {
		t.Errorf("RenderNFO must not mutate input Media, XMLName=%s", m.XMLName.Local)
	}
	// 注：m 的构造字面量需显式 XMLName: xml.Name{Local: "movie"}，否则 Local 为空串、"不被突变"无从断言
	// RenderMediaNFO 行为不变
	out3, err := RenderMediaNFO(m)
	if err != nil {
		t.Fatalf("render movie: %v", err)
	}
	if !strings.Contains(string(out3), "<movie>") {
		t.Errorf("RenderMediaNFO must keep movie root, got %s", string(out3))
	}
}
```

追加到 `drivers/emby_wrapper/nfo_internal_test.go`（需要新增 import：`"strings"`、`"github.com/OpenListTeam/OpenList/v4/internal/model"`）：

```go
// TestBuildEpisodeNFO：剧集 nfo 根元素 episodedetails，title=集名，保留 actors，无 plot 内容。
func TestBuildEpisodeNFO(t *testing.T) {
	content, err := buildEpisodeNFO("A", &model.EmbyDirSetting{Actors: "演员A"})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	got := string(content)
	if !strings.Contains(got, "<episodedetails>") {
		t.Errorf("missing episodedetails root, got %s", got)
	}
	if !strings.Contains(got, "<![CDATA[A]]>") {
		t.Errorf("missing title A, got %s", got)
	}
	if !strings.Contains(got, "<name>演员A</name>") {
		t.Errorf("missing actor, got %s", got)
	}
	if strings.Contains(got, "剧集介绍") {
		t.Errorf("episode nfo must not contain show plot, got %s", got)
	}
}

// TestBuildTVShowNFO：剧集级 nfo 根元素 tvshow，title=剧名，plot=剧集介绍，保留 actors。
func TestBuildTVShowNFO(t *testing.T) {
	content, err := buildTVShowNFO("测试剧", "剧集介绍", &model.EmbyDirSetting{Actors: "演员A"})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	got := string(content)
	if !strings.Contains(got, "<tvshow>") {
		t.Errorf("missing tvshow root, got %s", got)
	}
	if !strings.Contains(got, "<![CDATA[测试剧]]>") {
		t.Errorf("missing show name, got %s", got)
	}
	if !strings.Contains(got, "<![CDATA[剧集介绍]]>") {
		t.Errorf("missing plot, got %s", got)
	}
	if !strings.Contains(got, "<name>演员A</name>") {
		t.Errorf("missing actor, got %s", got)
	}
}

// TestBuildSeasonNFO：季 nfo 根元素 season，含 seasonnumber 与 seasonname。
func TestBuildSeasonNFO(t *testing.T) {
	got := string(buildSeasonNFO(2, "2024年"))
	if !strings.Contains(got, "<season>") {
		t.Errorf("missing season root, got %s", got)
	}
	if !strings.Contains(got, "<seasonnumber>2</seasonnumber>") {
		t.Errorf("missing seasonnumber 2, got %s", got)
	}
	if !strings.Contains(got, "<seasonname><![CDATA[2024年]]></seasonname>") {
		t.Errorf("missing seasonname, got %s", got)
	}
}
```

- [ ] **Step 2: 运行确认失败**

```bash
cd /Users/varg247/store/work-store/backend/openlist && /Library/Go/sdk/go1.25.4/bin/go test ./drivers/virtual_file/ -run TestRenderNFOWithRoot -count=1 && /Library/Go/sdk/go1.25.4/bin/go test ./drivers/emby_wrapper/ -run 'TestBuildEpisodeNFO|TestBuildTVShowNFO|TestBuildSeasonNFO' -count=1
```

Expected: FAIL（RenderNFO/buildEpisodeNFO/buildTVShowNFO/buildSeasonNFO 未定义）

- [ ] **Step 3: 实现 `drivers/virtual_file/util.go`** —— `RenderMediaNFO` 替换为：

```go
// RenderNFO 将 Media 结构渲染为指定根元素（movie/tvshow/episodedetails）的 NFO XML 文档（含 XML 头）。
// 不突变入参。
// 注意：不能通过 copy.XMLName 赋值改根元素——encoding/xml 中 XMLName 字段的 struct tag
// （xml:"movie"）优先于字段值；必须用 EncodeElement 的 start 参数（startTemplate 优先级）。
func RenderNFO(root string, m *Media) ([]byte, error) {
	var buf bytes.Buffer
	enc := xml.NewEncoder(&buf)
	enc.Indent("", "  ")
	if err := enc.EncodeElement(m, xml.StartElement{Name: xml.Name{Local: root}}); err != nil {
		return nil, err
	}
	if err := enc.Flush(); err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), buf.Bytes()...), nil
}

// RenderMediaNFO 渲染 <movie> 根元素的 NFO XML 文档（与 javdb 落盘 nfo 格式一致）。
// 供其他驱动（如 emby_wrapper）构建内存 nfo。
func RenderMediaNFO(m *Media) ([]byte, error) {
	return RenderNFO("movie", m)
}
```

（`xml` 已在 util.go 的 import 中）

- [ ] **Step 4: 实现 `drivers/emby_wrapper/nfo.go`** —— 追加构建函数并重构 `buildNFOContent`：

```go
// buildNFOWithRoot 构建指定根元素（movie/tvshow/episodedetails）的 nfo XML：title + plot + actor。
func buildNFOWithRoot(root, title, plot string, setting *model.EmbyDirSetting) ([]byte, error) {
	actors := splitActors(setting.Actors)
	actorInfos := make([]virtual_file.Actor, 0, len(actors))
	for _, a := range actors {
		actorInfos = append(actorInfos, virtual_file.Actor{Name: a})
	}
	return virtual_file.RenderNFO(root, &virtual_file.Media{
		Title: virtual_file.Inner{Inner: fmt.Sprintf("<![CDATA[%s]]>", title)},
		Plot:  virtual_file.Inner{Inner: fmt.Sprintf("<![CDATA[%s]]>", plot)},
		Actor: actorInfos,
	})
}

// buildNFOContent 构建与 javdb 格式一致的影片 nfo XML：title + actor + plot。
// plot 配置后 title 与 plot 同值（用户确认：plot 选项同时作用于 title 与 plot）；
// plot 未配置时 title 保持影片文件名（归一化）。
func buildNFOContent(title, fileName string, setting *model.EmbyDirSetting) ([]byte, error) {
	plot := buildPlot(setting.Plot, setting.AppendFileNameToPlot, fileName)
	nfoTitle := title
	if setting.Plot != "" {
		nfoTitle = plot
	}
	return buildNFOWithRoot("movie", nfoTitle, plot, setting)
}

// buildEpisodeNFO 构建剧集 nfo：title（原文件名去扩展名）+ actor，无 plot（plot 属于剧集级 tvshow.nfo）。
func buildEpisodeNFO(title string, setting *model.EmbyDirSetting) ([]byte, error) {
	return buildNFOWithRoot("episodedetails", title, "", setting)
}

// buildTVShowNFO 构建剧集级 nfo：title（自定义剧名）+ plot（剧集介绍）+ actor。
func buildTVShowNFO(showName, plot string, setting *model.EmbyDirSetting) ([]byte, error) {
	return buildNFOWithRoot("tvshow", showName, plot, setting)
}

// buildSeasonNFO 构建季 nfo：<season><seasonnumber>N</seasonnumber><seasonname>名称</seasonname></season>。
// Emby 的 SeasonNfoParser 读取 seasonnumber 设置季号、seasonname 设置显示名，
// 使任意命名的文件夹（如"2024年"）被识别为指定季。
func buildSeasonNFO(seasonNo int, name string) []byte {
	return []byte(fmt.Sprintf(`<?xml version="1.0" encoding="utf-8" standalone="yes"?>
<season>
  <seasonnumber>%d</seasonnumber>
  <seasonname><![CDATA[%s]]></seasonname>
</season>`, seasonNo, name))
}
```

- [ ] **Step 5: 运行确认通过**

```bash
cd /Users/varg247/store/work-store/backend/openlist && /Library/Go/sdk/go1.25.4/bin/go test ./drivers/virtual_file/ -count=1 && /Library/Go/sdk/go1.25.4/bin/go test ./drivers/emby_wrapper/ -count=1
```

Expected: PASS（既有 nfo/plot 测试不回归——buildNFOContent 行为不变）

- [ ] **Step 6: 提交**

```bash
cd /Users/varg247/store/work-store/backend/openlist && git add drivers/virtual_file/util.go drivers/virtual_file/render_nfo_test.go drivers/emby_wrapper/nfo.go drivers/emby_wrapper/nfo_internal_test.go && git commit -m "feat(emby_wrapper): build episode, tvshow and season nfo content"
```

---

### Task 4: List/Get TV 分支（季/剧集索引 + 反查 + Link 还原）

**Files:**
- Modify: `drivers/emby_wrapper/folder.go`（virtualEpisode 类型）
- Modify: `drivers/emby_wrapper/setting.go`（tvShowInfo / tvShowAncestor / isTVDir）
- Modify: `drivers/emby_wrapper/episode.go`（buildTVIndex / gatherVideos / collectSeasonVideos / withTVShowNFOs）
- Modify: `drivers/emby_wrapper/driver.go`（List 路由 / Get 反查 / resolveEpisodePath / virtualNFOForPath 重构 / newVirtualNFO）
- Test: `drivers/emby_wrapper/tv_show_test.go`（新文件，外部测试包）、`drivers/emby_wrapper/e2e_test.go`

**Interfaces:**
- Consumes: Task 2 的 `tvIndex`/`addEpisode`/`resolve`/`episodeName`/`byCreateTimeName`/`episodeVirtualName`/`isNumberedEpisode`、Task 3 的 `buildEpisodeNFO`/`buildTVShowNFO`/`buildSeasonNFO`、Task 1 的 `TvShow`/`TvShowName`/`FolderAddition`、现有 `resolveSetting`/`withVirtualNFOs`/`virtualNFO`/`nfoBaseName`
- Produces: `tvShowAncestor(dirPath string) (rootPath, showName string, ok bool, err error)`（最近的电视剧祖先，含自身）、`isTVDir(dirPath string) (bool, error)`、`buildTVIndex(ctx, rootPath string) (*tvIndex, error)`、`withTVShowNFOs(ctx, dir model.Obj, rootPath, showName string, objs []model.Obj) []model.Obj`、`resolveEpisodePath(ctx, path string) (model.Obj, bool, error)`、`newVirtualEpisode(real model.Obj, name, path string) model.Obj`、`d.newVirtualNFO(path string, content []byte, modified time.Time) model.Obj`

- [ ] **Step 1: 写失败测试** `drivers/emby_wrapper/tv_show_test.go`（package emby_wrapper_test）：

```go
package emby_wrapper_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/emby_wrapper"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

// writeEpisodeFile 写下游文件并等待 15ms：local 驱动取文件系统 birth time 作为
// CreateTime（os.Chtimes 无法控制），sleep 保证创建时间严格递增、排序确定性。
func writeEpisodeFile(t *testing.T, relPath, content string) error {
	t.Helper()
	if err := writeDownstreamFile(t, relPath, content); err != nil {
		return err
	}
	time.Sleep(15 * time.Millisecond)
	return nil
}

// writeDirOrdered 建下游目录并等待 15ms（季号按文件夹创建时间排序）。
func writeDirOrdered(t *testing.T, relPath string) error {
	t.Helper()
	if err := writeDownstreamDir(t, relPath); err != nil {
		return err
	}
	time.Sleep(15 * time.Millisecond)
	return nil
}

func sortedNames(objs []model.Obj) []string {
	got := names(objs)
	sort.Strings(got)
	return got
}

func markTVShow(t *testing.T, d *emby_wrapper.EmbyWrapper, payload string) {
	markTVShowAt(t, d, "/Movies", payload)
}

func markTVShowAt(t *testing.T, d *emby_wrapper.EmbyWrapper, dirPath, payload string) {
	t.Helper()
	if err := d.Rename(context.Background(), &model.Object{Name: stdpath.Base(dirPath), Path: dirPath, IsFolder: true}, payload); err != nil {
		t.Fatalf("mark tv show %s: %+v", dirPath, err)
	}
}

// TestTVShowSeasonsAndRootEpisodes：根目录直接文件 = 第 1 季；直接子文件夹按
// 创建时间分配连续季号（根有视频从 2 起）；季内文件按创建时间编号。
func TestTVShowSeasonsAndRootEpisodes(t *testing.T) {
	d := setup(t)
	// 根目录已有 AAA.mkv（setup 创建，最早）→ 季 1；子文件夹按创建时间：2024年 早 → 季 2，2025年 → 季 3
	if err := writeDirOrdered(t, "/Movies/2024年"); err != nil {
		t.Fatalf("mkdir 2024年: %v", err)
	}
	if err := writeDirOrdered(t, "/Movies/2025年"); err != nil {
		t.Fatalf("mkdir 2025年: %v", err)
	}
	if err := writeEpisodeFile(t, "/Movies/2024年/A1.mp4", "a1"); err != nil {
		t.Fatalf("write A1: %v", err)
	}
	if err := writeEpisodeFile(t, "/Movies/2024年/A2.mp4", "a2"); err != nil {
		t.Fatalf("write A2: %v", err)
	}
	if err := writeEpisodeFile(t, "/Movies/2025年/B1.mp4", "b1"); err != nil {
		t.Fatalf("write B1: %v", err)
	}
	markTVShow(t, d, `{"tv_show":true,"tv_show_name":"测试剧","plot":"剧集介绍","actors":"演员A"}`)
	// 根目录：AAA 季1 + 两个季文件夹 + tvshow.nfo
	objs, err := d.List(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list root: %+v", err)
	}
	if got := sortedNames(objs); !reflect.DeepEqual(got, []string{"2024年", "2025年", "AAA-S01E01.mkv", "AAA-S01E01.nfo", "tvshow.nfo"}) {
		t.Errorf("root listing mismatch, got %v", got)
	}
	// 季 2（2024年）：A1 先建 → E01；含 season.nfo
	objs, err = d.List(context.Background(), &model.Object{Name: "2024年", Path: "/Movies/2024年", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list 2024年: %+v", err)
	}
	if got := sortedNames(objs); !reflect.DeepEqual(got, []string{"A1-S02E01.mp4", "A1-S02E01.nfo", "A2-S02E02.mp4", "A2-S02E02.nfo", "season.nfo"}) {
		t.Errorf("season 2 listing mismatch, got %v", got)
	}
	// 季 3（2025年）
	objs, err = d.List(context.Background(), &model.Object{Name: "2025年", Path: "/Movies/2025年", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list 2025年: %+v", err)
	}
	if got := sortedNames(objs); !reflect.DeepEqual(got, []string{"B1-S03E01.mp4", "B1-S03E01.nfo", "season.nfo"}) {
		t.Errorf("season 3 listing mismatch, got %v", got)
	}
}

// TestTVShowNoRootVideos：根目录无直接视频时子文件夹从第 1 季起。
func TestTVShowNoRootVideos(t *testing.T) {
	d := setup(t)
	if err := os.Remove(filepath.Join(localRoot, "Movies", "AAA.mkv")); err != nil {
		t.Fatalf("remove AAA: %v", err)
	}
	if err := writeDirOrdered(t, "/Movies/2024年"); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := writeEpisodeFile(t, "/Movies/2024年/A1.mp4", "a1"); err != nil {
		t.Fatalf("write A1: %v", err)
	}
	markTVShow(t, d, `{"tv_show":true}`)
	objs, err := d.List(context.Background(), &model.Object{Name: "2024年", Path: "/Movies/2024年", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list: %+v", err)
	}
	if got := sortedNames(objs); !reflect.DeepEqual(got, []string{"A1-S01E01.mp4", "A1-S01E01.nfo", "season.nfo"}) {
		t.Errorf("no-root-videos season must start at 1, got %v", got)
	}
}

// TestTVShowNestedFolderInSeason：季内的嵌套子文件夹文件并入该季编号（原地展示）。
func TestTVShowNestedFolderInSeason(t *testing.T) {
	d := setup(t)
	if err := writeDirOrdered(t, "/Movies/2024年"); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := writeEpisodeFile(t, "/Movies/2024年/C.mp4", "c"); err != nil {
		t.Fatalf("write C: %v", err)
	}
	if err := writeDirOrdered(t, "/Movies/2024年/专题"); err != nil {
		t.Fatalf("mkdir 专题: %v", err)
	}
	if err := writeEpisodeFile(t, "/Movies/2024年/专题/D.mp4", "d"); err != nil {
		t.Fatalf("write D: %v", err)
	}
	markTVShow(t, d, `{"tv_show":true}`)
	// C 先建 → 季 2 的 E01；D 后建 → E02，显示在 专题 内
	objs, err := d.List(context.Background(), &model.Object{Name: "专题", Path: "/Movies/2024年/专题", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list 专题: %+v", err)
	}
	if got := sortedNames(objs); !reflect.DeepEqual(got, []string{"D-S02E02.mp4", "D-S02E02.nfo"}) {
		t.Errorf("nested folder episodes mismatch, got %v", got)
	}
	// 2024年 自身列表：C 在根、专题文件夹原样（无 season.nfo——只有直接子文件夹是季）
	objs, err = d.List(context.Background(), &model.Object{Name: "2024年", Path: "/Movies/2024年", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list 2024年: %+v", err)
	}
	if got := sortedNames(objs); !reflect.DeepEqual(got, []string{"C-S02E01.mp4", "C-S02E01.nfo", "season.nfo", "专题"}) {
		t.Errorf("season listing with nested dir mismatch, got %v", got)
	}
}

// TestTVShowSeasonNFOContent：season.nfo 含分配的季号与原文件夹名。
func TestTVShowSeasonNFOContent(t *testing.T) {
	d := setup(t)
	if err := writeDirOrdered(t, "/Movies/2024年"); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := writeEpisodeFile(t, "/Movies/2024年/A1.mp4", "a1"); err != nil {
		t.Fatalf("write A1: %v", err)
	}
	markTVShow(t, d, `{"tv_show":true}`)
	got := readNFOLink(t, d, "/Movies/2024年/season.nfo")
	if !strings.Contains(got, "<seasonnumber>2</seasonnumber>") {
		t.Errorf("season.nfo must carry assigned season number, got %s", got)
	}
	if !strings.Contains(got, "<seasonname><![CDATA[2024年]]></seasonname>") {
		t.Errorf("season.nfo must carry original folder name, got %s", got)
	}
}

// TestTVShowNFOsContent：剧集 nfo（episodedetails、title=原名、actors、无 plot）与 tvshow.nfo（剧名+简介）。
func TestTVShowNFOsContent(t *testing.T) {
	d := setup(t)
	if err := writeDirOrdered(t, "/Movies/2024年"); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := writeEpisodeFile(t, "/Movies/2024年/A1.mp4", "a1"); err != nil {
		t.Fatalf("write A1: %v", err)
	}
	markTVShow(t, d, `{"tv_show":true,"tv_show_name":"测试剧","plot":"剧集介绍","actors":"演员A"}`)
	ep := readNFOLink(t, d, "/Movies/2024年/A1-S02E01.nfo")
	if !strings.Contains(ep, "<episodedetails>") {
		t.Errorf("episode nfo must use episodedetails root, got %s", ep)
	}
	if !strings.Contains(ep, "<![CDATA[A1]]>") {
		t.Errorf("episode nfo title must be original base name, got %s", ep)
	}
	if !strings.Contains(ep, "<name>演员A</name>") {
		t.Errorf("episode nfo must keep actors, got %s", ep)
	}
	if strings.Contains(ep, "剧集介绍") {
		t.Errorf("episode nfo must not contain show plot, got %s", ep)
	}
	show := readNFOLink(t, d, "/Movies/tvshow.nfo")
	if !strings.Contains(show, "<tvshow>") || !strings.Contains(show, "<![CDATA[测试剧]]>") || !strings.Contains(show, "<![CDATA[剧集介绍]]>") {
		t.Errorf("tvshow.nfo mismatch, got %s", show)
	}
}

// TestTVShowNameFallbackFolder：未设置剧名时回退文件夹名。
func TestTVShowNameFallbackFolder(t *testing.T) {
	d := setup(t)
	markTVShow(t, d, `{"tv_show":true}`)
	show := readNFOLink(t, d, "/Movies/tvshow.nfo")
	if !strings.Contains(show, "<![CDATA[Movies]]>") {
		t.Errorf("show name must fall back to folder name, got %s", show)
	}
}

// TestTVShowSkipsNumbered：已含 SxxExx 的文件保持原名，不消耗序号。
func TestTVShowSkipsNumbered(t *testing.T) {
	d := setup(t)
	if err := writeDirOrdered(t, "/Movies/2024年"); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := writeEpisodeFile(t, "/Movies/2024年/A1.mp4", "a1"); err != nil {
		t.Fatalf("write A1: %v", err)
	}
	if err := writeEpisodeFile(t, "/Movies/2024年/Show.S01E03.mkv", "s"); err != nil {
		t.Fatalf("write numbered: %v", err)
	}
	markTVShow(t, d, `{"tv_show":true}`)
	objs, err := d.List(context.Background(), &model.Object{Name: "2024年", Path: "/Movies/2024年", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list: %+v", err)
	}
	if got := sortedNames(objs); !reflect.DeepEqual(got, []string{"A1-S02E01.mp4", "A1-S02E01.nfo", "Show.S01E03.mkv", "Show.S01E03.nfo", "season.nfo"}) {
		t.Errorf("numbered file must keep name without consuming index, got %v", got)
	}
}

// TestTVShowRealSeasonNFOFileWins：下游真实 season.nfo 优先于虚拟。
func TestTVShowRealSeasonNFOFileWins(t *testing.T) {
	d := setup(t)
	if err := writeDirOrdered(t, "/Movies/2024年"); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := writeEpisodeFile(t, "/Movies/2024年/A1.mp4", "a1"); err != nil {
		t.Fatalf("write A1: %v", err)
	}
	if err := writeDownstreamFile(t, "/Movies/2024年/season.nfo", "real-content"); err != nil {
		t.Fatalf("write real season.nfo: %v", err)
	}
	markTVShow(t, d, `{"tv_show":true}`)
	if got := readNFOLink(t, d, "/Movies/2024年/season.nfo"); got != "real-content" {
		t.Errorf("real season.nfo must win, got %q", got)
	}
}

// TestGetAndLinkSeasonEpisode：季内虚拟剧集路径 Get 反查真实文件，Link 还原真实内容。
func TestGetAndLinkSeasonEpisode(t *testing.T) {
	d := setup(t)
	if err := writeDirOrdered(t, "/Movies/2024年"); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := writeEpisodeFile(t, "/Movies/2024年/A1.mp4", "a1"); err != nil {
		t.Fatalf("write A1: %v", err)
	}
	markTVShow(t, d, `{"tv_show":true}`)
	if got := readNFOLink(t, d, "/Movies/2024年/A1-S02E01.mp4"); got != "a1" {
		t.Errorf("episode must play real file content, got %q", got)
	}
}

// TestTVShowNestedTVSkipped：嵌套标记为电视剧的子文件夹独立成剧，父剧索引跳过它。
func TestTVShowNestedTVSkipped(t *testing.T) {
	d := setup(t)
	if err := writeDirOrdered(t, "/Movies/2024年"); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := writeEpisodeFile(t, "/Movies/2024年/A1.mp4", "a1"); err != nil {
		t.Fatalf("write A1: %v", err)
	}
	if err := writeDirOrdered(t, "/Movies/2024年/内嵌剧"); err != nil {
		t.Fatalf("mkdir 内嵌剧: %v", err)
	}
	if err := writeEpisodeFile(t, "/Movies/2024年/内嵌剧/X.mp4", "x"); err != nil {
		t.Fatalf("write X: %v", err)
	}
	markTVShow(t, d, `{"tv_show":true}`)
	markTVShowAt(t, d, "/Movies/2024年/内嵌剧", `{"tv_show":true,"tv_show_name":"内嵌剧名"}`)
	// 内嵌剧自身：独立成剧，季 1
	objs, err := d.List(context.Background(), &model.Object{Name: "内嵌剧", Path: "/Movies/2024年/内嵌剧", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list 内嵌剧: %+v", err)
	}
	if got := sortedNames(objs); !reflect.DeepEqual(got, []string{"X-S01E01.mp4", "X-S01E01.nfo", "tvshow.nfo"}) {
		t.Errorf("nested tv show must be independent, got %v", got)
	}
	// 父剧的季 2：X 不参与编号
	objs, err = d.List(context.Background(), &model.Object{Name: "2024年", Path: "/Movies/2024年", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list 2024年: %+v", err)
	}
	if got := sortedNames(objs); !reflect.DeepEqual(got, []string{"A1-S02E01.mp4", "A1-S02E01.nfo", "内嵌剧", "season.nfo"}) {
		t.Errorf("nested tv must be skipped by parent season, got %v", got)
	}
}

// TestGetVirtualEpisodeNotFound：无匹配的虚拟剧集路径保持 404。
func TestGetVirtualEpisodeNotFound(t *testing.T) {
	d := setup(t)
	markTVShow(t, d, `{"tv_show":true}`)
	if _, err := d.Get(context.Background(), "/Movies/NOPE-S01E01.mp4"); err == nil {
		t.Fatal("unmapped virtual episode must not be served")
	}
}
```

（`tv_show_test.go` 需要 import `stdpath "path"`——markTVShowAt 中 `stdpath.Base`）

追加到 `drivers/emby_wrapper/e2e_test.go`：

```go
// TestEndToEndTVShowThroughFS：TV 模式经 fs 层全链路（等价于 strm 落盘路径：虚拟名落盘、播放还原真实文件）。
func TestEndToEndTVShowThroughFS(t *testing.T) {
	_ = setup(t)
	if err := writeDirOrdered(t, "/Movies/2024年"); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := writeEpisodeFile(t, "/Movies/2024年/A1.mp4", "a1"); err != nil {
		t.Fatalf("write A1: %v", err)
	}
	if err := fs.Rename(context.Background(), "/ew/Movies", `{"tv_show":true,"tv_show_name":"测试剧"}`); err != nil {
		t.Fatalf("rename via fs: %+v", err)
	}
	objs, err := fs.List(context.Background(), "/ew/Movies", &fs.ListArgs{})
	if err != nil {
		t.Fatalf("fs list: %+v", err)
	}
	episodeFound, tvshowFound, seasonFound := false, false, false
	for _, o := range objs {
		switch o.GetName() {
		case "AAA-S01E01.mkv":
			episodeFound = true
		case "tvshow.nfo":
			tvshowFound = true
		case "2024年":
			seasonFound = true
		}
	}
	if !episodeFound || !tvshowFound || !seasonFound {
		t.Fatalf("fs list must contain episode/tvshow.nfo/season folder, got %v", names(objs))
	}
	// 季文件夹：虚拟剧集 + season.nfo
	objs, err = fs.List(context.Background(), "/ew/Movies/2024年", &fs.ListArgs{})
	if err != nil {
		t.Fatalf("fs list season: %+v", err)
	}
	if !containsName(objs, "A1-S02E01.mp4") || !containsName(objs, "season.nfo") {
		t.Fatalf("season folder listing mismatch, got %v", names(objs))
	}
	// 播放链路：季内虚拟剧集路径 → 还原真实文件内容
	link, _, err := fs.Link(context.Background(), "/ew/Movies/2024年/A1-S02E01.mp4", model.LinkArgs{})
	if err != nil {
		t.Fatalf("fs link episode: %+v", err)
	}
	if link.RangeReader == nil {
		t.Fatal("episode link must have a range reader")
	}
	rc, err := link.RangeReader.RangeRead(context.Background(), http_range.Range{Length: -1})
	if err != nil {
		t.Fatalf("range read: %+v", err)
	}
	defer rc.Close()
	body, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read: %+v", err)
	}
	if string(body) != "a1" {
		t.Errorf("episode must play the real file content, got %q", string(body))
	}
}

// containsName 判断列表是否包含指定名称。
func containsName(objs []model.Obj, name string) bool {
	for _, o := range objs {
		if o.GetName() == name {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: 运行确认失败**

```bash
cd /Users/varg247/store/work-store/backend/openlist && /Library/Go/sdk/go1.25.4/bin/go test ./drivers/emby_wrapper/ -run 'TestTVShow|TestGetAndLinkSeasonEpisode|TestGetVirtualEpisodeNotFound|TestEndToEndTVShowThroughFS' -count=1
```

Expected: FAIL（tvShowAncestor/buildTVIndex/withTVShowNFOs/resolveEpisodePath/newVirtualEpisode 未定义或 TV 分支未生效）

- [ ] **Step 3: 实现 `drivers/emby_wrapper/folder.go`** —— 追加：

```go
// virtualEpisode 虚拟剧集对象：GetName 返回虚拟名（如 A-S02E01.mp4），
// GetPath 返回下游真实路径，Link 无需拦截即可转发真实文件。
// 不实现 ObjUnwrap：解包会泄露下游真实路径。
type virtualEpisode struct {
	model.Obj
	name string
	path string
}

func (v *virtualEpisode) GetName() string { return v.name }
func (v *virtualEpisode) GetPath() string { return v.path }

func newVirtualEpisode(real model.Obj, name, path string) model.Obj {
	return &virtualEpisode{Obj: real, name: name, path: path}
}
```

- [ ] **Step 4: 实现 `drivers/emby_wrapper/setting.go`** —— 追加（import 增加 `stdpath`）：

```go
// tvShowInfo 返回 dirPath 是否为电视剧文件夹及剧名（自定义剧名为空时回退文件夹名）。
// 本地生效，不继承（子文件夹是否为电视剧由自身标记决定）。
func (d *EmbyWrapper) tvShowInfo(dirPath string) (string, bool, error) {
	item, err := GetEmbyDirSetting(d.ID, dirPath)
	if err != nil {
		return "", false, err
	}
	if item == nil || !item.TvShow {
		return "", false, nil
	}
	name := strings.TrimSpace(item.TvShowName)
	if name == "" {
		name = stdpath.Base(dirPath)
	}
	return name, true, nil
}

// isTVDir 判断 dirPath 是否被标记为电视剧（本地标记，不继承）。
func (d *EmbyWrapper) isTVDir(dirPath string) (bool, error) {
	item, err := GetEmbyDirSetting(d.ID, dirPath)
	if err != nil {
		return false, err
	}
	return item != nil && item.TvShow, nil
}

// tvShowAncestor 返回 dirPath 最近的电视剧祖先（含自身）：根路径 + 剧名。
// 任一目录的 List/Get 先经此判断走 TV 分支还是影片分支。
func (d *EmbyWrapper) tvShowAncestor(dirPath string) (string, string, bool, error) {
	dirPath = utils.FixAndCleanPath(dirPath)
	for {
		if name, ok, err := d.tvShowInfo(dirPath); err != nil {
			return "", "", false, err
		} else if ok {
			return dirPath, name, true, nil
		}
		if utils.PathEqual(dirPath, "/") {
			break
		}
		dirPath = stdpath.Dir(dirPath)
	}
	return "", "", false, nil
}
```

- [ ] **Step 5: 实现 `drivers/emby_wrapper/episode.go`** —— 追加（文件末尾；import 增加 `"context"`、`"sort"`、`"github.com/OpenListTeam/OpenList/v4/internal/driver"`、`"github.com/OpenListTeam/OpenList/v4/internal/op"`、`"github.com/OpenListTeam/OpenList/v4/pkg/utils"`）：

```go
// tvFile 收集到的视频：真实对象 + 规范路径（wrapper 命名空间）。
type tvFile struct {
	obj  model.Obj
	path string
}

// buildTVIndex 构建整个剧集树的索引：列根目录 → 直接子文件夹（跳过自身标记为
// TV 的，独立成剧）按创建时间+名称排序分配连续季号（根目录存在直接视频时从第 2 季
// 起，否则从第 1 季起）→ 各季递归收集视频 → 季内按创建时间+名称编号。
// 一次构建，List 展示与 Get 反查共用。
func (d *EmbyWrapper) buildTVIndex(ctx context.Context, rootPath string) (*tvIndex, error) {
	rootPath = utils.FixAndCleanPath(rootPath)
	idx := &tvIndex{
		root:      rootPath,
		byVirtual: map[string]model.Obj{},
		titles:    map[string]string{},
		names:     map[string]string{},
		nfoBases:  map[string]string{},
		byReal:    map[string]string{},
		seasonNo:  map[string]int{},
	}
	remoteStorage, remoteActualPath, err := d.remote()
	if err != nil {
		return nil, err
	}
	objs, err := op.List(ctx, remoteStorage, stdpath.Join(remoteActualPath, rootPath), model.ListArgs{})
	if err != nil {
		return nil, err
	}
	var rootVideos []model.Obj
	var seasonDirs []model.Obj
	for _, o := range objs {
		if o.IsDir() {
			isTV, err := d.isTVDir(stdpath.Join(rootPath, o.GetName()))
			if err != nil {
				return nil, err
			}
			if !isTV {
				seasonDirs = append(seasonDirs, o)
			}
			continue
		}
		if _, ok := d.supportSuffix[utils.Ext(o.GetName())]; ok {
			rootVideos = append(rootVideos, o)
		}
	}
	// 根目录直接视频 = 第 1 季
	sort.SliceStable(rootVideos, func(i, j int) bool { return byCreateTimeName(rootVideos[i], rootVideos[j]) })
	ep := 0
	for _, o := range rootVideos {
		canonical := stdpath.Join(rootPath, o.GetName())
		if isNumberedEpisode(o.GetName()) {
			idx.addEpisode(o, canonical, o.GetName())
			continue
		}
		ep++
		idx.addEpisode(o, canonical, episodeVirtualName(o.GetName(), 1, ep))
	}
	// 直接子文件夹：创建时间+名称排序 → 连续季号
	sort.SliceStable(seasonDirs, func(i, j int) bool { return byCreateTimeName(seasonDirs[i], seasonDirs[j]) })
	seasonBase := 1
	if len(rootVideos) > 0 {
		seasonBase = 2
	}
	for i, dir := range seasonDirs {
		dirPath := stdpath.Join(rootPath, dir.GetName())
		idx.seasonNo[dirPath] = seasonBase + i
		if err := d.collectSeasonVideos(ctx, idx, remoteStorage, remoteActualPath, dirPath, seasonBase+i); err != nil {
			return nil, err
		}
	}
	return idx, nil
}

// collectSeasonVideos 递归收集季文件夹下所有视频并登记进索引，季内按创建时间+名称编号。
func (d *EmbyWrapper) collectSeasonVideos(ctx context.Context, idx *tvIndex, remoteStorage driver.Driver, remoteActualPath, dirPath string, seasonNo int) error {
	files, err := d.gatherVideos(ctx, remoteStorage, remoteActualPath, dirPath)
	if err != nil {
		return err
	}
	sort.SliceStable(files, func(i, j int) bool { return byCreateTimeName(files[i].obj, files[j].obj) })
	ep := 0
	for _, f := range files {
		if isNumberedEpisode(f.obj.GetName()) {
			idx.addEpisode(f.obj, f.path, f.obj.GetName())
			continue
		}
		ep++
		idx.addEpisode(f.obj, f.path, episodeVirtualName(f.obj.GetName(), seasonNo, ep))
	}
	return nil
}

// gatherVideos 递归收集 dirPath 下所有视频（跳过自身标记为 TV 的子文件夹）。
func (d *EmbyWrapper) gatherVideos(ctx context.Context, remoteStorage driver.Driver, remoteActualPath, dirPath string) ([]tvFile, error) {
	objs, err := op.List(ctx, remoteStorage, stdpath.Join(remoteActualPath, dirPath), model.ListArgs{})
	if err != nil {
		return nil, err
	}
	var out []tvFile
	for _, o := range objs {
		if o.IsDir() {
			child := stdpath.Join(dirPath, o.GetName())
			isTV, err := d.isTVDir(child)
			if err != nil {
				return nil, err
			}
			if isTV {
				continue
			}
			sub, err := d.gatherVideos(ctx, remoteStorage, remoteActualPath, child)
			if err != nil {
				return nil, err
			}
			out = append(out, sub...)
			continue
		}
		if _, ok := d.supportSuffix[utils.Ext(o.GetName())]; ok {
			out = append(out, tvFile{obj: o, path: stdpath.Join(dirPath, o.GetName())})
		}
	}
	return out, nil
}

// withTVShowNFOs TV 模式展示：当前目录的直接视频按索引映射为虚拟剧集（原地），
// 生成剧集 nfo（episodedetails：title=原文件名、actors、无 plot）；
// 剧集根目录追加 tvshow.nfo；直接子文件夹（季）追加 season.nfo。
// 真实同名 nfo/文件优先：同目录已存在同名文件或 nfo 时跳过虚拟生成。
func (d *EmbyWrapper) withTVShowNFOs(ctx context.Context, dir model.Obj, rootPath, showName string, objs []model.Obj) []model.Obj {
	dirPath := dir.GetPath()
	idx, err := d.buildTVIndex(ctx, rootPath)
	if err != nil {
		utils.Log.Warnf("emby wrapper: build tv index %s: %+v", rootPath, err)
		return objs
	}
	setting, err := d.resolveSetting(dirPath)
	if err != nil {
		utils.Log.Warnf("emby wrapper: resolve setting %s: %+v", dirPath, err)
		return objs
	}
	if setting == nil {
		setting = &model.EmbyDirSetting{}
	}
	realNFO := map[string]bool{}
	realFiles := map[string]bool{}
	for _, o := range objs {
		if o.IsDir() {
			continue
		}
		name := o.GetName()
		if strings.EqualFold(utils.Ext(name), "nfo") {
			realNFO[strings.ToLower(nfoBaseName(name)+".nfo")] = true
			continue
		}
		realFiles[strings.ToLower(name)] = true
	}
	out := make([]model.Obj, 0, len(objs)+2)
	addedNFO := map[string]bool{}
	for _, o := range objs {
		if o.IsDir() {
			out = append(out, o)
			continue
		}
		name := o.GetName()
		if strings.EqualFold(utils.Ext(name), "nfo") {
			out = append(out, o)
			continue
		}
		if _, ok := d.supportSuffix[utils.Ext(name)]; !ok {
			out = append(out, o)
			continue
		}
		epName, ok := idx.episodeName(o)
		if !ok {
			out = append(out, o)
			continue
		}
		if epName != name && realFiles[strings.ToLower(epName)] {
			// 虚拟名已被真实文件占用：原样展示，不生成虚拟剧集
			out = append(out, o)
			continue
		}
		out = append(out, newVirtualEpisode(o, epName, o.GetPath()))
		nfoName := strings.TrimSuffix(epName, stdpath.Ext(epName)) + ".nfo"
		if realNFO[strings.ToLower(nfoName)] || addedNFO[nfoName] {
			continue
		}
		content, err := buildEpisodeNFO(idx.titles[strings.ToLower(epName)], setting)
		if err != nil {
			utils.Log.Warnf("emby wrapper: build episode nfo %s: %+v", nfoName, err)
			continue
		}
		addedNFO[nfoName] = true
		out = append(out, &virtualNFO{
			Object: model.Object{
				Name:     nfoName,
				Size:     int64(len(content)),
				Modified: o.ModTime(),
				Path:     stdpath.Join(dirPath, nfoName),
				ID:       "vnfo-" + nfoName,
			},
			content: content,
		})
	}
	// tvshow.nfo：仅剧集根目录（真实同名 nfo 优先）
	if utils.PathEqual(dirPath, rootPath) && !realNFO["tvshow.nfo"] {
		content, err := buildTVShowNFO(showName, setting.Plot, setting)
		if err == nil {
			modified := dir.ModTime()
			if idx.last != nil {
				modified = idx.last.ModTime()
			}
			out = append(out, &virtualNFO{
				Object: model.Object{
					Name:     "tvshow.nfo",
					Size:     int64(len(content)),
					Modified: modified,
					Path:     stdpath.Join(dirPath, "tvshow.nfo"),
					ID:       "vnfo-tvshow.nfo",
				},
				content: content,
			})
		}
	}
	// season.nfo：直接子文件夹（季），真实同名 nfo 优先
	if !utils.PathEqual(dirPath, rootPath) && utils.PathEqual(stdpath.Dir(dirPath), rootPath) {
		if seasonNo, ok := idx.seasonNo[dirPath]; ok && !realNFO["season.nfo"] {
			content := buildSeasonNFO(seasonNo, stdpath.Base(dirPath))
			modified := dir.ModTime()
			if idx.last != nil {
				modified = idx.last.ModTime()
			}
			out = append(out, &virtualNFO{
				Object: model.Object{
					Name:     "season.nfo",
					Size:     int64(len(content)),
					Modified: modified,
					Path:     stdpath.Join(dirPath, "season.nfo"),
					ID:       "vnfo-season.nfo",
				},
				content: content,
			})
		}
	}
	return out
}
```

- [ ] **Step 6: 实现 `drivers/emby_wrapper/driver.go`** —— 四处修改：

**(a) List** 改为按最近电视剧祖先路由（import 增加 `"time"`）：

```go
	objs = d.decorate(dir.GetPath(), objs)
	if rootPath, showName, ok, err := d.tvShowAncestor(dir.GetPath()); err != nil {
		utils.Log.Warnf("emby wrapper: tv show ancestor %s: %+v", dir.GetPath(), err)
	} else if ok {
		return d.withTVShowNFOs(ctx, dir, rootPath, showName, objs), nil
	}
	return d.withVirtualNFOs(dir.GetPath(), objs), nil
```

**(b) Get** 下游未命中时反查虚拟剧集：

```go
	obj, err := op.Get(ctx, remoteStorage, stdpath.Join(remoteActualPath, path))
	if err != nil {
		if ep, ok, e2 := d.resolveEpisodePath(ctx, path); e2 != nil {
			return nil, e2
		} else if ok {
			return ep, nil
		}
		return nil, err
	}
```

**(c) 新增 resolveEpisodePath 与 newVirtualNFO**（追加到 virtualNFOForPath 之后）：

```go
// resolveEpisodePath 在父目录处于某部电视剧内时按虚拟名反查真实文件。
// 返回 (包装对象, true, nil)：命中虚拟剧集；(nil, false, nil)：非 TV 树或未命中。
func (d *EmbyWrapper) resolveEpisodePath(ctx context.Context, path string) (model.Obj, bool, error) {
	parentDir := stdpath.Dir(path)
	rootPath, _, ok, err := d.tvShowAncestor(parentDir)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, nil
	}
	idx, err := d.buildTVIndex(ctx, rootPath)
	if err != nil {
		return nil, false, err
	}
	real := idx.resolve(stdpath.Base(path))
	if real == nil {
		return nil, false, nil
	}
	return newVirtualEpisode(real, stdpath.Base(path), stdpath.Join(parentDir, real.GetName())), true, nil
}

// newVirtualNFO 构造虚拟 nfo 对象。
func (d *EmbyWrapper) newVirtualNFO(path string, content []byte, modified time.Time) model.Obj {
	return &virtualNFO{
		Object: model.Object{
			Name:     stdpath.Base(path),
			Size:     int64(len(content)),
			Modified: modified,
			Path:     path,
			ID:       "vnfo-" + stdpath.Base(path),
		},
		content: content,
	}
}
```

**(d) virtualNFOForPath 整体替换**（TV 分支：tvshow.nfo / season.nfo / 剧集 nfo；真实 nfo 优先扫描提前到公共位置）：

```go
// virtualNFOForPath 尝试为 .nfo 路径构建虚拟对象。
// 返回 (obj, true, nil)：命中虚拟 nfo；(nil, false, nil)：应转发下游（无设置/无匹配影片/存在真实 nfo）。
func (d *EmbyWrapper) virtualNFOForPath(ctx context.Context, path string) (model.Obj, bool, error) {
	parentDir := stdpath.Dir(path)
	base := strings.TrimSuffix(stdpath.Base(path), ".nfo")
	rootPath, showName, isTV, err := d.tvShowAncestor(parentDir)
	if err != nil {
		return nil, false, err
	}
	setting, err := d.resolveSetting(parentDir)
	if err != nil {
		return nil, false, err
	}
	if setting == nil && !isTV {
		return nil, false, nil
	}
	if setting == nil {
		setting = &model.EmbyDirSetting{}
	}
	remoteStorage, remoteActualPath, err := d.remote()
	if err != nil {
		return nil, false, err
	}
	objs, err := op.List(ctx, remoteStorage, stdpath.Join(remoteActualPath, parentDir), model.ListArgs{})
	if err != nil {
		return nil, false, err
	}
	// 真实 nfo 优先：下游存在同名真实 nfo 时交给下游 Get（含 tvshow.nfo/season.nfo）
	for _, o := range objs {
		if o.IsDir() {
			continue
		}
		if strings.EqualFold(utils.Ext(o.GetName()), "nfo") && strings.EqualFold(nfoBaseName(o.GetName()), base) {
			return nil, false, nil
		}
	}
	// TV 模式分支
	if isTV {
		idx, err := d.buildTVIndex(ctx, rootPath)
		if err != nil {
			return nil, false, err
		}
		// tvshow.nfo：仅剧集根目录
		if utils.PathEqual(parentDir, rootPath) && strings.EqualFold(base, "tvshow") {
			content, err := buildTVShowNFO(showName, setting.Plot, setting)
			if err != nil {
				return nil, false, err
			}
			modified := time.Time{}
			if idx.last != nil {
				modified = idx.last.ModTime()
			}
			return d.newVirtualNFO(path, content, modified), true, nil
		}
		// season.nfo：直接子文件夹（季）
		if !utils.PathEqual(parentDir, rootPath) && utils.PathEqual(stdpath.Dir(parentDir), rootPath) && strings.EqualFold(base, "season") {
			if seasonNo, ok := idx.seasonNo[parentDir]; ok {
				modified := time.Time{}
				if idx.last != nil {
					modified = idx.last.ModTime()
				}
				return d.newVirtualNFO(path, buildSeasonNFO(seasonNo, stdpath.Base(parentDir)), modified), true, nil
			}
		}
		// 剧集 nfo：虚拟名匹配
		epName, ok := idx.nfoBases[strings.ToLower(base)]
		if !ok {
			return nil, false, nil
		}
		real := idx.resolve(epName)
		content, err := buildEpisodeNFO(idx.titles[strings.ToLower(epName)], setting)
		if err != nil {
			return nil, false, err
		}
		return d.newVirtualNFO(path, content, real.ModTime()), true, nil
	}
	// 影片模式（原有逻辑）：basename 匹配真实影片文件
	var movieObj model.Obj
	for _, o := range objs {
		if o.IsDir() {
			continue
		}
		if nfoBaseName(o.GetName()) == base {
			if _, ok := d.supportSuffix[utils.Ext(o.GetName())]; ok {
				movieObj = o
			}
		}
	}
	if movieObj == nil {
		return nil, false, nil
	}
	content, err := buildNFOContent(base, movieObj.GetName(), setting)
	if err != nil {
		return nil, false, err
	}
	return d.newVirtualNFO(path, content, movieObj.ModTime()), true, nil
}
```

- [ ] **Step 7: 运行确认通过**

```bash
cd /Users/varg247/store/work-store/backend/openlist && /Library/Go/sdk/go1.25.4/bin/go test ./drivers/emby_wrapper/ -count=1
```

Expected: PASS（新增 TV 用例 + 既有 nfo/get/name_actor/plot/e2e 用例全部通过）

- [ ] **Step 8: 提交**

```bash
cd /Users/varg247/store/work-store/backend/openlist && git add drivers/emby_wrapper/folder.go drivers/emby_wrapper/setting.go drivers/emby_wrapper/episode.go drivers/emby_wrapper/driver.go drivers/emby_wrapper/tv_show_test.go drivers/emby_wrapper/e2e_test.go && git commit -m "feat(emby_wrapper): serve tv-show trees as seasons with numbered episodes and season nfo"
```

---

### Task 5: addition 展示 + MkdirConfig + 全量验证

**Files:**
- Modify: `drivers/emby_wrapper/folder.go`（wrapObj 签名扩展）
- Modify: `drivers/emby_wrapper/driver.go`（decorate / Get / MkdirConfig）
- Test: `drivers/emby_wrapper/driver_test.go`、`drivers/emby_wrapper/get_test.go`

**Interfaces:**
- Consumes: Task 1 的 `FolderAddition` 新字段、`EmbyDirSetting.TvShow/TvShowName`
- Produces: `wrapObj(obj model.Obj, path, actors, plot, tvShowName string, useNameAsActor bool, appendFileNameToPlot *bool, tvShow bool, folder bool) model.Obj`（decorate 与 Get 调用点同步更新）；MkdirConfig 六字段（+tv_show、tv_show_name）

- [ ] **Step 1: 更新 `folder.go` wrapObj**

```go
func wrapObj(obj model.Obj, path, actors, plot, tvShowName string, useNameAsActor bool, appendFileNameToPlot *bool, tvShow bool, folder bool) model.Obj {
	wrapped := &wrappedObj{Obj: obj, path: path}
	if !folder {
		return wrapped
	}
	use := useNameAsActor
	return &embyFolder{Obj: wrapped, addition: FolderAddition{
		Actors:               actors,
		UseNameAsActor:       &use,
		Plot:                 plot,
		AppendFileNameToPlot: appendFileNameToPlot,
		TvShow:               &tvShow,
		TvShowName:           tvShowName,
	}}
}
```

- [ ] **Step 2: 更新 `driver.go` decorate 与 Get**

`decorate`：

```go
	out := make([]model.Obj, len(objs))
	for i, o := range objs {
		p := stdpath.Join(dirPath, o.GetName())
		s, ok := settings[p]
		actors, use, plot, tvShowName, tvShow := "", false, "", "", false
		var appendFlag *bool
		if ok {
			actors, use, plot = s.Actors, s.UseNameAsActor, s.Plot
			appendFlag = s.AppendFileNameToPlot
			tvShowName, tvShow = s.TvShowName, s.TvShow
		}
		out[i] = wrapObj(o, p, actors, plot, tvShowName, use, appendFlag, tvShow, o.IsDir())
	}
	return out
```

`Get` 的目录设置读取段：

```go
	actors, use, plot, tvShowName, tvShow := "", false, "", "", false
	var appendFlag *bool
	if obj.IsDir() {
		if item, e := GetEmbyDirSetting(d.ID, path); e != nil {
			utils.Log.Warnf("emby wrapper: get dir setting %s: %+v", path, e)
		} else if item != nil {
			actors, use, plot = item.Actors, item.UseNameAsActor, item.Plot
			appendFlag = item.AppendFileNameToPlot
			tvShowName, tvShow = item.TvShowName, item.TvShow
		}
	}
	return wrapObj(obj, path, actors, plot, tvShowName, use, appendFlag, tvShow, obj.IsDir()), nil
```

- [ ] **Step 3: 更新 `MkdirConfig`**（追加两项）

```go
		{
			Name:    "tv_show",
			Type:    conf.TypeBool,
			Default: "false",
			Help:    "标记该文件夹为电视剧：根目录直接文件为第 1 季，直接子文件夹按创建时间+名称排序分配季号（保留原名并生成 season.nfo 供 Emby 识别）；季内文件按创建时间编号为 S{季}E{集}.mp4（纯编号）；生成剧集 nfo（保留演员、无简介）与 tvshow.nfo（剧名/简介）；本地生效不继承",
		},
		{
			Name:    "tv_show_name",
			Type:    conf.TypeString,
			Default: "",
			Help:    "电视剧名称，写入 tvshow.nfo 的 title；为空时使用文件夹名",
		},
```

- [ ] **Step 4: 追加展示测试** `drivers/emby_wrapper/driver_test.go`：

```go
// TestFolderAdditionExposesTVShow：List 的文件夹 addition 暴露 tv_show 与 tv_show_name。
func TestFolderAdditionExposesTVShow(t *testing.T) {
	d := setup(t)
	if err := d.Rename(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, `{"tv_show":true,"tv_show_name":"测试剧"}`); err != nil {
		t.Fatalf("config: %+v", err)
	}
	root, err := d.List(context.Background(), &model.Object{Name: "Root", Path: "/", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list root: %+v", err)
	}
	for _, o := range root {
		if o.GetName() != "Movies" {
			continue
		}
		add, ok := o.(model.ObjAdditional)
		if !ok {
			t.Fatal("folder must expose additional")
		}
		fa, ok := add.GetAddition().(emby_wrapper.FolderAddition)
		if !ok {
			t.Fatalf("unexpected addition type %T", add.GetAddition())
		}
		if fa.TvShow == nil || !*fa.TvShow {
			t.Error("addition must expose tv_show=true")
		}
		if fa.TvShowName != "测试剧" {
			t.Errorf("expected tv_show_name=测试剧, got %q", fa.TvShowName)
		}
		return
	}
	t.Fatal("Movies folder not found")
}
```

追加到 `drivers/emby_wrapper/get_test.go`：

```go
// TestGetFolderExposesTVShow：Get 的文件夹 addition 暴露 tv_show 与 tv_show_name。
func TestGetFolderExposesTVShow(t *testing.T) {
	d := setup(t)
	if err := d.Rename(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, `{"tv_show":true,"tv_show_name":"测试剧"}`); err != nil {
		t.Fatalf("config: %+v", err)
	}
	obj, err := d.Get(context.Background(), "/Movies")
	if err != nil {
		t.Fatalf("get folder: %+v", err)
	}
	add, ok := obj.(model.ObjAdditional)
	if !ok {
		t.Fatal("folder must expose additional")
	}
	fa, ok := add.GetAddition().(emby_wrapper.FolderAddition)
	if !ok {
		t.Fatalf("unexpected addition type %T", add.GetAddition())
	}
	if fa.TvShow == nil || !*fa.TvShow {
		t.Error("Get must expose tv_show=true")
	}
	if fa.TvShowName != "测试剧" {
		t.Errorf("expected tv_show_name=测试剧, got %q", fa.TvShowName)
	}
}
```

- [ ] **Step 5: 全量验证**

```bash
cd /Users/varg247/store/work-store/backend/openlist && /Library/Go/sdk/go1.25.4/bin/go build ./drivers/emby_wrapper ./drivers/virtual_file ./internal/op ./internal/fs ./internal/db ./internal/model && /Library/Go/sdk/go1.25.4/bin/go vet ./drivers/emby_wrapper ./drivers/virtual_file && /Library/Go/sdk/go1.25.4/bin/go test ./drivers/emby_wrapper/ ./drivers/virtual_file/ ./drivers/cache/ -count=1
```

Expected: 全部 PASS（`go build ./...` 全量会因环境缺失 fuse.h 失败，与本次改动无关）

- [ ] **Step 6: 提交**

```bash
cd /Users/varg247/store/work-store/backend/openlist && git add drivers/emby_wrapper/folder.go drivers/emby_wrapper/driver.go drivers/emby_wrapper/driver_test.go drivers/emby_wrapper/get_test.go && git commit -m "feat(emby_wrapper): expose tv-show settings in mkdir form and folder addition"
```

---

## Self-Review

**1. Spec coverage:**
- tv_show/tv_show_name 设置（本地不继承）：Task 1 字段 + Task 4 tvShowInfo/isTVDir/tvShowAncestor（只查本地行）✓
- 创建时间+名称联合排序（文件与文件夹）：Task 2 byCreateTimeName + Task 4 buildTVIndex/collectSeasonVideos（CreateTime 回退 ModTime 由 model.Object 内置）✓
- 根目录直接文件 = 第 1 季：Task 4 buildTVIndex（rootVideos → season 1，存在时子文件夹季号从 2 起）✓
- 子文件夹 = 季（保留原名、不解析名字、按创建时间+名称分配连续季号）：Task 4 buildTVIndex（seasonDirs 排序 → seasonNo 连续分配）✓
- 季内递归收集 + 原地展示 + 跳过嵌套 TV：Task 4 gatherVideos / collectSeasonVideos ✓
- 跳过已编号文件（不消耗序号）：Task 4（isNumberedEpisode 分支）✓
- 剧集 nfo（episodedetails、title=原名、actors、无 plot）：Task 3 buildEpisodeNFO + Task 4 withTVShowNFOs ✓
- tvshow.nfo（剧名回退文件夹名、plot、actors，仅根目录）：Task 3 buildTVShowNFO + Task 4 ✓
- season.nfo（seasonnumber + seasonname，仅直接子文件夹；真实同名 nfo 优先）：Task 3 buildSeasonNFO + Task 4（List 与 Get 两路径 + TestTVShowSeasonNFOContent / TestTVShowRealSeasonNFOFileWins）✓
- 不物理改名、Get 唯一映射点、Link 还原真实文件：Task 4 virtualEpisode + resolveEpisodePath + TestGetAndLinkSeasonEpisode + e2e ✓
- 真实同名文件/nfo 优先：Task 4 realFiles/realNFO 检查 + TestTVShowRealSeasonNFOFileWins ✓
- 嵌套电视剧独立成剧：Task 4 gatherVideos/isTVDir + TestTVShowNestedTVSkipped ✓
- 展示层 addition + MkdirConfig：Task 5 ✓
- `-S{NN}E{MM}` 格式（Emby 可识别）：Spec 9，Task 2 episodeVirtualName 实现 ✓

**2. Placeholder scan:** 无 TBD；所有代码与测试完整给出；sed 命令含回退说明（编译报错即未匹配，手动补参）；`newTVIndexForTest` 为 Task 2 测试辅助（Task 4 的 buildTVIndex 是驱动方法，测试中不复用）。

**3. Type consistency:**
- `UpsertEmbyDirSetting(storageID, dirPath, actors, plot, tvShowName string, useNameAsActor, appendFileNameToPlot, tvShow *bool)`：Task 1 定义，driver.go Rename 与全部测试调用点一致（8 参）✓
- `episodeVirtualName(fileName string, seasonNo, epNo int) string`：Task 2 定义；Task 4 三处调用（根季 1 / 季内）一致 ✓
- `tvIndex.addEpisode(real model.Obj, canonicalPath, virtualName string)`：Task 2 定义；Task 4 buildTVIndex/collectSeasonVideos 传规范路径（`stdpath.Join` 构造，与 decorate 的路径一致）✓
- `tvShowAncestor(dirPath string) (string, string, bool, error)`：Task 4 定义；List/Get/virtualNFOForPath/resolveEpisodePath 调用一致 ✓
- `buildNFOWithRoot(root, title, plot string, setting) ([]byte, error)` / `buildEpisodeNFO` / `buildTVShowNFO`：Task 3 定义，Task 4 调用 ✓；`buildSeasonNFO(seasonNo int, name string) []byte` 无 error 返回，调用方不检查 err ✓
- `wrapObj` 9 参（Task 5）：decorate 与 Get 两处调用点同步更新；Task 1-4 期间保持旧签名不受影响 ✓
- `newVirtualEpisode(real model.Obj, name, path string) model.Obj`：Task 4 定义；withTVShowNFOs（name=epName、path=o.GetPath() 真实路径）与 resolveEpisodePath（name=请求名、path=parentDir+真实名）一致 ✓
- `virtualNFOForPath` 重构后保留原语义：非 TV 树行为与原来一致（真实 nfo 扫描提前到公共位置，逻辑等价；`setting == nil && !isTV` 早退保留）✓
- e2e 测试沿用 fs.Link 返回值 `(link, _, err)` 与 `http_range.Range{Length: -1}` 既有模式；`containsName` 辅助在同一文件定义 ✓
- 测试时间控制：`writeEpisodeFile`/`writeDirOrdered`（15ms sleep）保证 local 驱动 birth time 排序确定（Global Constraints 已注明原因）✓
