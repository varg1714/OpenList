## 代码审查：`feature/film-manage` vs `main`

### 概述

该分支引入了**稳定的媒体身份模型**（`FilmWork` / `FilmFile` / `SourceMagnet`），替代了旧的 `Film` / `MagnetCache` 方案，核心目的是统一 JavDB、FC2、Pornhub 三个驱动的文件名管理方式。整体设计合理，测试覆盖较好，但存在以下几个问题。

---

### 发现的问题

#### 1. 旧 job 文件中遗留了不再调用的死代码

**文件**：`drivers/javdb/job.go`、`drivers/fc2/job.go`

旧的定时任务函数（`reMatchSubtitles`、`scanSynopsis`、`reMatchTags`、`filterFilms`、`scanSampleImages`、`scanDMMPosters`、`refreshNfo`）仍然存在于文件中，但 `Init()` 已不再调用它们。新逻辑全部搬到了 `media_job.go` 中。

#### 2. Pornhub `Link` 错误处理行为变更（破坏了容错降级）

**文件**：`drivers/pornhub/driver.go:183-194`

**旧代码**：`getVideoLink` 失败时记录 warn 日志并返回 mocked link（优雅降级）：
```go
link, err := d.getVideoLink(embyFile.Url)
if err != nil {
    utils.Log.Warnf("failed to get video link: %v", err.Error())
    return videoLink, nil
}
```

**新代码**：`getVideoLink` 失败时直接返回 error，不再降级到 mocked link：
```go
url, err := d.getVideoLink(embyFile.SourceRef)
if err != nil {
    return nil, err  // 破坏了原有的容错行为
}
```

这意味着视频链接抓取临时失败时，用户将直接报错而无法播放，即使配置了 `MockedLink` 也无法兜底。

#### 3. Pornhub `Link` 丢失了 `Referer` 请求头

**文件**：`drivers/pornhub/driver.go:183-194`

旧代码在返回的视频链接上设置了 Referer 头，Pornhub 可能依赖该头来提供视频流：
```go
videoLink.Header = http.Header{
    "Referer": []string{d.ServerUrl},
}
```
新代码遗漏了此设置。

#### 4. Pornhub 的 `Url` 字段语义变更

在 `buildDiscoveredWork` 中：
- `SourceRef = code`（view key，如 `"ph5f8b6a3c2d1e"`）
- `SourceURL = canonical`（完整 URL）

但 `ConvertMediaFileToEmbyFile` 设置 `Url = work.SourceURL`（完整 URL），而旧的 `EmbyFileObj.Url` 存的是 view key。如果有外部消费者依赖 `Url` 字段来调用 `getVideoLink`，会因值不匹配而出错。

---

### 正确维护的部分

| 功能 | 状态 | 说明 |
|------|------|------|
| 文件列表 | 无回归 | `List` 仍返回 `EmbyFileDirWrapper` 包装 `EmbyFileObj`，形态一致 |
| 下载链接（cloudplay） | 无回归 | `cloudPlayMedia` 使用相同的 `tool.CloudPlay`；磁力通过 `SourceMagnet` 解析，保留了 javdb→suke 回退逻辑 |
| 下载信息（元数据） | 无回归 | `EmbyFileObj` 的 `Title`、`Synopsis`、`Actors`、`Tags`、`ReleaseTime`、`Thumbnail` 均正确填充 |
| 磁力回退（javdb/fc2） | 无回归 | JavDB：JavDB 磁力 → SukeBei 回退；FC2：SukeBei |
| 虚拟文件 NFO | 增强 | 新的 `nfo.go` 使用基于身份的路径（按 storageID 隔离） |
| 物料管理 | 增强 | 基于身份的路径、带安全检查的迁移日志 |
| 删除操作 | 无回归 | 通过 `DeleteMediaWork` 正确删除作品、文件和物料 |

---

### 迁移工具质量

**文件**：`internal/migration/media/database.go`

迁移设计良好：
- **幂等**：`UpsertDiscoveredWork` 模式，先查后建
- **校验**：`validatePlan` 在迁移后验证 work 身份、文件拓扑和磁力完整性
- **路径安全**：`safeArtifactPath` 拒绝符号链接和路径穿越
- **冲突检测**：`IdentityCollisionError` 阻止覆盖不匹配的数据
- **日志**：`artifact_journal.go` 记录所有物料移动/删除操作以供审计

`db_migration_test.go` 中的测试正确验证了：
- 身份冲突会阻止启动时迁移
- 物料冲突会阻止启动时迁移
- 已存在的规范化数据不会被覆盖

---

### 建议修复项

1. **恢复 Pornhub Link 的容错降级** — `getVideoLink` 失败时仍应返回 mocked link
2. **恢复 Pornhub Referer 头** — 补回 `videoLink.Header`
3. **清理死代码** — 删除 `javdb/job.go` 和 `fc2/job.go` 中不再被调用的旧函数
4. **文档化 `Url` vs `SourceRef` 语义** — 外部消费者应使用 `SourceRef` 而非 `Url` 进行 Pornhub 查询