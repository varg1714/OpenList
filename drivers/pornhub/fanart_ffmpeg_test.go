package pornhub

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/drivers/base"
)

func TestBuildExtractStreamRefererAndSeekAreInputOpts(t *testing.T) {
	adapter := &fanartFFmpeg{serverURL: "https://www.pornhub.com"}
	args := adapter.buildExtractArgs("https://video.example/film.mp4", 25.0)

	// Referer must be an INPUT option before -i.
	refererIdx := indexOfArg(args, "-referer")
	inputIdx := indexOfArg(args, "-i")
	videoIdx := indexOfArg(args, "https://video.example/film.mp4")
	if refererIdx < 0 {
		t.Fatal("-referer not found in args")
	}
	if inputIdx < 0 {
		t.Fatal("-i not found in args")
	}
	if videoIdx < 0 {
		t.Fatal("video URL not found in args")
	}
	if refererIdx+1 >= len(args) || args[refererIdx+1] != "https://www.pornhub.com" {
		t.Fatalf("-referer value = %v, want https://www.pornhub.com", args[refererIdx+1])
	}
	if refererIdx >= inputIdx || refererIdx >= videoIdx {
		t.Fatalf("referer at %d must be before -i at %d and URL at %d: args=%v", refererIdx, inputIdx, videoIdx, args)
	}
	if inputIdx >= videoIdx {
		t.Fatalf("-i at %d must be before video URL at %d: args=%v", inputIdx, videoIdx, args)
	}
	userAgentIdx := indexOfArg(args, "-user_agent")
	if userAgentIdx < 0 || userAgentIdx >= inputIdx {
		t.Fatalf("-user_agent at %d must be before -i at %d: args=%v", userAgentIdx, inputIdx, args)
	}
	if userAgentIdx+1 >= len(args) || args[userAgentIdx+1] != base.UserAgent {
		t.Fatalf("-user_agent value = %v, want %q", args[userAgentIdx+1], base.UserAgent)
	}

	// Seek (-ss) must be an INPUT option before -i.
	ssIdx := indexOfArg(args, "-ss")
	if ssIdx < 0 {
		t.Fatal("-ss not found in args")
	}
	if ssIdx >= inputIdx {
		t.Fatalf("-ss at %d must be before -i at %d: args=%v", ssIdx, inputIdx, args)
	}

	// Seek position value must appear immediately after -ss.
	if ssIdx+1 >= len(args) || args[ssIdx+1] != "25.000" {
		t.Fatalf("-ss value: %v, want 25.000 immediately after position %d", args[ssIdx+1], ssIdx)
	}

	if t.Failed() {
		t.Logf("full args: %v", args)
	}
}

func TestBuildProbeArgsUsesBrowserUserAgent(t *testing.T) {
	adapter := &fanartFFmpeg{serverURL: "https://www.pornhub.com"}
	args := adapter.buildProbeArgs("https://video.example/film.mp4")

	userAgentIdx := indexOfArg(args, "-user_agent")
	videoIdx := indexOfArg(args, "https://video.example/film.mp4")
	if userAgentIdx < 0 || userAgentIdx >= videoIdx {
		t.Fatalf("-user_agent at %d must be before video URL at %d: args=%v", userAgentIdx, videoIdx, args)
	}
	if userAgentIdx+1 >= len(args) || args[userAgentIdx+1] != base.UserAgent {
		t.Fatalf("-user_agent value = %v, want %q", args[userAgentIdx+1], base.UserAgent)
	}
}

func TestExtractFrameUsesCallerContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := (&fanartFFmpeg{serverURL: "https://www.pornhub.com"}).ExtractFrame(
		ctx,
		"https://video.example/film.mp4",
		25,
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ExtractFrame error = %v, want context.Canceled", err)
	}
}

func TestExtractFrameRetriesEmptyFastSeekWithBoundedPreciseSeek(t *testing.T) {
	var calls [][]string
	adapter := &fanartFFmpeg{
		serverURL: "https://www.pornhub.com",
		runCommand: func(_ context.Context, args []string) ([]byte, string, error) {
			calls = append(calls, args)
			if len(calls) == 1 {
				return nil, "", nil
			}
			return []byte("frame"), "", nil
		},
	}

	frame, err := adapter.ExtractFrame(context.Background(), "https://video.example/film.mp4", 189.42483333333334)
	if err != nil {
		t.Fatal(err)
	}
	if string(frame) != "frame" {
		t.Fatalf("frame = %q, want frame", frame)
	}
	if len(calls) != 2 {
		t.Fatalf("ffmpeg calls = %d, want 2", len(calls))
	}

	args := calls[1]
	inputIdx := indexOfArg(args, "-i")
	firstSeekIdx := indexOfArg(args, "-ss")
	secondSeekIdx := -1
	for index := firstSeekIdx + 1; index < len(args); index++ {
		if args[index] == "-ss" {
			secondSeekIdx = index
			break
		}
	}
	if firstSeekIdx < 0 || firstSeekIdx >= inputIdx || args[firstSeekIdx+1] != "184.425" {
		t.Fatalf("coarse seek args = %v, want -ss 184.425 before -i", args)
	}
	if secondSeekIdx <= inputIdx || args[secondSeekIdx+1] != "5.000" {
		t.Fatalf("precise seek args = %v, want -ss 5.000 after -i", args)
	}
}

func TestFanartFFmpegFormatSeconds(t *testing.T) {
	tests := []struct {
		input    float64
		expected string
	}{
		{0, "0.000"},
		{1.5, "1.500"},
		{99.123, "99.123"},
		{25.0, "25.000"},
	}
	for _, tc := range tests {
		if got := formatFanartSeconds(tc.input); got != tc.expected {
			t.Errorf("formatFanartSeconds(%v) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestParseProbeDurationRejectsNonPositiveAndNonFiniteValues(t *testing.T) {
	for _, duration := range []string{"0", "-1", "NaN", "+Inf"} {
		output := []byte(fmt.Sprintf(`{"format":{"duration":%q}}`, duration))
		if _, err := parseProbeDuration(output); err == nil {
			t.Errorf("parseProbeDuration accepted %s", duration)
		}
	}
}

func indexOfArg(args []string, target string) int {
	for i, a := range args {
		if a == target {
			return i
		}
	}
	return -1
}
