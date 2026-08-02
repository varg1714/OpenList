# 设计：将 Cache 定时扫描职责抽取为独立 ScheduledSync 驱动

日期：2026-08-02

## 背景与动机

Cache 驱动目前承担两个职责：

1. **缓存（数据面）**：`List`/`Get` 服务 `CacheList`/`CachedObj` 表中的快照，miss 时回源并 upsert（drivers/cache/driver.go）
2. **定时扫描（控制面）**：`Init` 中构建 cron（`sync_cron_expr` 优先，否则 `sync_interval_hours`），触发 `syncAll` 对 stale 缓存行做白名单约束的 BFS 遍历刷新（drivers/cache/sync.go:82）

问题：定时扫描逻辑与 Cache 驱动强耦合，无法复用于其他驱动（例如需要定时预热普通存储、或驱动自身有内部缓存需要周期刷新）。本设计将定时扫描职责抽取为独立的 ScheduledSync 驱动，使其可挂载到任意下游存储；Cache 驱动只保留缓存与白名单展示过滤职责。

## 核心洞察：白名单由下游驱动自身强制

Cache 的 `sync_paths` 白名单有双重职责：

- **扫描范围**：`syncAll` 用 `withinSyncPaths` 限制刷新哪些目录
- **浏览过滤**：`List` 用 `filterCachedObjs`/`visibleInSyncPaths` 只返回白名单内的条目（drivers/cache/driver.go:117-118,145-156）

关键结论：**定时驱动不需要自己解析白名单**。Cache 的 `List` 已经只返回白名单目录，定时驱动只要"跟随 List 返回值递归遍历"，就永远不会触达白名单之外的目录，也不会产生多余的下游访问。白名单配置保留在 Cache 驱动（展示过滤职责），ScheduledSync 驱动将其视为黑盒。

## 方案选择

- **方案 A（选定）通用遍历触发器**：ScheduledSync 驱动按 cron 触发，对下游做 BFS 遍历调用 `op.List`，`Refresh` 参数由配置决定；Cache 驱动移除定时职责。白名单约束依赖下游 List 自身的过滤（Cache 场景）或驱动自身 `sync_paths`（普通存储场景）。
- 方案 B（弃）Cache 导出同步接口（如 `SyncPaths`）：定时驱动优先调用接口、否则回退遍历。引入接口层且行为分叉，不如统一"调 List"这一唯一机制——Cache 的 `List(Refresh:true)` 本身就会回源刷新缓存行，无需专用接口。
- 方案 C（弃）纯触发器：定时驱动只负责到点调用，由下游注册动作。无默认行为，对非 Cache 下游没有意义。

## 新驱动 ScheduledSync（drivers/scheduled_sync/）

### Addition

```go
type Addition struct {
	RemotePath   string `json:"remote_path" required:"true" help:"the mount path of the downstream storage"`
	SyncCronExpr string `json:"sync_cron_expr" required:"true" help:"cron expression for scheduled scan, e.g. 0 3 * * *"`
	SyncPaths    string `json:"sync_paths" help:"directories to scan (downstream actual paths, one per line or comma separated); empty = walk from downstream root"`
	Refresh      bool   `json:"refresh" default:"true" help:"pass Refresh=true to downstream List calls; for Cache downstream this force-refreshes cache rows"`
}
```

- 只支持 cron 表达式触发（用户需求），不引入 interval 回退
- 前端表单由 struct tag 反射自动生成，无需改前端

### 触发流程（每个 cron 周期）

1. `op.GetStorageAndActualPath(RemotePath)` 解析下游存储与 actual path；失败则记日志、跳过本次运行
2. 解析 `SyncPaths`（换行/逗号分隔，下游实际路径坐标）转成驱动相对坐标；为空则种子为根 `/`
3. 种子按 `dirDepth` 升序排列后入 BFS 队列
4. 循环：弹出目录 → `op.List(ctx, downstream, stdpath.Join(actualPath, dirPath), model.ListArgs{Refresh: Refresh})`
   - 失败：记日志继续（保持现有"保行不删"哲学）
   - 成功：返回结果中 `IsFolder` 的子目录，若配置了白名单则须 `withinSyncPaths` 通过，入队
