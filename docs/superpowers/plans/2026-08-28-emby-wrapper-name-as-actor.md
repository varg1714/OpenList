# EmbyWrapper 文件夹名即 actor（use_name_as_actor）实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 EmbyWrapper 驱动上新增文件夹级配置 `use_name_as_actor`（bool）：开启后，该文件夹的**直接子文件夹**以其自身名称作为 actor，所有后代继承该 actor；手动 `actors` 设置优先级更高；最近的开启者优先。

**Architecture:** 扩展现有 `model.EmbyDirSetting`（加 `UseNameAsActor bool`）、`FolderAddition`（加 `UseNameAsActor *bool`，指针区分"未提供"与"false"）、`UpsertEmbyDirSetting`（双字段合并语义）、`resolveSetting`（自底向上 walk，命中开启者时合成 actor=开启者之下第一段目录名的 setting）、`decorate`/`Get` 的 addition 展示（own setting 含 use 状态）。MkdirConfig 新增 bool 项。

**Tech Stack:** Go 1.25.4（`/Library/Go/sdk/go1.25.4/bin/go`）、gorm、复用 `internal/op`/`internal/model`/`drivers/virtual_file`。

**Spec:** 用户需求（2026-08-28 确认）：
1. 新增 mkdir 属性 `use_name_as_actor`（bool 类型，`conf.TypeBool` 已存在）
2. 语义：A 开启后，A 的直接子文件夹（A1/A2/A3）actor = 各自名称；孙级（A11/A12）继承，actor 同为 A1（开启者之下第一段目录名）；**A 自身不获得 actor**
3. 手动 `actors` 设置与 name-as-actor 均为有效设置，**距离优先**（用户确认 2026-08-28）：自底向上遇到的第一个有效设置生效；手动设置在更远祖先、use 在近处时，近处 use 优先（如 /A 手动 X + /A/A1 use → /A/A1/A11 的 actor = A11）
4. 多层开启时最近的开启者优先（A 与 A1 都开，A11 的 actor = A11）
5. 重命名 JSON 不带 `use_name_as_actor` 字段时保持原值（`*bool`）；actors 留空只清 actors；两者皆空/关闭才删除行
6. 文件夹 addition（UI 预填）需携带 own setting 的 use 状态
7. 不修改 `text_types` 默认值（用户明确"先不改"）

## Global Constraints

- Go 工具链一律用 `/Library/Go/sdk/go1.25.4/bin/go`
- TDD：先失败测试再实现；每个任务独立提交
- 提交信息沿用仓库风格：`feat(emby_wrapper): ...`
- 与现有驱动行为兼容：`resolveSetting` 返回 `*model.EmbyDirSetting`（nil = 无有效设置），调用方（withVirtualNFOs / virtualNFOForPath）不变
- `UpsertEmbyDirSetting` 签名变化会影响既有测试调用点（db_test.go / setting_test.go / rename_test.go），同步更新

---

### Task 1: 模型与 CRUD 扩展（UseNameAsActor）

**Files:**
- Modify: `internal/model/emby.go`
- Modify: `drivers/emby_wrapper/db.go`
- Test: `drivers/emby_wrapper/db_test.go`

**Interfaces:**
- Consumes: 现有 `EmbyDirSetting`/`GetEmbyDirSetting`
- Produces: `model.EmbyDirSetting{ID, StorageID, DirPath, Actors, UseNameAsActor bool}`、`UpsertEmbyDirSetting(storageID uint, dirPath, actors string, useNameAsActor *bool) error`、`ListEmbyDirSettings(storageID uint) (map[string]model.EmbyDirSetting, error)` —— Task 2/3 依赖

- [ ] **Step 1: 模型加字段** `internal/model/emby.go`

```go
package model

// EmbyDirSetting 某个目录的 Emby 元数据设置。Actors 为空且 UseNameAsActor 为 false 表示未设置。
// UseNameAsActor 开启后，该目录的直接子文件夹以各自名称为 actor（后代继承）；手动 Actors 优先。
type EmbyDirSetting struct {
	ID             uint   `gorm:"primaryKey"`
	StorageID      uint   `gorm:"uniqueIndex:idx_emby_dir_setting"`
	DirPath        string `gorm:"uniqueIndex:idx_emby_dir_setting"`
	Actors         string
	UseNameAsActor bool
}
```

