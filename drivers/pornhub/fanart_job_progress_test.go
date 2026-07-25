package pornhub

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
)

func TestScanFilmFanartExtractsEvenlySpacedFrames(t *testing.T) {
	setupPornhubFanartTest(t)
	media := &mockFanartMedia{duration: 100.0}
	driver := newFanartDriver(media, func(_ context.Context, key string) (string, error) {
		return "https://example.test/video/" + key, nil
	})

	film := createFanartFilm(t, "extract-test", "viewkey123", 0, time.Time{})
	driver.scanFilmFanart(context.Background(), &film)

	stored := loadFanartFilm(t, film.ID)
	if stored.SampleImageCount != 3 || !stored.SampleImageComplete {
		t.Fatalf("progress = (%d, %t), want (3, true)", stored.SampleImageCount, stored.SampleImageComplete)
	}

	for index := 1; index <= 3; index++ {
		path, err := virtual_file.FanartPath(DriverName, film.Actor, film.Name, index)
		if err != nil {
			t.Fatal(err)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read fanart%d: %v", index, err)
		}
		wantPosition := 100.0 * float64(index) / 4.0
		want := fmt.Sprintf("frame at %.1f", wantPosition)
		if string(content) != want {
			t.Errorf("fanart%d = %q, want %q", index, content, want)
		}
	}
}

func TestScanFilmFanartRecreatesMissingPersistedFrame(t *testing.T) {
	setupPornhubFanartTest(t)
	media := &mockFanartMedia{duration: 60.0}
	driver := newFanartDriver(media, func(_ context.Context, key string) (string, error) {
		return "https://example.test/video/" + key, nil
	})

	film := createFanartFilm(t, "resume-test", "vk1", 1, time.Time{})
	driver.scanFilmFanart(context.Background(), &film)

	stored := loadFanartFilm(t, film.ID)
	if stored.SampleImageCount != 3 || !stored.SampleImageComplete {
		t.Fatalf("progress = (%d, %t), want (3, true)", stored.SampleImageCount, stored.SampleImageComplete)
	}

	path1, _ := virtual_file.FanartPath(DriverName, film.Actor, film.Name, 1)
	if _, err := os.Lstat(path1); err != nil {
		t.Errorf("missing persisted fanart1 should be recreated: %v", err)
	}
	path2, _ := virtual_file.FanartPath(DriverName, film.Actor, film.Name, 2)
	if _, err := os.Lstat(path2); err != nil {
		t.Errorf("fanart2 should exist: %v", err)
	}
}

func TestScanFilmFanartContinuesWhenConfiguredCountIncreases(t *testing.T) {
	setupPornhubFanartTest(t)
	driver := newFanartDriver(&mockFanartMedia{duration: 120}, func(_ context.Context, key string) (string, error) {
		return "https://example.test/video/" + key, nil
	})
	driver.FanartCount = 5

	film := createFanartFilm(t, "count-increase", "view-increase", 3, time.Time{})
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
	if stored.SampleImageCount != 5 || !stored.SampleImageComplete {
		t.Fatalf("progress = (%d, %t), want (5, true)", stored.SampleImageCount, stored.SampleImageComplete)
	}
	for index := 4; index <= 5; index++ {
		path, _ := virtual_file.FanartPath(DriverName, film.Actor, film.Name, index)
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("fanart%d not created after count increase: %v", index, err)
		}
	}
}

func TestScanFilmFanartRepairsCompletedFilmMissingFrame(t *testing.T) {
	setupPornhubFanartTest(t)
	driver := newFanartDriver(&mockFanartMedia{duration: 120}, func(_ context.Context, key string) (string, error) {
		return "https://example.test/video/" + key, nil
	})
	film := createFanartFilm(t, "completed-missing", "view-missing", 3, time.Time{})
	film.SampleImageComplete = true
	if err := db.GetDb().Model(&film).Update("sample_image_complete", true).Error; err != nil {
		t.Fatal(err)
	}
	for _, index := range []int{1, 3} {
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

	path, _ := virtual_file.FanartPath(DriverName, film.Actor, film.Name, 2)
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("missing completed fanart was not repaired: %v", err)
	}
	stored := loadFanartFilm(t, film.ID)
	if stored.SampleImageCount != 3 || !stored.SampleImageComplete {
		t.Fatalf("progress = (%d, %t), want (3, true)", stored.SampleImageCount, stored.SampleImageComplete)
	}
}

func TestScanFilmFanartAdvancesExistingFanartFiles(t *testing.T) {
	setupPornhubFanartTest(t)
	media := &mockFanartMedia{duration: 80.0}
	driver := newFanartDriver(media, func(_ context.Context, key string) (string, error) {
		return "https://example.test/video/" + key, nil
	})

	film := createFanartFilm(t, "existing-test", "vk2", 0, time.Time{})
	path1, err := virtual_file.FanartPath(DriverName, film.Actor, film.Name, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path1), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path1, []byte("existing fanart1"), 0o644); err != nil {
		t.Fatal(err)
	}

	driver.scanFilmFanart(context.Background(), &film)

	stored := loadFanartFilm(t, film.ID)
	if stored.SampleImageCount != 3 || !stored.SampleImageComplete {
		t.Fatalf("progress = (%d, %t), want (3, true)", stored.SampleImageCount, stored.SampleImageComplete)
	}

	content, _ := os.ReadFile(path1)
	if string(content) != "existing fanart1" {
		t.Error("fanart1 was overwritten")
	}
}

func TestScanFilmFanartExistingFanartTriggersBackgroundCleanup(t *testing.T) {
	setupPornhubFanartTest(t)
	var cleanupCalls int
	driver := newFanartDriver(&mockFanartMedia{duration: 80.0}, func(_ context.Context, key string) (string, error) {
		return "https://example.test/video/" + key, nil
	})
	driver.removeBackgroundCb = func(source, dir, name string) error {
		cleanupCalls++
		return nil
	}

	film := createFanartFilm(t, "existing-cleanup", "vkc", 0, time.Time{})
	path1, err := virtual_file.FanartPath(DriverName, film.Actor, film.Name, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path1), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path1, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}

	driver.scanFilmFanart(context.Background(), &film)

	if cleanupCalls < 1 {
		t.Errorf("background cleanup calls = %d, want at least 1 for recovered existing fanart", cleanupCalls)
	}
}

func TestScanFilmFanartPersistedProgressTriggersBackgroundCleanup(t *testing.T) {
	setupPornhubFanartTest(t)
	var cleanupCalls int
	driver := newFanartDriver(&mockFanartMedia{duration: 80}, func(_ context.Context, key string) (string, error) {
		return "https://example.test/video/" + key, nil
	})
	driver.removeBackgroundCb = func(source, dir, name string) error {
		cleanupCalls++
		return nil
	}

	film := createFanartFilm(t, "persisted-cleanup", "vkp", 1, time.Time{})
	driver.scanFilmFanart(context.Background(), &film)

	if cleanupCalls != 1 {
		t.Fatalf("background cleanup calls = %d, want 1 for persisted fanart progress", cleanupCalls)
	}
}
