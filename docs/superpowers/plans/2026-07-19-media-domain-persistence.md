# 虚拟媒体领域模型与持久化实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 增量加入稳定 Code 驱动的影片主体、文件分片和类型化运行时身份，不改变现有三驱动行为或删除旧表。

**Architecture:** 新模型放在独立 `media.go`，新 repository 放在独立 `media.go`，避免继续扩大旧 `film.go`。通过纯函数统一 Code、文件名和 NFO 标题投影；`EmbyFileObj` 增加显式身份字段，旧字段暂时保留以维持编译。

**Tech Stack:** Go、GORM、SQLite 测试数据库、现有 `model.StringArray` JSON serializer。

## Global Constraints

- 新模型必须是增量的；本计划不得删除 `Film`、`MagnetCache` 或旧 CRUD。
- `StorageID` 使用 `uint`，与 `model.Storage.ID` 一致。
- 运行时公共文件名只能由 `BuildMediaFileName` 生成。
- 不得使用 `as any`、忽略错误或引入新依赖。
- 所有新增 repository 行为必须先有 SQLite 失败测试。

---

### Task 1: Code、文件名和标题投影

**Files:**
- Create: `internal/model/media.go`
- Create: `internal/model/media_test.go`

**Interfaces:**
- Consumes: standard library `fmt`, `regexp`, `strings`.
- Produces:
  - `NormalizeMediaCode(source, value string) (string, error)`
  - `BuildMediaFileName(code string, partIndex, partCount int) (string, error)`
  - `BuildMediaTitle(code, rawTitle, translatedTitle string) string`

- [ ] **Step 1: 写规范化和投影失败测试**

```go
func TestNormalizeMediaCode(t *testing.T) {
	tests := []struct{ source, input, want string }{
		{"javdb", "abp-123", "ABP-123"},
		{"fc2", "1234567", "FC2-PPV-1234567"},
		{"fc2", "fc2-ppv-1234567", "FC2-PPV-1234567"},
		{"pornhub", "ph5fabc", "ph5fabc"},
	}
	for _, tt := range tests {
		got, err := NormalizeMediaCode(tt.source, tt.input)
		if err != nil || got != tt.want { t.Fatalf("%s/%s = %q, %v", tt.source, tt.input, got, err) }
	}
}

func TestBuildMediaFileName(t *testing.T) {
	if got, _ := BuildMediaFileName("ABP-123", 1, 1); got != "ABP-123.mp4" { t.Fatal(got) }
	if got, _ := BuildMediaFileName("ABP-123", 2, 3); got != "ABP-123-cd2.mp4" { t.Fatal(got) }
	if _, err := BuildMediaFileName("../ABP-123", 1, 1); err == nil { t.Fatal("unsafe code accepted") }
	if _, err := BuildMediaFileName("ABP-123", 0, 2); err == nil { t.Fatal("invalid part accepted") }
}

func TestBuildMediaTitle(t *testing.T) {
	if got := BuildMediaTitle("ABP-123", "原题", "译题"); got != "ABP-123 译题" { t.Fatal(got) }
	if got := BuildMediaTitle("ABP-123", "原题", ""); got != "ABP-123 原题" { t.Fatal(got) }
	if got := BuildMediaTitle("ABP-123", "", ""); got != "ABP-123" { t.Fatal(got) }
}
```

- [ ] **Step 2: 运行测试并确认失败**

Run:

```bash
/Library/Go/sdk/go1.25.4/bin/go test ./internal/model -run 'TestNormalizeMediaCode|TestBuildMedia' -v
```

Expected: FAIL，提示函数未定义。

- [ ] **Step 3: 实现最小纯函数**