- [ ] **Step 2: 更新 CRUD** `drivers/emby_wrapper/db.go` —— 整体替换为：

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

// UpsertEmbyDirSetting 保存目录设置。
// actors 去空格后为空表示清除 actors；useNameAsActor 为 nil 表示未提供（保持原值）。
// actors 为空且 useNameAsActor 最终为 false 时删除该目录的设置行。
func UpsertEmbyDirSetting(storageID uint, dirPath, actors string, useNameAsActor *bool) error {
	actors = strings.TrimSpace(actors)
	item, err := GetEmbyDirSetting(storageID, dirPath)
	if err != nil {
		return err
	}
	use := false
	if item != nil {
		use = item.UseNameAsActor
	}
	if useNameAsActor != nil {
		use = *useNameAsActor
	}
	if actors == "" && !use {
		return db.GetDb().Where("storage_id = ? AND dir_path = ?", storageID, dirPath).Delete(&model.EmbyDirSetting{}).Error
	}
	if item != nil {
		item.Actors = actors
		item.UseNameAsActor = use
		return db.GetDb().Save(item).Error
	}
	return db.GetDb().Create(&model.EmbyDirSetting{
		StorageID:      storageID,
		DirPath:        dirPath,
		Actors:         actors,
		UseNameAsActor: use,
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

- [ ] **Step 3: 更新既有测试的调用签名** `drivers/emby_wrapper/db_test.go` —— 所有 `UpsertEmbyDirSetting(id, path, actors)` 调用追加第三个参数 `nil`；`TestUpsertEmptyClearsEmbyDirSetting` 中清除调用为 `UpsertEmbyDirSetting(1, "/Movies", "  ", nil)`；`TestListEmbyDirSettings` 断言改为 `m["/a"].Actors != "A"`。

- [ ] **Step 4: 追加 use 语义测试**（追加到 db_test.go）

```go
func TestUpsertUseNameAsActorKeepsActors(t *testing.T) {
	if err := UpsertEmbyDirSetting(1, "/A", "X", boolPtr(true)); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	item, err := GetEmbyDirSetting(1, "/A")
	if err != nil || item == nil {
		t.Fatalf("get: %v %v", item, err)
	}
	if !item.UseNameAsActor || item.Actors != "X" {
		t.Errorf("expected use=true actors=X, got %+v", item)
	}
	// actors 清空不影响 use
	if err := UpsertEmbyDirSetting(1, "/A", "", nil); err != nil {
		t.Fatalf("clear actors: %v", err)
	}
	item, err = GetEmbyDirSetting(1, "/A")
	if err != nil || item == nil {
		t.Fatalf("get after clear actors: %v %v", item, err)
	}
	if !item.UseNameAsActor || item.Actors != "" {
		t.Errorf("use must survive actors clear, got %+v", item)
	}
}

func TestUpsertActorsKeepsUseNameAsActor(t *testing.T) {
	if err := UpsertEmbyDirSetting(1, "/A", "", boolPtr(true)); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// 不带 use 字段（nil）写 actors，use 保持
	if err := UpsertEmbyDirSetting(1, "/A", "Y", nil); err != nil {
		t.Fatalf("set actors: %v", err)
	}
	item, err := GetEmbyDirSetting(1, "/A")
	if err != nil || item == nil {
		t.Fatalf("get: %v %v", item, err)
	}
	if !item.UseNameAsActor || item.Actors != "Y" {
		t.Errorf("use must survive actors write, got %+v", item)
	}
}

func TestUpsertDisableUseNameAsActorDeletesRow(t *testing.T) {
	if err := UpsertEmbyDirSetting(1, "/A", "", boolPtr(true)); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	f := false
	if err := UpsertEmbyDirSetting(1, "/A", "", &f); err != nil {
		t.Fatalf("disable: %v", err)
	}
	item, err := GetEmbyDirSetting(1, "/A")
	if err != nil || item != nil {
		t.Errorf("row must be deleted, got %v %v", item, err)
	}
}

func boolPtr(b bool) *bool { return &b }
```

- [ ] **Step 5: 运行确认通过**

```bash
cd /Users/varg247/store/work-store/backend/openlist && /Library/Go/sdk/go1.25.4/bin/go test ./drivers/emby_wrapper/ -count=1
```

Expected: PASS（注意：setting_test.go / rename_test.go 调用 `UpsertEmbyDirSetting` 的旧签名会编译失败——先在本任务内把它们也更新为传 `nil`，保证编译通过；语义测试在 Task 3 补）

- [ ] **Step 6: 提交**

```bash
cd /Users/varg247/store/work-store/backend/openlist && git add internal/model/emby.go drivers/emby_wrapper/db.go drivers/emby_wrapper/db_test.go drivers/emby_wrapper/setting_test.go drivers/emby_wrapper/rename_test.go && git commit -m "feat(emby_wrapper): add use_name_as_actor field to directory setting with merge semantics"
```

---

### Task 2: FolderAddition / Rename / MkdirConfig / addition 展示

**Files:**
- Modify: `drivers/emby_wrapper/folder.go`
- Modify: `drivers/emby_wrapper/driver.go`
- Test: `drivers/emby_wrapper/rename_test.go`、`drivers/emby_wrapper/driver_test.go`

**Interfaces:**
- Consumes: `UpsertEmbyDirSetting` 新签名、`ListEmbyDirSettings` 新返回类型（Task 1）
- Produces: `FolderAddition{Actors string; UseNameAsActor *bool}`、`wrapObj(obj, path, actors string, useNameAsActor bool, folder bool)`、Rename 解析指针字段、MkdirConfig 双字段 —— Task 3 测试依赖 Rename 设置 use

- [ ] **Step 1: 更新 `folder.go`**

```go
package emby_wrapper

import "github.com/OpenListTeam/OpenList/v4/internal/model"

type FolderAddition struct {
	Actors         string `json:"actors"`
	UseNameAsActor *bool  `json:"use_name_as_actor"`
}

// wrappedObj 将下游对象包装进本驱动的路径命名空间（GetPath 返回本驱动相对路径）。
// 不实现 ObjUnwrap：解包会泄露下游真实路径（如本地驱动的文件系统路径），
// 导致 Rename/Link 的路径拼接错误。
type wrappedObj struct {
	model.Obj
	path string
}

func (w *wrappedObj) GetPath() string {
	return w.path
}

// embyFolder 文件夹对象：附带目录 emby 设置，供 UI 展示与重命名表单预填。
type embyFolder struct {
	model.Obj
	addition FolderAddition
}

func (f *embyFolder) GetAddition() model.Additional {
	return f.addition
}

func wrapObj(obj model.Obj, path, actors string, useNameAsActor bool, folder bool) model.Obj {
	wrapped := &wrappedObj{Obj: obj, path: path}
	if !folder {
		return wrapped
	}
	use := useNameAsActor
	return &embyFolder{Obj: wrapped, addition: FolderAddition{Actors: actors, UseNameAsActor: &use}}
}
```

- [ ] **Step 2: 更新 `driver.go`** 三处：

a) `decorate`（own setting 完整展示）：

```go
// decorate 将下游对象包装进本驱动路径命名空间，并给文件夹附带目录设置（用于 UI 展示与表单预填）。
func (d *EmbyWrapper) decorate(dirPath string, objs []model.Obj) []model.Obj {
	settings, err := ListEmbyDirSettings(d.ID)
	if err != nil {
		utils.Log.Warnf("emby wrapper: list dir settings: %+v", err)
		settings = map[string]model.EmbyDirSetting{}
	}
	out := make([]model.Obj, len(objs))
	for i, o := range objs {
		p := stdpath.Join(dirPath, o.GetName())
		s, ok := settings[p]
		actors, use := "", false
		if ok {
			actors, use = s.Actors, s.UseNameAsActor
		}
		out[i] = wrapObj(o, p, actors, use, o.IsDir())
	}
	return out
}
```

b) `Get` 的 addition 填充：

```go
	actors := ""
	use := false
	if obj.IsDir() {
		if item, e := GetEmbyDirSetting(d.ID, path); e != nil {
			utils.Log.Warnf("emby wrapper: get dir setting %s: %+v", path, e)
		} else if item != nil {
			actors, use = item.Actors, item.UseNameAsActor
		}
	}
	return wrapObj(obj, path, actors, use, obj.IsDir()), nil
```

c) `Rename` 与 `MkdirConfig`：

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
			Help:    "开启后该文件夹的直接子文件夹以各自名称为 actor（后代继承），手动设置的 actors 优先",
		},
	}
}

