# pkg/cron 基于 robfig/cron 重写（新增表达式调度）实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 用 `github.com/robfig/cron/v3` 重写 `pkg/cron`，保持现有 `NewCron(duration)`/`Do(f)`/`Stop()` 语义不变（17 处调用方零改动），新增 `NewCronExpr(expr string) (*Cron, error)` 支持 Linux 风格 5/6 段 cron 表达式。

**Architecture:** `pkg/cron` 从自研 ticker 改为 robfig 调度器的薄封装。每个 `Cron` 实例持有一个 robfig 调度器和一个 entryID。`NewCron(d)` 内部使用自定义 `everySchedule`（`Next = t + d`）固定间隔调度，绕开 `@every` 描述符的 1 秒粒度舍入；解析器配置 `SecondOptional` 使 5 段和 6 段表达式均可解析。调度器统一配置 `WithChain(SkipIfStillRunning, Recover)`：job 串行执行不重叠（保持旧 ticker 语义），panic 记录日志后继续。旧 API 签名与行为语义保持，新增构造函数返回 `(*Cron, error)`。

**Tech Stack:** Go 1.23.4（go.mod）、`github.com/robfig/cron/v3 v3.0.1`、标准库 `testing`/`time`。

## Global Constraints

- Go 工具链：`/Library/Go/sdk/go1.25.4/bin/go`（PATH 上没有时用全路径），gofmt 同理
- 依赖：仅新增 `github.com/robfig/cron/v3 v3.0.1`，不得引入其他新依赖
- 现有 API 签名 `NewCron(d time.Duration) *Cron`、`Do(f func())`、`Stop()` 必须保持不变
- 17 处调用方（drivers/*、internal/cache/utils.go）不允许改动
- 不暴露用户可见配置、不做分布式锁/持久化
- 测试用短间隔（100ms 级）验证行为，不写 sleep 3 秒的弱测试
- 提交信息遵循仓库现有风格（`feat:`/`deps:`/`docs:` 前缀）
- 包名冲突：pkg/cron 包内 import robfig 时必须别名 `robfig "github.com/robfig/cron/v3"`

---

### Task 1: 添加 robfig/cron 依赖

**Files:**
- Modify: `go.mod` / `go.sum`

**Interfaces:**
- Produces: 仓库可 `import "github.com/robfig/cron/v3"`，依赖锁入 go.sum

- [ ] **Step 1: 添加依赖**

Run:
```bash
/Library/Go/sdk/go1.25.4/bin/go get github.com/robfig/cron/v3@v3.0.1
```
Expected: `go.mod` require 增加 `github.com/robfig/cron/v3 v3.0.1`，`go.sum` 出现对应 hash，退出码 0。

- [ ] **Step 2: 提交**

```bash
git add go.mod go.sum
git commit -m "deps(cron): add robfig/cron v3.0.1"
```

---

### Task 2: 重写 pkg/cron 实现（TDD：先测试后实现）

**Files:**
- Rewrite: `pkg/cron/cron_test.go`（Step 1 先写）
- Rewrite: `pkg/cron/cron.go`（Step 3 再写）

**Interfaces:**
- Produces（本计划内后续任务/审查依赖的精确签名）:
  ```go
  func NewCron(d time.Duration) *Cron
  func NewCronExpr(expr string) (*Cron, error)
  func (c *Cron) Do(f func())
  func (c *Cron) Stop()
  ```
  `Cron` 结构体私有字段：`expr string`、`s *cron.Cron`（robfig）、`entryID cron.EntryID`、`mu sync.Mutex`、`sched cron.Schedule`（`NewCron` 路径的 `everySchedule`；`NewCronExpr` 路径为 nil）。
  语义约定：`Do` 每实例最多生效一次（entryID 非 0 后再次调用为 no-op）；`Stop` 幂等，Do 之前调用不阻塞；Stop 后实例不复用（与旧实现一致，调用方模式为 Stop 后置 nil 重建）。

- [ ] **Step 1: 先写测试（替换整个 cron_test.go）**

```go
package cron

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestNewCronExprRejectsInvalid(t *testing.T) {
	for _, expr := range []string{"", "abc", "0 3 * *", "60 * * * *", "0 3 * * * * *"} {
		if c, err := NewCronExpr(expr); err == nil {
			t.Fatalf("NewCronExpr(%q) = %+v, want error", expr, c)
		}
	}
}

func TestNewCronExprAcceptsValid(t *testing.T) {
	for _, expr := range []string{
		"0 3 * * *",
		"*/15 * * * *",
		"0 0 3 * * *",
		"@every 5m",
		"@daily",
		"CRON_TZ=Asia/Shanghai 0 3 * * *",
	} {
		if _, err := NewCronExpr(expr); err != nil {
			t.Fatalf("NewCronExpr(%q) unexpected error: %v", expr, err)
		}
	}
}