```go
var javCodePattern = regexp.MustCompile(`^[A-Z0-9]+(?:[-_.][A-Z0-9]+)+$`)
var fc2CodePattern = regexp.MustCompile(`^FC2-PPV-[0-9]+$`)

func NormalizeMediaCode(source, value string) (string, error) {
	value = strings.TrimSpace(value)
	switch source {
	case "javdb":
		value = strings.ToUpper(value)
		if !javCodePattern.MatchString(value) { return "", fmt.Errorf("invalid javdb code: %q", value) }
	case "fc2":
		value = strings.ToUpper(value)
		if !strings.HasPrefix(value, "FC2-PPV-") { value = "FC2-PPV-" + value }
		if !fc2CodePattern.MatchString(value) { return "", fmt.Errorf("invalid fc2 code: %q", value) }
	case "pornhub":
		if value == "" || strings.ContainsAny(value, `/\\`) { return "", fmt.Errorf("invalid pornhub view key: %q", value) }
	default:
		return "", fmt.Errorf("unsupported media source: %q", source)
	}
	return value, nil
}

func BuildMediaFileName(code string, partIndex, partCount int) (string, error) {
	if code == "" || strings.ContainsAny(code, `/\\`) { return "", fmt.Errorf("unsafe media code: %q", code) }
	if partCount < 1 || partIndex < 1 || partIndex > partCount { return "", fmt.Errorf("invalid part %d/%d", partIndex, partCount) }
	if partCount == 1 { return code + ".mp4", nil }
	return fmt.Sprintf("%s-cd%d.mp4", code, partIndex), nil
}

func BuildMediaTitle(code, rawTitle, translatedTitle string) string {
	title := strings.TrimSpace(translatedTitle)
	if title == "" { title = strings.TrimSpace(rawTitle) }
	if title == "" { return code }
	return code + " " + title
}
```

- [ ] **Step 4: 格式化并运行测试**

Run:

```bash
/Library/Go/sdk/go1.25.4/bin/gofmt -w internal/model/media.go internal/model/media_test.go
/Library/Go/sdk/go1.25.4/bin/go test ./internal/model -run 'TestNormalizeMediaCode|TestBuildMedia' -v
```

Expected: PASS。

- [ ] **Step 5: Commit（仅在用户明确要求提交时）**

```bash
git add internal/model/media.go internal/model/media_test.go
git commit -m "feat(media): add stable identity projections"
```

### Task 2: 新领域模型和约束

**Files:**
- Modify: `internal/model/media.go`
- Modify: `internal/model/media_test.go`

**Interfaces:**
- Consumes: `model.StringArray`.
- Produces: `FilmWork`, `FilmFile`, `SourceMagnet`, `CloudFileCache`, `FilmFileWithWork`, `MagnetFileEntry`.

- [ ] **Step 1: 写模型约束迁移测试**

测试使用 SQLite 内存库，AutoMigrate 新模型后验证：

```go
func TestMediaModelConstraints(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:media-model?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil { t.Fatal(err) }
	if err := db.AutoMigrate(&FilmWork{}, &FilmFile{}, &SourceMagnet{}, &CloudFileCache{}); err != nil { t.Fatal(err) }
	work := FilmWork{StorageID: 1, Source: "javdb", Code: "ABP-123", SourceRef: "v/1", PrimaryDir: "演员A"}
	if err := db.Create(&work).Error; err != nil { t.Fatal(err) }
	duplicate := work; duplicate.ID = 0
	if err := db.Create(&duplicate).Error; err == nil { t.Fatal("duplicate work accepted") }
	if err := db.Create(&FilmFile{WorkID: work.ID, PartIndex: 1, PartCount: 1}).Error; err != nil { t.Fatal(err) }
}
```

