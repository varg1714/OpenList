# 设计：pkg/cron 基于 robfig/cron 重写，新增表达式调度能力

日期：2026-08-02

## 背景与动机

项目现有的 `pkg/cron` 是一个自研轻量定时器，本质是 `time.NewTicker` 的封装（`pkg/cron/cron.go`）：

```go
type Cron struct {
    d  time.Duration
    ch chan struct{}
}
func NewCron(d time.Duration) *Cron
func (c *Cron) Do(f func())
func (c *Cron) Stop()
```

它只支持"每隔固定时长触发"，不支持 Linux 风格的 cron 表达式（如每天凌晨 3 点 `0 3 * * *`）。

需求：

1. 替换为行业标准库 `github.com/robfig/cron/v3`（MIT 协议，k8s/gitea/vault 等大型项目长期使用；目前 Go 生态中表达式调度的事实标准）。
2. **完全保留**现有 `NewCron(duration)` / `Do(f)` / `Stop()` 的 API 与语义，17 处现有调用方零改动。
3. 新增表达式 API，支持 Linux 标准 5 段表达式（以及可选的 6 段带秒写法）。
4. 只做库级能力，不改任何驱动配置，不暴露用户可见设置。

## 方案选择

- **方案 A（选定）**：`pkg/cron` 内部改为 robfig 实现，旧 API 语义不变（duration 转 `@every` 描述符），新增 `NewCronExpr`。
- 方案 B（弃）：双轨制，interval 继续 ticker、表达式走 robfig——两套实现两套语义。
- 方案 C（弃）：全量迁移新 API、重写所有调用方——改动面大，用户已否决。

## API 设计

对外 API（3 个方法不变，新增 1 个构造函数）：

```go
func NewCron(d time.Duration) *Cron            // 不变：内部转为 "@every <d>"
func NewCronExpr(expr string) (*Cron, error)   // 新增：解析 Linux 风格表达式
func (c *Cron) Do(f func())                    // 不变：注册任务并启动
func (c *Cron) Stop()                          // 不变：停止
```

## 内部实现

```go
package cron

import robfig "github.com/robfig/cron/v3"

type Cron struct {
    s       *robfig.Cron   // robfig 调度器，每个 Cron 实例一个
    entryID robfig.EntryID
}
```

### 调度器配置

用自定义解析器，同时接受 5 段（Linux 标准）和 6 段（带秒）表达式，并支持描述符：

```go
parser := robfig.NewParser(robfig.SecondOptional | robfig.Minute | robfig.Hour |
    robfig.Dom | robfig.Month | robfig.Dow | robfig.Descriptor)
s := robfig.New(robfig.WithParser(parser))
```

能力清单（均可通过表达式字符串使用）：

- 5 段标准表达式：`0 3 * * *`（每天 03:00）
- 6 段带秒表达式：`0 0 3 * * *`
- 描述符：`@every 5m`、`@daily`、`@hourly`、`@midnight`、`@weekly` 等
- 时区前缀：`CRON_TZ=Asia/Shanghai 0 3 * * *`
- 范围/步长/列表/星期别名：`*/15`、`1-5`、`MON-FRI` 等

### NewCron(d)

```go
func NewCron(d time.Duration) *Cron {
    c, err := NewCronExpr("@every " + d.String())
    if err != nil {
        panic(err) // duration 必合法，实际不可达
    }
    return c
}
```

`d.String()`（如 `2h0m0s`）是合法 Go duration，robfig `@every` 用 `time.ParseDuration` 解析，行为与旧 ticker 一致：从创建（实际是首次 `Do` 启动调度器）起每隔 N 触发，本地时区。

### Do / Stop

```go
func (c *Cron) Do(f func()) {
    id, err := c.s.AddFunc(c.expr, f)
    if err != nil { panic(err) }  // 表达式在 NewCronExpr 已验证，不可达
    c.entryID = id
    c.s.Start()                   // robfig: 已启动时 no-op，可安全重复调用
}

func (c *Cron) Stop() {
    c.s.Remove(c.entryID)
    c.s.Stop()
}
```

语义对比（新旧行为差异必须为 null 或正向改进）：

| 行为 | 旧实现 | 新实现 |
|---|---|---|
| 定时触发 | ticker，创建后每隔 N | `@every`，启动后每隔 N，等价 |
| Do 前调用 Stop | 死锁风险（无接收者时阻塞） | 安全 no-op |
| Stop 重复调用 | 幂等（select 保护） | robfig Stop 幂等 |
| 触发后 panic | 无恢复，goroutine 退出 | 无恢复，调度器记录日志后继续（robfig 默认 Recovery） |

`Stop` 后实例不再复用（与旧实现一致，调用方模式为 Stop 后置 nil 重建）。

## 错误处理

- `NewCron(d)` 签名不变（不返回 error），非法场景不可达。
- `NewCronExpr` 返回 `(*Cron, error)`，非法表达式（`0 3 * *`、`abc`、`60 * * * *` 等）在创建时立即返回错误，不进入 goroutine、不 panic。

## 测试

`pkg/cron/cron_test.go` 重写（现有测试为 3 秒 sleep 弱测试）：

1. **表达式解析**：合法 5 段 / 6 段 / `@every` / `@daily` / `CRON_TZ` 前缀通过；非法表达式（字段不足、越界值、垃圾字符串）返回 error。
2. **语义保持**：`NewCron(100 * time.Millisecond)` + `Do` 后，计数器在 ~300ms 内 ≥ 1。
3. **Stop 生效**：等待 ≥2 次触发后 Stop，再等待 ≥2 个间隔，计数不再增长。
4. **定点触发**：`NewCronExpr("0 3 * * *")` 的调度器 `Next()` 时间落在当地 03:00（不实际等待）。
5. **双调用安全**：`Stop()` 在 `Do` 之前调用不阻塞、不 panic。

## 依赖变更

- 新增 `github.com/robfig/cron/v3 v3.0.1`（go.mod / go.sum）。
- 删除自研 ticker 实现（`pkg/cron/cron.go` 整体重写，`Cron` 结构体私有字段变化，不影响外部调用方）。

## 明确不做（Out of Scope）

- 不改任何 driver / 调用方（17 处零改动）。
- 不加用户可见配置（不暴露给前端/设置页）。
- 不做分布式锁、持久化、多实例协调等超范围能力。
- 不迁移到 gocron 等其他调度库。