func TestNewCronFiresRepeatedly(t *testing.T) {
	var n int64
	c := NewCron(100 * time.Millisecond)
	c.Do(func() { atomic.AddInt64(&n, 1) })
	deadline := time.Now().Add(350 * time.Millisecond)
	for time.Now().Before(deadline) && atomic.LoadInt64(&n) < 2 {
		time.Sleep(10 * time.Millisecond)
	}
	if atomic.LoadInt64(&n) < 2 {
		t.Fatalf("expected >= 2 fires in 350ms, got %d", n)
	}
	c.Stop()
}

func TestStopHaltsFiring(t *testing.T) {
	var n int64
	c := NewCron(50 * time.Millisecond)
	c.Do(func() { atomic.AddInt64(&n, 1) })
	for atomic.LoadInt64(&n) < 2 {
		time.Sleep(10 * time.Millisecond)
	}
	c.Stop()
	before := atomic.LoadInt64(&n)
	time.Sleep(150 * time.Millisecond)
	if after := atomic.LoadInt64(&n); after != before {
		t.Fatalf("counter grew after Stop: before=%d after=%d", before, after)
	}
}

func TestStopBeforeDoDoesNotBlock(t *testing.T) {
	c := NewCron(time.Second)
	done := make(chan struct{})
	go func() {
		c.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop before Do blocked")
	}
}