- [ ] **Step 2: 运行测试并确认失败**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./internal/model -run TestMediaModelConstraints -v`

Expected: FAIL，模型未定义。

- [ ] **Step 3: 定义模型**

核心结构必须包含以下字段和标签：

```go
type FilmWork struct {
	ID uint `gorm:"primaryKey"`
	StorageID uint `gorm:"not null;uniqueIndex:idx_media_work_identity"`
	Source string `gorm:"not null;uniqueIndex:idx_media_work_identity"`
	Code string `gorm:"not null;uniqueIndex:idx_media_work_identity"`
	SourceRef string `gorm:"not null"`
	SourceURL string `gorm:"index"`
	PrimaryDir string `gorm:"not null;index"`
	RawTitle string
	TranslatedTitle string
	Synopsis string
	ImageURL string
	ReleaseDate time.Time
	Actors StringArray `gorm:"type:json;serializer:json"`
	Tags StringArray `gorm:"type:json;serializer:json"`
	TranslationStatus string `gorm:"index"`
	TranslationAttempts uint
	TranslationNextRetryAt *time.Time `gorm:"index"`
	TranslationLastError string
	TranslationVersion uint
	SynopsisScanAt *time.Time
	SynopsisNextRetryAt *time.Time `gorm:"index"`
	SynopsisLastError string
	SynopsisExcluded bool
	ReleaseScanAt *time.Time
	ReleaseNextRetryAt *time.Time `gorm:"index"`
	ReleaseLastError string
	ActorScanAt *time.Time
	ActorNextRetryAt *time.Time `gorm:"index"`
	ActorLastError string
	TagScanAt *time.Time
	TagNextRetryAt *time.Time `gorm:"index"`
	TagLastError string
	TagVersion uint
	MagnetScanAt *time.Time
	MagnetNextRetryAt *time.Time
	MagnetLastError string
	SampleImageCount int
	SampleImageComplete bool
	SampleImageScanAt *time.Time
	DMMPosterStatus string `gorm:"index"`
	DMMPosterScanAt *time.Time
	SubtitleScanAt *time.Time
	SubtitleNextRetryAt *time.Time
	SubtitleLastError string
	MetadataVersion uint `gorm:"not null;default:1"`
	NfoVersion uint `gorm:"not null;default:0"`
	NfoLastError string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type FilmFile struct {
	ID uint `gorm:"primaryKey"`
	WorkID uint `gorm:"not null;uniqueIndex:idx_media_file_part"`
	PartIndex int `gorm:"not null;uniqueIndex:idx_media_file_part;check:part_index >= 1"`
	PartCount int `gorm:"not null;check:part_count >= 1"`
	SourcePath string
	SourceSize int64
	SourceFileFingerprint string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type MagnetFileEntry struct { Path string `json:"path"`; Size int64 `json:"size"`; Fingerprint string `json:"fingerprint"` }
type MagnetFileManifest []MagnetFileEntry

type SourceMagnet struct {
	ID uint `gorm:"primaryKey"`; WorkID uint `gorm:"not null;uniqueIndex:idx_source_magnet_fingerprint"`
	MagnetURI string `gorm:"not null"`; Fingerprint string `gorm:"not null;uniqueIndex:idx_source_magnet_fingerprint"`
	Provider string; Priority int; Selected bool `gorm:"index"`; Subtitle bool
	FileManifest MagnetFileManifest `gorm:"type:json;serializer:json"`
	ScanAt *time.Time; LastError string; CreatedAt time.Time; UpdatedAt time.Time
}

type CloudFileCache struct {
	ID uint `gorm:"primaryKey"`; FilmFileID uint `gorm:"not null;uniqueIndex:idx_cloud_file_identity"`
	StorageIdentity string `gorm:"not null;uniqueIndex:idx_cloud_file_identity;uniqueIndex:idx_cloud_remote_identity"`
	Provider string `gorm:"not null"`; RemoteFileID string `gorm:"not null;uniqueIndex:idx_cloud_remote_identity"`
	ProviderOptions map[string]string `gorm:"type:json;serializer:json"`
	MagnetFingerprint string `gorm:"not null;index"`; VerifiedAt *time.Time; CreatedAt time.Time; UpdatedAt time.Time
}

type FilmFileWithWork struct { FilmFile; Work FilmWork `gorm:"foreignKey:WorkID"` }
```

- [ ] **Step 4: 运行模型测试**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./internal/model -run TestMediaModelConstraints -v`

Expected: PASS；SQLite 唯一约束拒绝重复 work/file/cache。

- [ ] **Step 5: Commit（仅在用户明确要求提交时）**

```bash
git add internal/model/media.go internal/model/media_test.go
git commit -m "feat(media): define work file and cache models"
```

### Task 3: 影片主体与文件 repository

**Files:**
- Create: `internal/db/media.go`
- Create: `internal/db/media_test.go`

**Interfaces:**
- Consumes: Task 2 models.
- Produces: master plan 中声明的七个 repository 函数。

- [ ] **Step 1: 建立 SQLite 测试夹具并写失败测试**

覆盖以下行为：

```go
func TestUpsertDiscoveredWorkPreservesPrimaryDirAndState(t *testing.T)
func TestEnsureSingleFilmFileIsIdempotent(t *testing.T)
func TestListFilmWorksScopesStorageSourceAndDir(t *testing.T)
func TestGetFilmFileWithWorkPreloadsParent(t *testing.T)
func TestReplaceFilmFilesRejectsNonContiguousParts(t *testing.T)
```

`TestUpsert...` 必须先创建成功翻译状态，再用另一个目录和空状态 rediscover，断言 `PrimaryDir` 与状态未变化，但 `RawTitle/ImageURL/SourceURL` 更新。

- [ ] **Step 2: 运行测试并确认失败**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./internal/db -run 'TestUpsertDiscoveredWork|TestEnsureSingleFilmFile|TestListFilmWorks|TestGetFilmFileWithWork|TestReplaceFilmFiles' -v`

Expected: FAIL，repository 函数未定义。

- [ ] **Step 3: 实现 discovery upsert 与查询**

`UpsertDiscoveredWork` 必须使用明确更新列，禁止 `UpdateAll`：

```go
func UpsertDiscoveredWork(work *model.FilmWork) error {
	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name:"storage_id"},{Name:"source"},{Name:"code"}},
		DoUpdates: clause.AssignmentColumns([]string{"source_ref","source_url","raw_title","image_url","release_date","updated_at"}),
	}).Create(work).Error
}
```

实现 `EnsureSingleFilmFile` 时，仅在 work 没有文件时创建 `{PartIndex:1, PartCount:1}`；已存在多分片时返回明确错误，不得覆盖拓扑。

增加：

```go
func ReplaceFilmFiles(workID uint, files []model.FilmFile) error
```

它在事务内验证 `PartIndex=1..N`、所有 `PartCount=N` 后替换；供首次暴露前的 FC2 权威拓扑使用。

- [ ] **Step 4: 运行 repository 测试**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./internal/db -run 'TestUpsertDiscoveredWork|TestEnsureSingleFilmFile|TestListFilmWorks|TestGetFilmFileWithWork|TestReplaceFilmFiles' -v`

