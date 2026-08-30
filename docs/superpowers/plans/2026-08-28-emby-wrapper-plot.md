# EmbyWrapper plot 配置实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 文件夹配置新增 `plot`（string）与 `append_file_name_to_plot`（bool）：plot 指定后该文件夹下所有影片的 nfo plot 使用该值；append 开启后追加去扩展名的原始文件名到 plot（格式 `plot + '-' + 文件名`，plot 未设置时直接以文件名为 plot）。字段独立继承（分维度），单独配置即触发 nfo 生成。

**Architecture:** `EmbyDirSetting` 加 `Plot string` 与 `AppendFileNameToPlot *bool`（三态：nil=未配置/false=显式关闭/true=开启）；`UpsertEmbyDirSetting` 扩展合并语义（各字段独立：空串/ nil 保持或清除）；`resolveSetting` 改为**分维度解析**（一次自底向上 walk，actors 维度、plot 维度、append 维度各自收集最近有效值，合并返回合成行；任一维度命中即非 nil）；`buildNFOContent` 增加 plot 计算（拼接用去扩展名的原始文件名）；MkdirConfig 与 folder addition 增加两字段。

**Tech Stack:** Go 1.25.4（`/Library/Go/sdk/go1.25.4/bin/go`）、gorm（`*bool` 列，NULL=未配置）、复用 `virtual_file.Media.Plot`（CDATA）。

**Spec:** 用户需求（2026-08-28 确认）：
1. `plot`：指定后该文件夹下所有影片（含子文件夹，继承）的 nfo plot 用该值
2. `append_file_name_to_plot`：开启后 plot = `plot + '-' + 文件名`；plot 未设置时 plot = 文件名本身；文件名**不含扩展名**（保留 cd 等原始部分，仅去最后一个扩展名）
3. **分维度继承**：各字段独立解析最近有效值（如 /A plot=P + /A/A1 actors=Y → A1 影片 actor=Y、plot=P；append 同理，显式 false 阻断上层继承）
4. **单独配置触发**：仅 plot（或 append）配置、无 actors 的文件夹也生成 nfo（actor 为空）
5. 与现有机制一致：距离优先（近处设置优先）、use_name_as_actor 合成 actor 不干扰 plot 继承、真实 nfo 优先、一 basename 一 nfo

## Global Constraints

- Go 工具链一律用 `/Library/Go/sdk/go1.25.4/bin/go`
- TDD：先失败测试再实现；每个任务独立提交
- 提交信息沿用仓库风格：`feat(emby_wrapper): ...`
- `UpsertEmbyDirSetting` 签名变化影响全部既有调用点（db_test / setting_test / rename_test / driver.go Rename），同步更新
- 既有测试断言 `item.Actors` / addition 字段保持兼容：resolveSetting 返回合成行，`Actors` 语义不变；`FolderAddition` 新增字段不影响既有断言
- 不修改 `text_types` 默认值（用户明确"先不改"）

---

### Task 1: 模型 + CRUD + Rename 表单扩展

**Files:**
- Modify: `internal/model/emby.go`
- Modify: `drivers/emby_wrapper/db.go`
- Modify: `drivers/emby_wrapper/folder.go`
- Modify: `drivers/emby_wrapper/driver.go`（Rename）
- Test: `drivers/emby_wrapper/db_test.go`

**Interfaces:**
- Consumes: 现有 EmbyDirSetting / GetEmbyDirSetting
- Produces: `EmbyDirSetting{..., Plot string, AppendFileNameToPlot *bool}`、`UpsertEmbyDirSetting(storageID uint, dirPath, actors, plot string, useNameAsActor, appendFileNameToPlot *bool) error`、`FolderAddition{Actors string; UseNameAsActor *bool; Plot string; AppendFileNameToPlot *bool}` —— Task 2/3 依赖

- [ ] **Step 1: 模型加字段** `internal/model/emby.go`

```go
package model

// EmbyDirSetting 某个目录的 Emby 元数据设置。各字段独立生效（分维度继承）：
// Actors 为空且 UseNameAsActor 为 false 表示未配置演员；Plot 为空表示未配置简介；
// AppendFileNameToPlot 为 nil 表示未配置（false 为显式关闭，阻断上层继承）。
// 全部字段未配置时不应存在该行。
type EmbyDirSetting struct {
	ID                   uint   `gorm:"primaryKey"`
	StorageID            uint   `gorm:"uniqueIndex:idx_emby_dir_setting"`
	DirPath              string `gorm:"uniqueIndex:idx_emby_dir_setting"`
	Actors               string
	UseNameAsActor       bool
	Plot                 string
	AppendFileNameToPlot *bool
}
```

