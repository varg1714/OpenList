package cache

import (
	"strings"
	"testing"
)

func TestBuildSyncCronPrecedence(t *testing.T) {
	c, err := buildSyncCron("0 3 * * *", 5)
	if err != nil || c == nil {
		t.Fatalf("expr should win over interval: cron=%v err=%v", c, err)
	}
	c, err = buildSyncCron("", 3)
	if err != nil || c == nil {
		t.Fatalf("interval fallback: cron=%v err=%v", c, err)
	}
	c, err = buildSyncCron("", 0)
	if err != nil || c != nil {
		t.Fatalf("disabled when both empty: cron=%v err=%v", c, err)
	}
}

func TestBuildSyncCronRejectsInvalidExprEvenWithInterval(t *testing.T) {
	if c, err := buildSyncCron("60 * * * *", 5); err == nil {
		t.Fatalf("invalid expr must error even when interval is set (no silent fallback), got cron=%v", c)
	}
}

// buildSyncCron assumes trimmed input: a whitespace-only expr must be
// treated as empty, falling back to the interval — mirrors Init's
// strings.TrimSpace before the call.
func TestBuildSyncCronEmptyAfterTrim(t *testing.T) {
	c, err := buildSyncCron(strings.TrimSpace("   "), 3)
	if err != nil || c == nil {
		t.Fatalf("whitespace-only expr should fall back to interval: cron=%v err=%v", c, err)
	}
	c, err = buildSyncCron(strings.TrimSpace("   "), 0)
	if err != nil || c != nil {
		t.Fatalf("whitespace-only expr with no interval should disable sync: cron=%v err=%v", c, err)
	}
}
