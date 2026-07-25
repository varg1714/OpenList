//go:build e2e

package pornhub

import (
	"bytes"
	"context"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"
)

func TestFanartFFmpegE2ERefererAndExtraction(t *testing.T) {
	const referer = "https://www.pornhub.com/"
	videoURL, acceptedRequests := newRefererVideoServer(t, referer)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	adapter := &fanartFFmpeg{serverURL: referer}

	duration, err := adapter.ProbeDuration(ctx, videoURL)
	if err != nil {
		t.Fatal(err)
	}
	if duration <= 0 {
		t.Fatalf("duration = %f, want positive", duration)
	}

	frame, err := adapter.ExtractFrame(ctx, videoURL, duration/2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jpeg.Decode(bytes.NewReader(frame)); err != nil {
		t.Fatalf("decode extracted JPEG: %v", err)
	}
	if acceptedRequests.Load() < 2 {
		t.Fatalf("accepted requests = %d, want requests from both ffprobe and ffmpeg", acceptedRequests.Load())
	}
}

func TestFanartWorkflowE2ERefererAndThreeFrames(t *testing.T) {
	setupPornhubFanartTest(t)
	const referer = "https://www.pornhub.com/"
	videoURL, acceptedRequests := newRefererVideoServer(t, referer)

	driver := newFanartDriver(&fanartFFmpeg{serverURL: referer}, func(_ context.Context, _ string) (string, error) {
		return videoURL, nil
	})
	film := createFanartWork(t, "workflow-e2e", "view-e2e", 0, time.Time{})
	paths, err := virtual_file.PosterPaths(DriverName, film.PrimaryDir, film.Code)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.Background), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Background, []byte("old background"), 0o644); err != nil {
		t.Fatal(err)
	}

	driver.scanFilmFanart(context.Background(), &film)

	stored := loadFanartWork(t, film.ID)
	if stored.SampleImageCount != 3 || !stored.SampleImageComplete {
		t.Fatalf("progress = (%d, %t), want (3, true)", stored.SampleImageCount, stored.SampleImageComplete)
	}
	if _, err := os.Lstat(paths.Background); !os.IsNotExist(err) {
		t.Fatalf("background still exists: %v", err)
	}
	for index := 1; index <= 3; index++ {
		path, err := virtual_file.FanartPath(DriverName, film.PrimaryDir, film.Code, index)
		if err != nil {
			t.Fatal(err)
		}
		frame, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read fanart%d: %v", index, err)
		}
		if _, err := jpeg.Decode(bytes.NewReader(frame)); err != nil {
			t.Fatalf("decode fanart%d: %v", index, err)
		}
	}
	if acceptedRequests.Load() < 4 {
		t.Fatalf("accepted requests = %d, want ffprobe plus three ffmpeg requests", acceptedRequests.Load())
	}
}

func TestFanartFFmpegE2EPreciseRetryAfterEmptyFastSeek(t *testing.T) {
	const referer = "https://www.pornhub.com/"
	videoURL, acceptedRequests := newRefererVideoServer(t, referer)
	direct := &fanartFFmpeg{serverURL: referer}
	callCount := 0
	adapter := &fanartFFmpeg{
		serverURL: referer,
		runCommand: func(ctx context.Context, args []string) ([]byte, string, error) {
			callCount++
			if callCount == 1 {
				return nil, "", nil
			}
			return direct.executeExtract(ctx, args)
		},
	}

	frame, err := adapter.ExtractFrame(context.Background(), videoURL, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jpeg.Decode(bytes.NewReader(frame)); err != nil {
		t.Fatalf("decode precise-retry JPEG: %v", err)
	}
	if callCount != 2 {
		t.Fatalf("ffmpeg calls = %d, want 2", callCount)
	}
	if acceptedRequests.Load() == 0 {
		t.Fatal("precise retry did not request the video")
	}
}

func newRefererVideoServer(t *testing.T, referer string) (string, *atomic.Int32) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Fatal("ffmpeg is required for the e2e test")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Fatal("ffprobe is required for the e2e test")
	}

	videoPath := filepath.Join(t.TempDir(), "source.mp4")
	generate := exec.Command(
		"ffmpeg",
		"-hide_banner",
		"-loglevel", "error",
		"-f", "lavfi",
		"-i", "testsrc=size=320x180:rate=10",
		"-t", "2",
		"-c:v", "mpeg4",
		"-pix_fmt", "yuv420p",
		"-movflags", "+faststart",
		"-y",
		videoPath,
	)
	if output, err := generate.CombinedOutput(); err != nil {
		t.Fatalf("generate test video: %v: %s", err, output)
	}

	var acceptedRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Referer() != referer || request.UserAgent() != base.UserAgent {
			response.WriteHeader(http.StatusForbidden)
			return
		}
		acceptedRequests.Add(1)

		video, err := os.Open(videoPath)
		if err != nil {
			http.Error(response, err.Error(), http.StatusInternalServerError)
			return
		}
		defer video.Close()
		info, err := video.Stat()
		if err != nil {
			http.Error(response, err.Error(), http.StatusInternalServerError)
			return
		}
		http.ServeContent(response, request, info.Name(), info.ModTime(), video)
	}))
	t.Cleanup(server.Close)
	return server.URL + "/source.mp4", &acceptedRequests
}