- [ ] **Step 2: 更新 CRUD** `drivers/emby_wrapper/db.go` —— 整体替换：

```go
package emby_wrapper

import (
	"errors"
	"strings"

	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"gorm.io/gorm"
)

func GetEmbyDirSetting(storageID uint, dirPath string) (*model.EmbyDirSetting, error) {
	var item model.EmbyDirSetting
	err := db.GetDb().Where("storage_id = ? AND dir_path = ?", storageID, dirPath).First(&item).Error
	if err == nil {
		return &item, nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return nil, err
}

// UpsertEmbyDirSetting 保存目录设置。各字段独立合并：
// actors/plot 去空格后为空表示清除对应字段；useNameAsActor/appendFileNameToPlot 为 nil 表示未提供（保持原值）。
// 所有字段均未配置时删除该目录的设置行。
func UpsertEmbyDirSetting(storageID uint, dirPath, actors, plot string, useNameAsActor, appendFileNameToPlot *bool) error {
	actors = strings.TrimSpace(actors)
	plot = strings.TrimSpace(plot)
	item, err := GetEmbyDirSetting(storageID, dirPath)
	if err != nil {
		return err
	}
	use := false
	var appendFlag *bool
	if item != nil {
		use = item.UseNameAsActor
		appendFlag = item.AppendFileNameToPlot
	}
	if useNameAsActor != nil {
		use = *useNameAsActor
	}
	if appendFileNameToPlot != nil {
		appendFlag = appendFileNameToPlot
	}
	if actors == "" && plot == "" && !use && appendFlag == nil {
		return db.GetDb().Where("storage_id = ? AND dir_path = ?", storageID, dirPath).Delete(&model.EmbyDirSetting{}).Error
	}
	if item != nil {
		item.Actors = actors
		item.Plot = plot
		item.UseNameAsActor = use
		item.AppendFileNameToPlot = appendFlag
		return db.GetDb().Save(item).Error
	}
	return db.GetDb().Create(&model.EmbyDirSetting{
		StorageID:            storageID,
		DirPath:              dirPath,
		Actors:               actors,
		Plot:                 plot,
		UseNameAsActor:       use,
		AppendFileNameToPlot: appendFlag,
	}).Error
}

// ListEmbyDirSettings 返回该存储全部目录设置：dirPath -> setting。
func ListEmbyDirSettings(storageID uint) (map[string]model.EmbyDirSetting, error) {
	var items []model.EmbyDirSetting
	if err := db.GetDb().Where("storage_id = ?", storageID).Find(&items).Error; err != nil {
		return nil, err
	}
	out := make(map[string]model.EmbyDirSetting, len(items))
	for _, item := range items {
		out[item.DirPath] = item
	}
	return out, nil
}
```

- [ ] **Step 3: 更新既有测试调用签名** `drivers/emby_wrapper/db_test.go` / `setting_test.go`：所有 `UpsertEmbyDirSetting(id, path, actors, use)` 调用追加 `plot=""` 与 `append=nil` 两个参数（形如 `UpsertEmbyDirSetting(1, "/Movies", "三上悠亚", "", nil, nil)`）。

- [ ] **Step 4: 追加合并语义测试**（追加到 db_test.go）：

