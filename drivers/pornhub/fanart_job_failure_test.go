package pornhub

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
)

func TestScanFilmFanartCleanupFailureDoesNotAdvanceProgress(t *testing.T) {
	setupPornhubFanartTest(t)
	media := &mockFanartMedia{duration: 100.0}
	driver := newFanartDriver(media, func(_ context.Context, key string) (string, error) {
		return "https://example.test/video/" + key, nil
	})
	driver.removeBackgroundCb = func(source, dir, name string) error {
		return fmt.Errorf("cleanup denied")
	}

	film := createFanartFilm(t, "cleanup-fail", "vkclean", 0, time.Time{})
	started := time.Now()
	driver.scanFilmFanart(context.Background(), &film)

	stored := loadFanartFilm(t, film.ID)
	if stored.SampleImageCount != 0 || stored.SampleImageComplete {
		t.Fatalf("progress = (%d, %t), want (0, false) - cleanup failure must prevent progress", stored.SampleImageCount, stored.SampleImageComplete)
	}
	if stored.SampleImageScanAt.Before(started) {
		t.Errorf("scan time = %s, want at or after %s", stored.SampleImageScanAt, started)
	}

	// Fanart should still be published (the extract succeeded).
	path1, _ := virtual_file.FanartPath(DriverName, film.Actor, film.Name, 1)
	if _, err := os.Lstat(path1); os.IsNotExist(err) {
		t.Error("fanart1 should exist even when cleanup fails")
	}
}

func TestScanFilmFanartTransientProbeFailureUpdatesScanTime(t *testing.T) {
	setupPornhubFanartTest(t)
	media := &mockFanartMedia{probeErr: fmt.Errorf("ffprobe timeout")}
	driver := newFanartDriver(media, func(_ context.Context, key string) (string, error) {
		return "https://example.test/video/" + key, nil
	})

	film := createFanartFilm(t, "probe-fail", "vk3", 0, time.Time{})
	started := time.Now()
	driver.scanFilmFanart(context.Background(), &film)

	stored := loadFanartFilm(t, film.ID)
	if stored.SampleImageCount != 0 || stored.SampleImageComplete {
		t.Fatalf("progress = (%d, %t), want (0, false)", stored.SampleImageCount, stored.SampleImageComplete)
	}
	if stored.SampleImageScanAt.Before(started) {
		t.Errorf("scan time = %s, want at or after %s", stored.SampleImageScanAt, started)
	}
}

func TestScanFilmFanartTransientExtractFailureUpdatesScanTime(t *testing.T) {
	setupPornhubFanartTest(t)
	media := &mockFanartMedia{
		duration: 100.0,
		extractErrs: map[float64]error{
			25.0: fmt.Errorf("ffmpeg crash"),
		},
	}
	driver := newFanartDriver(media, func(_ context.Context, key string) (string, error) {
		return "https://example.test/video/" + key, nil
	})

	film := createFanartFilm(t, "extract-fail", "vk4", 0, time.Time{})
	started := time.Now()
	driver.scanFilmFanart(context.Background(), &film)

	stored := loadFanartFilm(t, film.ID)
	if stored.SampleImageCount != 0 || stored.SampleImageComplete {
		t.Fatalf("progress = (%d, %t), want (0, false)", stored.SampleImageCount, stored.SampleImageComplete)
	}
	if stored.SampleImageScanAt.Before(started) {
		t.Errorf("scan time = %s, want at or after %s", stored.SampleImageScanAt, started)
	}
}

