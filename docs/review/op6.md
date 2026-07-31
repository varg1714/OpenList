结论：**不符合“仅调整文件名管理方式且不破坏原功能”的预期，当前不建议合并。**

注意：工作区实际检出的是 `main`，没有分支差异。以下按最匹配且最新的 `feature/film-manage`（`2016e6e6`）相对 `main` 审查。

**主要问题**
1. **[阻断] 同一影片无法同时属于多个演员、播放列表或个人收藏。**  
   `internal/model/media.go:69-75` 的唯一键只有 `(storage_id, source, code)`，但目录只由单个 `PrimaryDir` 表示。迁移在 `internal/migration/media/database.go:270` 合并身份，并在 `:529` 遇到不同目录时直接报冲突。JavDB 多演员影片、Pornhub 视频出现在多个列表、演员目录中的影片再加入个人收藏，都是正常场景；现在迁移会整体失败，运行时则只会显示在首次发现的目录。需要独立的 work-directory 关联表，或把目录纳入文件归属模型。

2. **[阻断] 115/PikPak 已下载文件缓存无法在新文件名下命中。**  
   迁移只生成 `SourceMagnet`，没有迁移 `MagnetCache.FileId`、`Option/pickCode` 和云端文件关联，见 `internal/migration/media/database.go:656-707`、`:801-805`。运行时仍在 `internal/offline_download/tool/cloud_play.go:48` 按 `driver_type + 新文件名` 精确查询旧表。旧的 `ABP-123 标题.mp4` 变为 `ABP-123.mp4` 后，已有下载链接和远端文件 ID 实际失效，会重新提交离线下载。迁移测试甚至构造了这些字段，但没有断言其目标映射，见 `internal/migration/media/database_test.go:311-315`。

3. **[高] 已迁移磁力和原有下载回退路径没有完整参与播放。**  
   FC2 在 `drivers/fc2/media_link.go:30-38` 每次实时抓取磁力，完全忽略已迁移的 `SourceMagnet` 和用户选择的磁力；网络不可用时，旧缓存存在也无法播放。JavDB 原来在首个磁力下载失败后会尝试 Suke 的另一磁力，现在 `drivers/javdb/driver.go:196-207` 只切换云盘提供方，仍使用同一磁力，属于下载容错能力下降。

4. **[高] 文件列表的大小和时间信息发生丢失。**  
   投影在 `drivers/virtual_file/media.go:138-140` 直接使用 `FilmFile.SourceSize/CreatedAt/UpdatedAt`。迁移的 `plannedFile` 不保存原时间和大小，创建记录时也未赋值，见 `internal/migration/media/database.go:142-147`、`:901-905`。结果是迁移后文件大小从原有非零展示值变为 `0`，创建和修改时间变为迁移时间，而不是旧 `Film.CreatedAt`。

5. **[高] 删除单个文件会留下无法恢复的 JavDB 空影片。**  
   `internal/db/media.go:506-513` 只删除 `FilmFile`，保留 work、磁力和附件。之后 JavDB 在 `drivers/javdb/util.go:44-58` 因 work 已存在而跳过发现，也不会重新执行 `EnsureSingleFilmFile`，导致影片永久从列表消失但相关数据仍残留。多段影片还会留下不连续的 `PartIndex/PartCount`。

6. **[高] Pornhub 下载链接行为被破坏。**  
   `drivers/pornhub/driver.go:194-199` 返回动态视频 URL 时不再附带原有的 `Referer: ServerUrl`，受防盗链保护的视频可能直接返回 403。提取链接失败时也从“返回 MockedLink”改成直接报错，这不是文件名管理变化。

7. **[中] 还有多项预期外功能变化。**  
   JavDB 的定时任务 `drivers/javdb/driver.go:53-64` 不再调用 `filterFilms`，`SyncNfo` 配置也已没有实际执行路径。Pornhub 的投影标题已经在 `drivers/virtual_file/media.go:152` 拼接过番号，`drivers/pornhub/util.go:108` 又拼一次，实际 NFO 会出现重复番号。播放列表目录标签和用户名为空时的演员目录回退也被移除。