```go
func TestUpsertPlotAndAppendMergeSemantics(t *testing.T) {
	// plot 单独配置：行保留
	if err := UpsertEmbyDirSetting(1, "/A", "", "P", nil, nil); err != nil {
		t.Fatalf("upsert plot: %v", err)
	}
	item, err := GetEmbyDirSetting(1, "/A")
	if err != nil || item == nil {
		t.Fatalf("get: %v %v", item, err)
	}
	if item.Plot != "P" {
		t.Errorf("expected plot=P, got %q", item.Plot)
	}
	// 写 actors 不影响 plot
	if err := UpsertEmbyDirSetting(1, "/A", "X", "", nil, nil); err != nil {
		t.Fatalf("set actors: %v", err)
	}
	item, err = GetEmbyDirSetting(1, "/A")
	if err != nil || item == nil {
		t.Fatalf("get: %v %v", item, err)
	}
	if item.Plot != "P" || item.Actors != "X" {
		t.Errorf("plot must survive actors write, got %+v", item)
	}
	// 清 plot 不影响 actors
	if err := UpsertEmbyDirSetting(1, "/A", "", "", nil, nil); err != nil {
		t.Fatalf("clear plot: %v", err)
	}
	item, err = GetEmbyDirSetting(1, "/A")
	if err != nil || item == nil {
		t.Fatalf("get: %v %v", item, err)
	}
	if item.Plot != "" || item.Actors != "X" {
		t.Errorf("expected plot cleared actors kept, got %+v", item)
	}
}

func TestUpsertAppendThreeState(t *testing.T) {
	// 开启 append（仅 append 配置）：行保留
	tf := true
	if err := UpsertEmbyDirSetting(1, "/A", "", "", nil, &tf); err != nil {
		t.Fatalf("enable append: %v", err)
	}
	item, err := GetEmbyDirSetting(1, "/A")
	if err != nil || item == nil {
		t.Fatalf("get: %v %v", item, err)
	}
	if item.AppendFileNameToPlot == nil || !*item.AppendFileNameToPlot {
		t.Errorf("expected append enabled, got %+v", item.AppendFileNameToPlot)
	}
	// 显式关闭 append：行保留（阻断上层继承），但 append 为 false
	ff := false
	if err := UpsertEmbyDirSetting(1, "/A", "", "", nil, &ff); err != nil {
		t.Fatalf("disable append: %v", err)
	}
	item, err = GetEmbyDirSetting(1, "/A")
	if err != nil || item == nil {
		t.Fatalf("row must survive explicit disable, got %v %v", item, err)
	}
	if item.AppendFileNameToPlot == nil || *item.AppendFileNameToPlot {
		t.Errorf("expected append disabled, got %+v", item.AppendFileNameToPlot)
	}
	// 全部字段清空（append 未提供）：删行
	if err := UpsertEmbyDirSetting(1, "/A", "", "", nil, nil); err != nil {
		t.Fatalf("clear all: %v", err)
	}
	item, err = GetEmbyDirSetting(1, "/A")
	if err != nil || item != nil {
		t.Errorf("expected row deleted, got %v %v", item, err)
	}
}
```

- [ ] **Step 5: 更新 `folder.go` 与 `driver.go` Rename**

`folder.go`：

```go
type FolderAddition struct {
	Actors               string `json:"actors"`
	UseNameAsActor       *bool  `json:"use_name_as_actor"`
	Plot                 string `json:"plot"`
	AppendFileNameToPlot *bool  `json:"append_file_name_to_plot"`
}
```

`driver.go` Rename：

```go
func (d *EmbyWrapper) Rename(ctx context.Context, srcObj model.Obj, newName string) error {
	if !srcObj.IsDir() {
		return errors.New("emby wrapper driver does not support renaming files")
	}
	var req FolderAddition
	if err := utils.Json.UnmarshalFromString(newName, &req); err != nil {
		return errors.Wrap(err, "invalid folder emby setting")
	}
	return UpsertEmbyDirSetting(d.ID, srcObj.GetPath(), req.Actors, req.Plot, req.UseNameAsActor, req.AppendFileNameToPlot)
}
```

- [ ] **Step 6: 运行确认通过**

```bash
cd /Users/varg247/store/work-store/backend/openlist && /Library/Go/sdk/go1.25.4/bin/go test ./drivers/emby_wrapper/ -count=1
```

Expected: PASS（既有测试语义不回归；新增合并语义测试通过）

- [ ] **Step 7: 提交**

```bash
cd /Users/varg247/store/work-store/backend/openlist && git add internal/model/emby.go drivers/emby_wrapper/db.go drivers/emby_wrapper/db_test.go drivers/emby_wrapper/folder.go drivers/emby_wrapper/driver.go drivers/emby_wrapper/setting_test.go && git commit -m "feat(emby_wrapper): add plot and append_file_name_to_plot directory settings"
```

---

### Task 2: resolveSetting 分维度解析

**Files:**
- Modify: `drivers/emby_wrapper/setting.go`
- Test: `drivers/emby_wrapper/setting_test.go`