func TestScanFilmFanartAuditsCompletedFilmWithoutResolvingVideo(t *testing.T) {
	setupPornhubFanartTest(t)
	media := &mockFanartMedia{duration: 100.0}
	driver := newFanartDriver(media, func(_ context.Context, _ string) (string, error) {
		return "", errors.New("completed audit should not resolve video")
	})

	film := createFanartFilm(t, "completed", "vk5", 3, time.Time{})
	film.SampleImageComplete = true
	if err := db.GetDb().Model(&film).Update("sample_image_complete", true).Error; err != nil {
		t.Fatal(err)
	}
	for index := 1; index <= 3; index++ {
		path, err := virtual_file.FanartPath(DriverName, film.Actor, film.Name, index)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("existing"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	driver.scanFilmFanart(context.Background(), &film)

	stored := loadFanartFilm(t, film.ID)
	if !stored.SampleImageComplete {
		t.Error("completed film should remain complete")
	}
	if stored.SampleImageScanAt.IsZero() {
		t.Error("completed disk audit did not update scan time")
	}
}

func TestScanFilmFanartCancellationDoesNotDelayRetry(t *testing.T) {
	setupPornhubFanartTest(t)
	driver := newFanartDriver(&mockFanartMedia{}, func(_ context.Context, _ string) (string, error) {
		return "", context.Canceled
	})
	film := createFanartFilm(t, "cancel-retry", "vk-cancel", 0, time.Time{})

	driver.scanFilmFanart(context.Background(), &film)

	stored := loadFanartFilm(t, film.ID)
	if !stored.SampleImageScanAt.IsZero() {
		t.Fatalf("cancelled scan time = %s, want zero", stored.SampleImageScanAt)
	}
}

func TestScanFilmFanartRemovesBackgroundThenPromotesLegacyPoster(t *testing.T) {
	dataDir := setupPornhubFanartTest(t)
	media := &mockFanartMedia{duration: 100.0}
	driver := newFanartDriver(media, func(_ context.Context, key string) (string, error) {
		return "https://example.test/video/" + key, nil
	})

	film := createFanartFilm(t, "bg-test", "vk6", 0, time.Time{})
	paths, err := virtual_file.PosterPaths(DriverName, film.Actor, film.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.Poster), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.LegacyPoster, []byte("old poster"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Background, []byte("regular background"), 0o644); err != nil {
		t.Fatal(err)
	}
	driver.removeBackgroundCb = func(_, _, _ string) error {
		if _, err := os.Lstat(paths.LegacyPoster); err != nil {
			return fmt.Errorf("legacy poster unavailable before background cleanup: %w", err)
		}
		if _, err := os.Lstat(paths.Poster); !os.IsNotExist(err) {
			return fmt.Errorf("poster promoted before background cleanup: %v", err)
		}
		return os.Remove(paths.Background)
	}

	driver.scanFilmFanart(context.Background(), &film)

	if _, err := os.Lstat(paths.Background); !os.IsNotExist(err) {
		t.Fatalf("background not removed: %v", err)
	}
	content, err := os.ReadFile(paths.Poster)
	if err != nil {
		t.Fatalf("promoted poster not readable: %v", err)
	}
	if string(content) != "old poster" {
		t.Fatalf("promoted poster content = %q, want %q", content, "old poster")
	}
	if _, err := os.Lstat(paths.LegacyPoster); !os.IsNotExist(err) {
		t.Fatalf("legacy poster not renamed: %v", err)
	}

	fanartPath := filepath.Join(dataDir, "emby", DriverName, film.Actor, virtual_file.GetRealName(virtual_file.AppendImageName(film.Name)), "fanart1.jpg")
	if _, err := os.Lstat(fanartPath); err != nil {
		t.Fatalf("fanart1 not published: %v", err)
	}
}
func TestScanFilmFanartDisabledWhenCountZero(t *testing.T) {
	setupPornhubFanartTest(t)
	d := &Pornhub{Addition: Addition{FanartCount: 0, FanartScanLimit: 10}}
	d.scanFanart(context.Background())
}

func TestScanFilmFanartDisabledWhenLimitZero(t *testing.T) {
	setupPornhubFanartTest(t)
	d := &Pornhub{Addition: Addition{FanartCount: 3, FanartScanLimit: 0}}
	d.scanFanart(context.Background())
}

func TestPornhubDropCancelsFanartContext(t *testing.T) {
	driver := &Pornhub{Addition: Addition{
		FanartCount:          3,
		FanartScanLimit:      10,
		FanartScanTime:       360,
		MatchFilmTagScanTime: 60,
	}}
	if err := driver.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	if driver.fanartCtx == nil {
		t.Fatal("fanart context was not initialized")
	}
	if err := driver.Drop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(driver.fanartCtx.Err(), context.Canceled) {
		t.Fatalf("fanart context error = %v, want context.Canceled", driver.fanartCtx.Err())
	}
}

func TestScanFilmFanartQueryUses72HourRetryWindow(t *testing.T) {
	// The job must scan with a fixed 72h window regardless of FanartScanTime.
	d := &Pornhub{Addition: Addition{FanartCount: 3, FanartScanLimit: 10, FanartScanTime: 5}}
	scanInterval := d.fanartRetryInterval()
	want := 72 * time.Hour
	if scanInterval != want {
		t.Errorf("fanart retry interval = %s, want %s", scanInterval, want)
	}
}
