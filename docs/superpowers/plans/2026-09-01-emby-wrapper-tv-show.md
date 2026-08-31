# EmbyWrapper 电视剧模式（TV Show）实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 文件夹可标记为电视剧（`tv_show` + 自定义剧名 `tv_show_name`）：直接文件按 ModTime 旧→新自动编号为剧集（虚拟名 `原基础名-S01E01.mp4` 格式），生成剧集 nfo（`<episodedetails>`：title=原文件名、actors、无 plot）与 `tvshow.nfo`（`<tvshow>`：剧名+简介+actors）；文件不物理改名，下游 Get/Link 还原真实路径。

**Architecture:** 沿用现有虚拟 nfo 模式：`EmbyDirSetting` 新增 `TvShow bool` 与 `TvShowName string`（本地生效不继承）；新增 `episode.go` 负责编号/排序/虚拟名映射（一次构建，List 展示与 Get 反查共用）；`virtual_file.RenderNFO` 根元素参数化以支持 `tvshow`/`episodedetails`；`Get` 是虚拟→真实路径的唯一映射点（`virtualEpisode.GetPath()` 返回真实路径，Link 现有代码零改动）；`resolveSetting` 在 TV 边界阻断 plot/append 维度向子目录继承。

**Tech Stack:** Go 1.25.4（`/Library/Go/sdk/go1.25.4/bin/go`）、gorm、复用 `virtual_file.RenderNFO`（CDATA）与既有测试设施（`setup(t)`/`writeDownstreamFile`/`readNFOLink`/`names`）。

**Spec:** 用户需求（2026-09-01 逐项确认）：
1. 文件夹新增设置 `tv_show`（bool，标记为电视剧）与 `tv_show_name`（string，自定义剧名，为空回退文件夹名）；**本地生效，不继承**
2. TV 模式只作用于**直接文件**（子文件夹不参与编号，原样展示）
3. 编号方式：**动态推导**——直接视频文件按 ModTime 旧→新排序，未编号文件虚拟命名为 `原基础名-S01E%02d+扩展名`（E01 起）；**已含 `SxxExx`/`NxNN` 编号的文件跳过**、保持原名（含其 nfo 生成）；同时间按文件名升序稳定排序
4. 每个剧集文件（含已编号的）生成虚拟剧集 nfo：根元素 `<episodedetails>`，title=原文件名去扩展名，actors=现有继承解析结果（`resolveSetting`），**无 plot**；`-cd` 多段不合并（一文件一集）
5. 生成虚拟 `tvshow.nfo`：根元素 `<tvshow>`，title=自定义剧名（空→文件夹名），plot=该目录解析出的 plot（剧集介绍），actors 同
6. **不物理改名**：虚拟名只存在于展示层（List/Get 返回的对象名）；`virtualEpisode.GetPath()` 返回真实路径，Link 直接转发真实文件（`op.Link` 前必经 Get，Get 是唯一映射点）；真实同名文件/nfo 优先（下游已存在同名文件或 nfo 时跳过虚拟生成）
7. `tv_show` 标记**向下阻断 plot/append 维度继承**（防止剧集介绍泄漏进子文件夹影片 nfo）；演员维度不阻断
8. 剧集命名格式用 `-S01E01`（Emby 官方文档+解析器确认 `-E01` 不被识别；`anything_s01e02` 是受支持模式）

## Global Constraints

- Go 工具链一律用 `/Library/Go/sdk/go1.25.4/bin/go`
- TDD：先失败测试再实现；每个任务独立提交
- 提交信息沿用仓库风格：`feat(emby_wrapper): ...`
- `UpsertEmbyDirSetting` 签名变化影响全部既有调用点（db_test.go / setting_test.go 共约 40 处，Task 1 用 sed 批量更新并编译验证）
- 全量 `go build ./...` 会因环境缺失 fuse.h 失败（与本改动无关）；验证使用受控包集：`drivers/emby_wrapper`、`drivers/virtual_file`、`internal/op`、`internal/fs`、`internal/db`、`internal/model`
- 不修改 `text_types` 默认值
- 既有测试语义不回归：`buildNFOContent` 重构为内部调用 `buildNFOWithRoot("movie", ...)`，行为保持不变

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
- Produces: `EmbyDirSetting{..., TvShow bool, TvShowName string}`、`UpsertEmbyDirSetting(storageID uint, dirPath, actors, plot, tvShowName string, useNameAsActor, appendFileNameToPlot *bool, tvShow *bool) error`（tvShowName 空串清除；tvShow nil 保持原值）、`FolderAddition{..., TvShow *bool, TvShowName string}` —— Task 2/4/6 依赖

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

### Task 2: episode.go 剧集编号与映射（纯函数）

**Files:**
- Create: `drivers/emby_wrapper/episode.go`
- Test: `drivers/emby_wrapper/episode_internal_test.go`

**Interfaces:**
- Consumes: 无（纯函数；`utils.Ext` 返回无点小写扩展名，与 `Init` 的 supportSuffix 键一致）
- Produces: `isNumberedEpisode(fileName string) bool`、`episodeVirtualName(fileName string, idx int) string`、`episodeIndex{byVirtual, titles, names, nfoBases, byReal, last}`、`buildEpisodeIndex(files []model.Obj, supportSuffix map[string]struct{}) *episodeIndex` —— Task 4 依赖（List 展示与 Get 反查共用）

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
		idx      int
		want     string
	}{
		{"AAA.mkv", 1, "AAA-S01E01.mkv"},
		{"B.mp4", 2, "B-S01E02.mp4"},
		{"C", 12, "C-S01E12"},
		{"D.mkv", 100, "D-S01E100.mkv"},
	}
	for _, c := range cases {
		if got := episodeVirtualName(c.fileName, c.idx); got != c.want {
			t.Errorf("episodeVirtualName(%q, %d) = %q, want %q", c.fileName, c.idx, got, c.want)
		}
	}
}

