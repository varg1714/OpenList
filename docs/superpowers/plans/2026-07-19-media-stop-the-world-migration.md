# 虚拟媒体停服迁移、回滚与清理实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 提供可 dry-run、可恢复、可回滚的停服迁移工具，将三驱动旧 Film/MagnetCache 数据和本地制品切换到新模型，并在验证后删除旧模型。

**Architecture:** `internal/migration/media` 以 migration-local legacy structs 读取旧表，先生成完整报告和制品 manifest，再事务填充新表并按 JSONL journal 移动文件。Cobra 命令提供 `dry-run/apply/verify/rollback/finalize` 五个动作；finalize 仅在 verification report 通过后允许。

**Tech Stack:** Go、GORM、Cobra、JSON/JSONL、SHA-256、os.Rename、SQLite/MySQL/PostgreSQL。

## Global Constraints

- 依赖前三份计划全部完成并通过全量测试。
- 服务必须停止；apply/finalize 需要显式 `--backup-confirmed`。
- 不自动覆盖、合并或删除冲突文件。
- 任何权威 Film/SourceMagnet 身份冲突都会阻止 apply。
- 云盘 provider cache 可在明确报告后丢弃；来源磁力不可静默丢弃。
- 所有文件变更必须先有 manifest，再有 journal pending 记录。
- finalize 前必须存在通过的 verify report，并且 journal 无 pending/failed。

---

### Task 1: 迁移动作与参数契约

**Files:**
- Create: `internal/migration/media/options.go`
- Create: `internal/migration/media/options_test.go`

**Interfaces:**
- Produces:

```go
type Action string
const (ActionDryRun Action = "dry-run"; ActionApply Action = "apply"; ActionVerify Action = "verify"; ActionRollback Action = "rollback"; ActionFinalize Action = "finalize")

type Options struct {
	Action Action
	DataDir, OutputDir, MappingFile string
	BackupConfirmed bool
}

func ValidateOptions(opts Options) error
```

- [ ] **Step 1: 写迁移参数失败测试**

覆盖未知 action、apply/finalize 未传 `--backup-confirmed`、缺 output dir、rollback 缺 journal。

- [ ] **Step 2: 运行测试并确认失败**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./internal/migration/media -run TestValidateOptions -v`

Expected: FAIL。

- [ ] **Step 3: 实现 action 解析和参数验证**

接受的最终命令形态记录为：

```text
openlist media-migrate <dry-run|apply|verify|rollback|finalize>
  --output <dir>
  --mapping <json>
  --backup-confirmed
```

`ValidateOptions` 对 action 白名单、output dir、mapping 文件扩展名、backup confirmation 和 rollback journal 前置条件返回明确错误。此 Task 不注册 Cobra 命令，也不创建尚无完整 handler 的 dispatch；命令在 Task 7 所有动作实现后一次性接入。

- [ ] **Step 4: 运行测试**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./internal/migration/media -run TestValidateOptions -v`

Expected: PASS。

- [ ] **Step 5: Commit（仅在明确要求提交时）**

```bash
git add internal/migration/media/options.go internal/migration/media/options_test.go
git commit -m "feat(media): define migration action contract"
```

### Task 2: Legacy 读取、StorageID 映射和 Code 分类

**Files:**
- Create: `internal/migration/media/legacy.go`
- Create: `internal/migration/media/classify.go`
- Create: `internal/migration/media/classify_test.go`
- Create: `internal/migration/media/testdata/mapping.json`

**Interfaces:**
- Produces:

