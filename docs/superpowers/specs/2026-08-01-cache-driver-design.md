# cache 驱动：挂载型列表持久化缓存

日期：2026-08-01
状态：已确认设计（等待实施）

## 背景与动机

下游存储（如 115、夸克等）访问不稳定或缓慢。希望新增一个挂载型驱动，将下游存储的**文件列表**持久化缓存到数据库，使目录浏览不再依赖下游访问；文件下载（Link）仍转发到下游实时获取。

**明确范围**：
- 只缓存文件列表（目录结构元数据），**不缓存文件内容**
- Link 恒转发下游，下游实时生成下载链接
- 附加属性（媒体元数据、驱动特有身份字段）不缓存、不暴露

## 核心决策

| 决策点 | 结论 | 理由 |
|---|---|---|
| 实现路径 | 独立驱动（方案 A） | 隔离性好、按挂载点选择性启用、不动核心缓存逻辑 |
| 缓存内容 | 对象基础字段快照（name/path/size/modified/ctime/is_folder/hash/id）+ 可选缩略图 | Obj 接口方法全集即可提取，任何驱动对象都适用 |
| 快照判定 | 全部缓存，无需类型判定 | 见"为何全量可行" |
| 新鲜度 | TTL + stale-if-error + 定时任务刷新 | 下游不稳定时仍可浏览 |
| 定时任务 | `pkg/cron`（`NewCron(d).Do(fn)` + `Stop()`，项目已有 15+ 驱动使用） | 复用现有模式，driver Init 启动、Drop 停止 |
| 强制刷新 | 消费 `ListArgs.Refresh`，透传链路已存在 | 前端 refresh=true → fs.List → op.List → 驱动 List |
| Get | 实现 `driver.Getter`，查 DB 快照按名匹配，miss 回源单文件 | 避免每次 Get 都 List 父目录 |
| Link | 纯转发 `op.Link(下游, path)` | 内部重新解析真实对象 |

## 为何全量快照可行（不破坏下游驱动功能）

1. **Link 是 path-based 的**：`op.Link(ctx, storage, path)` 内部会 `op.Get` 在下游重新解析**真实对象**（含全部内部字段），再调用下游驱动自己的 Link。115 的 `Pc` 字段、javdb 的 FilmFileID 等类型断言全部发生在 op.Link 内部，与缓存对象类型无关。
2. **Get 同理**：Getter miss 时回源 `op.Get(下游, path)` 单文件，拿真实对象。
3. **展示**：列表页只消费基础字段 + 缩略图，快照完全满足。

**已知取舍**：挂载 javdb/fc2 等媒体元数据驱动时，媒体墙/emby 集成功能退化（Title/Actors/海报等附加字段不暴露），退化为"普通文件列表 + 下载"。用户确认接受。

## 数据流

```
用户请求 /cache_mount/xxx
   │
   ▼
List(path):  查 DB 快照（CacheList 行）
  ├─ 命中且未过期 → 返回快照对象（0 次下游访问）
  ├─ 命中已过期 → op.List(下游) 回源：成功 → 写回 DB 并返回；失败 → 返回过期快照（stale-if-error）
  └─ 未命中 → op.List(下游) 回源：成功 → 写 DB 并返回；失败 → 报错
  注：args.Refresh=true 时跳过命中分支，强制回源

Get(path):  查 DB 父目录快照
  ├─ 命中（含过期，不判 TTL）→ 按 name 匹配返回（0 次下游访问）
  │   理由：Get 结果仅作展示与路径载体，Link 会重新解析真实对象
  └─ 未命中 → op.Get(下游, path) 回源（单文件，不写 DB，无完整列表）

Link(path): 恒转发 op.Link(下游, 路径) → 下游重新解析真实对象 → 返回链接

后台定时任务: 每 sync_interval_hours 遍历 DB 已知目录树（用快照中的子目录发现子目录，不访问下游发现结构），刷新 TTL 过期的目录
```

## 组件设计

### 1. 新增文件

| 文件 | 内容 |
|---|---|
| `drivers/cache/driver.go` | `Cache` 结构体（embed `model.Storage` + `Addition`），`Init/Drop/Config/GetAddition/Get/List/Link` |
| `drivers/cache/meta.go` | `Addition`（remote_path/ttl_hours/sync_interval_hours）、config、`RegisterDriver` |
| `drivers/cache/snapshot.go` | `CachedObj` 快照结构、对象 ↔ 快照转换（快照化/反快照化） |
| `drivers/cache/db.go` | `CacheList` GORM 读写/过期查询/清理 |
| `drivers/cache/sync.go` | 定时同步 goroutine（`pkg/cron`） |
| `internal/model/cache.go` | `CacheList` 模型定义（跨包共享） |
| `drivers/all.go` | 追加 `_ "…/drivers/cache"` |
| `internal/db/db.go` | AutoMigrate 追加 `CacheList`（db.go:14） |

### 2. Addition 配置

| 字段 | 默认 | 说明 |
|---|---|---|
| `remote_path` | 必填 | 挂载的下游存储路径 |
| `ttl_hours` | 24 | 列表快照有效期（小时），过期后定时任务刷新；浏览时过期回源失败仍返回过期快照 |
| `sync_interval_hours` | 1 | 后台同步周期（小时），0 = 禁用定时任务 |

### 3. DB 模型

