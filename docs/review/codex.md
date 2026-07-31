## 审查结论

**当前 `feature/film-manage` 不符合“只改变文件名/管理方式、不影响原功能”的预期，暂不建议合并。**

数据库规范化设计、迁移的事务控制、dry-run、路径安全和文件迁移日志整体比较完整；但下载缓存、JavDB 过滤、备用磁力、Pornhub 防盗链请求头以及文件元信息存在明确回归。

## 主要问题

### [P1] 迁移后已有云盘下载缓存无法按新文件名命中

新播放流程仍然通过 `driver_type + name` 精确查询 `MagnetCache`：

- `/Users/varg247/store/work-store/backend/openlist/internal/offline_download/tool/cloud_play.go:45-48`
- `/Users/varg247/store/work-store/backend/openlist/drivers/javdb/media_link.go:14-29`

但迁移工具只把 `Magnet` 转换为 `SourceMagnet`：

- `/Users/varg247/store/work-store/backend/openlist/internal/migration/media/database.go:656-693`

它没有为新稳定文件名迁移或创建对应的 `MagnetCache`，因此以下字段没有被新播放入口继续使用：

- `FileId`
- `Option`，例如 115 的 `pickCode`
- `SubtitleUrls`
- `ScanAt`
- `ScanCount`

代码甚至已经通过 `cachePartIndex` 算出了缓存属于哪个分片，但返回值在 `/Users/varg247/store/work-store/backend/openlist/internal/migration/media/database.go:675` 被直接丢弃。

分支自己的测试数据已经能说明问题：

- 旧文件名：`abp-123 Original title.mp4`
- 旧缓存 FileId：`remote-javdb`
- 新文件名：`ABP-123.mp4`

由于名称精确匹配，迁移后第一次播放会缓存未命中并重新离线下载，原有云盘文件 ID 和下载信息等同于丢失。

**建议：**迁移时按分片为云盘类型的 `MagnetCache` 创建新文件名别名，完整保留 `FileId/Option/SubtitleUrls/ScanAt/ScanCount`；增加“迁移后直接调用新文件名缓存查询能够返回旧 FileId”的集成测试。

---

### [P1] JavDB 的 `Filter` 功能已停止执行

`main` 的 JavDB 定时任务会调用：

```go
d.filterFilms()
```

但分支中的定时任务已经移除了该调用：

- `/Users/varg247/store/work-store/backend/openlist/drivers/javdb/driver.go:53-64`

与此同时：

- `Filter` 配置仍然暴露：`/Users/varg247/store/work-store/backend/openlist/drivers/javdb/meta.go:25`
- 旧 `filterFilms` 仍然存在：`/Users/varg247/store/work-store/backend/openlist/drivers/javdb/job.go:666-687`
- 但它现在没有生产调用方，并且仍操作旧 `Film` 表，而不是新的 `FilmWork`

因此用户已经配置的影片过滤规则不会再生效，原本应从文件列表中删除的影片会继续保留。现有测试只直接调用 `filterFilms()`，没有覆盖定时任务是否接入，也没有覆盖规范化数据。

**建议：**实现基于 `FilmWork` 的过滤和删除逻辑，并重新接入 cron；测试应从 `FilmWork/FilmFile` 初始化数据并验证作品、分片和本地 artifacts 都被删除。

---

### [P1] JavDB 下载失去了备用磁力链接回退

`main` 的播放逻辑在首选磁力下载失败后，会从 Suke 获取不同的磁力并再次尝试。

分支的新逻辑只做了：

1. 用当前选中的磁力尝试主云盘；
2. 用同一个磁力尝试备用云盘；
3. 可选返回 MockedLink。

相关代码：

- `/Users/varg247/store/work-store/backend/openlist/drivers/javdb/driver.go:196-207`
- `/Users/varg247/store/work-store/backend/openlist/drivers/javdb/util.go:364-386`

虽然数据库中会保存多个 `SourceMagnet`，但播放只调用 `GetSelectedSourceMagnet`。`SelectSourceMagnet` 除测试外没有生产调用方：

- `/Users/varg247/store/work-store/backend/openlist/internal/db/source_magnet.go:46-66`

结果是第二、第三个磁力永远不会在首选磁力下载失败后被尝试。这是明确的下载成功率回归，不属于文件名管理变化。

