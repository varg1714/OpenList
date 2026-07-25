package pornhub

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/robertkrimen/otto"
)

func TestGetVideoLinkUsesCallerContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := (&Pornhub{Addition: Addition{ServerUrl: "https://example.test"}}).getVideoLink(ctx, "view-key")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("getVideoLink error = %v, want context.Canceled", err)
	}
}

func TestRunPornhubScriptUsesCallerContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	err := runPornhubScript(ctx, otto.New(), `for (;;) {}`)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("runPornhubScript error = %v, want context.DeadlineExceeded", err)
	}
}
