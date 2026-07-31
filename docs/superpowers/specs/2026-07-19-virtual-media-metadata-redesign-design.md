# 虚拟媒体元数据与稳定标识重构

**状态：** 已提议，设计已获批并进入规格说明
**日期：** 2026-07-19
**范围：** JavDB、FC2、Pornhub、共享虚拟媒体持久化、制品，以及 JavDB/FC2 云盘播放

## 1. 概述

以明确的影片主体和文件标识替换职责过载的 `Film.Name`、`Film.Title` 与 `Film.Url` 契约。

- 一个影片主体具有稳定、来源特定的 `Code`，并拥有元数据。
- 一个可播放文件是影片主体的一个分片，并根据 `Code` 及其分片索引投影出稳定文件名。
- JavDB 和 FC2 的来源磁力与 PikPak/115 远程文件缓存分开存储。
- JavDB、FC2 和 Pornhub 使用新的影片主体/文件模型。迁移后删除旧版 `Film` 表。
- JavDB 和 FC2 的列表请求保留轻量级来源列表发现，但不再翻译、补充元数据、获取磁力或写入本地制品。
- 保留现有 cron 任务。它们查询新的影片主体模型，并维护阶段特定的重试数据，而非消费新的通用任务队列。
- 通过预计算清单和持久化操作日志，原地迁移现有本地海报、背景图、NFO 文件和字幕。

部署在本地进行，整个迁移期间可以停止服务。不要求在线双写兼容性。

## 2. 当前问题

### 2.1 JavDB 标识编码在文件名中

JavDB 当前使用形如 `CODE translated title.mp4` 的文件名。消费者通过 `splitName`、`splitCode` 或 `av.GetFilmCode` 恢复编号。因此，`Name` 同时充当以下角色：

- 来源标识载体；
- OpenList/WebDAV 文件名；
- 显示文本；
- 本地制品目录键；
- 来源磁力缓存键；
- 云盘播放远程文件缓存键。

任何标题、翻译、清理或截断变更都可能改变依赖标识的行为和本地路径。

### 2.2 FC2 的文件名看似明确，但标识存储不一致

FC2 已暴露 `FC2-PPV-ID.mp4` 或 `FC2-PPV-ID-cdN.mp4`，但其 `Url` 字段可能包含裸数字 ID 或完整的 `FC2-PPV-ID`。运行时代码仍在多条路径中从 `Name` 恢复规范编号。

### 2.3 元数据工作阻塞列表请求

JavDB `List` 当前会进入 `mappingNames`，其可能抓取 AirAV、调用 OpenAI、替换数据库行并写入图片/NFO 文件。这使用户可见的列表延迟依赖外部元数据服务和本地文件系统工作。

### 2.4 来源缓存和云盘缓存共用一张职责过载的表

`MagnetCache` 表示两种不同概念：

1. JavDB/FC2 影片主体到来源磁力的映射。
2. PikPak/115 虚拟文件名到远程文件 ID 的映射。

这些层使用不同的 `DriverType` 值，以及不一致的 `Name`/`Code` 查询规则。提供商类型也无法标识具体云盘存储或账户。

### 2.5 CloudPlay 从名称重建多分片标识

CloudPlay 对远程文件排序并合成 `-cdN.mp4` 名称。这可能将分片绑定到错误的远程文件，并使正确性依赖文件名解析和排序顺序。

### 2.6 旧版 Film 模型混合影片主体元数据与文件行

FC2 多分片影片主体会在各行中重复全部元数据。JavDB 和 Pornhub 也将主目录位置及 NFO 演员元数据存储在同一行中。该模型无法表达一个值属于影片主体、可播放文件还是缓存条目。

## 3. 目标

1. 将规范 `Code` 用作影片主体唯一稳定的业务标识。
2. 仅通过 OpenList 和 WebDAV 暴露 `CODE.mp4` 或 `CODE-cdN.mp4`。
3. 标题及所有其他元数据均不进入文件名、路径、缓存键或链接标识。
4. 影片主体元数据只存储一次，文件分片单独存储。
5. 在 `List -> Get -> Link -> CloudPlay` 中传递类型化标识，不再重新解析字符串。
6. 保持 JavDB/FC2 列表发现轻量级，同时将元数据补充和制品处理移至现有 cron 任务。
7. 持久化足够的阶段特定状态，以重试失败的翻译和元数据扫描。
8. 在停服迁移期间保留现有海报、背景图、NFO 文件和字幕。
9. 将 Pornhub 迁移至相同的影片主体/文件模型，并删除旧版 `Film` 表。
10. 保持当前主目录语义：每个影片主体只有一个可见的 OpenList 目录，同时 `Actors` 仍为 NFO 元数据。

