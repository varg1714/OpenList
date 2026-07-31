# 虚拟媒体 CloudPlay 与双层缓存实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 用 `WorkID/FilmFileID`、来源磁力指纹和具体云盘存储身份替换基于文件名的磁力与远程文件缓存。

**Architecture:** 来源磁力 repository 与云盘文件 repository 分离。新增可注入依赖的 `mediaCloudPlayer` 以单测缓存命中和下载分支；远端 multipart 通过来源 manifest 路径/大小匹配，不再排序并合成 `cdN`。

**Tech Stack:** Go、GORM、现有 offline-download Tool、PikPak/115 driver、SQLite 单测和 fake driver。

## Global Constraints

- 依赖 `2026-07-19-media-domain-persistence.md` 已完成。
- 本计划新增 `CloudPlayMedia`，旧 `CloudPlay` 暂时保留至三驱动和迁移计划完成。
- `StorageIdentity` 必须来自实际平衡后 storage 的 `model.Storage.ID`。
- manifest 歧义、缺失或多对一时不得写任何 `CloudFileCache`。
- 磁力指纹变化时旧缓存不可命中。

---

### Task 1: SourceMagnet repository 与候选选择

**Files:**
- Create: `internal/db/source_magnet.go`
- Create: `internal/db/source_magnet_test.go`

**Interfaces:**
- Consumes: `model.SourceMagnet`.
- Produces:
  - `UpsertSourceMagnets(workID uint, magnets []model.SourceMagnet) error`
  - `GetSelectedSourceMagnet(workID uint) (model.SourceMagnet, error)`
  - `SelectSourceMagnet(workID, magnetID uint) error`
  - `ListSourceMagnets(workID uint) ([]model.SourceMagnet, error)`

- [ ] **Step 1: 写失败测试**

覆盖：同 fingerprint 幂等、只允许一个 selected、priority 最小者默认选中、切换 selected 的事务性。

```go
func TestUpsertSourceMagnetsDeduplicatesFingerprint(t *testing.T)
func TestSelectSourceMagnetLeavesOneSelected(t *testing.T)
func TestGetSelectedSourceMagnetUsesPriorityWhenNoneSelected(t *testing.T)
```

- [ ] **Step 2: 运行测试并确认失败**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./internal/db -run 'Test.*SourceMagnet' -v`

Expected: FAIL，函数未定义。

- [ ] **Step 3: 实现事务 repository**

`SelectSourceMagnet` 必须在同一事务中先清除 work 的 selected，再设置目标行，并验证目标属于该 work：

```go
func SelectSourceMagnet(workID, magnetID uint) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.SourceMagnet{}).Where("work_id = ?", workID).Update("selected", false).Error; err != nil { return err }
		res := tx.Model(&model.SourceMagnet{}).Where("id = ? AND work_id = ?", magnetID, workID).Update("selected", true)
		if res.Error != nil { return res.Error }
		if res.RowsAffected != 1 { return gorm.ErrRecordNotFound }
		return nil
	})
}
```

- [ ] **Step 4: 运行测试**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./internal/db -run 'Test.*SourceMagnet' -v`

Expected: PASS。

- [ ] **Step 5: Commit（仅在明确要求提交时）**

```bash
git add internal/db/source_magnet.go internal/db/source_magnet_test.go
git commit -m "feat(media): add source magnet repository"
```

### Task 2: CloudFileCache repository 与指纹失效

**Files:**
- Create: `internal/db/cloud_file_cache.go`
- Create: `internal/db/cloud_file_cache_test.go`

**Interfaces:**
- Produces:
  - `GetCloudFileCache(storageIdentity string, filmFileID uint, fingerprint string) (model.CloudFileCache, error)`
  - `ReplaceCloudFileCaches(storageIdentity, fingerprint string, caches []model.CloudFileCache) error`
  - `DeleteCloudFileCache(storageIdentity string, filmFileID uint) error`
  - `DeleteStaleCloudFileCaches(workID uint, fingerprint string) error`

- [ ] **Step 1: 写失败测试**

