**总体结论：不建议合并**

5 条审查线中：2 条通过、3 条失败。虽然新增测试全部通过，但没有覆盖关键旧契约。

**阻塞问题**
- **Pornhub 下载请求头丢失**：`Link()` 不再设置旧有的 `Referer`，可能导致 CDN 返回 403。`drivers/pornhub/driver.go:185`
- **历史云播缓存失效**：云播仍以文件名查询 `MagnetCache.FileId/Option`，而新文件名变成纯番号；迁移工具没有重建缓存键，历史下载信息无法命中，首次播放会重新离线下载。`internal/offline_download/tool/cloud_play.go:45` `internal/model/media.go:43`
- **FC2 已迁移磁力未被播放路径使用**：迁移会写入 `SourceMagnet`，但 FC2 播放仍每次实时访问 Sukebei，而不是先读取迁移后的磁力。外部网站不可用时，即使数据库已有磁力也会播放失败。`drivers/fc2/media_link.go:17` `drivers/fc2/media_link.go:42`
- **JavDB 收藏行为退化**：新增收藏不再即时翻译、补充简介和生成海报/NFO，只依赖后续定时任务。`drivers/javdb/util.go:239`
- **JavDB 过滤功能被移除**：旧 cron 调用的 `filterFilms()` 没有新实现，现有 Filter 配置静默失效。`drivers/javdb/driver.go:52`

**迁移评价**
- 影片身份、标题、演员、标签、多分片、磁力 URI、NFO/海报路径迁移整体设计较严谨，具备事务、dry-run、碰撞检测和 journal 恢复。
- 但 `MagnetCache.FileId/Option` 仍被当前云播代码使用，却没有按新文件名迁移；因此不能认定“下载信息完整迁移”。
- `SubtitleOnly`、`ScanCount`、`SubtitleUrls` 等字段也没有目标映射，需要明确确认属于可废弃数据。`internal/model/film.go:43` `internal/migration/media/database.go:656`
- 被跳过的缓存仅记录 ID，不删除源行；正式迁移前必须逐项审查 `SkippedMagnetCaches`。

**验证情况**
- JavDB、FC2、Pornhub、virtual-file、数据库和迁移相关定向测试均通过。
- 缺少 `Link → CloudPlay → 历史缓存命中`、Pornhub Referer、JavDB Filter/即时元数据的回归测试。
- 纯番号文件名是明确的用户可见变化；若这是设计目标可以接受，但不能据此牺牲下载缓存和元数据行为。