func (d *EmbyWrapper) Rename(ctx context.Context, srcObj model.Obj, newName string) error {
	if !srcObj.IsDir() {
		return errors.New("emby wrapper driver does not support renaming files")
	}
	var req FolderAddition
	if err := utils.Json.UnmarshalFromString(newName, &req); err != nil {
		return errors.Wrap(err, "invalid folder emby setting")
	}
	return UpsertEmbyDirSetting(d.ID, srcObj.GetPath(), req.Actors, req.UseNameAsActor)
}
```

- [ ] **Step 3: 追加测试** `drivers/emby_wrapper/rename_test.go`：

```go
func TestRenameEnableAndDisableUseNameAsActor(t *testing.T) {
	d := setup(t)
	objs, err := d.List(context.Background(), &model.Object{Name: "Root", Path: "/", IsFolder: true}, model.ListArgs{})
	if err != nil {
		t.Fatalf("list root: %+v", err)
	}
	var movies model.Obj
	for _, o := range objs {
		if o.GetName() == "Movies" {
			movies = o
		}
	}
	if movies == nil {
		t.Fatal("Movies folder not found")
	}
	if err := d.Rename(context.Background(), movies, `{"use_name_as_actor":true}`); err != nil {
		t.Fatalf("enable: %+v", err)
	}
	item, err := getSettingForTest(d, "/Movies")
	if err != nil || item == nil || !item.UseNameAsActor {
		t.Fatalf("expected use_name_as_actor enabled, got %v %v", item, err)
	}
	if err := d.Rename(context.Background(), movies, `{"use_name_as_actor":false}`); err != nil {
		t.Fatalf("disable: %+v", err)
	}
	item, err = getSettingForTest(d, "/Movies")
	if err != nil || item != nil {
		t.Errorf("expected row deleted after disable, got %v %v", item, err)
	}
}