```go
type LegacyFilm struct {
	ID uint; URL, Name, Image, Source, Actor, ActorID string
	Date, CreatedAt time.Time
	Actors model.StringArray; Title, Synopsis string
	SynopsisScanAt time.Time; SynopsisExcluded bool
	SampleImageCount int; SampleImageComplete bool; SampleImageScanAt time.Time
	DMMPosterStatus string; DMMPosterScanAt time.Time
	Tags model.StringArray; SubtitleOnly bool
}
type LegacyMagnetCache struct {
	ID uint; Magnet, FileID, Name, Code, DriverType string
	Option map[string]string; Subtitle bool; ScanAt time.Time; ScanCount uint
	SubtitleURLs model.StringArray
}
type StorageMapping map[string]uint
type ClassifiedFile struct { PartIndex, PartCount int; LegacyFilmIDs []uint; SourcePath string; SourceSize int64 }
type Conflict struct { Kind, Key, Message string; LegacyFilmIDs, LegacyCacheIDs []uint; Blocking bool }
type ClassifiedWork struct { StorageID uint; Source, Code, PrimaryDir string; Rows []LegacyFilm; Files []ClassifiedFile; Conflicts []Conflict }
type Classification struct { Works []ClassifiedWork; AirAVRows []LegacyFilm; LegacyMagnets []LegacyMagnetCache; Conflicts []Conflict }
func (c Classification) CanApply() bool
func LoadLegacyRows(tx *gorm.DB) ([]LegacyFilm, []LegacyMagnetCache, error)
func ClassifyLegacyWorks(rows []LegacyFilm, storages []model.Storage, mapping StorageMapping) (Classification, error)
```

- [ ] **Step 1: 写来源分类表驱动测试**

覆盖：JavDB `ABP-123 标题.mp4`、FC2 裸/完整 URL、FC2 `-cdN`、Pornhub viewKey、AirAV cache、未知 source、多个 storage 的 actor 唯一匹配、个人收藏映射缺失。

- [ ] **Step 2: 写冲突失败测试**

覆盖 code 冲突、part 缺口、相同 part 不同权威字段、两个 storage 无法归属。阻塞冲突必须存在于结果且 `CanApply()==false`。

- [ ] **Step 3: 运行测试并确认失败**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./internal/migration/media -run 'TestClassify|TestResolveLegacyStorage' -v`

Expected: FAIL。

- [ ] **Step 4: 实现 migration-only 解析**

旧表名通过当前 DB naming strategy 解析：

```go
filmTable := tx.NamingStrategy.TableName("Film")
magnetTable := tx.NamingStrategy.TableName("MagnetCache")
```

JavDB/FC2 名称正则只能存在于此包。`StorageMapping` key 使用稳定字符串 `source:primaryDir` 或 `source:legacyFilmID`，报告明确要求哪一个 key。

- [ ] **Step 5: 运行测试**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./internal/migration/media -run 'TestClassify|TestResolveLegacyStorage' -v`

Expected: PASS。

- [ ] **Step 6: Commit（仅在明确要求提交时）**

```bash
git add internal/migration/media/legacy.go internal/migration/media/classify.go internal/migration/media/classify_test.go internal/migration/media/testdata/mapping.json
git commit -m "feat(media): classify legacy film data"
```

### Task 3: Preflight 报告和迁移前后计数

**Files:**
- Create: `internal/migration/media/report.go`
- Create: `internal/migration/media/report_test.go`

**Interfaces:**
- Produces:

```go
type MigrationCounts struct { LegacyFilms, LegacyMagnets, Works, Files, SourceMagnets, CloudCaches, MergedRows, DroppedAirAV, DroppedCloudCaches, BlockingConflicts int }
type WorkReport struct { StorageID uint; Source, Code, PrimaryDir string; LegacyFilmIDs []uint; PartIndexes []int }
type PreflightReport struct { Version int; GeneratedAt time.Time; Counts MigrationCounts; Works []WorkReport; Conflicts []Conflict; CanApply bool }
func WritePreflightReport(path string, report PreflightReport) error
func ReadPreflightReport(path string) (PreflightReport, error)
```

- [ ] **Step 1: 写稳定 JSON 和计数测试**

同一输入两次生成除 timestamp 外相同顺序；work 按 StorageID/Source/Code，conflict 按 kind/legacy ID 排序。

- [ ] **Step 2: 运行并确认失败**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./internal/migration/media -run TestPreflightReport -v`

Expected: FAIL。

- [ ] **Step 3: 实现报告并接入 dry-run**

dry-run 必须只读 DB/文件系统，输出：

```text
preflight.json
conflicts.json
artifact-manifest.jsonl
```

如果 `BlockingConflicts > 0`，命令仍成功生成报告，但返回具名 `ErrPreflightBlocked` 以产生非零退出码。

- [ ] **Step 4: 运行测试**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./internal/migration/media -run 'TestPreflightReport|TestDryRun' -v`

Expected: PASS 且测试 DB 行数不变。

- [ ] **Step 5: Commit（仅在明确要求提交时）**

