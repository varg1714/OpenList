package pornhub

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/base"
)

// fanartMediaOps abstracts ffmpeg operations so job tests can run without
// live Pornhub or a real ffmpeg binary.
type fanartMediaOps interface {
	ProbeDuration(ctx context.Context, videoURL string) (float64, error)
	ExtractFrame(ctx context.Context, videoURL string, positionSec float64) ([]byte, error)
}

// fanartFFmpeg is the production adapter backed by ffprobe and ffmpeg.
// It sends Referer as an INPUT option (placed before -i) and uses
// context-aware timeouts for every ffmpeg invocation.
type fanartFFmpeg struct {
	serverURL  string
	runCommand func(context.Context, []string) ([]byte, string, error)
}

const fanartPreciseSeekWindow = 5.0

func (a *fanartFFmpeg) ProbeDuration(ctx context.Context, videoURL string) (float64, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	command := exec.CommandContext(probeCtx, "ffprobe", a.buildProbeArgs(videoURL)...)
	stderr := &bytes.Buffer{}
	command.Stderr = stderr
	output, err := command.Output()
	if err != nil {
		if contextErr := probeCtx.Err(); contextErr != nil {
			return 0, fmt.Errorf("ffprobe %s: %w", videoURL, contextErr)
		}
		return 0, fmt.Errorf("ffprobe %s: %s: %w", videoURL, stderr.String(), err)
	}
	return parseProbeDuration(output)
}

func (a *fanartFFmpeg) buildProbeArgs(videoURL string) []string {
	return []string{
		"-hide_banner",
		"-loglevel", "error",
		"-user_agent", base.UserAgent,
		"-referer", a.serverURL,
		"-show_format",
		"-show_streams",
		"-of", "json",
		videoURL,
	}
}

func parseProbeDuration(output []byte) (float64, error) {
	var probe struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(output, &probe); err != nil {
		return 0, fmt.Errorf("parse ffprobe output: %w", err)
	}
	duration, err := strconv.ParseFloat(probe.Format.Duration, 64)
	if err != nil {
		return 0, fmt.Errorf("parse video duration: %w", err)
	}
	if duration <= 0 || math.IsNaN(duration) || math.IsInf(duration, 0) {
		return 0, fmt.Errorf("invalid video duration: %s", probe.Format.Duration)
	}
	return duration, nil
}

func (a *fanartFFmpeg) buildExtractArgs(videoURL string, positionSec float64) []string {
	return []string{
		"-hide_banner",
		"-loglevel", "error",
		"-user_agent", base.UserAgent,
		"-referer", a.serverURL,
		"-ss", formatFanartSeconds(positionSec),
		"-i", videoURL,
		"-vframes", "1",
		"-f", "image2",
		"-vcodec", "mjpeg",
		"pipe:",
	}
}

func (a *fanartFFmpeg) buildPreciseExtractArgs(videoURL string, positionSec float64) []string {
	coarsePosition := math.Max(positionSec-fanartPreciseSeekWindow, 0)
	preciseOffset := positionSec - coarsePosition
	args := []string{
		"-hide_banner",
		"-loglevel", "error",
		"-user_agent", base.UserAgent,
		"-referer", a.serverURL,
	}
	if coarsePosition > 0 {
		args = append(args, "-ss", formatFanartSeconds(coarsePosition))
	}
	args = append(args, "-i", videoURL)
	if preciseOffset > 0 {
		args = append(args, "-ss", formatFanartSeconds(preciseOffset))
	}
	return append(args,
		"-vframes", "1",
		"-f", "image2",
		"-vcodec", "mjpeg",
		"pipe:",
	)
}

func (a *fanartFFmpeg) ExtractFrame(ctx context.Context, videoURL string, positionSec float64) ([]byte, error) {
	frameCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	output, stderr, err := a.executeExtract(frameCtx, a.buildExtractArgs(videoURL, positionSec))
	if err != nil {
		if contextErr := frameCtx.Err(); contextErr != nil {
			return nil, fmt.Errorf("extract frame at %.1fs: %w", positionSec, contextErr)
		}
		return nil, fmt.Errorf("extract frame at %.1fs: %s: %w", positionSec, stderr, err)
	}
	if len(output) > 0 {
		return output, nil
	}

	output, preciseStderr, err := a.executeExtract(frameCtx, a.buildPreciseExtractArgs(videoURL, positionSec))
	if err != nil {
		if contextErr := frameCtx.Err(); contextErr != nil {
			return nil, fmt.Errorf("extract frame at %.1fs: %w", positionSec, contextErr)
		}
		return nil, fmt.Errorf("extract frame at %.1fs with precise retry: %s: %w", positionSec, preciseStderr, err)
	}
	if len(output) == 0 {
		diagnostics := strings.TrimSpace(preciseStderr)
		if diagnostics == "" {
			diagnostics = strings.TrimSpace(stderr)
		}
		return nil, fmt.Errorf("extract frame at %.1fs: empty output after precise retry: %s", positionSec, diagnostics)
	}
	return output, nil
}

func (a *fanartFFmpeg) executeExtract(ctx context.Context, args []string) ([]byte, string, error) {
	if a.runCommand != nil {
		return a.runCommand(ctx, args)
	}
	output := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	command := exec.CommandContext(ctx, "ffmpeg", args...)
	command.Stdout = output
	command.Stderr = stderr
	err := command.Run()
	return output.Bytes(), stderr.String(), err
}

func formatFanartSeconds(seconds float64) string {
	return fmt.Sprintf("%.3f", seconds)
}