5. 重入保护：`pkg/cron` 的 `SkipIfStillRunning` 链保证同实例串行执行

### 驱动生命周期

- `Init`：校验 `RemotePath` 非空、`SyncCronExpr` 合法（`cron.NewCronExpr` 解析失败返回错误，`initStorage` 会将该存储标记 failed 并展示错误）；启动 cron
- `Drop`：停止 cron
- `List`/`Get` 等数据面方法为 no-op（不可浏览的控制面驱动），`Config` 设 `NoUpload: true`
- 存储 disabled → `Drop` → cron 停止，生命周期跟随存储

## Cache 驱动变更

### 移除

- `Addition`：`SyncIntervalHours`、`SyncCronExpr`（drivers/cache/meta.go）
- `Cache` 结构体 `cron` 字段、`buildSyncCron`（driver.go:42-50）、`Init`/`Drop` 中的 cron 启停
- `syncAll`（sync.go:82-166）、`ListCacheLists`（db.go:46，唯一调用方是 syncAll）
- `TTLHours`：唯一使用方是 syncAll 的 staleness 判断（sync.go:93-106），浏览/读取路径从不检查 TTL，syncAll 移除后是死字段

### 保留

- `sync_paths`（浏览展示过滤职责）、`filterCachedObjs`/`visibleInSyncPaths`/`syncPathEntries`
- `List`/`Get`/`UpsertCacheList`/`snapshot.go` 全部逻辑——这是 ScheduledSync 驱动"安全跟随"的根基

### 共享代码（新建 internal/syncpaths 包）

从 drivers/cache/sync.go 移入两个驱动共用的工具：

- `parseSyncPaths`：白名单字符串（换行/逗号分隔）解析
- `withinSyncPaths`：判断相对路径是否位于白名单条目子树内
- `syncPathEntries`：白名单（下游实际路径坐标）转驱动相对坐标

留在 Cache（纯展示层）：`visibleInSyncPaths`、`filterCachedObjs`。

## 数据流

```
cron 触发
  → ScheduledSync 解析下游（remote_path）
  → BFS: op.List(downstream, dir, Refresh)
      ├─ Cache 下游: List 白名单过滤 + Refresh=true 回源 upsert 缓存行
      └─ 普通下游: 直接调远程 API（预热驱动内部缓存）
  → 仅对返回的 IsFolder 且白名单内子目录继续
```

## 行为差异与迁移影响

- 存量 Cache 配置中的 `sync_interval_hours`/`sync_cron_expr`/`ttl_hours` 在 unmarshal 时被静默丢弃（JSON 反序列化忽略未知字段），老用户的定时刷新停止；需新建 ScheduledSync 存储指向其 Cache 挂载路径
- `refresh=true` 时每次触发刷新所有访问到的目录，不再有 TTL 过期门槛；用户可通过 `refresh` 配置选择行为
- ScheduledSync 白名单为空 + Cache 下游：从根遍历，但根 List 已被 Cache 自身白名单过滤，实际扫描范围仍受控
- ScheduledSync 白名单非空 + 普通下游：等价于 Cache 旧的 whitelist 扫描范围语义（子树遍历）

## 错误处理

- 单目录 List 失败：记日志继续遍历（不删除、不清空缓存行）
- 下游解析失败/下游存储不存在：跳过本次运行
- cron 表达式非法：Init 返回错误，存储标记 failed，页面上可见

## 测试

- `internal/syncpaths`：`parseSyncPaths`/`withinSyncPaths`/`syncPathEntries` 单测（从 cache 测试迁移）
- ScheduledSync：利用现有 `op.GetStorageAndActualPath` 测试基建注册假下游，验证：
  - cron 触发后按白名单约束遍历、`Refresh` 参数按配置传递
  - 白名单为空时从根遍历
  - 目录 List 失败不中断整体遍历
  - Init 对非法 cron 表达式/空 remote_path 报错
- Cache：删除 syncAll/cron 相关测试（schedule_test.go、sync_test.go 中对应部分），保留并确认 List/Get/白名单展示过滤测试