func TestCronExprNextFiresAtFixedTime(t *testing.T) {
	c, err := NewCronExpr("0 3 * * *")
	if err != nil {
		t.Fatal(err)
	}
	if c.entryID != 0 {
		t.Fatalf("expected zero entryID before Do, got %d", c.entryID)
	}
	c.Do(func() {})
	defer c.Stop()
	var next time.Time
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		entries := c.s.Entries()
		if len(entries) == 1 && !entries[0].Next.IsZero() {
			next = entries[0].Next
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if next.IsZero() {
		t.Fatal("entry Next never computed")
	}
	now := time.Now()
	expect := time.Date(now.Year(), now.Month(), now.Day(), 3, 0, 0, 0, time.Local)
	if now.After(expect) {
		expect = expect.AddDate(0, 0, 1)
	}
	if !next.Equal(expect) {
		t.Fatalf("next = %v, want %v", next, expect)
	}
}
```

- [ ] **Step 2: 运行测试确认失败（TDD 红）**

Run:
```bash
/Library/Go/sdk/go1.25.4/bin/go test ./pkg/cron/ -count=1
```
Expected: FAIL——旧实现没有 `NewCronExpr`，且 `Cron` 结构体缺 `s`/`entryID` 字段，编译报 `undefined: NewCronExpr` 等错误。这正是红阶段。

- [ ] **Step 3: 重写实现（替换整个 cron.go）**

```go
package cron

import (
	"sync"
	"time"

	robfig "github.com/robfig/cron/v3"
)

type Cron struct {
	expr    string
	s       *robfig.Cron
	entryID robfig.EntryID
	mu      sync.Mutex
	sched   robfig.Schedule
}

// everySchedule fires at fixed intervals without robfig's 1-second granularity.
type everySchedule struct{ d time.Duration }

func (e everySchedule) Next(t time.Time) time.Time { return t.Add(e.d) }

var parser = robfig.NewParser(robfig.SecondOptional | robfig.Minute | robfig.Hour |
	robfig.Dom | robfig.Month | robfig.Dow | robfig.Descriptor)

// chain serializes job runs and recovers panics, restoring the old ticker's
// non-overlapping execution and adding panic safety.
var chain = robfig.WithChain(
	robfig.SkipIfStillRunning(robfig.DefaultLogger),
	robfig.Recover(robfig.DefaultLogger),
)

// NewCron returns a Cron that invokes f at fixed intervals of d, starting when
// Do is called.
func NewCron(d time.Duration) *Cron {
	if d <= 0 {
		// Prevent a zero-interval busy loop: with d = 0, robfig would
		// compute Next(t) = t and spin forever.
		d = time.Second
	}
	return &Cron{
		expr:  "", // schedule path routes via c.sched, never AddFunc
		s:     robfig.New(robfig.WithParser(parser), chain),
		sched: everySchedule{d},
	}
}

// NewCronExpr returns a Cron that invokes f per the cron expression expr. Note
// that robfig's @every descriptor truncates sub-second durations to 1 second;
// use NewCron for sub-second intervals.
func NewCronExpr(expr string) (*Cron, error) {
	if _, err := parser.Parse(expr); err != nil {
		return nil, err
	}
	return &Cron{
		expr: expr,
		s:    robfig.New(robfig.WithParser(parser), chain),
	}, nil
}

func (c *Cron) Do(f func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entryID != 0 {
		return
	}
	if c.sched != nil {
		c.entryID = c.s.Schedule(c.sched, robfig.FuncJob(f))
	} else {
		id, err := c.s.AddFunc(c.expr, f)
		if err != nil {
			panic(err)
		}
		c.entryID = id
	}
	c.s.Start()
}

func (c *Cron) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.s.Remove(c.entryID)
	c.s.Stop()
}
```

- [ ] **Step 4: 运行测试确认通过（TDD 绿）**

Run:
```bash
/Library/Go/sdk/go1.25.4/bin/go test ./pkg/cron/ -count=1 -v
```
Expected: 全部 PASS。`TestCronExprNextFiresAtFixedTime` 中 `entries[0].Next` 应为当地次日 03:00（若当前时间早于 03:00 则为当日）；robfig 默认本地时区，与 `time.Local` 一致。

- [ ] **Step 5: go vet 检查**

Run:
```bash
/Library/Go/sdk/go1.25.4/bin/go vet ./pkg/cron/
```
Expected: 无输出，退出码 0。

- [ ] **Step 6: 全仓编译验证（17 处调用方零改动编译通过）**

Run:
```bash
/Library/Go/sdk/go1.25.4/bin/go build ./...
```
Expected: 编译成功，无错误（调用方均未改动，仍走旧 API 签名）。

- [ ] **Step 7: gofmt 格式化检查**

Run:
```bash
/Library/Go/sdk/go1.25.4/bin/gofmt -l pkg/cron/
```
Expected: 无输出（文件已格式化）。

- [ ] **Step 8: 提交**

```bash
git add pkg/cron/cron.go pkg/cron/cron_test.go
git commit -m "feat(cron): rewrite pkg/cron on robfig/cron, add expression scheduling"
```

---

### Task 3: 回归验证与计划文档提交

**Files:**
- 无源码改动（只跑测试）

**Interfaces:**
- Consumes: Task 2 的 `pkg/cron` 新实现
- Produces: 回归通过结论

- [ ] **Step 1: 运行依赖 pkg/cron 的包测试**

Run:
```bash
/Library/Go/sdk/go1.25.4/bin/go test ./internal/cache/ ./drivers/cache/ -count=1
```
Expected: PASS（或与改动前一致的既有失败，不得出现新增失败）。

- [ ] **Step 2: 提交计划文档**

```bash
git add docs/superpowers/plans/2026-08-02-cron-robfig.md
git commit -m "docs(cron): add robfig rewrite implementation plan"
```

- [ ] **Step 3: 确认工作区干净**

Run:
```bash
git status --short
```
Expected: 无未提交改动。