func TestBuildEpisodeIndex(t *testing.T) {
	support := map[string]struct{}{"mp4": {}, "mkv": {}}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	obj := func(name string, ts time.Time) model.Obj {
		return &model.Object{Name: name, Path: "/dir/" + name, IsFolder: false, Modified: ts}
	}
	idx := buildEpisodeIndex([]model.Obj{
		obj("B.mp4", base.Add(2*time.Hour)),
		obj("A.mp4", base),
		obj("Skip.S01E03.mkv", base.Add(3*time.Hour)),
		obj("readme.txt", base.Add(4*time.Hour)),
		obj("C.mp4", base.Add(1*time.Hour)),
	}, support)
	// 时间旧→新：A→E01、C→E02、B→E03；已编号文件保持原名
	if got := idx.resolve("A-S01E01.mp4"); got == nil || got.GetName() != "A.mp4" {
		t.Errorf("A-S01E01.mp4 must map to A.mp4, got %v", got)
	}
	if got := idx.resolve("C-S01E02.mp4"); got == nil || got.GetName() != "C.mp4" {
		t.Errorf("C-S01E02.mp4 must map to C.mp4, got %v", got)
	}
	if got := idx.resolve("B-S01E03.mp4"); got == nil || got.GetName() != "B.mp4" {
		t.Errorf("B-S01E03.mp4 must map to B.mp4, got %v", got)
	}
	if got := idx.resolve("Skip.S01E03.mkv"); got == nil || got.GetName() != "Skip.S01E03.mkv" {
		t.Errorf("numbered file must keep its name, got %v", got)
	}
	if got := idx.resolve("readme.txt"); got != nil {
		t.Errorf("non-video must not be an episode, got %v", got)
	}
	if got := idx.resolve("A.mp4"); got != nil {
		t.Errorf("original name must not resolve (virtual name replaces it), got %v", got)
	}
	if got := idx.titles["a-s01e01.mp4"]; got != "A" {
		t.Errorf("title of A episode must be A, got %q", got)
	}
	if got := idx.nfoBases["a-s01e01"]; got != "A-S01E01.mp4" {
		t.Errorf("nfo base must map to virtual name, got %q", got)
	}
	if idx.last == nil || idx.last.GetName() != "Skip.S01E03.mkv" {
		t.Errorf("last must be the newest video, got %v", idx.last)
	}
}

func TestBuildEpisodeIndexSameMtimeTieBreak(t *testing.T) {
	support := map[string]struct{}{"mp4": {}}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	obj := func(name string) model.Obj {
		return &model.Object{Name: name, Path: "/dir/" + name, Modified: base}
	}
	idx := buildEpisodeIndex([]model.Obj{
		obj("Z.mp4"),
		obj("A.mp4"),
	}, support)
	if got := idx.resolve("A-S01E01.mp4"); got == nil || got.GetName() != "A.mp4" {
		t.Errorf("same mtime must tie-break by name asc: A first, got %v", got)
	}
	if got := idx.resolve("Z-S01E02.mp4"); got == nil || got.GetName() != "Z.mp4" {
		t.Errorf("same mtime must tie-break by name asc: Z second, got %v", got)
	}
}
```

- [ ] **Step 2: 运行确认失败**

```bash
cd /Users/varg247/store/work-store/backend/openlist && /Library/Go/sdk/go1.25.4/bin/go test ./drivers/emby_wrapper/ -run 'TestIsNumberedEpisode|TestEpisodeVirtualName|TestBuildEpisodeIndex' -count=1
```

Expected: FAIL（编译错误：isNumberedEpisode 等未定义）

- [ ] **Step 3: 实现** `drivers/emby_wrapper/episode.go`

```go
package emby_wrapper

