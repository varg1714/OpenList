**结论**

本次改动不符合“仅调整文件名管理、完全保持原有功能”的预期，暂不建议合并。数据库迁移和稳定身份模型整体设计合理，但下载缓存兼容、多文件列表和部分任务链路存在功能风险。

**高优先级问题**

- **已有云播缓存无法稳定命中**：新逻辑把展示名统一生成成 `CODE.mp4` / `CODE-cdN.mp4`，见 `drivers/virtual_file/media.go:124` 和 `internal/model/media.go:43`；但 `CloudPlay` 仍按“驱动类型 + 文件名”查询旧缓存，见 `internal/offline_download/tool/cloud_play.go:45`、`internal/db/cloudplay.go:36`。旧缓存记录中的 `Name` 可能是旧的翻译名，导致已有 `FileId`、`pickCode` 等信息无法命中，系统会重新下载。该问题直接影响下载链接和下载信息维护。
- **FC2 自定义目录丢失多文件磁力的文件列表**：`drivers/fc2/util.go:60-72` 对每个发现的影片直接调用 `EnsureSingleFilmFile`，始终建立单文件拓扑。旧逻辑会在 `addStar` 中根据磁力文件列表创建多个 `-cdN` 文件，相关逻辑仍在 `drivers/fc2/util.go:225-244`。因此新扫描到的多文件 FC2 资源只显示一个文件，分片下载入口丢失。
- **迁移没有将云播缓存完整转换到目标模型**：`MagnetCache.FileId`、`Option`、`SubtitleUrls`、`ScanCount` 没有对应的 `SourceMagnet` 字段，迁移写入处仅保存磁力、提供方、优先级、字幕标记和扫描时间，见 `internal/migration/media/database.go:796-825`。源表虽然按设计保留，但目标数据并不完整；尤其 `FileId` 和 `Option` 是已有云盘文件链接所需的信息。
- **不同类型的缓存被当成源磁力处理**：`buildCachePlan` 会将 `PikPak`、`115 Cloud` 等非源站缓存按 code 归入 `SourceMagnet`，见 `internal/migration/media/database.go:711-730`。这会把云盘缓存磁力混入源磁力选择逻辑，且原始驱动类型没有保留。

**中优先级问题**

- `JavDB` 的热门影片任务仍读写旧 `Film`/`MissedFilm` 表，但 `addStar` 已写入 `FilmWork`，见 `drivers/javdb/driver.go:66-74`、`drivers/javdb/job.go:693-727`。迁移后的新影片可能被重复扫描，失败记录也不会进入新模型。
- `Pornhub.Link` 对旧格式 `EmbyFileObj` 不再回退使用 `Url`，并且移除了旧的 Referer 设置，见 `drivers/pornhub/driver.go:185-199`。新模型对象通常有 `SourceRef`，但旧缓存对象可能直接无法生成播放链接。
- `Film.SubtitleOnly` 未迁移到 `FilmWork`，见 `internal/model/film.go:33`；`MagnetCache.SubtitleUrls` 也未迁移。当前搜索未发现这些字段仍被运行时使用，但从“源数据完整迁移”角度属于缺失。

**已确认正常**

- `FilmWork`、`FilmFile`、`SourceMagnet` 的身份、文件拓扑、NFO/图片路径迁移具备事务、冲突检测、幂等和 journal 恢复机制。
- 已迁移的单文件及多文件资源，列表投影会正确保留 `WorkID`、`FilmFileID`、分片序号、标题、标签、演员、源 URL 等信息，见 `drivers/virtual_file/media.go:124-158`。
- 定向测试通过：`javdb`、`fc2`、`pornhub`、`virtual_file`、`internal/db`、`internal/migration/media`、`cmd/migrate-media`。
- 全仓 `go test ./...` 未完全通过，但失败来自无关问题：`jable_tv` 的格式化 vet 报错、缺少系统 `fuse.h`、Aria2 测试服务未启动。

另外，当前分支 diff 中没有发现 `BFCR`/`bfcr` 相关代码；实际涉及的是 `javdb`、`fc2` 和 `pornhub`。