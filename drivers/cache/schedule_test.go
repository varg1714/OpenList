package cache

import "testing"

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