## 4. 非目标

- 不在通用 OpenList `Obj` 或前端 API 中添加单独的显示名称。
- 不在 OpenList/WebDAV 文件名中显示标题。
- 不构建通用的持久化任务队列。
- 不将 Pornhub 来源列表发现移入 cron。
- 不改变 Pornhub 直链行为。
- 不自动在 `Actors` 中的每位演员名下暴露影片主体。
- 不将无关驱动迁移到虚拟媒体模型。
- 不支持在线滚动迁移或旧/新模型双写。

## 5. 标识与投影规则

### 5.1 规范编号

`Code` 由来源适配器规范化，且绝不包含标题、扩展名或分片后缀。

- JavDB 示例：`ABP-123`。
- FC2 示例：`FC2-PPV-1234567`。
- Pornhub 示例：稳定的 `viewKey`。

规范唯一性范围为 `(StorageID, Source, Code)`。由于同一驱动可能共存多个实例，`StorageID` 必填。

运行时代码不得从 `Name`、`Title`、`Url`、`GetRealName` 或任何正则表达式推导标识。仅一次性迁移工具允许字符串解析。

### 5.2 公共文件名

公共名称是投影，不是存储的标识：

```text
PartCount == 1: CODE.mp4
PartCount > 1:  CODE-cd{PartIndex}.mp4
```

分片索引从 1 开始，且必须连续至 `PartCount`。

普通来源列表发现会创建单个逻辑文件。权威的多分片拓扑可在首次暴露前，由已经具有经验证磁力清单的工作流创建，例如 FC2 手工采集。后台扫描不得悄然将已暴露的 `CODE.mp4` 转换为 `CODE-cd1.mp4` 及其同级文件。这类拓扑变更需要显式重建操作。

### 5.3 元数据标题

存储字段保持分离：

- `RawTitle`：可恢复时去除编号前缀的来源标题。
- `TranslatedTitle`：成功翻译后去除编号前缀的标题。

NFO/显示标题投影如下：

```text
存在 TranslatedTitle：CODE + " " + TranslatedTitle
否则存在 RawTitle：CODE + " " + RawTitle
否则：CODE
```

投影标题不会作为标识持久化，业务逻辑也绝不解析它。

### 5.4 主目录和演员

`PrimaryDir` 决定影片主体可见的唯一 OpenList 目录。若在另一位演员名下重新发现，可更新 NFO `Actors` 数组，但不得移动 `PrimaryDir`。

`Actors` 是写入 NFO、供 Emby 演员关联使用的元数据。它不会创建额外 OpenList 目录条目。

## 6. 新数据模型

以下名称为逻辑名称。最终 GORM 表名必须遵循项目现有命名约定。

### 6.1 FilmWork

一行表示一个 OpenList 存储实例中的一个来源影片主体。

| 字段 | 类型/约束 | 含义 |
|---|---|---|
| `ID` | 主键 | 内部影片主体标识 |
| `StorageID` | 非空，已索引 | 所属 OpenList 存储实例 |
| `Source` | 非空 enum/string | `javdb`、`fc2` 或 `pornhub` |
| `Code` | 非空 | 规范来源编号 |
| `SourceRef` | 非空 | 来源原生稳定引用，例如 Pornhub viewKey 或 FC2 ID |
| `SourceURL` | 可空 | 可获取的规范详情 URL，绝不是裸编号 |
| `PrimaryDir` | 非空 | 唯一可见的演员/分类/收藏目录 |
| `RawTitle` | 可空 | 去除编号前缀的未翻译来源标题 |
| `TranslatedTitle` | 可空 | 去除编号前缀的成功翻译标题 |
| `Synopsis` | 可空 | 剧情简介 |
| `ImageURL` | 可空 | 选定的来源图片 URL |
| `ReleaseDate` | 可空 | 发行日期 |
| `Actors` | JSON/string array | NFO 演员，独立于目录可见性 |
| `Tags` | JSON/string array | 影片主体标签 |
| `MetadataVersion` | 非空，默认 1 | 已选元数据版本 |
| `NfoVersion` | 非空，默认 0 | 最后一次成功发布的 NFO 元数据版本 |
| 扫描/状态字段 | 来源/阶段特定 | 见第 9 节 |
| 时间戳 | 非空 | 发现与更新时间戳 |