```bash
git add internal/migration/media/report.go internal/migration/media/report_test.go internal/migration/media/runner.go
git commit -m "feat(media): emit migration preflight reports"
```

### Task 4: 新表事务填充与缓存拆分

**Files:**
- Create: `internal/migration/media/database.go`
- Create: `internal/migration/media/database_test.go`

**Interfaces:**
- Produces:
  - `PopulateNewTables(tx *gorm.DB, classification Classification) (MigrationCounts, error)`
  - `VerifyPopulatedTables(tx *gorm.DB, classification Classification) error`

- [ ] **Step 1: 写完整 fixture 迁移失败测试**

fixture 包含：JavDB title name、FC2 3 parts、Pornhub、AirAV、source magnet、PikPak cache、115 mixed cache。断言 work/file/candidate/cache 数量和字段映射符合 spec。

- [ ] **Step 2: 写事务回滚测试**

制造重复 remote ID 违反 unique constraint，断言四张新表均为 0 行，旧表不变。

- [ ] **Step 3: 运行并确认失败**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./internal/migration/media -run 'TestPopulateNewTables|TestDatabaseMigrationRollback' -v`

Expected: FAIL。

- [ ] **Step 4: 实现确定性转换**

在单 DB 事务内：work -> files -> source magnets -> resolvable cloud caches。AirAV 不写新表；歧义 provider cache 计数后跳过；source magnet 无法归属则返回错误回滚。

apply 必须读取并比对同 output dir 的 `preflight.json`，重新分类结果摘要不一致时拒绝执行。

- [ ] **Step 5: 运行测试**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./internal/migration/media -run 'TestPopulateNewTables|TestDatabaseMigrationRollback' -v`

Expected: PASS。

- [ ] **Step 6: Commit（仅在明确要求提交时）**

```bash
git add internal/migration/media/database.go internal/migration/media/database_test.go internal/migration/media/runner.go
git commit -m "feat(media): populate normalized media tables"
```

### Task 5: 制品 manifest 和冲突预检

**Files:**
- Create: `internal/migration/media/artifact_manifest.go`
- Create: `internal/migration/media/artifact_manifest_test.go`

**Interfaces:**
- Produces:

```go
type ArtifactAction string
const (ArtifactMove ArtifactAction = "move"; ArtifactRename ArtifactAction = "rename"; ArtifactDeduplicate ArtifactAction = "deduplicate"; ArtifactRecreateLink ArtifactAction = "recreate-link"; ArtifactRegenerateNFO ArtifactAction = "regenerate-nfo")
type ArtifactManifestEntry struct { ID, WorkKey string; Action ArtifactAction; Kind, SourcePath, TargetPath, SHA256 string; Size int64; Conflict string }
func BuildArtifactManifest(dataDir string, classification Classification) ([]ArtifactManifestEntry, []Conflict, error)
func WriteArtifactManifest(path string, entries []ArtifactManifestEntry) error
```

- [ ] **Step 1: 写临时目录 fixture 测试**

覆盖 JavDB title directory、FC2/Pornhub stable directory、poster、legacy JPG/background symlink、NFO、fanart、subtitle。

- [ ] **Step 2: 写冲突矩阵测试**

覆盖目标不同 hash、file/dir、escaping symlink、case-fold collision、两个 work 同目标；再覆盖相同 hash deduplicate 合法。

- [ ] **Step 3: 运行并确认失败**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./internal/migration/media -run 'TestBuildArtifactManifest|TestArtifactConflict' -v`

Expected: FAIL。

- [ ] **Step 4: 实现只读 inventory**

使用 `Lstat`，符号链接读取 link target 而不跟随越界；普通文件流式 SHA-256。输出按 target path/source path 排序，确保 dry-run 可重复。

- [ ] **Step 5: 运行测试**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./internal/migration/media -run 'TestBuildArtifactManifest|TestArtifactConflict' -v`

Expected: PASS。

- [ ] **Step 6: Commit（仅在明确要求提交时）**

```bash
git add internal/migration/media/artifact_manifest.go internal/migration/media/artifact_manifest_test.go
git commit -m "feat(media): preflight artifact migration"
```

### Task 6: Durable journal、恢复与 rollback

