# 设计：Cache 驱动支持 Cron 表达式同步调度

日期：2026-08-02

## 背景与动机

Cache 驱动的后台同步调度目前只支持"固定间隔"（`SyncIntervalHours int`，每小时整数倍），无法配置"每天凌晨 3 点"这类定点触发。上一轮已将 `pkg/cron` 重写为基于 robfig/cron 的实现并新增了表达式能力（`NewCronExpr`），本设计将表达式能力接入 Cache 驱动的 Addition 配置，让用户在页面上输入 cron 表达式来自动调度后台同步。

## 关键约束：不能修改 SyncIntervalHours 的类型

`Addition` 配置持久化在 `model.Storage.Addition`（TEXT 列）中，是 **JSON 字符串**（internal/model/storage.go:15）。加载时通过 `json.Unmarshal` 反序列化到 Addition 结构体（internal/op/storage.go:111）。

- 现有用户数据形如 `{"sync_interval_hours": 2}`（数字）
- 若把 Go 字段类型改为 string，unmarshal 会报 `cannot unmarshal number into ... of type string`，`initStorage` 将该存储标记为 failed，**所有现有 Cache 存储全部加载失败**
- 因此：**保留 `SyncIntervalHours int` 原字段原类型不动**，新增独立字段，天然向后兼容（旧 JSON 无新 key → 零值）

前端表单由 struct tag 反射自动生成（internal/op/driver.go:46-53），新增字段自动出现在页面上，无需改前端仓库；一个字段只能是一种类型，不存在"number-or-string"混合 UI。

## 方案选择

- **方案 A（选定）隐式优先级**：新增 `SyncCronExpr string` 字段，cron 非空即用 cron，否则用原 interval。最少字段、无错误模式可配错。
- 方案 B（弃）显式 select 模式：新增 SyncMode select（interval/cron）。通用表单无联动显隐，两个输入框始终同屏，用户需手动保持模式与字段一致，多一个可配错的状态。
- 方案 C（弃）改字段类型 + 自定义 UnmarshalJSON 兼容 int/string：前端表单无法表达混合类型，且语义混淆。

## Addition 字段变更（drivers/cache/meta.go）

```go
type Addition struct {
	RemotePath        string `json:"remote_path" required:"true" help:"the mount path of the downstream storage"`
	TTLHours          int    `json:"ttl_hours" required:"true" type:"number" default:"24" help:"cache validity period in hours"`
	SyncIntervalHours int    `json:"sync_interval_hours" required:"true" type:"number" default:"1" help:"background sync interval in hours, 0 to disable"`
	SyncCronExpr      string `json:"sync_cron_expr" type:"text" help:"cron expression for background sync, e.g. 0 3 * * * or @every 12h; empty = use sync_interval_hours above"`
	SyncPaths         string `json:"sync_paths" type:"text" help:"directories to sync (downstream actual paths, one per line or comma separated); empty = sync all cached"`
}
```

- `SyncIntervalHours` 及默认值（`default:"1"`、init 中 `SyncIntervalHours: 1`）全部保留
- 前端自动渲染 text 输入框 + help 文案

## 调度逻辑（drivers/cache/driver.go Init）

```go
if d.cron != nil {
	d.cron.Stop()
	d.cron = nil
}
switch {
case strings.TrimSpace(d.SyncCronExpr) != "":
	c, err := cron.NewCronExpr(strings.TrimSpace(d.SyncCronExpr))
	if err != nil {
		return errors.Wrapf(err, "cache: invalid sync_cron_expr %q", d.SyncCronExpr)
	}
	d.cron = c
	d.cron.Do(d.syncAll)
case d.SyncIntervalHours > 0:
	d.cron = cron.NewCron(time.Duration(d.SyncIntervalHours) * time.Hour)
	d.cron.Do(d.syncAll)
}
```

优先级与语义：

| SyncCronExpr | SyncIntervalHours | 行为 |
|---|---|---|
| 非空（合法） | 任意 | cron 表达式调度（cron 优先） |
| 非空（非法） | 任意 | Init 报错，存储状态显示原因 |
| 空 | > 0 | interval 调度（与现状一致） |
| 空 | 0 | 禁用（与现状一致） |

## 错误处理

- 非法表达式（`60 * * * *`、`abc`、`0 3 * *`）：`NewCronExpr` 返回 error → Init 返回错误 → `initStorage` 标记存储 failed，状态栏展示具体错误
- 不静默回退到 interval（避免用户以为配置已生效）
- `TrimSpace` 防手误前后空格

## 测试（drivers/cache/）

1. **向后兼容回归**（最关键）：旧 JSON `{"sync_interval_hours": 2}` unmarshal 到新 Addition 成功且 `SyncCronExpr == ""`
2. Init 调度分支：
   - cron 合法 + interval 任意 → 创建 cron 调度（断言内部 cron 实例按表达式工作，或通过 cron 实例非 nil + Drop 无泄漏）
   - cron 非法 → Init 返回错误（含表达式原文）
   - cron 空 + interval > 0 → interval 调度
   - cron 空 + interval 0 → 无调度
3. 复用 pkg/cron 的 `NewCronExpr`（其自身已有表达式解析与定点触发的测试覆盖），Cache 侧只验证分支选择与错误传播

## 明确不做（Out of Scope）

- 不改 SyncIntervalHours 类型、不做数据迁移
- 不加显式 select 模式、不做前端联动显隐
- 不给其他驱动（pornhub/javdb/fc2 等）加 cron 配置——它们同样用 `NewCron(duration)` 的 interval 模式，本次只改 Cache 驱动
- 不做表达式合法性的前端实时校验（Init 失败时存储状态会显示原因）
