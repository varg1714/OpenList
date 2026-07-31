## 代码审查：`feature/film-manage` vs `main`

### 概述

该分支引入了**结构化媒体身份系统**，将旧的字符串型身份（`Film` + `MagnetCache` 表）替换为 `FilmWork` + `FilmFile` + `SourceMagnet` 表。51 个文件，+8700/-850 行。

**架构评估**：设计合理。实现了番号归一化（如 `FC2-PPV-` 前缀自动补齐）、多分片文件（`ABP-123-cd1.mp4`）、基于指纹的磁力去重，以及基于身份的静态资源路径（`emby/{source}/{dir}/{code}/poster.jpg`）。

---

### ✅ 功能完整性（无回归）

| 功能点 | 结论 |
|---|---|
| **文件列表** | ✅ `ListMediaFiles` → `ListFilmFilesWithWorks` → `ConvertMediaFileToEmbyFile` 返回完整 `EmbyFileObj`，含 ID、路径、文件名、大小、时间戳 |
| **下载链接 (Link)** | ✅ `mediaFileFromObj` → `GetSelectedSourceMagnet` / `mediaMagnets` → `tool.CloudPlay`，磁力解析逻辑完整保留 |
| **下载信息（磁力）** | ✅ `SourceMagnet` 存储 `MagnetURI`、`Fingerprint`、`Provider`、`Subtitle`、`Selected` |
| **元数据** | ✅ 所有字段（标题、演员、标签、简介、发行日期、预览图、DMM海报）均保留在 `FilmWork` 中 |
| **NFO/图片缓存** | ✅ 通过 `MediaIdentity` 走基于身份的路径。旧函数（`CacheImage`、`UpdateNfo`、`SaveSubtitles`）同时支持旧路径和新路径，向后兼容 |
| **字幕功能** | ✅ JavDB 字幕检测移至 `scanMediaMetadataAndMagnets`，字幕文件下载通过 `scanMediaSubtitles` + `SaveMediaSubtitles` |
| **删除 (Remove)** | ✅ `DeleteMediaWork` 同时删除 DB 记录和静态资源 |
| **收藏 (addStar)** | ✅ FC2 和 JavDB 的 `addStar` 均已重构，使用新表 + 基于身份的静态资源 |

---

### ✅ 迁移工具质量

迁移工具（`cmd/migrate-media` / `internal/migration/media/`）**设计严谨**：

- **两阶段**：预检（只读 DB 句柄做干运行验证）→ 执行（事务写入）
- **幂等**：可安全重复执行，有测试 `TestMigrateLegacyMediaRerunIsIdempotent` 验证
- **碰撞回滚**：身份碰撞、跨存储静态资源根碰撞在**任何写入之前**检测并阻止
- **优雅跳过**：孤立缓存、不兼容拓扑、不支持的源（`airav`）跳过并记录统计
- **边界覆盖**：FC2 字母数字番号（`050525_01-10MU`）、JavDB 紧凑番号（`hamesamurai0258`）、多分片（`cd1/cd2/cd3`）、Pornhub URL 推导番号
- **测试全部通过**：`internal/migration/media`（30+ 测试），`internal/db`、`drivers/virtual_file`、`internal/model` 均绿

---

### ⚠️ 发现的问题

#### 1. 死代码 —— 旧 cron 函数保留但不再被调用
**文件**：`drivers/fc2/job.go`、`drivers/javdb/job.go`

两个文件保留了旧的 cron 函数（`scanSampleImages`、`reMatchSubtitles`、`reMatchTags`、`scanSynopsis`、`refreshNfo`、`filterFilms`、`reMatchReleaseTime`），但新的 `Init()` cron 已不再调用它们。这些函数引用**旧表**（`MagnetCache`、旧 `Film`）和**旧静态资源路径**。能编译，但是死代码。

**结论**：代码债务，非回归。但可能误导后续维护者。

#### 2. Pornhub `Link()`：Referer 请求头丢失
**文件**：`drivers/pornhub/driver.go`

旧代码设置 `videoLink.Header = http.Header{"Referer": {d.ServerUrl}}`，新代码完全去掉了：

```go
// 旧：始终设置 Referer
videoLink.Header = http.Header{"Referer": []string{d.ServerUrl}}

// 新：不设置 Referer
```

对于**离线下载**（`OfflineDownload=true`），`tool.CloudPlay` 会自行处理请求头，无影响。对于**直链下载**（`OfflineDownload=false`），缺少 Referer 可能导致 Pornhub CDN 拒绝请求。

**建议**：确认下游 `tool.CloudPlay` 和直链 HTTP 客户端是否会自行添加 Pornhub Referer，若不会，`Link()` 仍需设置。

#### 3. JavDB 发现：翻译变为异步（非同步）
**文件**：`drivers/javdb/util.go`

旧 `getFilms` → `mappingNames()` 在目录列表时**同步**执行 AI 翻译 + NFO 缓存。新 `getFilms` → `fetchFilms()` 仅写入原始发现数据，翻译由 cron（`scanTranslations`）异步完成。

**影响**：新发现的 JavDB 影片在 cron 运行前显示原始标题（如 `ABP-123 Original Title`），而非翻译后的标题。标题仍然有含义，只是未经 AI 翻译。这是**有意的设计选择**（将发现与内容增强解耦），非 bug。

#### 4. FC2 `addStar`：去掉同步 whatLink 截图回退
**文件**：`drivers/fc2/util.go`

旧 `addStar` 在添加收藏时**同步**调用 `getWhatLinkInfo` 为无缩略图的影片获取截图。新 `addStar` 不再做此操作，截图由 `scanMediaSampleImages` cron 异步拉取。

**影响**：收藏后截图不会即时出现，下次 cron 才会拉取。同样是故意的行为变更。

#### 5. `fc2/shkebei.go` 已删除 —— 功能已迁移
该文件包含旧版 `getMagnet`（爬取 `sukebei.nyaa.si`）和 `cacheFC2Magnet`。已由 `media_link.go` 的 `mediaMagnets`（使用 `av.GetMetaFromSuke`）替代。功能等价，无回归。

---

### 📊 总结

| 类别 | 数量 |
|---|---|
| 功能回归 | **0** |
| 有意行为变更 | 2（异步翻译、延迟截图） |
| 需关注的潜在问题 | 1（Pornhub Referer 头） |
| 代码债务 | `fc2/job.go`、`javdb/job.go` 中死代码 |

**最终结论**：该分支正确地**仅做了文件名管理方式的变更**。文件列表、下载链接、下载信息、NFO 和图片静态资源的维护均通过新的身份系统完整保留。迁移工具测试充分、安全可靠。

**唯一待处理项**：验证 Pornhub `Link()` Referer 请求头缺失对直链下载的影响。