约束：

- 唯一 `(StorageID, Source, Code)`；
- 经来源规范化后，`Code` 必须非空；
- `PrimaryDir` 仅能通过显式移动操作变更；
- 来源适配器负责编号规范化。

### 6.2 FilmFile

一行表示影片主体的一个可播放分片。

| 字段 | 类型/约束 | 含义 |
|---|---|---|
| `ID` | 主键 | 供 `Object.ID` 使用的内部文件标识 |
| `WorkID` | 非空 FK | 父影片主体 |
| `PartIndex` | 非空，check >= 1 | 稳定逻辑分片编号 |
| `PartCount` | 非空，check >= 1 | 预期同级分片数量 |
| `SourcePath` | 可空 | 来自权威磁力清单的路径/名称 |
| `SourceSize` | 可空 | 用于远程映射的大小证据 |
| `SourceFileFingerprint` | 可空 | 可选的稳定清单指纹 |
| 时间戳 | 非空 | 创建/更新时间戳 |

约束：

- 唯一 `(WorkID, PartIndex)`；
- 每个影片主体至少有一个文件；
- 所有同级行的 `PartCount` 一致；
- 索引从 1 到 `PartCount` 连续，由应用和迁移检查验证。

标识不需要文件名列。公共文件名根据父级 `Code`、`PartIndex` 和 `PartCount` 计算。

### 6.3 SourceMagnet

一个影片主体可以有多个来源磁力候选项。

| 字段 | 含义 |
|---|---|
| `ID`、`WorkID` | 磁力标识和父影片主体 |
| `MagnetURI` | 完整磁力 URI |
| `Fingerprint` | 规范化 info hash 或等效指纹 |
| `Provider` | JavDB、Sukebei 或其他来源 |
| `Priority`、`Selected` | 确定性的候选项选择 |
| `Subtitle` | 候选项是否已知包含字幕 |
| `FileManifest` | 来源文件路径、大小和映射证据 |
| `ScanAt`、`LastError` | 刷新及失败诊断信息 |

约束：

- 唯一 `(WorkID, Fingerprint)`；
- 每个影片主体最多一个已选磁力；
- 改变已选指纹会使关联到此前指纹的云盘缓存失效。

### 6.4 CloudFileCache

此表将逻辑 `FilmFile` 映射到由云盘播放创建的具体远程对象。

| 字段 | 含义 |
|---|---|
| `ID`、`FilmFileID` | 缓存行和逻辑文件 |
| `StorageIdentity` | 具体 PikPak/115 存储/账户标识，而非仅提供商类型 |
| `Provider` | PikPak 或 115 |
| `RemoteFileID` | 提供商远程文件 ID |
| `ProviderOptions` | 提供商数据，例如 115 `pickCode` |
| `MagnetFingerprint` | 创建远程文件时使用的来源磁力版本 |
| `VerifiedAt` | 上次成功验证远程链接的时间 |

约束：

- 唯一 `(StorageIdentity, FilmFileID)`；
- 在该提供商适用时，唯一 `(StorageIdentity, RemoteFileID)`；
- 指纹不匹配会使该行过期且不符合查询条件。

### 6.5 运行时对象标识

`EmbyFileObj` 必须携带：

- `WorkID`；
- `FilmFileID`；
- `Code`；
- `PartIndex`；
- `PartCount`；
- 来源适配器所需的显式来源引用/URL。

`Object.ID` 标识 `FilmFileID`。`Object.Name` 是投影的公共文件名。链接、删除、刷新和缓存操作使用类型化 ID 与字段，绝不重新解析名称。

## 7. 驱动行为

