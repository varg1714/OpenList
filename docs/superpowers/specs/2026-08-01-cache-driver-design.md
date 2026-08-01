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
| 新鲜度 | 定时任务刷新 + 手动强制刷新；List 命中即返回不判 TTL | 浏览时 0 次下游访问，新鲜度由后台保证 |
| 定时任务 | `pkg/cron`（`NewCron(d).Do(fn)` + `Stop()`，项目已有 15+ 驱动使用） | 复用现有模式，driver Init 启动、Drop 停止（不清数据） |
| 强制刷新 | 消费 `ListArgs.Refresh`，透传链路已存在 | 前端 refresh=true → fs.List → op.List → 驱动 List |
| Get | 实现 `driver.Getter`，查 DB 快照按名匹配，miss 回源单文件 | 避免每次 Get 都 List 父目录 |
| Link | 纯转发 `op.Link(下游, path)` | 内部重新解析真实对象 |
| DB 写入 | 每目录一行，整行 JSON 完整覆盖（upsert） | 覆盖天然实现增删同步，单行写入原子 |

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
  ├─ 命中 → 返回快照对象（0 次下游访问；不判 TTL，新鲜度由定时任务保证）
  └─ 未命中 → op.List(下游) 回源：成功 → 写 DB 并返回；失败 → 报错
  注：args.Refresh=true 时跳过命中分支，强制回源并更新 DB

Get(path):  查 DB 父目录快照
  ├─ 命中（含过期，不判 TTL）→ 按 name 匹配返回（0 次下游访问）
  │   理由：Get 结果仅作展示与路径载体，Link 会重新解析真实对象
  └─ 未命中 → op.Get(下游, path) 回源（单文件，不写 DB，无完整列表）

Link(path): 恒转发 op.Link(下游, 路径) → 下游重新解析真实对象 → 返回链接

后台定时任务: 每 sync_interval_hours 遍历 DB 已知目录树
  - 遍历结构来自 DB 快照（快照中的子目录即待刷新对象，不访问下游发现结构）
  - 对 UpdatedAt 超过 TTL 的目录调用下游 op.List 回源
  - 回源成功 → 完整覆盖 DB 行（覆盖天然实现：删除缺失项、新增新项、改名）
  - 回源失败 → 保留旧缓存行（stale），记录详细日志；待日志样本足够后再按错误细节决定保留/删除策略
```

## 组件设计

### 1. 新增文件

| 文件 | 内容 |
|---|---|
| `drivers/cache/driver.go` | `Cache` 结构体（embed `model.Storage` + `Addition`），`Init/Drop/Config/GetAddition/Get/List/Link` |
| `drivers/cache/meta.go` | `Addition`（remote_path/ttl_hours/sync_interval_hours）、config、`RegisterDriver` |
| `drivers/cache/snapshot.go` | 对象 ↔ 快照转换（`toCachedObj`/`fromCachedObj`，基于 `model.CachedObj`） |
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
	ID        uint        `gorm:"primaryKey"`
	StorageID uint        `gorm:"uniqueIndex:idx_cache_storage_dir"` // 缓存驱动自身 storage ID
	DirPath   string      `gorm:"uniqueIndex:idx_cache_storage_dir"` // 相对 remote_path 的目录路径
	Data      []CachedObj `gorm:"type:json;serializer:json"`         // JSON 对象直接存储（项目先例 film.go:89），整行覆盖 upsert
	UpdatedAt time.Time
}
```

- 唯一索引 `(StorageID, DirPath)`：同一下游可被多个缓存挂载点引用，互不干扰
- 单行存一个目录的完整快照（SQLite json 字段上限足够），整行覆盖天然实现增删同步，单行写入原子

### 4. 快照结构

```go
type CachedObj struct {
	ID        string            `json:"id"`
	Path      string            `json:"path"`
	Name      string            `json:"name"`
	Size      int64             `json:"size"`
	Modified  time.Time         `json:"modified"`
	Ctime     time.Time         `json:"ctime"`
	IsFolder  bool              `json:"is_folder"`
	HashInfo  map[string]string `json:"hash_info"` // hash 类型名 → 值（序列化安全；utils.HashInfo 的 JSON 输出为 {}，不可直接持久化）
	Thumbnail string            `json:"thumbnail"`
}
```

转换规则：
- 快照化：从 `model.Obj` 接口方法提取基础字段；`model.GetThumb(obj)` 成功则带缩略图；`Path` 统一写相对 cache 挂载域路径（`stdpath.Join(dir.GetPath(), obj.GetName())`，覆盖下游返回的路径）；HashInfo 经 `obj.GetHash().Export()` 转为 `map[string]string`
- 反快照化：Thumbnail 非空 → `*model.ObjThumb`，否则 → `*model.Object`；HashInfo 经 `utils.GetHashByName` 逐个恢复为 `utils.NewHashInfoByMap`