Expected: PASS。

- [ ] **Step 5: Commit（仅在用户明确要求提交时）**

```bash
git add internal/db/media.go internal/db/media_test.go
git commit -m "feat(media): add work and file repositories"
```

### Task 4: 类型化 EmbyFileObj 与新转换路径

**Files:**
- Modify: `internal/model/object.go:107`
- Create: `drivers/virtual_file/media.go`
- Create: `drivers/virtual_file/media_test.go`

**Interfaces:**
- Consumes: `FilmFileWithWork`, `BuildMediaFileName`, `BuildMediaTitle`.
- Produces:
  - `ConvertMediaFileToEmbyFile(item model.FilmFileWithWork) (model.EmbyFileObj, error)`
  - `WrapMediaFiles(files []model.EmbyFileObj) []model.EmbyFileDirWrapper`

- [ ] **Step 1: 写转换失败测试**

```go
func TestConvertMediaFileToEmbyFileCarriesTypedIdentity(t *testing.T) {
	item := model.FilmFileWithWork{FilmFile:model.FilmFile{ID:9,WorkID:3,PartIndex:1,PartCount:1}, Work:model.FilmWork{ID:3,Code:"ABP-123",RawTitle:"原题",TranslatedTitle:"译题",PrimaryDir:"演员A"}}
	got, err := ConvertMediaFileToEmbyFile(item)
	if err != nil { t.Fatal(err) }
	if got.Name != "ABP-123.mp4" || got.Title != "ABP-123 译题" || got.ID != "9" { t.Fatalf("%+v", got) }
	if got.WorkID != 3 || got.FilmFileID != 9 || got.Code != "ABP-123" { t.Fatalf("identity lost: %+v", got) }
}
```