**Interfaces:**
- Consumes: `GetEmbyDirSetting`（Task 1）
- Produces: `resolveSetting(dirPath) (*model.EmbyDirSetting, error)` 分维度合并语义：一次自底向上 walk，actors 维度（手动/use 合成，距离优先）、plot 维度（最近非空）、append 维度（最近非 nil）独立收集；任一命中返回合成行（`DirPath` = 被解析目录，仅作溯源），全未命中返回 nil —— withVirtualNFOs / virtualNFOForPath 的 `setting == nil` 判定与 `setting.Actors` 消费不变

- [ ] **Step 1: 写失败测试**（追加到 setting_test.go）：

```go
// TestResolveSettingPlotDimension：plot 独立继承。
func TestResolveSettingPlotDimension(t *testing.T) {
	d := newTestWrapper()
	d.ID = 1
	// /A 设置 plot=P；/A/A1 只设置 actors=Y
	if err := UpsertEmbyDirSetting(d.ID, "/A", "", "P", nil, nil); err != nil {
		t.Fatalf("set plot on A: %v", err)
	}
	if err := UpsertEmbyDirSetting(d.ID, "/A/A1", "Y", "", nil, nil); err != nil {
		t.Fatalf("set actors on A1: %v", err)
	}
	// A1 影片：actor=Y（近处），plot=P（独立继承）
	item, err := d.resolveSetting("/A/A1")
	if err != nil || item == nil {
		t.Fatalf("expected setting, got %v %v", item, err)
	}
	if item.Actors != "Y" || item.Plot != "P" {
		t.Errorf("expected actors=Y plot=P, got %+v", item)
	}
	// A2（无自身设置）：actor 空、plot=P
	item, err = d.resolveSetting("/A/A2")
	if err != nil || item == nil {
		t.Fatalf("expected setting, got %v %v", item, err)
	}
	if item.Actors != "" || item.Plot != "P" {
		t.Errorf("expected actors empty plot=P, got %+v", item)
	}
	// 无任何配置的目录：nil
	item, err = d.resolveSetting("/Other")
	if err != nil || item != nil {
		t.Errorf("expected nil for /Other, got %v %v", item, err)
	}
}

// TestResolveSettingAppendDimension：append 三态独立继承，显式 false 阻断上层。
func TestResolveSettingAppendDimension(t *testing.T) {
	d := newTestWrapper()
	d.ID = 1
	tf := true
	ff := false
	// /A 开 append + plot=P
	if err := UpsertEmbyDirSetting(d.ID, "/A", "", "P", nil, &tf); err != nil {
		t.Fatalf("config A: %v", err)
	}
	item, err := d.resolveSetting("/A/A1")
	if err != nil || item == nil {
		t.Fatalf("expected inherited append, got %v %v", item, err)
	}
	if item.AppendFileNameToPlot == nil || !*item.AppendFileNameToPlot || item.Plot != "P" {
		t.Errorf("expected append=true plot=P inherited, got %+v", item)
	}
	// A1 显式关闭 append：阻断继承
	if err := UpsertEmbyDirSetting(d.ID, "/A/A1", "", "", nil, &ff); err != nil {
		t.Fatalf("disable append on A1: %v", err)
	}
	item, err = d.resolveSetting("/A/A1")
	if err != nil || item == nil {
		t.Fatalf("expected setting, got %v %v", item, err)
	}
	if item.AppendFileNameToPlot == nil || *item.AppendFileNameToPlot {
		t.Errorf("explicit false must block inheritance, got %+v", item.AppendFileNameToPlot)
	}
	// plot 仍独立继承 P
	if item.Plot != "P" {
		t.Errorf("plot must still inherit P, got %q", item.Plot)
	}
}

// TestResolveSettingPlotWithNameAsActor：use 合成 actor 与 plot 独立并存。
func TestResolveSettingPlotWithNameAsActor(t *testing.T) {
	d := newTestWrapper()
	d.ID = 1
	if err := UpsertEmbyDirSetting(d.ID, "/A", "", "P", boolPtr(true), nil); err != nil {
		t.Fatalf("config A: %v", err)
	}
	item, err := d.resolveSetting("/A/A1")
	if err != nil || item == nil {
		t.Fatalf("expected setting, got %v %v", item, err)
	}
	if item.Actors != "A1" || item.Plot != "P" {
		t.Errorf("expected actors=A1 plot=P, got %+v", item)
	}
}
```