### 7.1 JavDB 和 FC2 List

List 保留轻量级来源列表发现：

1. 获取来源列表页面。
2. 规范化 `Code`、`SourceRef` 和 `SourceURL`。
3. Upsert 列表可用字段，例如 `RawTitle`、图片 URL、发行日期和主目录。
4. 确保新发现的影片主体存在默认的 `FilmFile` 第 1 分片。
5. 使用纯编号文件名返回当前数据库快照。

List 不得：

- 调用 AirAV 或 OpenAI；
- 获取来源磁力详情；
- 写入 NFO、海报、背景图、样片或字幕文件；
- 通过替换行来更改文件名；
- 在重新发现时重置成功的扫描状态。

### 7.2 Pornhub List 和 Link

Pornhub 保持现有同步方式：

- 来源发现可以继续使用当前 spider 流程；
- `viewKey` 成为 `Code` 和 `SourceRef`；
- 每个影片主体有一个 `FilmFile`；
- 公共名称仍为 `viewKey.mp4`；
- Link 从 `SourceRef` 解析直接来源视频 URL，且不使用 `SourceMagnet` 或 `CloudFileCache`。

### 7.3 现有 cron 任务

不得新增通用 `MediaJob` 表。保留现有驱动 cron 函数、扫描预算和批处理。其查询和更新从旧版 `Film` 行迁移到 `FilmWork`，并在需要时迁移到同级 `FilmFile` 行。

每个任务仅拥有其目标字段和扫描状态。一项失败必须被记录，批处理必须继续。

### 7.4 Link 和 CloudPlay

JavDB/FC2 Link 流程：

1. 接收类型化 `FilmFile` 标识。
2. 加载父影片主体和已选 `SourceMagnet`。
3. 若不存在可用磁力，使用显式 `Code`/`SourceURL` 同步获取并缓存候选项。
4. 解析具体云盘存储及其 `StorageIdentity`。
5. 按 `(StorageIdentity, FilmFileID)` 查找 `CloudFileCache`，并验证磁力指纹。
6. 命中有效缓存时，按 `RemoteFileID` 和提供商选项建立链接。
7. 未命中时，将已选磁力提交给配置的云盘提供商。
8. 使用权威清单路径/大小/指纹证据，将已下载远程对象映射到同级 `FilmFile` 行。
9. 仅在映射完整且无歧义时持久化映射。
10. 返回所请求分片的链接。

JavDB 保留主/备用云盘提供商回退。FC2 保留其配置的提供商行为。

CloudPlay 不得对远程文件排序，并推断第 N 项是 `cdN`。若映射不完整或有歧义，则返回错误且不写入缓存行。

## 8. 本地制品布局

新路径包含存储标识和稳定编号：

```text
data/emby/{source}/{storageID}/{primaryDir}/{CODE}/
```

预期内容包括：

- `poster.jpg`；
- `CODE.jpg` 和任何所需背景链接；
- `CODE.nfo`；
- `fanartN.jpg` 和样片图片；
- 基于 `CODE` 命名的字幕，以及在需要时带显式分片索引的字幕。

多分片文件共享影片主体目录。制品辅助函数接收 `StorageID`、`Code` 和显式分片信息。它们不得调用 `GetRealName` 恢复标识。

NFO 和图片发布使用临时文件，随后进行原子重命名。若制品发布失败，影片主体元数据仍是权威数据，制品阶段仍可重试。

## 9. 阶段状态与重试规则

`FilmWork` 的存在意味着影片主体已被发现。不存在聚合的 `MetadataStatus` 或 `DetailStatus`。

仅使用区分待处理、失败、完成、已禁用及重试资格所需的阶段特定数据：

- 翻译：显式 `TranslationStatus`、尝试次数、下次重试、最后错误和翻译版本；
- 简介：`Synopsis`、扫描时间戳，以及现有行为所需的重试时间戳/错误；
- 发行日期：可空值和扫描时间戳；
- 演员：可空/空值和扫描时间戳；
- 标签：当前标签和扫描时间戳/版本；
- 磁力：来源磁力存在性加上影片主体级扫描时间戳/错误；
- 海报、样片图片和字幕：迁移并保留其现有专用状态/扫描字段；
- NFO：当 `MetadataVersion > NfoVersion` 时重新生成。