func TestRenameWithoutUseFieldKeepsIt(t *testing.T) {
	d := setup(t)
	movies := &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}
	if err := d.Rename(context.Background(), movies, `{"use_name_as_actor":true}`); err != nil {
		t.Fatalf("enable: %+v", err)
	}
	// 只改 actors，不带 use 字段：use 必须保持
	if err := d.Rename(context.Background(), movies, `{"actors":"A"}`); err != nil {
		t.Fatalf("set actors: %+v", err)
	}
	item, err := getSettingForTest(d, "/Movies")
	if err != nil || item == nil {
		t.Fatalf("get: %v %v", item, err)
	}
	if !item.UseNameAsActor || item.Actors != "A" {
		t.Errorf("use must survive actors-only rename, got %+v", item)
	}
}
```

- [ ] **Step 4: 追加 addition 展示测试** `drivers/emby_wrapper/driver_test.go`：

```go
func TestFolderAdditionExposesUseNameAsActor(t *testing.T) {
	d := setup(t)
	if err := d.Rename(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, `{"use_name_as_actor":true}`); err != nil {
		t.Fatalf("enable: %+v", err)
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
		if fa.UseNameAsActor == nil || !*fa.UseNameAsActor {
			t.Error("addition must expose use_name_as_actor=true")
		}
		return
	}
	t.Fatal("Movies folder not found")
}
```

- [ ] **Step 5: 运行确认通过**

```bash
cd /Users/varg247/store/work-store/backend/openlist && /Library/Go/sdk/go1.25.4/bin/go test ./drivers/emby_wrapper/ -count=1
```

Expected: PASS

- [ ] **Step 6: 提交**

```bash
cd /Users/varg247/store/work-store/backend/openlist && git add drivers/emby_wrapper/ && git commit -m "feat(emby_wrapper): expose use_name_as_actor in mkdir form, rename and folder addition"
```

---

### Task 3: resolveSetting 名称即 actor 解析

**Files:**
- Modify: `drivers/emby_wrapper/setting.go`
- Test: `drivers/emby_wrapper/setting_test.go`

**Interfaces:**
- Consumes: `GetEmbyDirSetting`（Task 1）、`UpsertEmbyDirSetting`（Task 1）、`Rename`（Task 2，测试用）
- Produces: `resolveSetting(dirPath) (*model.EmbyDirSetting, error)` 扩展语义：**距离优先**（自底向上第一个有效设置生效，含手动 actors 与 use_name_as_actor 开启者；用户确认混合场景下近处优先）；命中开启者时返回合成 setting（`Actors` = 开启者之下第一段目录名，`DirPath` = 开启者路径仅作溯源）—— withVirtualNFOs / virtualNFOForPath 无需改动

- [ ] **Step 1: 写失败测试**（追加到 setting_test.go）

```go
func TestResolveSettingUseNameAsActor(t *testing.T) {
	d := newTestWrapper()
	d.ID = 1
	// A 开启 use_name_as_actor
	if err := UpsertEmbyDirSetting(d.ID, "/A", "", boolPtr(true)); err != nil {
		t.Fatalf("enable on A: %v", err)
	}
	// 直接子文件夹：actor = 自身名
	item, err := d.resolveSetting("/A/A1")
	if err != nil || item == nil {
		t.Fatalf("expected A1 name-as-actor, got %v %v", item, err)
	}
	if item.Actors != "A1" {
		t.Errorf("expected actors=A1, got %q", item.Actors)
	}
	// 孙级继承：actor 同为 A1
	item, err = d.resolveSetting("/A/A1/A11")
	if err != nil || item == nil {
		t.Fatalf("expected A11 inherited actor, got %v %v", item, err)
	}
	if item.Actors != "A1" {
		t.Errorf("expected inherited actors=A1, got %q", item.Actors)
	}
	// 更深的层级同样继承 A1
	item, err = d.resolveSetting("/A/A1/A11/A111")
	if err != nil || item == nil {
		t.Fatalf("expected deep inherited actor, got %v %v", item, err)
	}
	if item.Actors != "A1" {
		t.Errorf("expected deep actors=A1, got %q", item.Actors)
	}
	// 其他直接子文件夹：自身名
	item, err = d.resolveSetting("/A/A2")
	if err != nil || item == nil {
		t.Fatalf("expected A2 name-as-actor, got %v %v", item, err)
	}
	if item.Actors != "A2" {
		t.Errorf("expected actors=A2, got %q", item.Actors)
	}
	// 开启者自身不获得 actor
	item, err = d.resolveSetting("/A")
	if err != nil || item != nil {
		t.Errorf("enabler itself must have no actor, got %v %v", item, err)
	}
}

