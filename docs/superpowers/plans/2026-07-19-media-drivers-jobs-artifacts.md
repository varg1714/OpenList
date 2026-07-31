# 虚拟媒体三驱动、定时任务与制品实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 JavDB、FC2、Pornhub 的运行时读写迁到 FilmWork/FilmFile，稳定公开文件名，并把 JavDB/FC2 enrichment 与制品写入移出 List。

**Architecture:** 先建立 StorageID+Code 制品路径和 work/file 查询，再逐个迁移驱动。现有 cron 函数和批次预算保留，repository 查询改为 work 级；JavDB/FC2 Link 最后统一切到 `CloudPlayMedia`，Pornhub 保持 SourceRef 直链。

**Tech Stack:** Go、GORM repository、现有 cron、Resty/Colly/Selenium、OpenAI 翻译、virtual_file 制品工具。

## Global Constraints

- 依赖领域持久化和 CloudPlay 两份计划已完成。
- List 中禁止 AirAV/OpenAI、磁力、NFO、海报、fanart、sample、字幕调用。
- 不新增 MediaJob；每个现有 job 只更新自己负责的 FilmWork 字段。
- 单条 job 失败不得 `return` 终止整个批次，应记录后 `continue`。
- 所有制品路径必须包含 `source/storageID/primaryDir/code`。
- 本计划完成后，三驱动运行时不得调用 `splitCode` 或 `av.GetFilmCode(name)`。

---

### Task 1: 稳定制品定位器

**Files:**
- Create: `drivers/virtual_file/media_artifact.go`
- Create: `drivers/virtual_file/media_artifact_test.go`
- Modify: `drivers/virtual_file/types.go`

**Interfaces:**
- Produces:

```go
type MediaIdentity struct { StorageID uint; Source, PrimaryDir, Code string }
type MediaArtifactPaths struct { Root, Poster, LegacyPoster, Background, NFO string }
func ResolveMediaArtifactPaths(identity MediaIdentity) (MediaArtifactPaths, error)
func MediaFanartPath(identity MediaIdentity, index int) (string, error)
func MediaSubtitlePath(identity MediaIdentity, partIndex, subtitleIndex int, ext string) (string, error)
```

- [ ] **Step 1: 写路径安全失败测试**

```go
func TestResolveMediaArtifactPathsUsesStorageAndCode(t *testing.T)
func TestResolveMediaArtifactPathsRejectsUnsafeComponents(t *testing.T)
func TestMediaMultipartArtifactsShareWorkRoot(t *testing.T)
func TestMediaSubtitlePathIncludesPartWhenMultipart(t *testing.T)
```

期望 root：`{DataDir}/emby/javdb/12/演员A/ABP-123`，NFO：`ABP-123.nfo`。

- [ ] **Step 2: 运行测试并确认失败**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/virtual_file -run 'TestResolveMediaArtifact|TestMediaMultipart|TestMediaSubtitle' -v`

Expected: FAIL。

- [ ] **Step 3: 实现路径定位器**

使用 `filepath.Abs/Rel` 和现有 containment 思路；每个组件拒绝空值、`.`、`..`、绝对路径及 `/\\`。`StorageID` 通过 `strconv.FormatUint` 进入路径。不要调用 `CutString` 或 `GetRealName`。

扩展 `MediaInfo`：

```go
Identity *MediaIdentity
PartIndex int
PartCount int
```

旧字段保留，供迁移完成前的非媒体调用者使用。

- [ ] **Step 4: 运行测试**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/virtual_file -run 'TestResolveMediaArtifact|TestMediaMultipart|TestMediaSubtitle' -v`

Expected: PASS。

- [ ] **Step 5: Commit（仅在明确要求提交时）**

```bash
git add drivers/virtual_file/media_artifact.go drivers/virtual_file/media_artifact_test.go drivers/virtual_file/types.go
git commit -m "feat(media): add storage scoped artifact paths"
```

### Task 2: NFO、海报、fanart 与字幕使用显式身份

**Files:**
- Modify: `drivers/virtual_file/nfo.go`
- Modify: `drivers/virtual_file/poster.go`
- Modify: `drivers/virtual_file/fanart.go`
- Modify: `drivers/virtual_file/poster_test.go`
- Modify: `drivers/virtual_file/fanart_test.go`
- Create: `drivers/virtual_file/media_nfo_test.go`

**Interfaces:**
- Consumes: `MediaInfo.Identity` 和 Task 1 paths.
- Produces: 新模型调用者不再传 `FileName` 决定身份。

- [ ] **Step 1: 写 NFO 投影与原子发布测试**

测试 `MediaInfo{Identity:&MediaIdentity{...}, Title:"ABP-123 译题"}` 写入 `ABP-123.nfo`，XML title 正确；模拟写失败时旧 NFO 内容不变。

