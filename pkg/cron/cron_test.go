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

func TestJobFiresDoNotOverlap(t *testing.T) {
	var concurrent, maxConcurrent int64
	c := NewCron(10 * time.Millisecond)
	c.Do(func() {
		cur := atomic.AddInt64(&concurrent, 1)
		defer atomic.AddInt64(&concurrent, -1)
		for {
			prev := atomic.LoadInt64(&maxConcurrent)
			if prev >= cur || atomic.CompareAndSwapInt64(&maxConcurrent, prev, cur) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	})
	time.Sleep(150 * time.Millisecond)
	c.Stop()
	if m := atomic.LoadInt64(&maxConcurrent); m != 1 {
		t.Fatalf("jobs overlapped: max concurrent = %d, want 1", m)
	}
}

func TestStopHaltsFiring(t *testing.T) {
	var n int64
	c := NewCron(50 * time.Millisecond)
	c.Do(func() { atomic.AddInt64(&n, 1) })
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && atomic.LoadInt64(&n) < 2 {
		time.Sleep(10 * time.Millisecond)
	}
	if atomic.LoadInt64(&n) < 2 {
		t.Fatalf("expected >= 2 fires, got %d", n)
	}
	c.Stop()
	time.Sleep(20 * time.Millisecond)
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
	if next.Hour() != 3 || next.Minute() != 0 || next.Second() != 0 {
		t.Fatalf("next = %v, want hour=3 minute=0 second=0", next)
	}
	if !next.After(now) || next.Sub(now) >= 26*time.Hour {
		t.Fatalf("next = %v, want within 26h after now %v", next, now)
	}
}