- [ ] **Step 2: 运行确认失败**

```bash
cd /Users/varg247/store/work-store/backend/openlist && /Library/Go/sdk/go1.25.4/bin/go test ./drivers/emby_wrapper/ -run 'TestResolveSettingPlotDimension|TestResolveSettingAppendDimension|TestResolveSettingPlotWithNameAsActor' -count=1
```

Expected: FAIL（resolveSetting 尚不解析 plot/append）

- [ ] **Step 3: 实现** `drivers/emby_wrapper/setting.go` 整体替换：

```go
package emby_wrapper

import (
	stdpath "path"
	"strings"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

// resolveSetting 返回 dirPath 生效的目录设置（分维度合并，各字段独立继承）。
// 距离优先：自底向上遇到的第一个有效值即生效（用户确认的语义：近处设置优先于祖先设置）。
// actors 维度：手动 actors 非空，或 use_name_as_actor 开启者（非 origin 自身，合成开启者之下
// 第一段目录名）；plot 维度：最近的非空 Plot；append 维度：最近的显式 AppendFileNameToPlot
// （*bool，nil=未配置，false=显式关闭并阻断上层继承）。
// 任一维度命中即返回合成行（DirPath = 被解析目录，仅作溯源；消费方只应读取 Actors/Plot/
// AppendFileNameToPlot）；全部未命中返回 nil。
func (d *EmbyWrapper) resolveSetting(dirPath string) (*model.EmbyDirSetting, error) {
	dirPath = utils.FixAndCleanPath(dirPath)
	origin := dirPath
	var actorsItem *model.EmbyDirSetting
	plot := ""
	var appendFlag *bool
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
			if plot == "" {
				plot = strings.TrimSpace(item.Plot)
			}
			if appendFlag == nil && item.AppendFileNameToPlot != nil {
				appendFlag = item.AppendFileNameToPlot
			}
		}
		if actorsItem != nil && plot != "" && appendFlag != nil {
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

Expected: PASS（既有继承/use/距离优先测试不回归——合成行 Actors 语义不变）

- [ ] **Step 5: 提交**

```bash
cd /Users/varg247/store/work-store/backend/openlist && git add drivers/emby_wrapper/setting.go drivers/emby_wrapper/setting_test.go && git commit -m "feat(emby_wrapper): resolve plot and append dimensions independently"
```

---

### Task 3: nfo plot 构建 + List/Get 集成

**Files:**
- Modify: `drivers/emby_wrapper/nfo.go`（buildNFOContent + plot 拼接）
- Modify: `drivers/emby_wrapper/driver.go`（virtualNFOForPath 调用）
- Test: `drivers/emby_wrapper/name_actor_test.go`（或新文件 `plot_test.go`）

**Interfaces:**
- Consumes: `resolveSetting` 分维度（Task 2）、`virtual_file.Media.Plot`（已存在）
- Produces: `buildNFOContent(title, fileName string, setting *model.EmbyDirSetting) ([]byte, error)`（plot 在内部计算）、`plotFileName(fileName string) string`（去最后一个扩展名）

- [ ] **Step 1: 写失败测试** `drivers/emby_wrapper/plot_test.go`：

```go
package emby_wrapper_test