- [ ] **Step 2: 写海报/fanart/字幕路径回归测试**

覆盖不同 StorageID 不冲突、multipart 共目录、subtitle part 后缀、非法路径拒绝。

- [ ] **Step 3: 运行测试并确认失败**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/virtual_file -run 'TestMediaNFO|TestMediaPoster|TestMediaFanart|TestMediaSubtitle' -v`

Expected: FAIL，旧函数仍由 FileName 派生路径。

- [ ] **Step 4: 增加 identity 分支并保留 legacy 分支**

当 `MediaInfo.Identity != nil` 时必须使用新定位器，并通过同目录临时文件 + `Sync` + `Rename` 发布；否则保留现有路径逻辑，直到迁移 finalize 删除。

新增显式 API，避免新调用者误入 legacy：

```go
func UpdateMediaNfo(info MediaInfo) error
func PublishMediaPoster(identity MediaIdentity, content []byte) error
func CacheMediaFanart(ctx context.Context, identity MediaIdentity, index int, request HTTPFileCacheRequest) error
func SaveMediaSubtitles(identity MediaIdentity, partIndex int, subtitles []string) error
```

- [ ] **Step 5: 运行 virtual_file 全包测试**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/virtual_file -v`

Expected: PASS，新旧路径均有覆盖。

- [ ] **Step 6: Commit（仅在明确要求提交时）**

```bash
git add drivers/virtual_file/nfo.go drivers/virtual_file/poster.go drivers/virtual_file/fanart.go drivers/virtual_file/*_test.go
git commit -m "feat(media): publish artifacts from explicit identity"
```

### Task 3: Work/File 列表、Get 和 Remove 共享服务

**Files:**
- Modify: `drivers/virtual_file/media.go`
- Modify: `drivers/virtual_file/media_test.go`

**Interfaces:**
- Produces:

```go
func ListMediaFiles(storageID uint, source, primaryDir string) ([]model.EmbyFileObj, error)
func ResolveMediaObj(storageID uint, source, primaryDir, groupName, fileName string) (model.Obj, error)
func ResolveMediaActorTreeObj(storageID uint, source, path, rootID string, modified time.Time) (model.Obj, error)
func DeleteMediaWork(workID uint) error
```

- [ ] **Step 1: 写列表/Get 失败测试**

覆盖纯 Code group、multipart files、两个 StorageID 隔离、错误文件名找不到、`Object.ID == FilmFileID`。

- [ ] **Step 2: 写删除事务失败测试**

删除 work 时级联或显式删除 FilmFile、SourceMagnet、CloudFileCache；注入 repository 错误时事务回滚。实际磁盘删除由驱动在 DB 事务成功后调用显式 artifact API。

- [ ] **Step 3: 运行测试并确认失败**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/virtual_file -run 'TestListMediaFiles|TestResolveMedia|TestDeleteMediaWork' -v`

Expected: FAIL。

- [ ] **Step 4: 实现基于 ID/Code 的共享服务**

`groupName` 必须与 work.Code 精确相等；`fileName` 与 `BuildMediaFileName` 精确相等。禁止 LIKE 前缀和 `GetRealName`。

- [ ] **Step 5: 运行测试**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/virtual_file -run 'TestListMediaFiles|TestResolveMedia|TestDeleteMediaWork' -v`

Expected: PASS。

- [ ] **Step 6: Commit（仅在明确要求提交时）**

```bash
git add drivers/virtual_file/media.go drivers/virtual_file/media_test.go
git commit -m "feat(media): resolve virtual files by work identity"
```

### Task 4: JavDB 轻量发现与翻译 job

**Files:**
- Modify: `drivers/javdb/util.go`
- Modify: `drivers/javdb/driver.go`
- Modify: `drivers/javdb/job.go`
- Create: `drivers/javdb/discovery_test.go`
- Create: `drivers/javdb/translation_job_test.go`

**Interfaces:**
- Consumes: `UpsertDiscoveredWork`, `EnsureSingleFilmFile`, ListMediaFiles.
- Produces:
  - `discoverFilms(dirName string, urlFunc func(int) string) error`
  - `scanTranslations()`

- [ ] **Step 1: 写 List 轻量化失败测试**

为 JavDB 注入或使用现有 test seam 统计 AirAV、BatchTranslate、CacheImageAndNfo、getMagnet 调用。执行 actor List 后断言：列表抓取 1 次，上述四类调用均为 0，DB 中存在 Code/RawTitle/SourceURL 和 `ABP-123.mp4`。

- [ ] **Step 2: 写翻译 job 失败/重试测试**

覆盖：

