以下是 `feature/film-manage` 相对 `main` 的审查结论。我用 5 个并行 agent 分别审查了各驱动、数据库 schema 和迁移工具,并对高影响项做了逐一手工核验。

## 总体结论

这次改动的**主体符合预期**:把旧的 `Film` 行 + 磁盘布局的管理方式,替换为类型化身份模型(`FilmWork`/`FilmFile`/`SourceMagnet`),运行时路径改为 `data/emby/{source}/{primaryDir}/{code}`。文件列表、元数据(magnet/字幕/synopsis/tags/样图/NFO/翻译)在三个驱动里基本都保留了。数据库 schema 合理,`AutoMigrate` 是增量的,旧 `Film`/`MagnetCache` 表和数据在正常启动时不会被破坏。迁移工具整体保守、fail-closed,**没有发现数据丢失级缺陷**——所有删除都经过哈希校验,legacy 行永不删除,无递归删除。

但核验中发现**几处真实的功能行为回退**,建议合并前确认是否为有意为之。

## 确认的功能回退

**1. Pornhub 播放链接丢失 `Referer` 头(高风险,需验证)**
`drivers/pornhub/driver.go` 旧 `Link()` 返回时设置了 `videoLink.Header = {"Referer": d.ServerUrl}`,新版本只设 `videoLink.URL`,不再带 Referer。如果 Pornhub CDN 的 mp4 要求 Referer 才能播放,链接会失效。这是纯粹的下载链接功能缺失,和文件名管理无关。

**2. FC2 播放每次都实时抓取,忽略已持久化的磁盘(可靠性回退)**
`drivers/fc2/media_link.go` 的 `cloudPlayMedia` 闭包每次播放都调 `mediaMagnets` → `av.GetMetaFromSuke` 实时抓取。对比之下 JAVDB 的 `getMagnet`(util.go:370)是 cache-first,读 `db.GetSelectedSourceMagnet`。所以 FC2 既没有 cache-first,也不使用它自己 upsert 进 `SourceMagnet` 的数据,Suke 一旦不可用播放就失败。JAVDB 无此问题。

**3. FC2 已删除的分类影片会在下次扫描时复活(行为回退)**
`drivers/fc2/util.go:40` 的 `getFilms` 无条件对每个抓到的 ID 执行 `UpsertDiscoveredWork`,不再过滤 missed/已删除。旧逻辑用 `QueryUnMissedFilms`/`CreateMissedFilms` 黑名单避免复活,现在 `QueryUnMissedFilms` 只剩 JAVDB 在用(job.go:711)。收藏夹不受影响,仅分类目录。

**4. JAVDB 播放丢失"第二磁盘"重试(弹性下降)**
旧 `Link()` 首个 magnet 失败后会从 Suke 取一个**不同的** magnet 重试;新版只是换 cloud driver 重试**同一个** magnet(`media_link.go:14-30`),然后回落 mock。`mediaMagnets` 的 javdb→Suke 回退仅在 javdb 返回 0 条时触发,对"有但下载失败"的 magnet 没有补救。

**5. `DeleteMediaFile` 不级联、破坏多段拓扑(数据完整性,中)**
`internal/db/media.go:506` 只删单条 `FilmFile`,不像 `DeleteFilmWork` 那样级联。删单段作品的唯一文件会留下孤儿 `FilmWork`+`SourceMagnet`;删多段中的第 2 段会留下 `part_count` 与实际不符的空洞,之后 `ReplaceFilmFiles` 会报拓扑错误。JAVDB 的 `Remove`(driver.go:214)单文件路径会走到这里。

## 低风险 / 需知悉项

- **磁盘选择在重扫时被覆盖**:`UpsertSourceMagnets` + `ensureSelectedSourceMagnet`(source_magnet.go)每次重扫会按 incoming 标志重新选,冲掉 `SelectSourceMagnet` 的手动选择。但**`SelectSourceMagnet` 目前无任何非测试调用者**,手动选择还没接到用户操作,所以是潜在 bug 而非当前回退。
- **迁移不复制 `MagnetCache.SubtitleUrls`/`FileId`/`Option` 到 `SourceMagnet`**:非数据丢失(`MagnetCache` 行保留,运行时下载仍直接读它),但新表本身不携带这些下载句柄/字幕 URL,文档值得补一句说明。`Film.SubtitleOnly` 同理未进新 schema。
- **迁移的 selected 选择不偏好带字幕磁盘**:取 cache ID 最小的作为 selected,与运行时 `ensureSelectedSourceMagnet` 自洽,但可能与 legacy 偏好带字幕的行为不同。全部磁盘都保留,可事后改。
- **同一 code 下两个不同视频文件会被合并**:`buildFileTopology` 只保留第一个的 `sourcePath`。视频在远端存储不在 `data/emby`,无磁盘丢失,但第二个视频的 `SourcePath` 未记录。属边界情况。

## 已排除的误报

- **Pornhub fanart/`-background` 丢失**:误报。Pornhub 仍走旧 `CacheImageAndNfo`→`CacheImage`(nfo.go:93 仍写 `-background`),只有 JAVDB/FC2 用新的 `PublishMediaPoster`。
- 各驱动的文件列表集合、文件名(`{code}.mp4`/`{code}-cdN.mp4`)、NFO 字段均保留。
- schema 的唯一索引/check 约束正确,legacy 表未动,迁移的字段映射、多段索引、哈希门控删除、journal v2 断点续传、v1 fail-closed 均与文档一致且有测试覆盖。

优先建议确认 **#1(Pornhub Referer)** 和 **#2(FC2 实时抓取)**,这两项直接影响你关心的"下载链接/下载信息"可用性;**#5** 是数据完整性,建议让 `DeleteMediaFile` 在删最后一段时级联删 work、或重排剩余段。