**迁移评价**
本地附件迁移本身较严谨：有哈希碰撞预检、路径和符号链接检查、幂等 journal、事务写入，影片的标题、简介、演员、标签、发布日期及多数扫描状态也有迁移。主要缺陷在于目标模型无法表示多目录归属，且云端下载缓存字段没有目标映射；因此不能认定源数据已完整迁移。

相关包测试全部通过，`git diff --check` 也通过，但现有测试没有覆盖上述升级兼容场景。全量 `go test ./...` 被仓库环境问题阻断：缺少 `public/dist`、FUSE 头文件和 aria2 服务，另有既有 `jable_tv` vet 错误。

三个子任务结果已取回，整体上强化了刚才“不建议合并”的结论，并补充了几项遗漏。

**驱动行为审查**
- Pornhub 视频链接缺少 `Referer`，错误时也不再回退 `MockedLink`。
- Pornhub 海报下载同样丢失 `Referer`，可能缓存失败。
- FC2 删除影片不再写入 `MissedFilm`，下一轮扫描会重新添加。
- JavDB/FC2 删除单文件只删 `FilmFile`，没有同步清理磁力、附件和工作状态。
- JavDB 的翻译、演员、简介、磁力等从列表时同步获取改成定时异步获取，首次列表和新增收藏会暂时缺少元数据。
- `scanMediaSubtitles` 保存字幕后不再补充“字幕”标签或刷新 NFO。
- FC2 抓取错误现在会令整个目录列表失败；MissAV 批处理遇到一个错误就终止剩余项目。

**媒体投影审查**
- `Object.ID` 从旧 `Film.ID` 变为新 `FilmFile.ID`，迁移没有保留映射。若调用方持久化该 ID，会把同一影片识别为新对象。这项需要结合 API/Emby 实际用法确认影响等级。
- `Size` 从固定非零值变为通常未赋值的 `SourceSize=0`，确认是普遍问题。
- 文件创建和修改时间改成新 `FilmFile` 的建表时间，没有继承旧影片时间。
- JavDB/FC2 的 `Link` 强制要求 `FilmFileID`；任何遗留或手工构造的旧式 `EmbyFileObj` 都会报 `media file identity is missing`。
- 旧 `Film` 体系的大量任务函数仍留在代码中但已成为死代码，存在以后误调用旧表的风险。

**迁移审查**
- 再次确认 `MagnetCache.FileId`、`Option/pickCode`、`ScanCount`、`SubtitleUrls` 没有目标字段。
- 这些旧行虽然没有被物理删除，但新文件名无法命中旧 `MagnetCache.Name`，所以远端下载缓存实际上不可用。
- 同一 identity、同一目录存在多个非 `-cdN` 文件时，`buildFileTopology` 静默只取第一条 `SourcePath`，其他文件名被丢弃，没有冲突检查。
- CLI 只输出跳过数量，不输出被跳过的行 ID 和原因，迁移成功后难以审计具体损失。
- 正面结论是附件迁移设计较可靠：哈希校验、路径与符号链接安全、multipart 字幕重命名、事务、dry-run 和 journal 恢复均有较完整测试。

**对子任务结果的校正**
- 子任务声称 JavDB 原先也会写 `MissedFilm`，这一点不准确；该行为主要属于 FC2。JavDB 的实际回归是缓存和附件清理缺失，以及删除后留下空 work。
- 子任务认为 Pornhub 会进入通用 `mediaFileFromObj`，实际 Pornhub 有独立 `Link` 路径，因此不能据此认定 Pornhub 因缺少 `FilmFileID` 直接失败。
- `FileId/Option` 不是从数据库中删除，而是“没有迁移到新模型且因文件名变化无法再命中”，此前结论以此为准。

综合三组结果，至少多确认了 **Pornhub 海报 Referer、字幕标签/NFO、FC2 批处理失败策略、对象 ID 变化、非 multipart 重复文件丢失** 五类兼容问题。当前分支仍不能视为仅修改文件名管理方式。