# cache 驱动：定时同步白名单（SyncPaths）

日期：2026-08-02
状态：已确认设计（等待实施）

## 背景与动机

cache 驱动的后台定时任务 `syncAll` 会遍历所有已缓存目录并递归 BFS 下钻到每个子目录，最终把下游整棵树都扫描并写入缓存。当用户只关心下游的部分子目录时，整树扫描造成不必要的下游访问开销。

希望新增一个**同步白名单**配置：定时任务只扫描白名单定义的子树，其余目录不扫描、不刷新。

**明确范围**：
- 白名单**只影响定时任务同步**；浏览（List）行为完全不变（惰性缓存 + 手动强制刷新照旧）
- 不引入通配符/glob，仅精确目录路径
- 白名单留空 = 完全保持现有行为（向后兼容）

## 核心决策

| 决策点 | 结论 | 理由 |
|---|---|---|
| 配置形式 | `Addition` 新增 `SyncPaths string`，`type:"text"`，换行/逗号分隔 | 现有表单框架无数组字段；`type:"text"` 让前端渲染为多行文本输入框支持换行编辑 |
| 路径坐标系 | 条目填写**下游存储的实际路径**（与 `GetStorageAndActualPath` 返回的 actualPath 同坐标系），运行时转换为驱动相对坐标 | 用户以网盘内部目录（如 `/1`）配置，而非 OpenList 挂载路径（如 `/网盘/A/1`） |
| 条目语义 | 条目 = 整个子树递归同步；白名单目录即使从未浏览过也由定时任务首次拉取入缓存（主动种子同步） | 符合"扫描这些子目录"的直觉 |
| 白名单外缓存行 | 不刷新、不删除（保留但不触碰） | 浏览到时会自然重建；行为保守 |
| 覆盖语义 | 不变：List 取回什么就覆盖什么（UpsertCacheList），出错保留旧行 | 与现有 syncAll 一致 |
| 解析时机 | `syncAll` 每次运行时解析转换；`Init` 时也解析一次（仅日志校验，不阻断初始化） | 下游 storage 配置变更后依然正确 |
| 无效条目 | 不在 actualPath 之下的条目记日志忽略 | 配置错误不影响其余条目 |

## 坐标转换

```
actualPath = GetStorageAndActualPath(RemotePath).actualPath   // 例：/（网盘根）或 /xx（挂载点下的子目录）
对每个条目 w（下游实际路径，如 /1 或 /xx/1）:
  FixAndCleanPath(w)
  若 !utils.IsSubPath(actualPath, w) → 记日志忽略
  否则 rel = TrimPrefix(w, actualPath) → 驱动相对坐标（与缓存行 DirPath 一致）
```

- RemotePath=`/网盘/A` → actualPath=`/` → 白名单 `/1` → rel `/1`（网盘根下的 `1` 目录）
- RemotePath=`/网盘/A/xx` → actualPath=`/xx` → 白名单 `/xx/1` → rel `/1`
- 条目 == actualPath（如配置 `/` 或 `/xx`）→ rel `/` → 等价于全量同步

## 同步逻辑（syncAll）

```
无白名单：现有逻辑（全部缓存行按 TTL 刷新 + 递归下钻）

有白名单：
  1. 解析白名单 → rel 条目集合
  2. 队列种子 = rel 条目（未浏览也首次拉取）+ 白名单子树内已过期的缓存行
  3. BFS：
     - 列出目录 → UpsertCacheList（覆盖语义不变）
     - 子目录入队条件：子目录路径位于白名单子树内（路径边界匹配）
     - 白名单外的缓存行/子目录：不刷新、不删除
  4. 出错保留旧行（与现状一致）
```

子树匹配用路径边界：`child == entry || strings.HasPrefix(child, entry+"/")`（现有 `utils.IsSubPath(entry, child)` 即满足，但需注意参数顺序与语义）。

## 组件设计

### 修改文件

| 文件 | 内容 |
|---|---|
| `drivers/cache/meta.go` | `Addition` 新增 `SyncPaths string`（`type:"text"`，help 说明留空行为） |
| `drivers/cache/sync.go` | 新增解析/转换/匹配 helper；`syncAll` 按白名单约束扫描范围与下钻 |
| `drivers/cache/driver.go` | `Init` 时解析校验一次（仅日志） |

## 测试

| 用例 | 覆盖点 |
|---|---|
| 解析函数 | 换行/逗号混合分隔、去重、路径清理、空值/空白行丢弃 |
| 坐标转换 | actualPath=`/`；actualPath 为子路径；条目不在 actualPath 下（忽略） |
| 子树匹配 | 精确匹配、后代匹配、`/1` 不误匹配 `/12`、根 `/` |
| syncAll 白名单 | 种子同步未浏览白名单目录；跳过白名单外缓存行；BFS 下钻受限 |

## 已知取舍

- 白名单外目录的缓存行会一直留在 DB 中（不被刷新，浏览到时重建），不做主动清理
- 配置为多行文本，路径需手动填写，无图形化选择器