**Files:**
- Create: `internal/migration/media/journal.go`
- Create: `internal/migration/media/journal_test.go`

**Interfaces:**
- Produces:

```go
type JournalState string
type JournalEntry struct { ActionID string; State JournalState; SourcePath, TargetPath, PreHash, PostHash, Error string; UpdatedAt time.Time }
func ApplyArtifactManifest(entries []ArtifactManifestEntry, journalPath string) error
func ResumeArtifactMigration(manifestPath, journalPath string) error
func RollbackArtifactMigration(journalPath string) error
func RollbackPopulatedTables(tx *gorm.DB, preflight PreflightReport) error
```

- [ ] **Step 1: 写 crash-resume 测试**

通过可注入 `renameFile` 在第 2 步返回错误；断言第 1 步 done、第 2 步 failed。恢复后不重复第 1 步，并完成剩余步骤。

- [ ] **Step 2: 写反向 rollback 测试**

完成多步 move/rename 后 rollback，断言原路径/hash 恢复、目标消失；若目标被外部修改则拒绝回滚并报告。

数据库测试先填充新表并保留旧表，调用 `RollbackPopulatedTables` 后断言四张新表中本次迁移的行清空、旧 Film/MagnetCache 行完全不变。若 legacy 表已被 finalize 删除，函数必须返回 `ErrFinalizeAlreadyApplied`，提示只能恢复外部数据库备份。

- [ ] **Step 3: 运行并确认失败**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./internal/migration/media -run 'TestArtifactJournal|TestRollbackArtifact' -v`

Expected: FAIL。

- [ ] **Step 4: 实现 write-ahead JSONL journal**

每个动作执行前 append+fsync pending，执行后校验 postcondition 再 append+fsync done。journal 读取以同 ActionID 最后一条状态为准。rollback 按 done 动作逆序执行。

`rollback` action 的顺序固定为：验证未 finalize -> 反转 artifact journal -> 事务清除由 preflight work keys 标识的新表数据 -> 重新验证旧表计数与 preflight 一致。任一步失败都停止并保留报告。

- [ ] **Step 5: 运行测试**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./internal/migration/media -run 'TestArtifactJournal|TestRollbackArtifact' -v`

Expected: PASS。

- [ ] **Step 6: Commit（仅在明确要求提交时）**

```bash
git add internal/migration/media/journal.go internal/migration/media/journal_test.go
git commit -m "feat(media): journal artifact migration and rollback"
```

### Task 7: Verify report 和 finalize 关卡

**Files:**
- Create: `cmd/media_migrate.go`
- Create: `cmd/media_migrate_test.go`
- Create: `internal/migration/media/verify.go`
- Create: `internal/migration/media/verify_test.go`
- Create: `internal/migration/media/runner.go`
- Create: `internal/migration/media/runner_test.go`

**Interfaces:**
- Produces:

```go
type CheckResult struct { Name string; Passed bool; Expected, Actual, Error string }
type VerificationReport struct { Passed bool; CheckedAt time.Time; Data, Artifacts, Runtime []CheckResult }
func Verify(ctx context.Context, db *gorm.DB, opts Options) (VerificationReport, error)
func Finalize(ctx context.Context, db *gorm.DB, opts Options) error
```

- [ ] **Step 1: 写 verify 失败矩阵**

覆盖 unresolved code、缺 file、part gap、fingerprint 缺失、cloud cache orphan、manifest hash 不符、journal pending、旧 title directory 残留。

- [ ] **Step 2: 写 finalize 拒绝测试**

无 verify report、report failed、report 与当前 preflight hash 不匹配、未确认 backup 时均拒绝；全部通过时才删除 legacy rows/table。

- [ ] **Step 3: 运行并确认失败**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./internal/migration/media -run 'TestVerify|TestFinalize' -v`

Expected: FAIL。

- [ ] **Step 4: 实现验证和 finalize**

verify report 写临时文件后原子 rename。finalize 先重新验证，再事务删除 old Film/AirAV/MagnetCache rows 并 `DropTable` legacy tables；保留 migration output 文件。

实现 `Run(ctx, database, opts)` 的完整 action switch；每个 action 都调用本计划已经定义的真实 handler，不允许默认成功或未实现分支。

注册 Cobra 命令。`RunE` 只初始化 config/log/DB，不调用 `data.InitData`、storage bootstrap 或 server；defer `db.Close()` 后调用 `media.Run`。CLI 测试覆盖五个 action 的 dispatch、无效 action 和参数错误。

`rollback` 只支持 finalize 前状态；finalize 后命令返回明确错误并打印数据库备份恢复要求，不声称能够重建已删除的 legacy 表。

- [ ] **Step 5: 运行测试**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./internal/migration/media -run 'TestVerify|TestFinalize' -v`