- [ ] **Step 2: 运行测试并确认失败**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/virtual_file -run TestConvertMediaFileToEmbyFileCarriesTypedIdentity -v`

Expected: FAIL，字段/函数未定义。

- [ ] **Step 3: 增加运行时字段和转换函数**

`EmbyFileObj` 新增：

```go
WorkID uint
FilmFileID uint
Code string
PartIndex int
PartCount int
SourceRef string
SourceURL string
```

转换函数设置 `Object.ID = strconv.FormatUint(uint64(file.ID), 10)`、`Object.Path = work.PrimaryDir`，并从投影函数生成 `Name/Title`。`WrapMediaFiles` 以 `WorkID` 分组，不使用 `GetRealName(Name)`。

- [ ] **Step 4: 运行新旧 virtual_file 测试**

Run:

```bash
/Library/Go/sdk/go1.25.4/bin/go test ./drivers/virtual_file -v
```

Expected: PASS；旧转换路径仍编译。

- [ ] **Step 5: Commit（仅在用户明确要求提交时）**

```bash
git add internal/model/object.go drivers/virtual_file/media.go drivers/virtual_file/media_test.go
git commit -m "feat(media): carry typed work and file identity"
```

### Task 5: 增量 AutoMigrate 和基础回归

**Files:**
- Modify: `internal/db/db.go:13-16`
- Modify: `internal/db/film_sample_image_test.go:17-29`

**Interfaces:**
- Consumes: Task 2 models.
- Produces: 启动时存在新表，同时旧表仍存在。

- [ ] **Step 1: 写 AutoMigrate 共存测试**

在 DB 测试中断言 `Migrator().HasTable(&model.Film{})`、`HasTable(&model.FilmWork{})`、`HasTable(&model.FilmFile{})`、`HasTable(&model.SourceMagnet{})`、`HasTable(&model.CloudFileCache{})` 均为 true。

- [ ] **Step 2: 运行测试并确认失败**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./internal/db -run TestMediaTablesCoexistWithLegacyTables -v`

Expected: FAIL，新表未迁移。

- [ ] **Step 3: 将四个新模型加入 AutoMigrate 列表**

保持 `model.Film` 和 `model.MagnetCache` 在列表中，新增四个模型；不要改表名或执行 DropTable。

- [ ] **Step 4: 运行计划 1 验收**

Run:

```bash
/Library/Go/sdk/go1.25.4/bin/gofmt -w internal/model/media.go internal/model/media_test.go internal/model/object.go internal/db/media.go internal/db/media_test.go internal/db/db.go drivers/virtual_file/media.go drivers/virtual_file/media_test.go
/Library/Go/sdk/go1.25.4/bin/go test ./internal/model ./internal/db ./drivers/virtual_file
/Library/Go/sdk/go1.25.4/bin/go test ./...
```

Expected: 全部 PASS；没有旧 Film 行为变化。

- [ ] **Step 5: Commit（仅在用户明确要求提交时）**

```bash
git add internal/db/db.go internal/db/film_sample_image_test.go
git commit -m "feat(media): migrate new media tables additively"
```