- pending 调用 BatchTranslate 后写 `TranslatedTitle` 和 success；
- 空结果写 retry_wait、Attempts+1、NextRetryAt；
- retry 时间未到不查询；
- 一条失败后继续处理下一条；
- translation version 增加可重新扫描。

- [ ] **Step 3: 运行测试并确认失败**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/javdb -run 'TestJavDBListIsDiscoveryOnly|TestScanTranslations' -v`

Expected: FAIL，List 仍调用 mappingNames。

- [ ] **Step 4: 拆分 discovery 和 translation**

`getJavPageInfo` 输出立刻规范化 Code；`discoverFilms` 只 upsert 列表字段。`List` 调用 discovery 后使用 `ListMediaFiles`。将 AirAV candidate + BatchTranslate 逻辑移入 `scanTranslations`，并加入现有 cron callback。

移除 `mappingNames` 从 List 调用链；函数可暂留给迁移前测试，但不得有运行时 caller。

- [ ] **Step 5: 运行测试**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/javdb -run 'TestJavDBListIsDiscoveryOnly|TestScanTranslations' -v`

Expected: PASS。

- [ ] **Step 6: Commit（仅在明确要求提交时）**

```bash
git add drivers/javdb/util.go drivers/javdb/driver.go drivers/javdb/job.go drivers/javdb/discovery_test.go drivers/javdb/translation_job_test.go
git commit -m "refactor(javdb): move translation out of list"
```

### Task 5: JavDB 现有 job 迁移到 FilmWork

**Files:**
- Modify: `internal/db/media.go`
- Modify: `drivers/javdb/job.go`
- Modify: `drivers/javdb/job_test.go`

**Interfaces:**
- Consumes: work-level stage fields and explicit artifact APIs.
- Produces: work-level versions of synopsis、tags、subtitle、sample、DMM poster、NFO queries/updates.

- [ ] **Step 1: 将每类现有 eligibility 测试复制为 FilmWork fixture**

至少覆盖：DMM poster pending/retry/success、sample batch budget、synopsis scan interval/excluded、subtitle retry、tag empty/subtitle-only、NFO version。

- [ ] **Step 2: 运行测试并确认失败**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/javdb ./internal/db -run 'Test.*FilmWork|Test.*MediaWork' -v`

Expected: FAIL，queries 仍返回 Film。

- [ ] **Step 3: 增加 work-level repository 函数并迁移 job**

增加并使用以下明确 repository：`QueryTranslationMediaWorks`、`UpdateMediaWorkTranslation`、`QueryEmptySynopsisMediaWorks`、`UpdateMediaWorkSynopsis`、`QueryTagMediaWorks`、`UpdateMediaWorkTags`、`QuerySampleImageMediaWorks`、`UpdateMediaWorkSampleProgress`、`QueryDMMPosterMediaWorks`、`UpdateMediaWorkDMMPosterStatus`、`QuerySubtitleMediaWorks`、`UpdateMediaWorkSubtitleScan`。所有 job 使用 `work.Code`、`work.StorageID` 和 `work.PrimaryDir`；制品调用使用 `MediaIdentity`。

将任何循环内网络错误后的 `return` 改为记录 stage error 后 `continue`。保留原有 sleep、limit 和 request budget。

- [ ] **Step 4: 运行 JavDB 和 DB 全测试**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/javdb ./internal/db -v`

Expected: PASS。

- [ ] **Step 5: Commit（仅在明确要求提交时）**

```bash
git add internal/db/media.go drivers/javdb/job.go drivers/javdb/job_test.go
git commit -m "refactor(javdb): maintain metadata per media work"
```

### Task 6: FC2 规范身份、发现、收藏和 job

**Files:**
- Modify: `drivers/fc2/util.go`
- Modify: `drivers/fc2/driver.go`
- Modify: `drivers/fc2/job.go`
- Modify: `drivers/fc2/job_test.go`
- Create: `drivers/fc2/discovery_test.go`

**Interfaces:**
- Consumes: NormalizeMediaCode、ReplaceFilmFiles、ListMediaFiles、CloudPlayMedia.

- [ ] **Step 1: 写 FC2 双格式和 List 轻量化失败测试**

断言裸 `123` 和完整 code upsert 同一 work；`SourceRef` 为规范 code，`SourceURL` 只存真实 URL；List 不翻译、不写制品、不抓磁力。

- [ ] **Step 2: 写 addStar 权威 multipart 测试**

磁力 manifest 在首次持久化前有 3 个有效文件时，创建一个 work + 3 files，名称投影 `cd1..cd3`；重复 addStar 幂等；不连续/歧义 manifest 不创建部分数据。

- [ ] **Step 3: 写 release/actor/translation job 回归**

单条来源错误后继续下一条；失败写 retry 字段；成功写 work 元数据并使 NFO version 落后等待重建。