### 5. 转发实现（chunk 驱动模式）

```go
func (d *Cache) remote() (driver.Driver, string, error) {
    return op.GetStorageAndActualPath(d.RemotePath) // 下游存储 + 实际根路径
}

func (d *Cache) List(ctx, dir, args) ([]model.Obj, error) {
    // 1. DB 查询 (StorageID, dir.GetPath())
    // 2. 命中且 !args.Refresh → 反快照化返回（不判 TTL）
    // 3. 回源：op.List(ctx, remoteStorage, stdpath.Join(remoteActualPath, dir.GetPath()), args)
    //    成功 → 快照化写 DB（整行覆盖 upsert）；返回统一反快照化对象（标准 Object/ObjThumb，缩略图保留）
    //    失败 → 报错（无缓存可用，原样返回下游错误）
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
  2. 按目录深度升序（先父后子），选出 `UpdatedAt` 超过 TTL 的目录作为待刷新集合
  3. 对待刷新目录**调用下游** `op.List` 回源：
     - 成功 → 以最新结果**完整覆盖** DB 行（覆盖天然实现：删除缺失项、新增新项、改名）
     - 失败 → **保留旧缓存行**（stale）并记录详细日志（不删行：下游超时/5xx 属暂时性故障，删行会导致浏览 miss 回源雪崩；待日志收集充分后按错误细节决定保留/删除策略）
  4. 刷新后的快照中发现的新子目录（快照中 IsFolder 且不在 DB）→ 加入待刷新队列，直至无新目录
- 遍历结构（哪个目录需要刷新）来自 DB 快照，不访问下游发现结构；**刷新内容必须来自下游**
- 防重入：单 goroutine 串行执行，无需额外锁

### 7. 失效与清理

- TTL 过期：仅由定时任务判断并刷新（List 命中不判 TTL）
- `Drop()`：仅 `cron.Stop()`，**不删除 DB 记录**——重启后缓存继续有效（持久化意义所在）
- 定时任务回源失败：**不删行**，保留旧缓存（stale）并记录详细日志（错误类型区分策略待日志样本收集后决定）
- 存储被删除后残留的孤儿行：接受（数据量小，每目录一行；重新挂载同 remote_path 可复用）

### 8. 错误处理

- 回源失败 + 无缓存 → 原样返回下游错误
- 定时任务回源失败 → 保留该目录旧缓存行（stale），记录详细日志（含 dirPath 与完整错误），不阻塞后续目录
- `remote_path` 无效 → Init 时校验并报错（参照 chunk 驱动 Init 校验模式）
- 下游存储未初始化（Status != WORK）→ `op.List` 自身会拒绝（op/fs.go:27），缓存命中路径不受影响

### 9. Config

- `NoCache: false`：保留 op 层内存目录缓存叠加（内存短 TTL + DB 长 TTL 双级）
- 只读驱动：不实现 MakeDir/Move/Rename/Copy/Remove/Put 等写接口（默认不支持）

## 测试策略

- 单元测试（`drivers/cache/`）：
  - 快照化/反快照化往返（含缩略图、空目录、特殊字符文件名）
  - List 命中缓存（下游零调用，用 mock driver 计数）
  - List miss 回源成功 → 写 DB 且返回快照对象
  - List miss 回源失败 → 报错
  - args.Refresh 强制回源并更新 DB
  - Get 命中父目录快照按名匹配 / miss 回源
  - Link 转发调用下游 op.Link
  - 定时任务：过期目录被下游回源覆盖（含删除缺失项/新增项），失败目录行保留为 stale
- 集成测试基建：参照 `internal/op/storage_test.go` 的 setupDB 模式
- 定时任务测试：缩短 TTL/interval 触发同步，验证只刷新过期目录、覆盖语义正确

## 边界与已知限制

- 下游目录变更最迟在下一次定时任务刷新后可见；手动刷新（Refresh）立即可见
- 大目录单行 JSON 存储，超宽目录（数十万文件）需实测 SQLite 行大小
- 缓存快照中的缩略图 URL 可能有时效（签名 URL），过期仅影响图片显示
- 挂载媒体元数据驱动（javdb/fc2）时附加字段不暴露，媒体墙/emby 功能退化（已确认接受）
- `sync_interval_hours=0` 且无手动刷新时，列表保持静态（DB 有即返回）