```go
func TestGetCloudFileCacheScopesStorageAndFingerprint(t *testing.T)
func TestReplaceCloudFileCachesIsAtomic(t *testing.T)
func TestDeleteStaleCloudFileCachesOnlyDeletesSiblingFiles(t *testing.T)
```

原子性测试传入两个相同 `RemoteFileID`，预期事务失败且零行写入。

- [ ] **Step 2: 运行测试并确认失败**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./internal/db -run 'Test.*CloudFileCache' -v`

Expected: FAIL。

- [ ] **Step 3: 实现严格查询与事务替换**

查询条件必须同时包含：

```go
Where("storage_identity = ? AND film_file_id = ? AND magnet_fingerprint = ?", storageIdentity, filmFileID, fingerprint)
```

`ReplaceCloudFileCaches` 在事务内验证所有 row 的 storage/fingerprint 与参数一致，先删除同 storage 下涉及的 file IDs，再批量创建。

- [ ] **Step 4: 运行测试**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./internal/db -run 'Test.*CloudFileCache' -v`

Expected: PASS。

- [ ] **Step 5: Commit（仅在明确要求提交时）**

```bash
git add internal/db/cloud_file_cache.go internal/db/cloud_file_cache_test.go
git commit -m "feat(media): add storage scoped cloud file cache"
```

### Task 3: 远端文件证据匹配器

**Files:**
- Create: `internal/offline_download/tool/media_match.go`
- Create: `internal/offline_download/tool/media_match_test.go`

**Interfaces:**
- Consumes: sibling `[]model.FilmFile`, selected magnet manifest, remote `[]model.Obj`.
- Produces:
  - `type RemoteFileEvidence struct { ID, Path string; Size int64; Options map[string]string }`
  - `MatchRemoteMediaFiles(files []model.FilmFile, manifest model.MagnetFileManifest, remotes []RemoteFileEvidence) (map[uint]RemoteFileEvidence, error)`

- [ ] **Step 1: 写匹配矩阵失败测试**

```go
func TestMatchRemoteMediaFilesByNormalizedPathAndSize(t *testing.T)
func TestMatchRemoteMediaFilesRejectsAmbiguousBasenames(t *testing.T)
func TestMatchRemoteMediaFilesRejectsIncompleteMultipart(t *testing.T)
func TestMatchRemoteMediaFilesAllowsSingleRemoteWithoutManifest(t *testing.T)
```

精确测试包含两个同 basename、不同目录的文件，确保完整规范路径优先。单文件无 manifest 仅在 logical files 和有效 remote 都恰好一个时允许。

- [ ] **Step 2: 运行测试并确认失败**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./internal/offline_download/tool -run TestMatchRemoteMediaFiles -v`

Expected: FAIL。

- [ ] **Step 3: 实现确定性匹配**

算法顺序：

1. 规范路径分隔符为 `/`，清理前导 `./`，禁止 `..`。
2. 将 `FilmFile.SourcePath/SourceSize` 与 manifest 对齐。
3. 以完整路径和大小匹配 remote；仅完整路径不可用时才允许唯一 basename+size。
4. 每个 logical file 必须恰有一个 remote，每个 remote 最多被使用一次。
5. 任一候选数不是 1 时返回包含 file ID 和候选列表的错误。

禁止调用 `slices.SortFunc` 形成身份，也不得生成 `cdN` 名称。

- [ ] **Step 4: 运行测试**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./internal/offline_download/tool -run TestMatchRemoteMediaFiles -v`

Expected: PASS。

- [ ] **Step 5: Commit（仅在明确要求提交时）**

```bash
git add internal/offline_download/tool/media_match.go internal/offline_download/tool/media_match_test.go
git commit -m "feat(cloudplay): map remote files from manifest evidence"
```

### Task 4: 可测试的类型化 CloudPlay 服务

**Files:**
- Create: `internal/offline_download/tool/media_cloud_play.go`
- Create: `internal/offline_download/tool/media_cloud_play_test.go`
- Modify: `internal/offline_download/tool/cloud_play.go`

**Interfaces:**
- Consumes: `FilmFileWithWork`, selected `SourceMagnet`, Task 1-3 repositories/matcher.
- Produces:

```go
type CloudPlayRequest struct {
	Provider string
	DriverPath string
	File model.FilmFileWithWork
	MagnetGetter func(context.Context, model.FilmWork) ([]model.SourceMagnet, error)
}

func CloudPlayMedia(ctx context.Context, args model.LinkArgs, req CloudPlayRequest) (*model.Link, error)
```

- [ ] **Step 1: 写服务失败测试**

通过注入以下依赖测试，不访问真实云盘：

```go
type mediaCloudPlayDeps struct {
	resolveStorage func(string) driver.Driver
	download func(context.Context, string, string, string, string) (*Status, error)
	listRemote func(context.Context, driver.Driver, string) ([]model.Obj, error)
	linkRemote func(context.Context, driver.Driver, model.CloudFileCache, model.LinkArgs) (*model.Link, error)
}
```

测试必须覆盖：

- 有效 fingerprint 缓存命中时不 download；
- stale fingerprint 时 download；
- 缺 SourceMagnet 时调用 getter 并选中；
- multipart 歧义时返回错误且缓存行数为 0；
- PikPak `fileList == 0` 仅对单分片允许使用任务返回 ID；
- 115 options 保留 `pickCode`。

- [ ] **Step 2: 运行测试并确认失败**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./internal/offline_download/tool -run TestCloudPlayMedia -v`

Expected: FAIL。

- [ ] **Step 3: 实现 `mediaCloudPlayer.play` 与公开 wrapper**

`StorageIdentity` 明确使用：

```go
storageIdentity := strconv.FormatUint(uint64(storage.GetStorage().ID), 10)
```

流程必须严格按 spec：加载/获取 magnet -> cache lookup -> download -> evidence match -> 原子写 siblings cache -> link requested file。任何失败均不得留下部分 sibling cache。

保留旧 `CloudPlay` 函数原样或仅抽取可共享 provider link helper；本计划不得切换驱动调用者。

- [ ] **Step 4: 运行 CloudPlay 测试与旧包测试**

Run:

```bash
/Library/Go/sdk/go1.25.4/bin/gofmt -w internal/offline_download/tool/media_match.go internal/offline_download/tool/media_match_test.go internal/offline_download/tool/media_cloud_play.go internal/offline_download/tool/media_cloud_play_test.go internal/db/source_magnet.go internal/db/source_magnet_test.go internal/db/cloud_file_cache.go internal/db/cloud_file_cache_test.go
/Library/Go/sdk/go1.25.4/bin/go test ./internal/offline_download/tool ./internal/db
```

Expected: PASS。

- [ ] **Step 5: Commit（仅在明确要求提交时）**

```bash
git add internal/offline_download/tool/media_cloud_play.go internal/offline_download/tool/media_cloud_play_test.go internal/offline_download/tool/cloud_play.go
git commit -m "feat(cloudplay): add typed media cloud player"
```

### Task 5: 缓存失效和错误路径回归

**Files:**
- Modify: `internal/offline_download/tool/media_cloud_play_test.go`
- Modify: `internal/db/cloud_file_cache_test.go`

- [ ] **Step 1: 增加删除远程文件后的回归测试**

当 cache link 返回 not-found 时，断言删除当前 `(StorageIdentity, FilmFileID)` cache，并在同一次请求中执行一次重新 download；第二次仍失败则返回错误，不循环。

- [ ] **Step 2: 增加磁力切换回归测试**

从 fingerprint A 切换到 B 后，断言 A cache 不命中；B 下载成功后 sibling cache 全部写 B。

- [ ] **Step 3: 运行计划 2 验收**

Run:

```bash
/Library/Go/sdk/go1.25.4/bin/go test ./internal/offline_download/tool ./internal/db
/Library/Go/sdk/go1.25.4/bin/go test ./...
```

Expected: PASS；旧 JavDB/FC2 仍调用旧 CloudPlay，行为未切换。

- [ ] **Step 4: Commit（仅在明确要求提交时）**

```bash
git add internal/offline_download/tool/media_cloud_play_test.go internal/db/cloud_file_cache_test.go
git commit -m "test(cloudplay): cover stale and missing remote caches"
```