- [ ] **Step 4: 运行测试并确认失败**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/fc2 -run 'TestFC2Discovery|TestFC2AddStar|TestRematchMedia' -v`

Expected: FAIL。

- [ ] **Step 5: 迁移 FC2 运行路径**

删除运行时 `Url=code` 双语义；List/upsert 走 work；manual addStar 在事务中写 work、manifest magnets 和 files；job 改查 work；Link 调用 `CloudPlayMedia`。

- [ ] **Step 6: 运行 FC2 全测试**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/fc2 ./internal/db ./internal/offline_download/tool -v`

Expected: PASS。

- [ ] **Step 7: Commit（仅在明确要求提交时）**

```bash
git add drivers/fc2 internal/db/media.go
git commit -m "refactor(fc2): persist canonical media works"
```

### Task 7: Pornhub 迁移到 Work/File 且保持直链

**Files:**
- Modify: `drivers/pornhub/util.go`
- Modify: `drivers/pornhub/driver.go`
- Modify: `drivers/pornhub/job.go`
- Create: `drivers/pornhub/media_test.go`

- [ ] **Step 1: 写发现、文件名和直链失败测试**

断言 `viewKey` 同时成为 Code/SourceRef，文件名 `viewKey.mp4`；两个 StorageID 数据隔离；Link 的 getVideoLink 参数来自 SourceRef，不从 Name/Url 解析。

- [ ] **Step 2: 写 tag job FilmWork 测试**

空 tags work 被扫描，失败不终止后续 work，成功增加 tags 并提升 MetadataVersion。

- [ ] **Step 3: 运行测试并确认失败**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/pornhub -run 'TestPornhubMedia|TestPornhubTagJob' -v`

Expected: FAIL。

- [ ] **Step 4: 迁移 Pornhub**

保留 Selenium/解析逻辑，替换 Film CRUD 为 work/file repository；Get/List 使用共享 media resolver；Link 通过 `EmbyFileObj.SourceRef`。

- [ ] **Step 5: 运行测试**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/pornhub ./drivers/virtual_file -v`

Expected: PASS。

- [ ] **Step 6: Commit（仅在明确要求提交时）**

```bash
git add drivers/pornhub
git commit -m "refactor(pornhub): use media work identity"
```

### Task 8: JavDB Link、Remove 与运行时字符串解析清理

**Files:**
- Modify: `drivers/javdb/driver.go`
- Modify: `drivers/javdb/util.go`
- Modify: `drivers/javdb/job.go`
- Modify: `drivers/fc2/driver.go`
- Modify: `drivers/fc2/util.go`
- Modify: `internal/av/utils.go`
- Create: `drivers/javdb/link_test.go`
- Create: `drivers/fc2/link_test.go`

- [ ] **Step 1: 写 JavDB/FC2 Link 类型化身份测试**

通过 fake CloudPlay wrapper 断言请求携带正确 WorkID/FileID/Code/PartIndex；缺 magnet 时 getter 收到 FilmWork；JavDB 主 provider 失败后使用 backup；FC2 只调用配置 provider。

- [ ] **Step 2: 写 Remove 隔离测试**

删除 storage 1 的同 code work，不得删除 storage 2；删除 multipart group 时删除所有 files、cache 和本 storage artifact root。

- [ ] **Step 3: 切换两个驱动到 `CloudPlayMedia` 并删除运行时解析调用**

JavDB source magnet getter 使用 `work.Code/work.SourceURL`；FC2 使用 `work.Code`。所有 `splitCode`/`GetFilmCode(name)` caller 改为显式字段。

- [ ] **Step 4: 静态搜索验证**

Run:

```bash
rg 'GetFilmCode\(|splitCode\(' drivers/javdb drivers/fc2 drivers/pornhub internal/offline_download/tool
```

Expected: 新运行路径零匹配；允许旧迁移辅助代码仅存在于 `internal/migration/media`（在下一计划创建）。

- [ ] **Step 5: 运行计划 3 验收**

Run:

```bash
/Library/Go/sdk/go1.25.4/bin/gofmt -w drivers/javdb drivers/fc2 drivers/pornhub drivers/virtual_file internal/db/media.go internal/model/object.go
/Library/Go/sdk/go1.25.4/bin/go test ./drivers/javdb ./drivers/fc2 ./drivers/pornhub ./drivers/virtual_file ./internal/db ./internal/offline_download/tool
/Library/Go/sdk/go1.25.4/bin/go test ./...
```

Expected: PASS；三驱动只使用新模型。旧 Film 表仍保留，供迁移读取。

- [ ] **Step 6: Commit（仅在明确要求提交时）**

```bash
git add drivers/javdb drivers/fc2 internal/av/utils.go
git commit -m "refactor(media): use typed identity for link and remove"
```