**建议：**播放时按 `priority` 遍历 `ListSourceMagnets`，对每个磁力依次尝试主/备用云盘；成功后再更新 `selected`，失败则记录 `LastError`。需要增加 Link 层测试覆盖“首选磁力失败、第二磁力成功”。

---

### [P1] Pornhub 播放和封面请求丢失 Referer

旧 Pornhub 播放链接会携带：

```go
Referer: d.ServerUrl
```

分支的新 Link 返回 URL 时没有设置任何 Header：

- `/Users/varg247/store/work-store/backend/openlist/drivers/pornhub/driver.go:185-199`

另外，旧的封面缓存会设置 `ImgUrlHeaders["Referer"]`，新的稳定 artifact 缓存没有：

- `/Users/varg247/store/work-store/backend/openlist/drivers/pornhub/util.go:82-94`

对于有防盗链校验的 Pornhub 视频或图片地址，这可能导致：

- 视频链接返回 403；
- 海报下载失败；
- NFO 中有元数据但对应 poster 缺失。

同时，旧实现解析播放链接失败时会回退到 mocked link 并返回 `nil` error；新实现直接返回错误，错误处理语义也发生了变化。

现有 `/Users/varg247/store/work-store/backend/openlist/drivers/pornhub/discovery_test.go:103-119` 只验证 URL，没有验证 Referer 和失败回退。

**建议：**恢复播放链接和图片下载的 Referer；明确保留旧 mocked-link 回退语义，并增加 Header/失败场景测试。

---

### [P2] 文件列表中的大小和时间信息没有被保持

新投影直接使用 `FilmFile`：

- `Size = file.SourceSize`
- `Modified = file.UpdatedAt`
- `Ctime = file.CreatedAt`

位置：

- `/Users/varg247/store/work-store/backend/openlist/drivers/virtual_file/media.go:132-141`

但是新发现的文件由 `EnsureSingleFilmFile` 创建时没有设置大小：

- `/Users/varg247/store/work-store/backend/openlist/internal/db/media.go:52-55`

迁移创建 `FilmFile` 时也只迁移了 `SourcePath`，没有迁移旧 `Film.CreatedAt`，也没有填充大小：

- `/Users/varg247/store/work-store/backend/openlist/internal/migration/media/database.go:895-911`

因此迁移后：

- 文件大小从旧实现的非零值变成 `0`；
- `Modified/Ctime` 变成迁移执行时间，而不是原影片入库时间；
- Emby 等客户端可能把所有迁移影片识别为同一时间新增；
- 文件 API 的大小信息发生可见变化。

**建议：**至少把旧 `Film.CreatedAt` 迁移到每个 `FilmFile.CreatedAt/UpdatedAt`；如果无法获得真实大小，应明确保留旧兼容值或采用统一、文档化的未知大小策略。需要增加迁移前后对象投影元信息对比测试。

## 迁移工具评价

以下部分实现得比较可靠：

- dry-run 使用只读 SQLite；
- apply 前执行完整 preflight；
- 支持表前缀；
- 数据库写入有事务；
- artifact 路径有防穿越和 symlink 检查；
- journal 支持中断恢复；
- legacy `Film` 和 `MagnetCache` 不会被直接删除；
- 标题、简介、演员、标签、发布日期、样例图和 DMM 状态的基本映射有测试覆盖；
- multipart 连续性和 artifact 冲突采用 fail-closed。

但鉴于 **云盘 FileId/Option 没有转接到新名称**，目前不能认为“源数据已经准确迁移到目标数据”。迁移准确性只覆盖了影片元数据和磁力来源，没有覆盖完整的下载缓存状态。

## 验证结果

已在临时导出的 `feature/film-manage` 分支上执行：

```text
go test ./drivers/fc2 ./drivers/javdb ./drivers/pornhub \
  ./drivers/virtual_file ./internal/db ./internal/migration/media \
  ./internal/model ./cmd/migrate-media ./tools/fanart-repair
```

上述目标包全部通过。

同时：

- `git diff --check main...feature/film-manage` 通过；
- 变更 Go 文件全部通过 `gofmt` 检查；
- 完整 `go test ./...` 仍受仓库基线/环境问题影响，其中 `jable_tv` 格式检查错误和依赖本地 aria2 服务的测试已在 `main` 上复现，不是本分支引入。

不过现有测试没有覆盖上述关键回归，尤其是迁移后的云盘缓存命中、JavDB 多磁力回退、Filter 定时执行以及 Pornhub Referer。