func TestResolveSettingManualActorsWin(t *testing.T) {
	d := newTestWrapper()
	d.ID = 1
	if err := UpsertEmbyDirSetting(d.ID, "/A", "", boolPtr(true)); err != nil {
		t.Fatalf("enable on A: %v", err)
	}
	if err := UpsertEmbyDirSetting(d.ID, "/A/A1", "手动演员", nil); err != nil {
		t.Fatalf("manual on A1: %v", err)
	}
	item, err := d.resolveSetting("/A/A1")
	if err != nil || item == nil {
		t.Fatalf("expected manual actors, got %v %v", item, err)
	}
	if item.Actors != "手动演员" {
		t.Errorf("manual actors must win, got %q", item.Actors)
	}
	// 手动设置继承给子树
	item, err = d.resolveSetting("/A/A1/A11")
	if err != nil || item == nil {
		t.Fatalf("expected inherited manual actors, got %v %v", item, err)
	}
	if item.Actors != "手动演员" {
		t.Errorf("manual actors must inherit, got %q", item.Actors)
	}
	// 未被手动覆盖的兄弟分支仍用名称
	item, err = d.resolveSetting("/A/A2")
	if err != nil || item == nil {
		t.Fatalf("expected A2 name-as-actor, got %v %v", item, err)
	}
	if item.Actors != "A2" {
		t.Errorf("expected actors=A2, got %q", item.Actors)
	}
}