建议字段和任务归属如下。字段可以直接位于 `FilmWork`；不为它们建立通用任务表。

| 阶段 | 权威结果 | 调度/重试字段 | 完成判定 |
|---|---|---|---|
| 翻译 | `TranslatedTitle` | `TranslationStatus`、`TranslationAttempts`、`TranslationNextRetryAt`、`TranslationLastError`、`TranslationVersion` | 状态为 `success`；Pornhub 为 `disabled` |
| 简介 | `Synopsis` | `SynopsisScanAt`、`SynopsisNextRetryAt`、`SynopsisLastError` | 简介非空，或来源已确认 `not_found` 且未到重扫版本 |
| 发行日期 | `ReleaseDate` | `ReleaseScanAt`、`ReleaseNextRetryAt`、`ReleaseLastError` | 日期非空，或来源已确认无数据 |
| 演员 | `Actors` | `ActorScanAt`、`ActorNextRetryAt`、`ActorLastError` | 演员非空，或来源已确认无数据 |
| 标签 | `Tags` | `TagScanAt`、`TagNextRetryAt`、`TagLastError`、`TagVersion` | 已按当前版本扫描；允许合法空数组 |
| 磁力 | `SourceMagnet` 行 | `MagnetScanAt`、`MagnetNextRetryAt`、`MagnetLastError` | 至少存在一个可选候选项，或来源已确认 `not_found` |
| DMM 海报 | 现有海报状态 | 迁移现有 `DMMPosterStatus`、`DMMPosterScanAt` | 沿用现有成功/未找到/短暂错误语义 |
| 样片图片 | 现有样片统计 | 迁移现有 count/complete/scan 字段 | 沿用现有组级完成语义 |
| 字幕 | 字幕文件及磁力字幕信息 | 独立扫描时间、下次重试和最后错误 | 已保存字幕，或在当前扫描版本下确认无结果 |
| NFO | 本地 `CODE.nfo` | `MetadataVersion`、`NfoVersion`、`NfoLastError` | `NfoVersion == MetadataVersion` |

`not_found` 是对应阶段的扫描结论，不是影片主体总状态。阶段版本变化或手动重扫可以清除该结论并重新尝试。

翻译需要显式状态，因为空结果无法区分未处理、翻译失败或已禁用的翻译。Pornhub 翻译为 `disabled`。

重试策略：

- 短暂失败使用指数退避；
- 保留现有批处理预算和来源速率限制；
- 一次失败仅影响一个影片主体和一个阶段；
- `not_found` 或已耗尽的短期重试可通过手动重新扫描或阶段版本递增重新激活；
- 已成功的影片主体不会因普通 List 重新发现而重置。

## 10. 停服迁移

### 10.0 迁移前后数据对照

