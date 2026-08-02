package scheduled_sync

import (
	"context"
	"strings"
	"testing"
)

func TestInitRejectsEmptyRemotePath(t *testing.T) {
	d := schedWith(Addition{SyncCronExpr: "0 3 * * *"})
	if err := d.Init(context.Background()); err == nil {
		t.Fatal("expected error for empty remote path")
	}
}

func TestInitRejectsEmptyCronExpr(t *testing.T) {
	d := schedWith(Addition{RemotePath: "/fake"})
	if err := d.Init(context.Background()); err == nil {
		t.Fatal("expected error for empty cron expr")
	}
}

func TestInitRejectsInvalidCronExpr(t *testing.T) {
	d := schedWith(Addition{RemotePath: "/fake", SyncCronExpr: "60 * * * *"})
	err := d.Init(context.Background())
	if err == nil || !strings.Contains(err.Error(), "sync_cron_expr") {
		t.Fatalf("expected error mentioning sync_cron_expr, got %v", err)
	}
}

func TestInitStartsCronDropStops(t *testing.T) {
	d := schedWith(Addition{RemotePath: "/fake", SyncCronExpr: " 0 3 * * * "})
	if err := d.Init(context.Background()); err != nil {
		t.Fatalf("init: %+v", err)
	}
	if d.cron == nil {
		t.Fatal("expected cron started after init")
	}
	if err := d.Drop(context.Background()); err != nil {
		t.Fatalf("drop: %+v", err)
	}
	if d.cron != nil {
		t.Fatal("expected cron stopped after drop")
	}
}

func TestReInitRestartsCron(t *testing.T) {
	d := schedWith(Addition{RemotePath: "/fake", SyncCronExpr: "0 3 * * *"})
	if err := d.Init(context.Background()); err != nil {
		t.Fatalf("first init: %+v", err)
	}
	if err := d.Init(context.Background()); err != nil {
		t.Fatalf("second init: %+v", err)
	}
	if d.cron == nil {
		t.Fatal("expected cron rebuilt after re-init")
	}
}

func TestInitBuildsRateLimiter(t *testing.T) {
	d := schedWith(Addition{RemotePath: "/fake", SyncCronExpr: "0 3 * * *", ListRateLimit: 10})
	if err := d.Init(context.Background()); err != nil {
		t.Fatalf("init: %+v", err)
	}
	if d.limiter == nil {
		t.Fatal("expected limiter when list_rate_limit > 0")
	}
}

func TestInitSkipsRateLimiterWhenZero(t *testing.T) {
	d := schedWith(Addition{RemotePath: "/fake", SyncCronExpr: "0 3 * * *"})
	if err := d.Init(context.Background()); err != nil {
		t.Fatalf("init: %+v", err)
	}
	if d.limiter != nil {
		t.Fatal("expected no limiter when list_rate_limit is 0")
	}
}