```go
type CacheList struct {
    ID        uint      `gorm:"primaryKey"`
    StorageID uint      `gorm:"uniqueIndex:idx_cache_storage_dir"` // 缓存驱动自身 storage ID
    DirPath   string    `gorm:"uniqueIndex:idx_cache_storage_dir"` // 相对 remote_path 的目录路径
    Data      string    `gorm:"type:text"` // JSON 序列化的 []CachedObj 快照
    UpdatedAt time.Time
}
```

- 唯一索引 `(StorageID, DirPath)`：同一下游可被多个缓存挂载点引用，互不干扰
- 单行存一个目录的完整快照 JSON（SQLite text 上限足够）

### 4. 快照结构

```go
type CachedObj struct {
    ID        string
    Path      string
    Name      string
    Size      int64
    Modified  time.Time
    Ctime     time.Time
    IsFolder  bool
    HashInfo  utils.HashInfo
    Thumbnail string // 可选，原始对象带缩略图才存
}
```

转换规则：
- 快照化：从 `model.Obj` 接口方法提取基础字段；`model.GetThumb(obj)` 成功则带缩略图；`Path` 统一写相对 cache 挂载域路径（`stdpath.Join(dir.GetPath(), obj.GetName())`，覆盖下游返回的路径）
- 反快照化：Thumbnail 非空 → `*model.ObjThumb`，否则 → `*model.Object`

### 5. 转发实现（chunk 驱动模式）

```go
func (d *Cache) remote() (driver.Driver, string, error) {
    return op.GetStorageAndActualPath(d.RemotePath) // 下游存储 + 实际根路径
}

func (d *Cache) List(ctx, dir, args) ([]model.Obj, error) {
    // 1. DB 查询 (StorageID, dir.GetPath())
    // 2. 命中且未过期且 !args.Refresh → 反快照化返回
    // 3. 回源：op.List(ctx, remoteStorage, stdpath.Join(remoteActualPath, dir.GetPath()), args)
    //    成功 → 快照化写 DB（upsert）；返回统一反快照化对象（标准 Object/ObjThumb，缩略图保留）
    //    失败且有过期快照 → 返回过期快照
    // 注意：两种路径（缓存命中/回源）返回对象类型一致，均为快照反序列化的标准对象，
    // 不暴露下游驱动特有类型；快照中 Path 存相对 cache 挂载域的路径
}

func (d *Cache) Get(ctx, path) (model.Obj, error) {
    // 1. 父目录 DB 快照命中（含过期）→ 按 name 匹配返回
    // 2. 回源：op.Get(ctx, remoteStorage, stdpath.Join(remoteActualPath, path))
    //    成功 → 修正 Path 为相对 cache 挂载域后返回（包装为 model.Object）
}

func (d *Cache) Link(ctx, file, args) (*model.Link, error) {
    // 恒转发：op.Link(ctx, remoteStorage, stdpath.Join(remoteActualPath, file.GetPath()), args)
}
```

### 6. 定时同步

- `Init` 时若 `sync_interval_hours > 0`：`cron.NewCron(interval).Do(d.syncAll)`，`Drop` 时 `Stop()`
- `syncAll`：
  1. 查询该 storage 的全部 `CacheList` 行
  2. 按目录深度升序（先父后子），对 `UpdatedAt` 超过 TTL 的目录回源刷新
  3. 刷新后发现的子目录（快照中 IsFolder）若不在 DB → 加入待刷新队列（用缓存快照发现结构，不访问下游发现结构）
  4. 回源失败（下游错误/目录已删除）→ 保留过期快照（stale-if-error）；已删除 → 删除 DB 行
- 防重入：单 goroutine 串行执行，无需额外锁

### 7. 失效与清理

- TTL 过期：定时任务刷新；浏览时过期回源失败返回 stale
- `Drop()`：`cron.Stop()` + 删除该 storage 全部 CacheList 行
- 惰性清理：访问时顺带删除过期行（可选优化，首个版本可省略，由 Drop 兜底）

### 8. 错误处理

- 回源失败 + 无缓存 → 原样返回下游错误
- 回源失败 + 有过期缓存 → 返回过期快照（stale-if-error），不报错
- `remote_path` 无效 → Init 时校验并报错（参照 chunk 驱动 Init 校验模式）
- 下游存储未初始化（Status != WORK）→ `op.List` 自身会拒绝（op/fs.go:27），缓存命中路径不受影响

### 9. Config

- `NoCache: false`：保留 op 层内存目录缓存叠加（内存短 TTL + DB 长 TTL 双级）
- 只读驱动：不实现 MakeDir/Move/Rename/Copy/Remove/Put 等写接口（默认不支持）

## 测试策略

- 单元测试（`drivers/cache/`）：
  - 快照化/反快照化往返（含缩略图、空目录、特殊字符文件名）
  - List 命中缓存（下游零调用，用 mock driver 计数）
  - List 过期回源成功 → 写回 DB
  - List 过期回源失败 → 返回 stale
  - args.Refresh 强制回源
  - Get 命中父目录快照按名匹配 / miss 回源
  - Link 转发调用下游 op.Link
- 集成测试基建：参照 `internal/op/storage_test.go` 的 setupDB 模式
- 定时任务测试：缩短 TTL/interval 触发同步，验证只刷新过期目录

## 边界与已知限制

- 下游目录变更最迟 TTL 后可见；手动刷新（Refresh）立即可见
- 大目录单行 JSON 存储，超宽目录（数十万文件）需实测 SQLite 行大小
- 缓存快照中的缩略图 URL 可能有时效（签名 URL），过期仅影响图片显示
- 挂载媒体元数据驱动（javdb/fc2）时附加字段不暴露，媒体墙/emby 功能退化（已确认接受）