| 旧数据 | 迁移前示例/含义 | 新数据 | 迁移后的变化 |
|---|---|---|---|
| JavDB `Film.Name` | `ABP-123 中文标题.mp4` | `FilmWork.Code=ABP-123` + `FilmFile(part=1)` | 对外文件名固定为 `ABP-123.mp4`，标题不再参与标识 |
| JavDB `Film.Url` | JavDB 详情页 URL | `SourceRef` + `SourceURL` | URL 仅用于来源访问，不再承担去重或 code 传递 |
| JavDB `Film.Title` | 通常为 `ABP-123 中文标题` | `RawTitle`/`TranslatedTitle` | 去掉 code 后按可确认来源拆分；NFO 生成时重新拼接 code |
| FC2 `Film.Name` | `FC2-PPV-123[-cdN].mp4` | `FilmWork.Code` + `FilmFile.PartIndex` | 文件名外观基本不变，分片身份从字符串转为字段 |
| FC2 `Film.Url` | 裸 `123` 或 `FC2-PPV-123` | `Code=FC2-PPV-123` + `SourceRef` | 消除双格式；`SourceURL` 仅在真实 URL 存在时填写 |
| Pornhub `Film.Name/Url` | `viewKey` 或其 `.mp4` 投影 | `Code=SourceRef=viewKey` + 单个 `FilmFile` | 对外仍为 `viewKey.mp4`，Link 直接使用 `SourceRef` |
| `Film.Actor`/`ActorId` | 唯一可见目录及历史辅助 ID | `FilmWork.PrimaryDir` + `StorageID` | 明确所属存储实例；重新发现不会移动主目录 |
| `Film.Actors` | NFO 演员数组 | `FilmWork.Actors` | 保持元数据语义，不创建额外目录入口 |
| 分片 Film 重复行 | 每个 `-cdN` 行重复标题、简介、演员、标签 | 一个 `FilmWork` + 多个 `FilmFile` | 影片元数据只维护一次 |
| Film 扫描字段 | 混在每个分片行上 | `FilmWork` 阶段字段 | 每部影片每个阶段只维护一份状态 |
| 来源层 `MagnetCache` | `javdb/fc2 + Name/Code -> Magnet` | 一个或多个 `SourceMagnet` | 按 `WorkID + Fingerprint` 去重，保留来源和文件清单 |
| 云盘层 `MagnetCache` | `PikPak/115 + Name -> FileId` | `CloudFileCache` | 按 `StorageIdentity + FilmFileID` 查询，并绑定磁力指纹 |
| AirAV `Film` 行 | JavDB 辅助命名缓存 | 不迁移为影片主体 | 计数并报告后删除，由后台任务重新抓取 |
| 本地制品目录 | `{source}/{dir}/{CODE Title}/` | `{source}/{storageID}/{dir}/{CODE}/` | 原地移动并按 code 重命名 sidecar；保留海报、背景图、样片和字幕 |

迁移工具必须为每一类输出迁移前数量、迁移后数量、合并数量、丢弃的可再生缓存数量及阻塞冲突数量。数量核对是删除旧表前的硬性关卡。

### 10.1 迁移机制

实现显式、可重复运行的迁移命令/工具。不得将数据转换隐藏在 GORM `AutoMigrate` 中。

必需阶段：

```text
停止服务
-> 备份数据库
-> 清点并哈希制品
-> 运行只读预检
-> 创建/填充新表
-> 生成制品清单
-> 使用持久化日志执行制品操作
-> 验证数据库、制品和驱动行为
-> 删除旧版 Film/AirAV 数据和表
-> 启动新服务
```

预检报告未解决的权威数据时，不进行任何变更。

### 10.2 解析 StorageID

旧版 Film 行不可靠地包含存储标识。

解析规则：

1. 若该来源恰有一个存储实例，则分配给它。
2. 若存在多个实例，尽可能使用唯一演员配置匹配。
3. 对个人收藏或其他歧义行，要求操作人员提供显式迁移映射。
4. 若权威行仍无法解析，则中止预检。

### 10.3 旧版影片主体分组

按已解析的 `(StorageID, Source, CanonicalCode)` 对旧版 JavDB、FC2 和 Pornhub 行分组。

- JavDB：仅在迁移期间解析旧版带标题名称，并验证结果。
- FC2：将裸 ID 和 `FC2-PPV-*` 变体规范化为完整规范形式。
- Pornhub：使用旧版 `Url`/来源引用中的 `viewKey`。
- AirAV：将行视为可重建的 JavDB 辅助缓存，报告数量，且不将其迁移为影片主体。

### 10.4 影片主体元数据合并

- 合并演员和标签。
- 仅当规范化值一致时选择非空标量值。
- 重复行的目录不一致时，保留最早旧版影片主体行的目录作为 `PrimaryDir`，在报告中列出该决定，并允许显式覆盖映射。
- 在存储 `RawTitle` 或 `TranslatedTitle` 前，从旧版标题值移除前导编号。
- 具有已知来源的现有翻译标题成为 `TranslatedTitle`。
- 无法确定来源的值保留在迁移报告中；缺失的原始标题保持为空，并可重新扫描。
- 冲突的非空权威标题、URL、编号或分片拓扑会中止预检，除非有已记录的来源特定规则解决冲突。

### 10.5 FilmFile 迁移

- 不含 `-cdN` 的旧版名称成为单文件影片主体的第 1 分片。
- 旧版 `-cdN` 后缀成为显式 `PartIndex=N`。
- 仅当标识和证据一致时，才合并重复的 `(WorkID, PartIndex)` 行。
- 分片必须不同且连续。
- 任何缺口、重复冲突或无效后缀都会中止预检。

