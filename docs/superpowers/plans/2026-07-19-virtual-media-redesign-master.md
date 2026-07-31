# 虚拟媒体稳定标识重构总控实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 JavDB、FC2、Pornhub 从旧 `Film`/`MagnetCache` 模型迁移到稳定 Code 驱动的影片主体、文件分片、来源磁力和云盘文件缓存模型。

**Architecture:** 按依赖关系拆成四份可独立评审的实施计划。前三份以增量方式加入新模型和新运行链路，旧表仍保留用于迁移；第四份实现停服迁移、制品移动、验证和最终清理。

**Tech Stack:** Go 1.23.4（项目实际工具链使用 Go 1.25.4）、GORM、SQLite/MySQL/PostgreSQL、Cobra、现有 cron 与 offline-download 框架。

## Global Constraints

- 公共文件名只能是 `CODE.mp4` 或 `CODE-cdN.mp4`。
- NFO 标题投影为 `CODE + translated/raw title`，标题不得参与身份、路径或缓存键。
- 运行时不得从 `Name`、`Title`、`Url` 或正则表达式反推 Code；旧字符串解析仅允许出现在迁移包。
- 唯一影片身份为 `(StorageID, Source, Code)`。
- 一个影片主体只有一个 `PrimaryDir`；`Actors` 仅为 NFO 元数据。
- JavDB/FC2 List 保留轻量发现，但不得调用 AirAV、OpenAI、磁力抓取或本地制品写入。
- Pornhub 保持同步发现和直接 Link，不进入 CloudPlay。
- 不引入通用 `MediaJob` 表；沿用现有 cron/job 查询模式和阶段字段。
- SourceMagnet 与 CloudFileCache 分表；云盘缓存必须包含具体 `StorageIdentity` 和磁力指纹。
- 远端多文件映射必须依据权威 manifest 路径/大小，歧义时失败且不得写缓存。
- 本地制品原地迁移；任何冲突在变更前中止，不覆盖不同内容。
- 不调用 Plan Agent；不在计划阶段修改业务代码。

---

## 计划集合与执行顺序

### Plan 1: 领域模型与持久化基础

文件：[2026-07-19-media-domain-persistence.md](./2026-07-19-media-domain-persistence.md)

交付：

- `FilmWork`、`FilmFile`、`SourceMagnet`、`CloudFileCache` 模型；
- Code/文件名/NFO 标题投影函数；
- 影片主体与文件 repository；
- `EmbyFileObj` 类型化身份；
- 新表 AutoMigrate，旧表保持可用。

验收命令：

```bash
/Library/Go/sdk/go1.25.4/bin/go test ./internal/model ./internal/db ./drivers/virtual_file
```

### Plan 2: CloudPlay 与双层缓存

文件：[2026-07-19-media-cloudplay-cache.md](./2026-07-19-media-cloudplay-cache.md)

依赖 Plan 1。交付：

- SourceMagnet/CloudFileCache repository；
- 存储实例级 `StorageIdentity`；
- manifest 驱动的远端文件映射；
- 类型化 CloudPlay 请求与 provider 缓存命中；
- 旧 CloudPlay 暂时保留，供尚未迁移的调用者编译。

验收命令：

```bash
/Library/Go/sdk/go1.25.4/bin/go test ./internal/offline_download/tool ./internal/db
```

### Plan 3: 三驱动、现有 job 与本地制品

文件：[2026-07-19-media-drivers-jobs-artifacts.md](./2026-07-19-media-drivers-jobs-artifacts.md)

依赖 Plan 1、2。交付：

- JavDB/FC2 轻量发现和后台翻译/元数据维护；
- Pornhub 新模型与直接 Link；
- 所有现有 job 改查 `FilmWork`；
- Code/StorageID 驱动的 NFO、海报、fanart 和字幕路径；
- JavDB/FC2 Link 使用类型化 CloudPlay；
- 运行时移除对 `GetFilmCode(name)` 的依赖。

验收命令：

```bash
/Library/Go/sdk/go1.25.4/bin/go test ./drivers/javdb ./drivers/fc2 ./drivers/pornhub ./drivers/virtual_file ./internal/offline_download/tool
```

### Plan 4: 停服迁移、回滚与最终清理

文件：[2026-07-19-media-stop-the-world-migration.md](./2026-07-19-media-stop-the-world-migration.md)

依赖 Plan 1-3。交付：

- `openlist media-migrate` Cobra 命令；
- StorageID 映射、旧数据分类和冲突报告；
- 新表填充、缓存拆分；
- 制品 manifest、journal、恢复和 rollback；
- 验证关卡；
- `--finalize` 后删除旧 Film/AirAV/MagnetCache 数据与旧运行时代码。

验收命令：

```bash
/Library/Go/sdk/go1.25.4/bin/go test ./internal/migration/media ./cmd
/Library/Go/sdk/go1.25.4/bin/go test ./...
/Library/Go/sdk/go1.25.4/bin/go build ./...
```

## 跨计划稳定接口

后续计划只能消费这些由前序计划定义的接口；如需改名，必须同步修改全部计划文档后再实现。

```go
// internal/model/media.go
func NormalizeMediaCode(source, value string) (string, error)
func BuildMediaFileName(code string, partIndex, partCount int) (string, error)
func BuildMediaTitle(code, rawTitle, translatedTitle string) string

// internal/db/media.go
func UpsertDiscoveredWork(work *model.FilmWork) error
func EnsureSingleFilmFile(workID uint) (model.FilmFile, error)
func GetFilmWork(id uint) (model.FilmWork, error)
func GetFilmFile(id uint) (model.FilmFile, error)
func GetFilmFileWithWork(id uint) (model.FilmFileWithWork, error)
func ListFilmWorks(storageID uint, source, primaryDir string) ([]model.FilmWork, error)
func ListFilmFiles(workID uint) ([]model.FilmFile, error)

// internal/offline_download/tool/cloud_play.go
func CloudPlayMedia(ctx context.Context, args model.LinkArgs, req CloudPlayRequest) (*model.Link, error)
```

## 集成关卡

- [ ] 完成 Plan 1 后，确认新增表不会删除或重写旧表。
- [ ] 完成 Plan 2 后，使用 fake remote files 证明歧义映射不写缓存。
- [ ] 完成 Plan 3 后，静态搜索确认三个驱动运行时不再调用 `GetFilmCode(name)`。
- [ ] 完成 Plan 3 后，确认 JavDB/FC2 List 测试中外部 enrichment stub 调用次数为 0。
- [ ] 完成 Plan 4 dry-run 后，人工审阅 migration report、artifact manifest 和冲突列表。
- [ ] 只有完整备份、dry-run 零阻塞冲突、rollback 演练通过后，才允许执行 apply/finalize。
- [ ] finalize 后运行全量测试、构建以及三驱动手工 List/Get/Link 验证。

## 提交边界

计划中的 commit 步骤仅在用户明确要求执行实现和提交后使用。推荐每个独立 TDD 任务一个原子提交，不把迁移数据、运行时切换和旧代码删除混在同一提交。