Expected: PASS。

- [ ] **Step 6: Commit（仅在明确要求提交时）**

```bash
git add cmd/media_migrate.go cmd/media_migrate_test.go internal/migration/media/verify.go internal/migration/media/verify_test.go internal/migration/media/runner.go internal/migration/media/runner_test.go
git commit -m "feat(media): verify and finalize migration"
```

### Task 8: 删除旧运行时模型与全量切换验证

**Files:**
- Modify: `internal/model/film.go`
- Modify: `internal/db/db.go`
- Delete or reduce: `internal/db/film.go`
- Delete or reduce: `internal/db/cloudplay.go`
- Modify: `drivers/javdb/job_test.go`
- Modify: `drivers/fc2/job_test.go`
- Modify: `internal/db/film_sample_image_test.go`
- Modify: `internal/migration/media/legacy.go`

**Interfaces:**
- Consumes: migration-local `LegacyFilm/LegacyMagnetCache`; no runtime old model.
- Produces: app startup不再 AutoMigrate 旧表，迁移工具仍可读取旧表。

- [ ] **Step 1: 静态列出所有旧模型引用并建立失败门禁**

Run:

```bash
rg 'model\.Film|model\.MagnetCache|QueryByActor\(|QueryMagnetCacheByName\(|GetFilmCode\(' --glob '*.go'
```

Expected before cleanup: 仅 migration-local 转换尚未完成；记录每个运行时匹配并逐项替换。

- [ ] **Step 2: 将迁移 legacy structs 补齐为独立表读取类型**

确保迁移包不 import 已删除的 `model.Film/MagnetCache`。表名由 naming strategy 获取，不依赖 `TableName()` 硬编码前缀。

- [ ] **Step 3: 删除旧模型、AutoMigrate 和未使用 CRUD**

保留 `MissedFilm`、`Actor`、`StringArray` 等仍被新模型/驱动使用的类型；必要时将它们移动到独立文件，避免删除 `film.go` 时误删。

- [ ] **Step 4: 迁移旧测试 fixture 到新模型**

所有 job eligibility、sample、poster、tag 测试使用 `FilmWork`；migration package 独立测试旧行。

- [ ] **Step 5: 运行静态门禁**

Run:

```bash
rg 'model\.Film|model\.MagnetCache|QueryByActor\(|QueryMagnetCacheByName\(|GetFilmCode\(' --glob '*.go' --glob '!internal/migration/media/**'
```

Expected: 零匹配。

- [ ] **Step 6: 运行完整质量门禁**

Run:

```bash
/Library/Go/sdk/go1.25.4/bin/gofmt -w cmd/media_migrate.go internal/migration/media internal/model internal/db drivers/javdb drivers/fc2 drivers/pornhub drivers/virtual_file internal/offline_download/tool
/Library/Go/sdk/go1.25.4/bin/go test ./internal/migration/media ./cmd
/Library/Go/sdk/go1.25.4/bin/go test ./...
/Library/Go/sdk/go1.25.4/bin/go build ./...
```

Expected: 全部 exit 0。

- [ ] **Step 7: 在复制数据上执行手工演练**

```bash
openlist --data /path/to/copied-data media-migrate dry-run --output /path/to/report
openlist --data /path/to/copied-data media-migrate apply --output /path/to/report --backup-confirmed
openlist --data /path/to/copied-data media-migrate verify --output /path/to/report
openlist --data /path/to/copied-data media-migrate rollback --output /path/to/report
```

Expected: dry-run/apply/verify 成功，rollback 恢复旧 DB 副本与制品 hash。finalize 只在单独复制数据的第二轮演练执行。

- [ ] **Step 8: Commit（仅在明确要求提交时）**

```bash
git add internal/model internal/db internal/migration/media cmd drivers
git commit -m "refactor(media): remove legacy film persistence"
```