### 10.6 磁力和云盘缓存迁移

将每个旧版 `MagnetCache` 行分类为来源层、云盘层、混合或无法解析。

- JavDB/FC2 来源行成为 `SourceMagnet`，按指纹去重。
- 保留多个有效候选磁力，并根据当前来源规则推导确定性的选择优先级。
- 仅当具体 `StorageIdentity` 和 `FilmFileID` 都能唯一解析时，提供商行才成为 `CloudFileCache`。
- 混合行可同时填充两张表。
- 歧义云盘提供商行可重建，可报告后经操作人员批准丢弃。
- 权威来源磁力必须保留，或显式列为阻塞冲突。

### 10.7 制品清单

在修改磁盘前，生成完整的机器可读预期操作列表。每项包括：

- 影片主体和文件 ID；
- 旧路径和目标路径；
- 操作类型（`move`、`rename`、`deduplicate`、`recreate-link`、`regenerate-nfo`）；
- 文件类型；
- 适用时的大小和内容哈希；
- 预检结果和冲突原因。

示例：

```text
MOVE data/emby/javdb/Actor/ABP-123 old title/
  -> data/emby/javdb/12/Actor/ABP-123/
RENAME .../ABP-123 old title.nfo -> .../ABP-123.nfo
KEEP .../fanart1.jpg SHA256=...
```

清单在变更前检测所有目标冲突。

### 10.8 制品日志

文件系统操作无法参与数据库事务。因此，迁移会在每项操作前后写入持久化日志条目。

- `pending`：操作已计划但尚未完成；
- `done`：后置条件已验证；
- `failed`：已记录错误，迁移停止。

日志支持中断运行恢复和逆序回滚。迁移绝不破坏性合并目录或覆盖现有不同文件。

冲突规则：

- 相同的常规文件在哈希比较后可以去重；
- 相同目标下内容不同则中止；
- 文件/目录冲突则中止；
- 意外或越界的符号链接则中止；
- 大小写折叠冲突则中止；
- 两个影片主体声明同一目标则中止。

文件移动后，从新模型重新生成 NFO 文件和所需背景链接，同时保留海报、背景图、样片和字幕。

### 10.9 旧版清理

仅在每个验证关卡均通过后：

1. 删除已迁移的 JavDB、FC2 和 Pornhub 行；
2. 删除可重建的 AirAV 行；
3. 删除旧版 `Film` 表和模型；
4. 删除旧 Film CRUD 和基于名称的解析路径；
5. 当不存在无关消费者时，删除已迁移的旧版 `MagnetCache` 行/表。

## 11. 回滚

最终清理前的回滚：

1. 若已为验证启动新服务，则停止它；
2. 按逆序反转已完成的制品日志操作；
3. 验证旧制品哈希和路径；
4. 恢复数据库备份；
5. 启动旧二进制文件/配置。

在新部署通过操作人员验收前，保留数据库备份、清单、日志、冲突报告和验证报告。

迁移必须支持 dry-run 且必须幂等：成功完成后再次运行时，应报告没有待执行变更。

## 12. 验证与验收

### 12.1 数据验证

- 没有未解决的存储标识或编号；
- 每个 `(StorageID, Source, Code)` 恰有一个影片主体；
- 每个影片主体至少一个文件；
- 分片索引连续且分片数一致；
- 按迁移分类统计的预期影片主体/文件数量；
- 演员/标签合并及标量冲突报告已核对；
- 每个已选来源磁力具有有效指纹；
- 每个迁移的云盘缓存均引用现有文件和存储标识；
- 不存在从文件名推导编号的剩余运行时查询。

### 12.2 制品验证

- 每个清单来源均已处理；
- 目标哈希与保留制品匹配；
- 验收后不再保留旧的带标题 JavDB 目录；
- 制品路径包含来源、存储 ID、主目录和编号；
- 多分片附属文件解析到预期影片主体/分片；
- 生成的 NFO 标题遵循 `CODE + title` 投影；
- NFO 演员与 `FilmWork.Actors` 匹配；
- 迁移回滚可在测试夹具中恢复旧目录树。

### 12.3 行为验证

