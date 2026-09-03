# 设计：Bilibili 本地快照 + 增量刷新（列表持久化）

日期：2026-09-03

## 背景与动机

当前 bilibili 驱动每次目录 List（框架 dirCache 过期后）都全量分页拉取 bilibili API：
一个 UP 的投稿可能几百条 = 十几页 × 2 请求，限流 2r/s 下需要数秒~十几秒，
且重启 openlist 后第一次浏览每个目录都要重新全量爬。用户诉求：

1. **浏览加速（A）**：目录刷新不再全量重拉，每次只查最新一页，有新内容才继续翻
2. **重启持久（C）**：数据入库永久存储，重启后秒开、断网/风控时仍有内容可看
3. 用户有**外部定时任务**，定期调用 openlist 的 list 接口（带 `refresh=true`
   穿透框架 dirCache）驱动数据更新——驱动不需要自己的定时器

### 方案选择（与用户逐项确认）

| 议题 | 选项 | 决策 |
|---|---|---|
| 存储模型 | 1. 框架通用"虚拟目录快照表"<br>2. 复用 FilmWork/FilmFile + 扩展字段<br>3. bilibili 专属表 | **1**——新表通用，未来 189pc 等虚拟目录驱动可复用；FilmWork 是影视作品语义（code/nfo/Emby 投影），硬塞污染公共设施 |
| 分页失败语义 | A. 原子替换（成功才整体写入）<br>B. 断点续传（逐页入库+完整性状态机） | **A**，且**部分结果不返回**（用户 Z 决策：数据稳定性优先，不允许半新半旧；页级退避重试保留，重试耗尽后 partial 语义作废，由旧快照兜底） |
| 刷新触发 | TTL 内不更新 vs 每次 List 都查 API | **每次 List 都被调用时查一次 API 看是否有新数据**，有则追加保存；无 TTL 概念——框架 dirCache（30min）已在上层挡重复调用，用户外部任务用 `refresh=true` 穿透 |
| 刷新按钮语义 | 强制全量重建 | 不做（用户外部任务负责刷新） |
| 容量上限 | MaxListItems 截断 vs 无上限 | **无上限**：拉取拉完 API 能给的所有数据；库不设容量、不裁剪（Addition MaxListItems 已由既有提交改为默认 0 = unlimited，字段保留可配） |
| 后台 job 预热（D） | cron 定时后台刷新 | 不做 v1.1，用户外部定时任务承担 |

## 非目标（v1.1 范围外）

- 后台定时刷新 job（openlist 内）
- 已删稿件/失效条目的校验与清理（永久存储语义：删除的稿件留在库里，播放时自然报错）
- 快照设施对 189pc / emby_wrapper 等其他驱动的接入（表通用，接入各自单独做）
- Link/Get/目录树结构/扫码登录逻辑：**全部不动**

## 架构

### 1. 框架通用快照表（internal/db + internal/model）

```go
// internal/model/virtual_snapshot.go
type VirtualDirSnapshot struct {
    ID        uint           `gorm:"primaryKey"`
    StorageID uint           `gorm:"index:idx_storage_key,priority:1"`
    DirKey    string         `gorm:"index:idx_storage_key,priority:2;type:varchar(255)"` // 驱动自定义，bilibili = 目录 obj.ID
    Owner     string         `gorm:"index:idx_storage_owner;type:varchar(64);default:''"` // 可选账号标识，bilibili = uid
    Data      datatypes.JSON `gorm:"type:json"`                                            // 驱动自管负载
    UpdatedAt time.Time
}
```

- 注册进 `internal/db/db.go` 的 AutoMigrate 列表（新表自动迁移）
- db API（internal/db/virtual_snapshot.go）：
  - `GetVirtualDirSnapshot(storageID uint, dirKey string) (*model.VirtualDirSnapshot, error)`
  - `UpsertVirtualDirSnapshot(s *model.VirtualDirSnapshot) error`（同 key 覆盖 data + updated_at）
  - `DeleteVirtualDirSnapshotsNotOwner(storageID uint, owner string) error`（一条 SQL，换账号清理用）

**DirKey 用目录 obj.ID 而非路径**：`followings` / `favs` / `up_{mid}` / `fav_{media_id}`——
不随显示名变化（重名消歧后缀、用户名改名都不影响快照命中）。

**Owner 语义**：bilibili 写入时填登录 uid。换账号（重新扫码/改 cookie）后 Init 拿到新 uid，
执行 `DeleteVirtualDirSnapshotsNotOwner`，防止旧账号数据混入新账号（增量"接上"判定
在换源后会把旧账号条目当已知基线，静默污染）。

### 2. 快照 Data 负载格式（bilibili 驱动自管）

```json
{
  "v": 1,
  "fetched_at": "2026-09-03T10:00:00+08:00",
  "items": [ "原始 API 条目（按目录类型）" ]
}
```

- 存**驱动已解析的原始条目**（非展示对象）：followings 目录存 `[]FollowItem`；
  up_/fav_ 目录存对应视频条目；favs 目录存收藏夹条目。快照只负责持久化，
  展示层（sanitizeName + 重名消歧 + videoObj 构造）每次从 items 重建——一次拉取、两种用途
- `fetched_at` 仅记录/调试用，无 TTL 语义

### 3. 驱动 List 数据流（统一门面，替代现有 listXxx 的直接 API 调用）

