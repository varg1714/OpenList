package pornhub

import (
	"context"
	"testing"
	"time"
)

func TestPornhubDropWaitsForActiveFanartWork(t *testing.T) {
	setupPornhubFanartTest(t)
	started := make(chan struct{})
	released := make(chan struct{})
	driver := newFanartDriver(&mockFanartMedia{}, func(ctx context.Context, _ string) (string, error) {
		close(started)
		<-ctx.Done()
		close(released)
		return "", ctx.Err()
	})
	createFanartWork(t, "drop-active", "view-active", 0, time.Time{})
	driver.fanartCtx, driver.fanartCancel = context.WithCancel(context.Background())

	go driver.runFanart()
	<-started
	if err := driver.Drop(context.Background()); err != nil {
		t.Fatal(err)
	}

	select {
	case <-released:
	default:
		t.Fatal("Drop returned before active fanart work stopped")
	}
}