- JavDB、FC2 和 Pornhub List/Get 返回纯编号文件名；
- JavDB/FC2 List 不执行翻译、AirAV、磁力或制品调用；
- Pornhub 直接 Link 行为保持不变；
- JavDB Link 支持主和备用提供商；
- FC2 Link 支持其配置的提供商；
- PikPak 和 115 缓存命中使用存储范围内的 `FilmFileID` 映射；
- 过期磁力指纹使云盘缓存条目失效；
- 缺失来源磁力触发同步获取和缓存；
- 歧义远程多分片映射失败且不写入缓存；
- 删除操作仅删除目标存储/影片主体/文件及相关制品/缓存；
- 翻译失败仍可重试，且绝不因空输出标记为成功。

## 13. 必需测试矩阵

1. JavDB、FC2 裸/完整 ID 及 Pornhub viewKey 的来源特定编号规范化。
2. 单文件和多分片影片主体的公共文件名投影。
3. 含翻译标题、仅原始标题和无标题数据的 NFO 标题投影。
4. 同一来源驱动两个实例的存储隔离。
5. 在另一位演员名下重新发现影片主体时的主目录稳定性。
6. 针对影片主体级阶段字段的现有 cron 任务查询。
7. 翻译重试、禁用翻译、模型回退和空结果处理。
8. 来源磁力候选项选择和指纹失效。
9. 同一提供商两个账户之间的云盘缓存隔离。
10. 按路径/大小进行远程清单映射及拒绝歧义映射。
11. JavDB 和 FC2 面向 PikPak 及 115 的单文件/多分片 CloudPlay。
12. 通过 SourceRef 的 Pornhub 直接 Link。
13. 对精确、多分片、重复、冲突和不可解析行的迁移 dry-run。
14. FC2 URL 规范化和 JavDB 带标题名称迁移。
15. 来源/云盘/混合旧版缓存分类。
16. 制品清单冲突检测、日志恢复、回滚和幂等重复运行。
17. 保留海报、背景图、样片、字幕和 NFO 内容。
18. 仅在验证关卡后执行最终旧版 Film/AirAV 清理。

## 14. 主要代码变更区域

实施计划至少必须覆盖：

- `internal/model/film.go`：在迁移支持存在后，以新模型替换旧版 Film/MagnetCache 概念。
- `internal/db/film.go` 和 `internal/db/cloudplay.go`：影片主体/文件/来源磁力/云盘缓存仓库与迁移查询。
- `drivers/virtual_file/film.go`：类型化影片主体/文件转换、分组、命名和路径解析。
- `drivers/virtual_file/nfo.go`、`poster.go` 和 `fanart.go`：感知存储、基于编号的制品路径。
- `drivers/javdb`：发现/补充分离、显式标识、任务、Link 和迁移适配器。
- `drivers/fc2`：规范编号/来源字段、影片主体/文件持久化、任务、Link 和缓存范围。
- `drivers/pornhub`：影片主体/文件持久化，以及使用 SourceRef 的直接 Link。
- `internal/offline_download/tool/cloud_play.go`：类型化文件标识、存储标识、清单映射和拆分后的缓存模型。
- `internal/av`：从已迁移流程中移除对 `GetFilmCode(name)` 的运行时依赖。
- `internal/bootstrap` 或专用命令包：显式停服迁移工具、报告、清单、日志、验证和回滚支持。

## 15. 已批准的设计决策

- 公共文件名为纯编号。
- NFO 标题包含编号加翻译/原始标题。
- JavDB/FC2 保留同步轻量级发现，补充处理在后台进行。
- Link 期间缺失磁力时，同步获取并缓存。
- 保留现有 cron/任务模式，不引入通用持久化任务队列。
- 阶段状态相互独立，不存在聚合元数据/详情状态。
- 本地制品原地迁移，不予丢弃。
- 影片主体元数据与可播放分片拆分。
- 来源磁力与云盘远程文件缓存拆分。
- 一个影片主体有一个主可见目录，演员仍为元数据。
- Pornhub 迁移至新的影片主体/文件模型。
- AirAV 旧版 Film 行视为可重建缓存。
- 成功迁移并验证后删除旧版 Film 表。
- 本规格工作流不使用 Plan Agent。