```
List 目录（dir.GetID() 分发不变）→ listWithSnapshot(key, fetchPage, keyOf, build):

  lock(key)  // 每目录粒度互斥（见并发）
  snap = db.Get(storageID, key)
  known = {keyOf(it) for it in snap.items}        // bvid / mid / media_id 集合
  merged = snap.items 的副本
  pn = 1
  loop:
    page, total, err = fetchPage(pn)              // 页级退避重试(1s/2s)保留
    err != nil:
      有快照 → unlock; 返回 build(snap.items)      // 降级：旧快照，不写库
      无快照 → unlock; 返回 err                    // 首次失败，明确报错
    page 为空 → break                              // API 拉完
    page 中全部 keyOf ∈ known → break              // 增量接上：无新内容
    merged += page 中未知条目（保持 API 顺序，新→旧在前）
    无快照 && len(merged) >= total → break         // 首次全量拉完
    pn++
  有新增（merged != snap.items）→ Upsert(snapshot{data: merged, fetched_at: now})
  unlock
  return build(merged)                             // 重建 obj 列表
```

要点：
- **全量 = 增量特例**：库空时 known 为空集，"页全已知"永不成立 → 拉到空页即全量
- **增量停止条件**：页内**出现任一已知条目即停**（实现偏差记录①：spec 原稿为
  "某页条目全部已在库中"——顺序假设下新条目连续在头部，一旦页内出现已知条目，
  该页之后必然全已知，继续翻页只会浪费请求；实现按此细化。全未知页继续翻；
  新增几十条 → 翻 1~3 页即停（每页 2 请求量级）
- **原子性**：库只在完整成功后整体覆盖；中途失败 → 库保持上次完整状态，
  本次 List 返回旧快照（用户可见、数据旧但完整）或首次报错。**绝无部分结果**
- 无新增时不写库（零 IO）

### 4. 并发控制

每目录（DirKey）粒度互斥：同一目录并发 List（dirCache 穿透 + 外部任务撞车）只允许
一次 API 拉取，其余等待共享结果；不同目录并行不受影响。
（实现选 singleflight 或自写 keyed mutex，不引入新依赖，看 go.mod 现成设施。）

### 5. 与既有代码的交互变更

| 位置 | 变更 |
|---|---|
| drivers/bilibili/api.go | `collectPages` 的 partial 语义作废（重试耗尽后直接返回错误）；`fetchWithRetry` 页级退避保留。调用方改走快照门面 |
| drivers/bilibili/driver.go | listFollowings/listUpVideos/listFavFolders/listFavVideos 改为经 listWithSnapshot 读快照数据重建（build 段 = 现展示逻辑抽出复用）；List/Get 分发、ID 体系、重名消歧不动 |
| drivers/bilibili/meta.go | Addition 不动（LimitRate/MaxListItems 已有提交处理，默认 0 = unlimited） |
| drivers/bilibili/driver.go Init | navInfo 成功后执行 DeleteVirtualDirSnapshotsNotOwner(storageID, uid)（换账号清理） |
| Drop / 编辑保存 | 不清快照（持久数据，同 qrcodeKey 保留原则） |
| 限流 | 不动：doGet 入口全局限流（burst 1 均匀间隔，默认 2r/s），分页节奏自然受控 |

### 6. 错误处理汇总

| 场景 | 行为 |
|---|---|
| 首次拉取失败（无快照） | List 返回错误（现状同款） |
| 增量检查失败（有快照） | 返回旧快照数据，不写库，下次 List 重试 |
| 页内部分失败（重试耗尽） | 同上——整个刷新作废，无部分结果 |
| 条目已删/不可见（播放时） | 快照保留，Link 报错（永久存储语义，范围外不清理） |
| 换账号 | Init 清非当前 uid 快照 → 首次浏览该目录走全量 |

## 顺序假设与验证点（实现时必须真实账号实测）

增量"接上即停"依赖列表**新条目在前**的 API 顺序：
- UP 投稿 arc/search：按发布时间倒序 ✓（已实测）
- 收藏夹视频：按收藏时间倒序（待实测确认）
- 关注列表 / 收藏夹列表：顺序（待实测确认）

若实测某列表顺序不符（新条目在尾部），该列表类型的增量会静默漏新 →
对策：该类型退化为每次全量拉取（关注/收藏夹列表本身很短，全量成本低，
不影响视频列表的增量收益）。spec 测试按实测确认后的顺序编写。

## 测试计划

- **internal/db**：新表 AutoMigrate 注册、Get/Upsert/DeleteNotOwner CRUD
  （沿用 internal/db 现有测试设施）
- **drivers/bilibili**（mockRoundTrip，离线）：
  - 首次 List → 全量多页拉完 → 落库 → 返回全量 obj
  - 二次 List 无新增 → 仅 1 次 API 请求（断言请求数）→ 返回库数据
  - 二次 List 有新增（第 1 页混入新 bvid）→ 翻页至接上 → 库追加保存
  - 增量中途页失败 → 返回旧快照、库不变、错误不返回
  - 首次失败 → 返回错误
  - 空目录 → 空快照落库，二次 List 单页即停
  - 换账号（uid 变化 + Init）→ 旧快照清除
  - 并发同目录 List → 仅一次 API 拉取
  - 快照重建下重名消歧/展示层输出稳定（沿用现有断言）
- **回归**：现有 41 项测试（目录树/扫码/播放/wbi）全绿

## 风险记录

- **大 UP 首拉耗时**：万级投稿 UP 全量 = 数百页 × 2 请求，2r/s 限流下可能数分钟；
  中途风控失败 → 整次作废下次重来（原子性代价）。实际 bilibili 分页 API 有上限，
  拉到空页自然停。可接受（仅首拉一次；增量每次 1~3 页）
- **顺序假设**（见上）——静默漏新的唯一风险点，实现时实测兜底
- **库体积**：无上限 + 永久累积，长列表 UP 数据量线性增长；
  单目录 items JSON 若达 MB 级（数千条），dirCache 内存与 List 返回体增大——可接受量级，记录不处理
- 用户已有提交（1ec38520/8cad8636 限流、风控、wbi 412 重签）与本设计兼容，不做回退