func TestResolveSettingNearestEnablerWins(t *testing.T) {
	d := newTestWrapper()
	d.ID = 1
	if err := UpsertEmbyDirSetting(d.ID, "/A", "", boolPtr(true)); err != nil {
		t.Fatalf("enable on A: %v", err)
	}
	if err := UpsertEmbyDirSetting(d.ID, "/A/A1", "", boolPtr(true)); err != nil {
		t.Fatalf("enable on A1: %v", err)
	}
	// A 与 A1 都开启：A11 使用最近的开启者 A1 -> actor = A11
	item, err := d.resolveSetting("/A/A1/A11")
	if err != nil || item == nil {
		t.Fatalf("expected nearest enabler actor, got %v %v", item, err)
	}
	if item.Actors != "A11" {
		t.Errorf("expected actors=A11 (nearest enabler), got %q", item.Actors)
	}
	// A1 自身：A1 开启但自身不获得 -> 继续向上命中 A -> actor = A1
	item, err = d.resolveSetting("/A/A1")
	if err != nil || item == nil {
		t.Fatalf("expected A1 from outer enabler, got %v %v", item, err)
	}
	if item.Actors != "A1" {
		t.Errorf("expected actors=A1 (outer enabler), got %q", item.Actors)
	}
}
```

- [ ] **Step 2: 运行确认失败**

```bash
cd /Users/varg247/store/work-store/backend/openlist && /Library/Go/sdk/go1.25.4/bin/go test ./drivers/emby_wrapper/ -run 'TestResolveSettingUseNameAsActor|TestResolveSettingManualActorsWin|TestResolveSettingNearestEnablerWins' -count=1
```

Expected: FAIL（resolveSetting 尚不识别 use_name_as_actor）

- [ ] **Step 3: 实现** `drivers/emby_wrapper/setting.go` 整体替换：

```go
package emby_wrapper

import (
	stdpath "path"
	"strings"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

// resolveSetting 返回 dirPath 生效的目录设置（自身或最近祖先）。
// 距离优先：自底向上遇到的第一个有效设置即生效（手动 actors 或 use_name_as_actor 开启者，
// 用户确认的语义：近处设置优先于祖先手动设置）。
// use_name_as_actor 开启者自身不获得 actor（配置只作用于其直接子文件夹及后代）；
// 命中开启者时返回合成 setting，Actors = 开启者之下第一段目录名。
func (d *EmbyWrapper) resolveSetting(dirPath string) (*model.EmbyDirSetting, error) {
	dirPath = utils.FixAndCleanPath(dirPath)
	origin := dirPath
	for {
		item, err := GetEmbyDirSetting(d.ID, dirPath)
		if err != nil {
			return nil, err
		}
		if item != nil {
			if strings.TrimSpace(item.Actors) != "" {
				return item, nil
			}
			if item.UseNameAsActor && !utils.PathEqual(dirPath, origin) {
				rel := strings.TrimPrefix(origin, dirPath)
				rel = strings.TrimPrefix(rel, "/")
				if idx := strings.Index(rel, "/"); idx != -1 {
					rel = rel[:idx]
				}
				if rel != "" {
					return &model.EmbyDirSetting{
						StorageID:      d.ID,
						DirPath:        dirPath,
						Actors:         rel,
						UseNameAsActor: false,
					}, nil
				}
			}
		}
		if utils.PathEqual(dirPath, "/") {
			return nil, nil
		}
		dirPath = stdpath.Dir(dirPath)
	}
}
```

- [ ] **Step 4: 运行确认通过**

```bash
cd /Users/varg247/store/work-store/backend/openlist && /Library/Go/sdk/go1.25.4/bin/go test ./drivers/emby_wrapper/ -count=1
```

Expected: PASS（含既有继承测试 TestResolveSettingInheritsAncestor）

- [ ] **Step 5: 提交**

```bash
cd /Users/varg247/store/work-store/backend/openlist && git add drivers/emby_wrapper/setting.go drivers/emby_wrapper/setting_test.go && git commit -m "feat(emby_wrapper): resolve folder name as actor with manual priority"
```

---

### Task 4: List/Get 集成 + 端到端 + 全量验证

**Files:**
- Test: `drivers/emby_wrapper/nfo_test.go`、`drivers/emby_wrapper/link_test.go`（或新文件 `name_actor_test.go`）

**Interfaces:**
- Consumes: Task 1-3 全部；`setup`/`writeDownstreamFile`/`writeDownstreamDir`/`getSettingForTest`/`boolPtr`（`boolPtr` 定义在 db_test.go 包内，外部测试包不可见——本任务测试需自备或改用 Rename 设置）

- [ ] **Step 1: 写失败测试** `drivers/emby_wrapper/name_actor_test.go`（外部测试包，用 Rename 设置，避免跨包访问 boolPtr）

```go
package emby_wrapper_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/http_range"
)