import (
	"fmt"
	stdpath "path"
	"regexp"
	"sort"
	"strings"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

// episodePattern 匹配已含 Emby 剧集编号的文件名（SxxExx 及其变体、NxNN）。
// 命中视为已编号，保持原名、跳过时间排序编号。
var episodePattern = regexp.MustCompile(`(?i)(?:^|[._ -])(?:s\d{1,4}[. _-]*e\d{1,3}|\d{1,4}x\d{1,3})(?:[._ -]|$)`)

// isNumberedEpisode 判断文件名是否已含剧集编号。
func isNumberedEpisode(fileName string) bool {
	return episodePattern.MatchString(fileName)
}

// episodeVirtualName 为未编号文件生成虚拟剧集名：原基础名-S01E%02d+原扩展名。
func episodeVirtualName(fileName string, idx int) string {
	ext := stdpath.Ext(fileName)
	return fmt.Sprintf("%s-S01E%02d%s", strings.TrimSuffix(fileName, ext), idx, ext)
}

// episodeIndex TV 文件夹的剧集映射：虚拟名 ↔ 真实对象。
// 一次构建，List 展示与 Get 反查共用。
type episodeIndex struct {
	byVirtual map[string]model.Obj // 小写虚拟名（含扩展名）→ 真实对象
	titles    map[string]string    // 小写虚拟名 → 集名（原文件名去扩展名）
	names     map[string]string    // 小写虚拟名 → 虚拟名（保留原样）
	nfoBases  map[string]string    // 小写虚拟名去扩展名 → 虚拟名
	byReal    map[string]string    // 真实路径 → 虚拟名
	last      model.Obj            // 排序后最新的视频对象（tvshow.nfo 时间戳用，无视频时为 nil）
}

// buildEpisodeIndex 将直接视频文件按 ModTime 旧→新编号为剧集：
// 已编号文件保持原名；未编号文件命名为 原基础名-S01E%02d+扩展名（E01 起）。
// 同时间按文件名升序稳定排序。
func buildEpisodeIndex(files []model.Obj, supportSuffix map[string]struct{}) *episodeIndex {
	var videos []model.Obj
	for _, o := range files {
		if o.IsDir() {
			continue
		}
		if _, ok := supportSuffix[utils.Ext(o.GetName())]; ok {
			videos = append(videos, o)
		}
	}
	sort.SliceStable(videos, func(i, j int) bool {
		if !videos[i].ModTime().Equal(videos[j].ModTime()) {
			return videos[i].ModTime().Before(videos[j].ModTime())
		}
		return videos[i].GetName() < videos[j].GetName()
	})
	idx := &episodeIndex{
		byVirtual: map[string]model.Obj{},
		titles:    map[string]string{},
		names:     map[string]string{},
		nfoBases:  map[string]string{},
		byReal:    map[string]string{},
	}
	n := 0
	for _, o := range videos {
		fileName := o.GetName()
		ext := stdpath.Ext(fileName)
		base := strings.TrimSuffix(fileName, ext)
		epName := fileName
		if !isNumberedEpisode(fileName) {
			n++
			epName = fmt.Sprintf("%s-S01E%02d%s", base, n, ext)
		}
		key := strings.ToLower(epName)
		idx.byVirtual[key] = o
		idx.titles[key] = base
		idx.names[key] = epName
		idx.nfoBases[strings.ToLower(strings.TrimSuffix(epName, ext))] = epName
		idx.byReal[o.GetPath()] = epName
		idx.last = o
	}
	return idx
}

// resolve 按虚拟名（含扩展名，大小写不敏感）反查真实对象；未命中返回 nil。
func (idx *episodeIndex) resolve(virtualName string) model.Obj {
	return idx.byVirtual[strings.ToLower(virtualName)]
}

// episodeName 返回真实对象对应的虚拟名。
func (idx *episodeIndex) episodeName(realObj model.Obj) (string, bool) {
	name, ok := idx.byReal[realObj.GetPath()]
	return name, ok
}
```

- [ ] **Step 4: 运行确认通过**

```bash
cd /Users/varg247/store/work-store/backend/openlist && /Library/Go/sdk/go1.25.4/bin/go test ./drivers/emby_wrapper/ -run 'TestIsNumberedEpisode|TestEpisodeVirtualName|TestBuildEpisodeIndex' -count=1
```

Expected: PASS

- [ ] **Step 5: 提交**

```bash
cd /Users/varg247/store/work-store/backend/openlist && git add drivers/emby_wrapper/episode.go drivers/emby_wrapper/episode_internal_test.go && git commit -m "feat(emby_wrapper): build episode index with time-ordered virtual names"
```

---

### Task 3: RenderNFO 根元素参数化 + 剧集/剧集级 nfo 构建

**Files:**
- Modify: `drivers/virtual_file/util.go`
- Modify: `drivers/emby_wrapper/nfo.go`
- Test: `drivers/virtual_file/render_nfo_test.go`、`drivers/emby_wrapper/nfo_internal_test.go`

**Interfaces:**
- Consumes: 现有 `virtual_file.Media`/`Inner`/`Actor`、`buildPlot`/`plotFileName`/`splitActors`
- Produces: `virtual_file.RenderNFO(root string, m *Media) ([]byte, error)`（拷贝语义，不突变入参；`RenderMediaNFO` 签名不变委托之）、`buildNFOWithRoot(root, title, plot string, setting *model.EmbyDirSetting) ([]byte, error)`、`buildEpisodeNFO(title string, setting *model.EmbyDirSetting) ([]byte, error)`、`buildTVShowNFO(showName, plot string, setting *model.EmbyDirSetting) ([]byte, error)` —— Task 4 依赖

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

追加到 `drivers/emby_wrapper/nfo_internal_test.go`：

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
```

`nfo_internal_test.go` 需要新增 import：`"strings"`、`"github.com/OpenListTeam/OpenList/v4/internal/model"`。

- [ ] **Step 2: 运行确认失败**

```bash
cd /Users/varg247/store/work-store/backend/openlist && /Library/Go/sdk/go1.25.4/bin/go test ./drivers/virtual_file/ -run TestRenderNFOWithRoot -count=1 && /Library/Go/sdk/go1.25.4/bin/go test ./drivers/emby_wrapper/ -run 'TestBuildEpisodeNFO|TestBuildTVShowNFO' -count=1
```

Expected: FAIL（RenderNFO/buildEpisodeNFO/buildTVShowNFO 未定义）

- [ ] **Step 3: 实现 `drivers/virtual_file/util.go`** —— `RenderMediaNFO` 替换为：

```go
// RenderNFO 将 Media 结构渲染为指定根元素（movie/tvshow/episodedetails）的 NFO XML 文档（含 XML 头）。
// 拷贝语义：不突变入参。
func RenderNFO(root string, m *Media) ([]byte, error) {
	copy := *m
	copy.XMLName = xml.Name{Local: root}
	return mediaToXML(&copy)
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
```

- [ ] **Step 5: 运行确认通过**

```bash
cd /Users/varg247/store/work-store/backend/openlist && /Library/Go/sdk/go1.25.4/bin/go test ./drivers/virtual_file/ -count=1 && /Library/Go/sdk/go1.25.4/bin/go test ./drivers/emby_wrapper/ -count=1
```

Expected: PASS（既有 nfo/plot 测试不回归——buildNFOContent 行为不变）

- [ ] **Step 6: 提交**

```bash
cd /Users/varg247/store/work-store/backend/openlist && git add drivers/virtual_file/util.go drivers/virtual_file/render_nfo_test.go drivers/emby_wrapper/nfo.go drivers/emby_wrapper/nfo_internal_test.go && git commit -m "feat(virtual_file): render nfo with configurable root element for tvshow and episodedetails"
```

---

### Task 4: List/Get TV 分支（虚拟剧集展示 + 反查 + Link 还原）

**Files:**
- Modify: `drivers/emby_wrapper/folder.go`（virtualEpisode 类型）
- Modify: `drivers/emby_wrapper/episode.go`（withTVShowNFOs）
- Modify: `drivers/emby_wrapper/setting.go`（tvShowInfo）
- Modify: `drivers/emby_wrapper/driver.go`（List/Get/resolveEpisodePath/virtualNFOForPath 重构/newVirtualNFO）
- Test: `drivers/emby_wrapper/tv_show_test.go`（新文件，外部测试包）、`drivers/emby_wrapper/e2e_test.go`

**Interfaces:**
- Consumes: Task 2 的 `buildEpisodeIndex`/`isNumberedEpisode`、Task 3 的 `buildEpisodeNFO`/`buildTVShowNFO`、Task 1 的 `TvShow`/`TvShowName`/`FolderAddition`、现有 `resolveSetting`/`withVirtualNFOs`/`virtualNFO`/`nfoBaseName`
- Produces: `tvShowInfo(dirPath string) (string, bool, error)`（剧名，空回退文件夹名；本地生效不继承）、`withTVShowNFOs(dir model.Obj, showName string, objs []model.Obj) []model.Obj`、`resolveEpisodePath(ctx, path) (model.Obj, bool, error)`、`newVirtualEpisode(real model.Obj, name, path string) model.Obj`、`d.newVirtualNFO(path string, content []byte, modified time.Time) model.Obj` —— Task 6 无依赖；Task 5 修改 resolveSetting 时保持此任务行为

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

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

// setMtime 设置下游文件修改时间（剧集排序依据）。
func setMtime(t *testing.T, relPath string, ts time.Time) {
	t.Helper()
	full := filepath.Join(localRoot, strings.TrimPrefix(relPath, "/"))
	if err := os.Chtimes(full, ts, ts); err != nil {
		t.Fatalf("chtimes %s: %v", relPath, err)
	}
}

func sortedNames(objs []model.Obj) []string {
	got := names(objs)
	sort.Strings(got)
	return got
}

func markTVShow(t *testing.T, d *emby_wrapper.EmbyWrapper, payload string) {
	t.Helper()
	if err := d.Rename(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, payload); err != nil {
		t.Fatalf("mark tv show: %+v", err)
	}
}

// TestTVShowListNamesAndOrder：直接文件按时间旧→新编号；原始名不再出现；含剧集 nfo 与 tvshow.nfo。
func TestTVShowListNamesAndOrder(t *testing.T) {
	d := setup(t)
	if err := writeDownstreamFile(t, "/Movies/B.mp4", "b"); err != nil {
		t.Fatalf("write B: %v", err)
	}
	if err := writeDownstreamFile(t, "/Movies/C.mp4", "c"); err != nil {
		t.Fatalf("write C: %v", err)
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	setMtime(t, "/Movies/AAA.mkv", base)       // 最早 → E01
	setMtime(t, "/Movies/C.mp4", base.Add(1*time.Hour))  // → E02
	setMtime(t, "/Movies/B.mp4", base.Add(2*time.Hour))  // → E03
	markTVShow(t, d, `{"tv_show":true,"tv_show_name":"测试剧","plot":"剧集介绍","actors":"演员A"}`)
	objs, err := d.List(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list: %+v", err)
	}
	want := []string{
		"AAA-S01E01.mkv", "AAA-S01E01.nfo",
		"B-S01E03.mp4", "B-S01E03.nfo",
		"C-S01E02.mp4", "C-S01E02.nfo",
		"tvshow.nfo",
	}
	if got := sortedNames(objs); !reflect.DeepEqual(got, want) {
		t.Errorf("expected %v, got %v", want, got)
	}
}

// TestTVShowSkipsNumbered：已含 SxxExx 的文件保持原名，不参与时间编号。
func TestTVShowSkipsNumbered(t *testing.T) {
	d := setup(t)
	if err := writeDownstreamFile(t, "/Movies/Skip.S01E03.mkv", "s"); err != nil {
		t.Fatalf("write skip: %v", err)
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	setMtime(t, "/Movies/AAA.mkv", base)
	setMtime(t, "/Movies/Skip.S01E03.mkv", base.Add(1*time.Hour))
	markTVShow(t, d, `{"tv_show":true}`)
	objs, err := d.List(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list: %+v", err)
	}
	want := []string{
		"AAA-S01E01.mkv", "AAA-S01E01.nfo",
		"Skip.S01E03.mkv", "Skip.S01E03.nfo",
		"tvshow.nfo",
	}
	if got := sortedNames(objs); !reflect.DeepEqual(got, want) {
		t.Errorf("expected %v, got %v", want, got)
	}
}

// TestTVShowRealFilePriority：虚拟名已被真实文件占用时，原文件原样展示且不生成虚拟 nfo。
func TestTVShowRealFilePriority(t *testing.T) {
	d := setup(t)
	if err := writeDownstreamFile(t, "/Movies/AAA-S01E01.mkv", "real"); err != nil {
		t.Fatalf("write real: %v", err)
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	setMtime(t, "/Movies/AAA.mkv", base)
	setMtime(t, "/Movies/AAA-S01E01.mkv", base.Add(1*time.Hour))
	markTVShow(t, d, `{"tv_show":true}`)
	objs, err := d.List(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list: %+v", err)
	}
	want := []string{
		"AAA-S01E01.mkv", // 真实文件（已编号，保持原名）
		"AAA.mkv",        // 虚拟名被占用：原样展示
		"tvshow.nfo",
	}
	if got := sortedNames(objs); !reflect.DeepEqual(got, want) {
		t.Errorf("expected %v, got %v", want, got)
	}
}

// TestTVShowNFOsContent：剧集 nfo（episodedetails、title=原文件名、actors、无 plot）与 tvshow.nfo（剧名+简介）。
func TestTVShowNFOsContent(t *testing.T) {
	d := setup(t)
	markTVShow(t, d, `{"tv_show":true,"tv_show_name":"测试剧","plot":"剧集介绍","actors":"演员A"}`)
	ep := readNFOLink(t, d, "/Movies/AAA-S01E01.nfo")
	if !strings.Contains(ep, "<episodedetails>") {
		t.Errorf("episode nfo must use episodedetails root, got %s", ep)
	}
	if !strings.Contains(ep, "<![CDATA[AAA]]>") {
		t.Errorf("episode nfo title must be original base name, got %s", ep)
	}
	if !strings.Contains(ep, "<name>演员A</name>") {
		t.Errorf("episode nfo must keep actors, got %s", ep)
	}
	if strings.Contains(ep, "剧集介绍") {
		t.Errorf("episode nfo must not contain show plot, got %s", ep)
	}
	show := readNFOLink(t, d, "/Movies/tvshow.nfo")
	if !strings.Contains(show, "<tvshow>") {
		t.Errorf("tvshow.nfo must use tvshow root, got %s", show)
	}
	if !strings.Contains(show, "<![CDATA[测试剧]]>") {
		t.Errorf("tvshow.nfo must contain show name, got %s", show)
	}
	if !strings.Contains(show, "<![CDATA[剧集介绍]]>") {
		t.Errorf("tvshow.nfo must contain plot, got %s", show)
	}
	if !strings.Contains(show, "<name>演员A</name>") {
		t.Errorf("tvshow.nfo must keep actors, got %s", show)
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

// TestGetAndLinkVirtualEpisode：虚拟剧集路径 Get 反查真实文件，Link 还原真实内容。
func TestGetAndLinkVirtualEpisode(t *testing.T) {
	d := setup(t)
	markTVShow(t, d, `{"tv_show":true}`)
	if got := readNFOLink(t, d, "/Movies/AAA-S01E01.mkv"); got != "x" {
		t.Errorf("episode must play real file content, got %q", got)
	}
}

// TestGetEpisodeNFORealFileWins：下游真实同名 nfo 优先于虚拟剧集 nfo。
func TestGetEpisodeNFORealFileWins(t *testing.T) {
	d := setup(t)
	if err := writeDownstreamFile(t, "/Movies/AAA-S01E01.nfo", "real-content"); err != nil {
		t.Fatalf("write real nfo: %v", err)
	}
	markTVShow(t, d, `{"tv_show":true}`)
	if got := readNFOLink(t, d, "/Movies/AAA-S01E01.nfo"); got != "real-content" {
		t.Errorf("real nfo must win, got %q", got)
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

`tv_show_test.go` 需要 import `"github.com/OpenListTeam/OpenList/v4/drivers/emby_wrapper"`（markTVShow 参数类型）。

追加到 `drivers/emby_wrapper/e2e_test.go`：

```go
// TestEndToEndTVShowThroughFS：TV 模式经 fs 层全链路（等价于 strm 落盘路径：虚拟名落盘、播放还原真实文件）。
func TestEndToEndTVShowThroughFS(t *testing.T) {
	_ = setup(t)
	if err := fs.Rename(context.Background(), "/ew/Movies", `{"tv_show":true,"tv_show_name":"测试剧"}`); err != nil {
		t.Fatalf("rename via fs: %+v", err)
	}
	objs, err := fs.List(context.Background(), "/ew/Movies", &fs.ListArgs{})
	if err != nil {
		t.Fatalf("fs list: %+v", err)
	}
	episodeFound, tvshowFound := false, false
	for _, o := range objs {
		switch o.GetName() {
		case "AAA-S01E01.mkv":
			episodeFound = true
		case "tvshow.nfo":
			tvshowFound = true
		}
	}
	if !episodeFound || !tvshowFound {
		t.Fatalf("fs list must contain virtual episode and tvshow.nfo, got %v", names(objs))
	}
	link, _, err := fs.Link(context.Background(), "/ew/Movies/AAA-S01E01.mkv", model.LinkArgs{})
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
	if string(body) != "x" {
		t.Errorf("episode must play the real file content, got %q", string(body))
	}
}
```

- [ ] **Step 2: 运行确认失败**

```bash
cd /Users/varg247/store/work-store/backend/openlist && /Library/Go/sdk/go1.25.4/bin/go test ./drivers/emby_wrapper/ -run 'TestTVShow|TestGetAndLinkVirtualEpisode|TestGetEpisodeNFORealFileWins|TestGetVirtualEpisodeNotFound|TestEndToEndTVShowThroughFS' -count=1
```

Expected: FAIL（tvShowInfo/withTVShowNFOs/resolveEpisodePath/newVirtualEpisode 未定义或 TV 分支未生效）

- [ ] **Step 3: 实现 `drivers/emby_wrapper/folder.go`** —— 追加：

```go
// virtualEpisode 虚拟剧集对象：GetName 返回虚拟名（如 A-S01E01.mp4），
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

- [ ] **Step 4: 实现 `drivers/emby_wrapper/setting.go`** —— 追加：

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
```

（setting.go 已有 stdpath/strings import）

- [ ] **Step 5: 实现 `drivers/emby_wrapper/episode.go`** —— 追加（文件末尾）：

```go
// withTVShowNFOs TV 模式：直接视频文件按时间编号为剧集（虚拟名展示），
// 生成剧集 nfo（episodedetails：title=原文件名、actors、无 plot）与 tvshow.nfo（剧名+简介）。
// 真实同名 nfo/文件优先：下游已存在同名文件或 nfo 时跳过虚拟生成。
func (d *EmbyWrapper) withTVShowNFOs(dir model.Obj, showName string, objs []model.Obj) []model.Obj {
	dirPath := dir.GetPath()
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
	var videos []model.Obj
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
		if _, ok := d.supportSuffix[utils.Ext(name)]; ok {
			videos = append(videos, o)
		}
	}
	idx := buildEpisodeIndex(videos, d.supportSuffix)
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
	// tvshow.nfo（真实同名 nfo 优先）
	if !realNFO["tvshow.nfo"] {
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
	return out
}
```

- [ ] **Step 6: 实现 `drivers/emby_wrapper/driver.go`** —— 四处修改：

**(a) List** 增加 TV 分支：

```go
	objs = d.decorate(dir.GetPath(), objs)
	if showName, ok, err := d.tvShowInfo(dir.GetPath()); err != nil {
		utils.Log.Warnf("emby wrapper: tv show info %s: %+v", dir.GetPath(), err)
	} else if ok {
		return d.withTVShowNFOs(dir, showName, objs), nil
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

**(c) 新增 resolveEpisodePath 与 newVirtualNFO**（追加到 virtualNFOForPath 之后；import 增加 `"time"`）：

```go
// resolveEpisodePath 在父目录为 TV 模式时按虚拟名反查真实文件。
// 返回 (包装对象, true, nil)：命中虚拟剧集；(nil, false, nil)：非 TV 模式或未命中。
func (d *EmbyWrapper) resolveEpisodePath(ctx context.Context, path string) (model.Obj, bool, error) {
	parentDir := stdpath.Dir(path)
	if _, ok, err := d.tvShowInfo(parentDir); err != nil {
		return nil, false, err
	} else if !ok {
		return nil, false, nil
	}
	remoteStorage, remoteActualPath, err := d.remote()
	if err != nil {
		return nil, false, err
	}
	objs, err := op.List(ctx, remoteStorage, stdpath.Join(remoteActualPath, parentDir), model.ListArgs{})
	if err != nil {
		return nil, false, err
	}
	idx := buildEpisodeIndex(objs, d.supportSuffix)
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

**(d) virtualNFOForPath 整体替换**（TV 分支 + 原有影片分支，真实 nfo 优先扫描提前到公共位置）：

```go
// virtualNFOForPath 尝试为 .nfo 路径构建虚拟对象。
// 返回 (obj, true, nil)：命中虚拟 nfo；(nil, false, nil)：应转发下游（无设置/无匹配影片/存在真实 nfo）。
func (d *EmbyWrapper) virtualNFOForPath(ctx context.Context, path string) (model.Obj, bool, error) {
	parentDir := stdpath.Dir(path)
	base := strings.TrimSuffix(stdpath.Base(path), ".nfo")
	showName, isTV, err := d.tvShowInfo(parentDir)
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
	// 真实 nfo 优先：下游存在同名真实 nfo 时交给下游 Get（含 tvshow.nfo）
	for _, o := range objs {
		if o.IsDir() {
			continue
		}
		if strings.EqualFold(utils.Ext(o.GetName()), "nfo") && strings.EqualFold(nfoBaseName(o.GetName()), base) {
			return nil, false, nil
		}
	}
	// TV 模式分支：tvshow.nfo 与剧集虚拟名 nfo
	if isTV {
		idx := buildEpisodeIndex(objs, d.supportSuffix)
		if strings.EqualFold(base, "tvshow") {
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
cd /Users/varg247/store/work-store/backend/openlist && git add drivers/emby_wrapper/folder.go drivers/emby_wrapper/episode.go drivers/emby_wrapper/setting.go drivers/emby_wrapper/driver.go drivers/emby_wrapper/tv_show_test.go drivers/emby_wrapper/e2e_test.go && git commit -m "feat(emby_wrapper): serve tv-show folders as time-ordered episodes with virtual names"
```

---

### Task 5: resolveSetting TV 边界阻断 plot/append

**Files:**
- Modify: `drivers/emby_wrapper/setting.go`
- Test: `drivers/emby_wrapper/setting_test.go`

**Interfaces:**
- Consumes: Task 1 的 `TvShow` 字段（Upsert 已支持）
- Produces: 无新接口；`resolveSetting` 语义扩展——当 walk 经过一个 `TvShow=true` 且**非 origin** 的目录时，plot/append 维度停止收集（不再向上继承，也不取自该目录自身）；演员维度不受影响继续向上。origin 自身（即被解析目录就是 TV 文件夹）不触发阻断，其 plot/append 正常生效（供 tvshow.nfo 使用）。全维度无值时仍返回 nil（阻断不产生空行）

- [ ] **Step 1: 写失败测试**（追加到 `drivers/emby_wrapper/setting_test.go`；用 `newTestWrapper()` 自动分配独立 storageID）：

```go
// TestResolveSettingTVBlocksPlotInheritance：TV 文件夹的 plot 不向子目录继承，actors 正常继承；
// TV 文件夹自身解析 plot 正常（供 tvshow.nfo）。
func TestResolveSettingTVBlocksPlotInheritance(t *testing.T) {
	d := newTestWrapper()
	tv := true
	if err := UpsertEmbyDirSetting(d.ID, "/TV", "Y", "P", "", nil, nil, &tv); err != nil {
		t.Fatalf("config TV folder: %v", err)
	}
	// TV 文件夹自身：plot 生效
	item, err := d.resolveSetting("/TV")
	if err != nil || item == nil {
		t.Fatalf("expected setting for TV folder, got %v %v", item, err)
	}
	if item.Plot != "P" || item.Actors != "Y" {
		t.Errorf("TV folder itself must use own plot/actors, got %+v", item)
	}
	// 子目录：plot 被阻断、actors 继承
	item, err = d.resolveSetting("/TV/Sub")
	if err != nil || item == nil {
		t.Fatalf("expected setting for sub, got %v %v", item, err)
	}
	if item.Plot != "" {
		t.Errorf("plot must be blocked below tv folder, got %q", item.Plot)
	}
	if item.Actors != "Y" {
		t.Errorf("actors must still inherit, got %q", item.Actors)
	}
}

// TestResolveSettingTVBlocksAppend：append 维度同样被阻断；TV 文件夹自身 append 生效。
func TestResolveSettingTVBlocksAppend(t *testing.T) {
	d := newTestWrapper()
	tv, tf := true, true
	if err := UpsertEmbyDirSetting(d.ID, "/TV2", "", "", "", nil, &tf, &tv); err != nil {
		t.Fatalf("config TV folder: %v", err)
	}
	item, err := d.resolveSetting("/TV2")
	if err != nil || item == nil {
		t.Fatalf("expected setting for TV folder, got %v %v", item, err)
	}
	if item.AppendFileNameToPlot == nil || !*item.AppendFileNameToPlot {
		t.Errorf("TV folder itself must keep append, got %+v", item.AppendFileNameToPlot)
	}
	item, err = d.resolveSetting("/TV2/Sub")
	if err != nil || item == nil {
		t.Fatalf("expected setting for sub, got %v %v", item, err)
	}
	if item.AppendFileNameToPlot != nil {
		t.Errorf("append must be blocked below tv folder, got %+v", item.AppendFileNameToPlot)
	}
}

// TestResolveSettingTVBlockWithActorsAbove：阻断后继续向上收集演员。
func TestResolveSettingTVBlockWithActorsAbove(t *testing.T) {
	d := newTestWrapper()
	tv := true
	if err := UpsertEmbyDirSetting(d.ID, "/TVB4", "Z", "", "", nil, nil, nil); err != nil {
		t.Fatalf("config parent: %v", err)
	}
	if err := UpsertEmbyDirSetting(d.ID, "/TVB4/Show", "", "", "", nil, nil, &tv); err != nil {
		t.Fatalf("config TV folder: %v", err)
	}
	item, err := d.resolveSetting("/TVB4/Show/Sub")
	if err != nil || item == nil {
		t.Fatalf("expected setting, got %v %v", item, err)
	}
	if item.Actors != "Z" {
		t.Errorf("actors above tv boundary must still inherit, got %q", item.Actors)
	}
	if item.Plot != "" {
		t.Errorf("plot must stay blocked, got %q", item.Plot)
	}
}

// TestResolveSettingTVBlockEmptyKeepsNil：仅阻断无配置时仍返回 nil（不产生空行）。
func TestResolveSettingTVBlockEmptyKeepsNil(t *testing.T) {
	d := newTestWrapper()
	tv := true
	if err := UpsertEmbyDirSetting(d.ID, "/TV5", "", "", "", nil, nil, &tv); err != nil {
		t.Fatalf("config TV folder: %v", err)
	}
	item, err := d.resolveSetting("/TV5/Sub")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if item != nil {
		t.Errorf("blocked-empty must resolve to nil, got %+v", item)
	}
}
```

- [ ] **Step 2: 运行确认失败**

```bash
cd /Users/varg247/store/work-store/backend/openlist && /Library/Go/sdk/go1.25.4/bin/go test ./drivers/emby_wrapper/ -run 'TestResolveSettingTV' -count=1
```

Expected: FAIL（当前 resolveSetting 不阻断，plot/append 会继承到子目录）

- [ ] **Step 3: 实现** `drivers/emby_wrapper/setting.go` —— `resolveSetting` 整体替换：

```go
// resolveSetting 返回 dirPath 生效的目录设置（分维度合并，各字段独立继承）。
// 距离优先：自底向上遇到的第一个有效值即生效（用户确认的语义：近处设置优先于祖先设置）。
// actors 维度：手动 actors 非空，或 use_name_as_actor 开启者（非 origin 自身，合成开启者之下
// 第一段目录名）；plot 维度：最近的非空 Plot；append 维度：最近的显式 AppendFileNameToPlot
// （*bool，nil=未配置，false=显式关闭并阻断上层继承）。
// TV 边界：walk 经过 TvShow=true 且非 origin 的目录时，plot/append 维度停止收集
// （剧集介绍属于该剧自身，不泄漏给子目录影片）；actors 维度不受影响。
// 任一维度命中即返回合成行（DirPath = 被解析目录，仅作溯源；消费方只应读取 Actors/Plot/
// AppendFileNameToPlot）；全部未命中返回 nil（阻断但无配置也返回 nil，不产生空行）。
func (d *EmbyWrapper) resolveSetting(dirPath string) (*model.EmbyDirSetting, error) {
	dirPath = utils.FixAndCleanPath(dirPath)
	origin := dirPath
	var actorsItem *model.EmbyDirSetting
	plot := ""
	var appendFlag *bool
	plotBlocked := false
	appendBlocked := false
	for {
		item, err := GetEmbyDirSetting(d.ID, dirPath)
		if err != nil {
			return nil, err
		}
		if item != nil {
			if actorsItem == nil {
				if strings.TrimSpace(item.Actors) != "" {
					actorsItem = item
				} else if item.UseNameAsActor && !utils.PathEqual(dirPath, origin) {
					rel := strings.TrimPrefix(origin, dirPath)
					rel = strings.TrimPrefix(rel, "/")
					if idx := strings.Index(rel, "/"); idx != -1 {
						rel = rel[:idx]
					}
					if rel != "" {
						actorsItem = &model.EmbyDirSetting{Actors: rel}
					}
				}
			}
			if item.TvShow && !utils.PathEqual(dirPath, origin) {
				plotBlocked = true
				appendBlocked = true
			}
			if !plotBlocked && plot == "" {
				plot = strings.TrimSpace(item.Plot)
			}
			if !appendBlocked && appendFlag == nil && item.AppendFileNameToPlot != nil {
				appendFlag = item.AppendFileNameToPlot
			}
		}
		if actorsItem != nil && (plotBlocked || plot != "") && (appendBlocked || appendFlag != nil) {
			break
		}
		if utils.PathEqual(dirPath, "/") {
			break
		}
		dirPath = stdpath.Dir(dirPath)
	}
	if actorsItem == nil && plot == "" && appendFlag == nil {
		return nil, nil
	}
	result := &model.EmbyDirSetting{
		StorageID:            d.ID,
		DirPath:              origin,
		Plot:                 plot,
		AppendFileNameToPlot: appendFlag,
	}
	if actorsItem != nil {
		result.Actors = actorsItem.Actors
		result.UseNameAsActor = actorsItem.UseNameAsActor
	}
	return result, nil
}
```

- [ ] **Step 4: 运行确认通过**

```bash
cd /Users/varg247/store/work-store/backend/openlist && /Library/Go/sdk/go1.25.4/bin/go test ./drivers/emby_wrapper/ -count=1
```

Expected: PASS（既有继承/use/plot 维度测试不回归——无 TV 标记时行为不变）

- [ ] **Step 5: 提交**

```bash
cd /Users/varg247/store/work-store/backend/openlist && git add drivers/emby_wrapper/setting.go drivers/emby_wrapper/setting_test.go && git commit -m "feat(emby_wrapper): block plot and append inheritance below tv-show folders"
```

---

### Task 6: addition 展示 + MkdirConfig + 全量验证

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
			Help:    "标记该文件夹为电视剧：直接子文件按时间旧→新自动编号为剧集（原基础名-S01E01.mp4 格式，已含 SxxExx 编号的文件跳过），生成剧集 nfo（保留演员、无简介）与 tvshow.nfo（剧名/简介）；本地生效不继承，并阻断 plot/append 向子目录继承",
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
- tv_show/tv_show_name 设置（本地不继承）：Task 1 字段 + Task 4 tvShowInfo（只查本地行）✓
- 时间旧→新编号 + 跳过已编号 + 同时间按名排序：Task 2 buildEpisodeIndex + TestBuildEpisodeIndex/TestTVShowSkipsNumbered ✓
- 虚拟命名 `原基础名-S01E01.mp4`：Task 2 episodeVirtualName ✓
- 剧集 nfo（episodedetails、title=原名、actors、无 plot）：Task 3 buildEpisodeNFO + Task 4 withTVShowNFOs + TestTVShowNFOsContent ✓
- tvshow.nfo（剧名回退文件夹名、plot、actors）：Task 3 buildTVShowNFO + Task 4 + TestTVShowNameFallbackFolder ✓
- 不物理改名、Get 唯一映射点、Link 还原真实文件：Task 4 virtualEpisode（GetPath=真实路径，Link 代码零改动）+ resolveEpisodePath + TestGetAndLinkVirtualEpisode + e2e ✓
- 真实同名文件/nfo 优先：Task 4 realFiles/realNFO 检查 + TestTVShowRealFilePriority/TestGetEpisodeNFORealFileWins ✓
- TV 边界阻断 plot/append（演员不阻断）：Task 5 + 三测试 ✓
- 子文件夹/非视频原样展示：Task 4 withTVShowNFOs（IsDir/非视频原样追加）✓
- 展示层 addition + MkdirConfig：Task 6 ✓
- `-S01E01` 格式（Emby 可识别）：Spec 8，Task 2 命名实现 ✓

**2. Placeholder scan:** 无 TBD；所有代码与测试完整给出；sed 命令含回退说明（编译报错即未匹配，手动补参）。

**3. Type consistency:**
- `UpsertEmbyDirSetting(storageID, dirPath, actors, plot, tvShowName string, useNameAsActor, appendFileNameToPlot, tvShow *bool)`：Task 1 定义，driver.go Rename 与全部测试调用点一致（8 参）✓
- `buildEpisodeIndex(files []model.Obj, supportSuffix map[string]struct{}) *episodeIndex`：Task 2 定义；Task 4 withTVShowNFOs（传 decorated videos，byReal 键=包装路径，episodeName(o) 同对象一致）与 resolveEpisodePath/virtualNFOForPath（传 raw objs，只用 resolve/nfoBases/last）一致 ✓
- `buildEpisodeNFO(title string, setting)` / `buildTVShowNFO(showName, plot string, setting)`：Task 3 定义，Task 4 调用 ✓
- `wrapObj` 9 参（Task 6）：decorate 与 Get 两处调用点同步更新；Task 1-5 期间保持旧签名不受影响 ✓
- `newVirtualEpisode(real model.Obj, name, path string) model.Obj`：Task 4 定义；withTVShowNFOs（name=虚拟名、path=o.GetPath() 真实路径）与 resolveEpisodePath（name=请求名、path=parentDir+真实名）一致 ✓
- `virtualNFOForPath` 重构后保留原语义：非 TV 模式行为与原来一致（真实 nfo 扫描提前到公共位置，逻辑等价）✓
- e2e 测试沿用 fs.Link 返回值 `(link, _, err)` 与 `http_range.Range{Length: -1}` 既有模式 ✓