import (
	"context"
	"strings"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

// TestPlotConfigured：plot 单独配置即生成 nfo，plot 出现在内容中。
func TestPlotConfigured(t *testing.T) {
	d := setup(t)
	if err := d.Rename(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, `{"plot":"测试简介"}`); err != nil {
		t.Fatalf("set plot: %+v", err)
	}
	objs, err := d.List(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list: %+v", err)
	}
	if got := names(objs); len(got) != 2 {
		t.Fatalf("expected [AAA.mkv AAA.nfo], got %v", got)
	}
	got := readNFOLink(t, d, "/Movies/AAA.nfo")
	if !strings.Contains(got, "<![CDATA[测试简介]]>") {
		t.Errorf("nfo must contain plot, got %s", got)
	}
}

// TestPlotAppendFileName：append 开启，plot = plot + '-' + 去扩展名文件名。
func TestPlotAppendFileName(t *testing.T) {
	d := setup(t)
	if err := d.Rename(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, `{"plot":"P","append_file_name_to_plot":true}`); err != nil {
		t.Fatalf("set plot+append: %+v", err)
	}
	got := readNFOLink(t, d, "/Movies/AAA.nfo")
	if !strings.Contains(got, "<![CDATA[P-AAA]]>") {
		t.Errorf("expected plot P-AAA, got %s", got)
	}
}

// TestPlotAppendWithoutPlot：append 开启但 plot 未设置，plot = 文件名本身。
func TestPlotAppendWithoutPlot(t *testing.T) {
	d := setup(t)
	if err := d.Rename(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, `{"append_file_name_to_plot":true}`); err != nil {
		t.Fatalf("set append: %+v", err)
	}
	got := readNFOLink(t, d, "/Movies/AAA.nfo")
	if !strings.Contains(got, "<![CDATA[AAA]]>") {
		t.Errorf("expected plot AAA, got %s", got)
	}
}

// TestPlotAppendDisabled：append 显式关闭，plot 原样。
func TestPlotAppendDisabled(t *testing.T) {
	d := setup(t)
	if err := d.Rename(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, `{"plot":"P"}`); err != nil {
		t.Fatalf("set plot: %+v", err)
	}
	got := readNFOLink(t, d, "/Movies/AAA.nfo")
	if !strings.Contains(got, "<![CDATA[P]]>") {
		t.Errorf("expected plot P, got %s", got)
	}
	if strings.Contains(got, "P-AAA") {
		t.Errorf("append must be off by default, got %s", got)
	}
}

// TestPlotInheritedDimension：plot 分维度继承到子文件夹。
func TestPlotInheritedDimension(t *testing.T) {
	d := setup(t)
	if err := writeDownstreamDir(t, "/Movies/A1"); err != nil {
		t.Fatalf("mkdir A1: %v", err)
	}
	if err := writeDownstreamFile(t, "/Movies/A1/BBB.mp4", "x"); err != nil {
		t.Fatalf("write movie: %v", err)
	}
	if err := d.Rename(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, `{"plot":"P","append_file_name_to_plot":true}`); err != nil {
		t.Fatalf("config Movies: %+v", err)
	}
	// A1 手动设 actors，plot 仍继承
	if err := d.Rename(context.Background(), &model.Object{Name: "A1", Path: "/Movies/A1", IsFolder: true}, `{"actors":"Y"}`); err != nil {
		t.Fatalf("set actors on A1: %+v", err)
	}
	got := readNFOLink(t, d, "/Movies/A1/BBB.nfo")
	if !strings.Contains(got, "<![CDATA[P-BBB]]>") {
		t.Errorf("plot must inherit and append, got %s", got)
	}
	if !strings.Contains(got, "<name>Y</name>") {
		t.Errorf("actors must use near value Y, got %s", got)
	}
}
```

- [ ] **Step 2: 运行确认失败**

```bash
cd /Users/varg247/store/work-store/backend/openlist && /Library/Go/sdk/go1.25.4/bin/go test ./drivers/emby_wrapper/ -run 'TestPlot' -count=1
```

Expected: FAIL（nfo 无 plot 字段；append 未生效）

- [ ] **Step 3: 实现 `nfo.go`** —— `buildNFOContent` 替换：

```go
// plotFileName 取去扩展名的原始文件名（保留 cd 等原始部分，仅去最后一个扩展名）。
func plotFileName(fileName string) string {
	if index := strings.LastIndex(fileName, "."); index != -1 {
		return fileName[:index]
	}
	return fileName
}

// buildPlot 计算 nfo plot：append 开启时拼接 plot + '-' + 文件名（plot 未设置则直接用文件名）。
func buildPlot(plot string, appendFlag *bool, fileName string) string {
	if appendFlag == nil || !*appendFlag {
		return plot
	}
	name := plotFileName(fileName)
	if plot == "" {
		return name
	}
	return plot + "-" + name
}

// buildNFOContent 构建与 javdb 格式一致的 nfo XML：title + actor + plot。
func buildNFOContent(title, fileName string, setting *model.EmbyDirSetting) ([]byte, error) {
	actors := splitActors(setting.Actors)
	actorInfos := make([]virtual_file.Actor, 0, len(actors))
	for _, a := range actors {
		actorInfos = append(actorInfos, virtual_file.Actor{Name: a})
	}
	return virtual_file.RenderMediaNFO(&virtual_file.Media{
		Title: virtual_file.Inner{Inner: fmt.Sprintf("<![CDATA[%s]]>", title)},
		Plot:  virtual_file.Inner{Inner: fmt.Sprintf("<![CDATA[%s]]>", buildPlot(setting.Plot, setting.AppendFileNameToPlot, fileName))},
		Actor: actorInfos,
	})
}
```

`withVirtualNFOs` 内调用改为 `buildNFOContent(title, o.GetName(), setting)`。

- [ ] **Step 4: 更新 `driver.go` virtualNFOForPath** —— `buildNFOContent(base, setting)` 改为 `buildNFOContent(base, movieObj.GetName(), setting)`（movieObj 为匹配的影片对象，原始文件名）。

- [ ] **Step 5: 运行确认通过**

```bash
cd /Users/varg247/store/work-store/backend/openlist && /Library/Go/sdk/go1.25.4/bin/go test ./drivers/emby_wrapper/ -count=1
```

Expected: PASS（新增 plot 用例 + 既有用例不回归；name_actor_test 的 `<name>A1</name>` 断言不受 Plot 字段影响）

- [ ] **Step 6: 提交**

```bash
cd /Users/varg247/store/work-store/backend/openlist && git add drivers/emby_wrapper/ && git commit -m "feat(emby_wrapper): build nfo plot from folder setting with optional file name append"
```

---

### Task 4: addition 展示 + MkdirConfig + 全量验证

**Files:**
- Modify: `drivers/emby_wrapper/driver.go`（decorate / Get addition 填充、MkdirConfig）
- Test: `drivers/emby_wrapper/driver_test.go`、`drivers/emby_wrapper/get_test.go`

**Interfaces:**
- Consumes: Task 1-3 全部
- Produces: 无新接口（addition 完整展示 plot/append；MkdirConfig 四字段）

- [ ] **Step 1: 更新 `decorate` 与 `Get`**（addition 携带 plot/append）：

`decorate`：

```go
	out := make([]model.Obj, len(objs))
	for i, o := range objs {
		p := stdpath.Join(dirPath, o.GetName())
		s, ok := settings[p]
		actors, use, plot, appendFlag := "", false, "", (*bool)(nil)
		if ok {
			actors, use, plot = s.Actors, s.UseNameAsActor, s.Plot
			appendFlag = s.AppendFileNameToPlot
		}
		out[i] = wrapObj(o, p, actors, use, plot, appendFlag, o.IsDir())
	}
	return out
```

`Get`：

```go
	actors, use, plot, appendFlag := "", false, "", (*bool)(nil)
	if obj.IsDir() {
		if item, e := GetEmbyDirSetting(d.ID, path); e != nil {
			utils.Log.Warnf("emby wrapper: get dir setting %s: %+v", path, e)
		} else if item != nil {
			actors, use, plot = item.Actors, item.UseNameAsActor, item.Plot
			appendFlag = item.AppendFileNameToPlot
		}
	}
	return wrapObj(obj, path, actors, use, plot, appendFlag, obj.IsDir()), nil
```

`folder.go` `wrapObj` 签名扩展：

```go
func wrapObj(obj model.Obj, path, actors string, useNameAsActor bool, plot string, appendFileNameToPlot *bool, folder bool) model.Obj {
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
	}}
}
```

- [ ] **Step 2: 更新 `MkdirConfig`**（四字段）：

```go
func (d *EmbyWrapper) MkdirConfig() []driver.Item {
	return []driver.Item{
		{
			Name:    "actors",
			Type:    conf.TypeString,
			Default: "",
			Help:    "演员列表，逗号分隔；仅对文件夹修改生效，设置后该文件夹及子文件夹内的影片会生成对应的虚拟 nfo 文件（内存构建，配合 strm 驱动落盘；strm 的 DownloadFileTypes 需包含 nfo）",
		},
		{
			Name:    "use_name_as_actor",
			Type:    conf.TypeBool,
			Default: "false",
			Help:    "开启后该文件夹的直接子文件夹以各自名称为 actor（后代继承），手动设置的 actors 优先；仅反映本目录自身状态，子目录的继承状态不在此显示",
		},
		{
			Name:    "plot",
			Type:    conf.TypeString,
			Default: "",
			Help:    "影片简介；设置后该文件夹及子文件夹内的影片 nfo 使用该简介（分维度独立继承，不影响 actors）",
		},
		{
			Name:    "append_file_name_to_plot",
			Type:    conf.TypeBool,
			Default: "false",
			Help:    "将去扩展名的影片文件名追加到 plot（格式：plot-文件名；plot 未设置时直接以文件名为 plot）",
		},
	}
}
```

- [ ] **Step 3: 追加展示测试** `drivers/emby_wrapper/driver_test.go`：

```go
func TestFolderAdditionExposesPlotAndAppend(t *testing.T) {
	d := setup(t)
	if err := d.Rename(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, `{"plot":"P","append_file_name_to_plot":true}`); err != nil {
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
		if fa.Plot != "P" {
			t.Errorf("expected plot=P, got %q", fa.Plot)
		}
		if fa.AppendFileNameToPlot == nil || !*fa.AppendFileNameToPlot {
			t.Error("addition must expose append_file_name_to_plot=true")
		}
		return
	}
	t.Fatal("Movies folder not found")
}
```

- [ ] **Step 4: 全量验证**

```bash
cd /Users/varg247/store/work-store/backend/openlist && /Library/Go/sdk/go1.25.4/bin/go build ./drivers/emby_wrapper ./drivers ./internal/op ./internal/fs ./internal/db ./internal/model && /Library/Go/sdk/go1.25.4/bin/go vet ./drivers/emby_wrapper && /Library/Go/sdk/go1.25.4/bin/go test ./drivers/emby_wrapper/ ./drivers/virtual_file/ ./drivers/cache/ -count=1
```

Expected: 全部 PASS（`go build ./...` 全量会因环境缺失 fuse.h 失败，与本次改动无关）

- [ ] **Step 5: 提交**

```bash
cd /Users/varg247/store/work-store/backend/openlist && git add drivers/emby_wrapper/ && git commit -m "feat(emby_wrapper): expose plot settings in mkdir form and folder addition"
```

---

## Self-Review

**1. Spec coverage:**
- plot 配置 + 继承：Task 1 字段 + Task 2 plot 维度 + Task 3 TestPlotInheritedDimension ✓
- append 拼接格式（plot-文件名；无 plot 用文件名）：Task 3 buildPlot + TestPlotAppendFileName / TestPlotAppendWithoutPlot ✓
- 文件名去扩展名：Task 3 plotFileName（仅去最后一个扩展名，保留 cd 等原始部分）✓
- 分维度继承（B 方案）：Task 2 TestResolveSettingPlotDimension（actors 近处 + plot 继承）✓
- append 三态（显式 false 阻断继承）：Task 1 TestUpsertAppendThreeState + Task 2 TestResolveSettingAppendDimension ✓
- 单独配置触发 nfo：Task 2（任一维度命中非 nil）+ Task 3 TestPlotConfigured ✓
- 与 use_name_as_actor 并存：Task 2 TestResolveSettingPlotWithNameAsActor ✓
- addition/MkdirConfig 展示：Task 4 ✓

**2. Placeholder scan:** 无 TBD；所有代码与测试完整给出；`(*bool)(nil)` 写法明确。

**3. Type consistency:**
- `UpsertEmbyDirSetting(storageID, dirPath, actors, plot string, useNameAsActor, appendFileNameToPlot *bool)`：Task 1 定义，Rename 调用 ✓；既有测试调用点更新为 `(id, path, actors, "", nil, nil)` 形式 ✓
- `buildNFOContent(title, fileName string, setting *model.EmbyDirSetting)`：Task 3 定义，withVirtualNFOs（传 `o.GetName()`）与 virtualNFOForPath（传 `movieObj.GetName()`）一致 ✓
- `wrapObj(obj, path, actors string, useNameAsActor bool, plot string, appendFileNameToPlot *bool, folder bool)`：Task 4 定义，decorate/Get 调用 ✓
- `FolderAddition` 四字段：Task 1 定义，Rename 解析与 GetAddition 展示共用 ✓
- resolveSetting 返回类型不变（`*model.EmbyDirSetting`），合成行 `Actors` 语义不变，既有断言兼容 ✓