// TestNameAsActorListAndContent：A 开启 use_name_as_actor 后，
// 直接子文件夹中的影片生成 nfo，actor = 子文件夹名，内容可经 Link 读取。
func TestNameAsActorListAndContent(t *testing.T) {
	d := setup(t)
	// /Movies 开启，直接子文件夹 /Movies/A1 放影片
	if err := writeDownstreamDir(t, "/Movies/A1"); err != nil {
		t.Fatalf("mkdir A1: %v", err)
	}
	if err := writeDownstreamFile(t, "/Movies/A1/BBB.mp4", "x"); err != nil {
		t.Fatalf("write movie: %v", err)
	}
	if err := d.Rename(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, `{"use_name_as_actor":true}`); err != nil {
		t.Fatalf("enable: %+v", err)
	}
	objs, err := d.List(context.Background(), &model.Object{Name: "A1", Path: "/Movies/A1", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list A1: %+v", err)
	}
	if got := names(objs); len(got) != 2 {
		t.Fatalf("expected [BBB.mp4 BBB.nfo], got %v", got)
	}
	// 读取 nfo 内容验证 actor
	obj, err := d.Get(context.Background(), "/Movies/A1/BBB.nfo")
	if err != nil {
		t.Fatalf("get nfo: %+v", err)
	}
	link, err := d.Link(context.Background(), obj, model.LinkArgs{})
	if err != nil {
		t.Fatalf("link: %+v", err)
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
	got := string(body)
	if !strings.Contains(got, "BBB") {
		t.Errorf("nfo must contain title BBB, got %s", got)
	}
	if !strings.Contains(got, "<name>A1</name>") {
		t.Errorf("nfo must contain actor A1, got %s", got)
	}
}

// TestNameAsActorSubtree：孙级目录继承最近开启者的直接子文件夹名。
func TestNameAsActorSubtree(t *testing.T) {
	d := setup(t)
	if err := writeDownstreamDir(t, "/Movies/A1/A11"); err != nil {
		t.Fatalf("mkdir A11: %v", err)
	}
	if err := writeDownstreamFile(t, "/Movies/A1/A11/CCC.mkv", "x"); err != nil {
		t.Fatalf("write movie: %v", err)
	}
	if err := d.Rename(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, `{"use_name_as_actor":true}`); err != nil {
		t.Fatalf("enable: %+v", err)
	}
	objs, err := d.List(context.Background(), &model.Object{Name: "A11", Path: "/Movies/A1/A11", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list A11: %+v", err)
	}
	if got := names(objs); len(got) != 2 {
		t.Fatalf("expected [CCC.mkv CCC.nfo], got %v", got)
	}
}

// TestNameAsActorNotOnEnablerItself：开启者自身目录不生成 nfo。
func TestNameAsActorNotOnEnablerItself(t *testing.T) {
	d := setup(t)
	if err := d.Rename(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, `{"use_name_as_actor":true}`); err != nil {
		t.Fatalf("enable: %+v", err)
	}
	objs, err := d.List(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list Movies: %+v", err)
	}
	if got := names(objs); len(got) != 1 || got[0] != "AAA.mkv" {
		t.Errorf("enabler itself must have no nfo, got %v", got)
	}
}

// TestNameAsActorManualActorsWin：手动 actors 覆盖名称即 actor。
func TestNameAsActorManualActorsWin(t *testing.T) {
	d := setup(t)
	if err := writeDownstreamDir(t, "/Movies/A1"); err != nil {
		t.Fatalf("mkdir A1: %v", err)
	}
	if err := writeDownstreamFile(t, "/Movies/A1/BBB.mp4", "x"); err != nil {
		t.Fatalf("write movie: %v", err)
	}
	if err := d.Rename(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, `{"use_name_as_actor":true}`); err != nil {
		t.Fatalf("enable: %+v", err)
	}
	if err := d.Rename(context.Background(), &model.Object{Name: "A1", Path: "/Movies/A1", IsFolder: true}, `{"actors":"手动演员"}`); err != nil {
		t.Fatalf("manual on A1: %+v", err)
	}
	obj, err := d.Get(context.Background(), "/Movies/A1/BBB.nfo")
	if err != nil {
		t.Fatalf("get nfo: %+v", err)
	}
	link, err := d.Link(context.Background(), obj, model.LinkArgs{})
	if err != nil {
		t.Fatalf("link: %+v", err)
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
	if !strings.Contains(string(body), "<name>手动演员</name>") {
		t.Errorf("manual actors must win, got %s", body)
	}
}
```

- [ ] **Step 2: 运行确认通过**

```bash
cd /Users/varg247/store/work-store/backend/openlist && /Library/Go/sdk/go1.25.4/bin/go test ./drivers/emby_wrapper/ -count=1
```

Expected: PASS（Task 3 已实现解析逻辑，本任务为集成验证）

- [ ] **Step 3: 全量验证**

```bash
cd /Users/varg247/store/work-store/backend/openlist && /Library/Go/sdk/go1.25.4/bin/go build ./drivers/emby_wrapper ./drivers ./internal/op ./internal/fs ./internal/db ./internal/model && /Library/Go/sdk/go1.25.4/bin/go vet ./drivers/emby_wrapper && /Library/Go/sdk/go1.25.4/bin/go test ./drivers/emby_wrapper/ ./drivers/virtual_file/ ./drivers/cache/ -count=1
```

Expected: 全部 PASS（`go build ./...` 全量会因环境缺失 fuse.h 失败，与本次改动无关）

- [ ] **Step 4: 提交**

```bash
cd /Users/varg247/store/work-store/backend/openlist && git add drivers/emby_wrapper/ && git commit -m "test(emby_wrapper): integration tests for folder-name-as-actor"
```

---

## Self-Review

**1. Spec coverage:**
- bool mkdir 属性：Task 2 MkdirConfig `conf.TypeBool` ✓
- 直接子文件夹 actor=自身名：Task 3 resolveSetting（开启者之下第一段）✓
- 孙级继承（A11/A12 actor=A1）：Task 3 + Task 4 TestNameAsActorSubtree ✓
- 开启者自身无 actor：Task 3 + Task 4 TestNameAsActorNotOnEnablerItself ✓
- 手动优先（含子树）：Task 3 TestResolveSettingManualActorsWin + Task 4 ✓
- 最近开启者优先：Task 3 TestResolveSettingNearestEnablerWins ✓
- 不带 use 字段保持原值：Task 1（*bool + merge 语义）+ Task 2 TestRenameWithoutUseFieldKeepsIt ✓
- actors 留空只清 actors 不清 use：Task 1 TestUpsertUseNameAsActorKeepsActors ✓
- UI 预填：Task 2 addition 展示 + TestFolderAdditionExposesUseNameAsActor ✓
- 不动 text_types 默认值 ✓（Global Constraints 第 7 条）

**2. Placeholder scan:** 无 TBD；所有测试代码完整给出；`boolPtr` 在包内（db_test.go），外部包测试统一用 Rename 避免跨包引用（Task 4 已注明）。

**3. Type consistency:**
- `UpsertEmbyDirSetting(storageID uint, dirPath, actors string, useNameAsActor *bool) error`：Task 1 定义，Task 2 Rename 使用 ✓
- `ListEmbyDirSettings(storageID) (map[string]model.EmbyDirSetting, error)`：Task 1 定义，Task 2 decorate 使用 ✓
- `wrapObj(obj, path, actors string, useNameAsActor bool, folder bool)`：Task 2 定义，decorate/Get 调用 ✓
- `FolderAddition{Actors string; UseNameAsActor *bool}`：Task 2 定义，Rename 解析 + GetAddition 展示共用 ✓
- `resolveSetting` 返回值类型不变（`*model.EmbyDirSetting`），调用方零改动 ✓
- 既有测试调用点更新：Task 1 Step 3/5 覆盖（db_test.go / setting_test.go / rename_test.go）✓
