package pornhub

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
)

func TestScanFilmFanartPromotesFirstLaterLandscapeAfterAllFramesExist(t *testing.T) {
	setupPornhubFanartTest(t)
	originalPromote := promoteLandscapeFanartCandidate
	t.Cleanup(func() { promoteLandscapeFanartCandidate = originalPromote })
	allFramesExisted := false
	promoteLandscapeFanartCandidate = func(identity virtual_file.MediaIdentity, candidateIndex int) (bool, error) {
		for index := 1; index <= 3; index++ {
			path, err := virtual_file.MediaFanartPath(identity, index)
			if err != nil {
				t.Fatal(err)
			}
			info, err := os.Lstat(path)
			if err != nil || !info.Mode().IsRegular() {
				t.Fatalf("fanart%d unavailable during promotion: %v", index, err)
			}
		}
		allFramesExisted = true
		return originalPromote(identity, candidateIndex)
	}
	media := &mockFanartMedia{
		duration: 100,
		frames: map[float64][]byte{
			25: (fanartJPEGFixture{width: 2, height: 3, fill: color.RGBA{R: 220, A: 255}}).encode(t),
			50: (fanartJPEGFixture{width: 4, height: 2, fill: color.RGBA{B: 220, A: 255}}).encode(t),
			75: (fanartJPEGFixture{width: 3, height: 4, fill: color.RGBA{G: 220, A: 255}}).encode(t),
		},
	}
	driver := newFanartDriver(media, func(_ context.Context, key string) (string, error) {
		return "https://example.test/video/" + key, nil
	})
	film := createFanartWork(t, "promotion-test", "view-promotion", 0, time.Time{})

	driver.scanFilmFanart(context.Background(), &film)

	if !allFramesExisted {
		t.Fatal("landscape promotion did not run after all configured frames existed")
	}
	firstPath, err := virtual_file.FanartPath(DriverName, film.PrimaryDir, film.Code, 1)
	if err != nil {
		t.Fatal(err)
	}
	secondPath, err := virtual_file.FanartPath(DriverName, film.PrimaryDir, film.Code, 2)
	if err != nil {
		t.Fatal(err)
	}
	firstWidth, firstHeight, firstColor := fanartJPEGDetails(t, firstPath)
	if firstWidth != 4 || firstHeight != 2 || firstColor != (color.RGBA{B: 220, A: 255}) {
		t.Fatalf("fanart1 = (%d, %d, %v), want landscape blue frame (4, 2, %v)", firstWidth, firstHeight, firstColor, color.RGBA{B: 220, A: 255})
	}
	secondWidth, secondHeight, secondColor := fanartJPEGDetails(t, secondPath)
	if secondWidth != 2 || secondHeight != 3 || secondColor != (color.RGBA{R: 220, A: 255}) {
		t.Fatalf("fanart2 = (%d, %d, %v), want original portrait red frame (2, 3, %v)", secondWidth, secondHeight, secondColor, color.RGBA{R: 220, A: 255})
	}
}

func TestScanFilmFanartKeepsOrderWhenAllFramesArePortrait(t *testing.T) {
	setupPornhubFanartTest(t)
	media := &mockFanartMedia{
		duration: 100,
		frames: map[float64][]byte{
			25: (fanartJPEGFixture{width: 2, height: 3, fill: color.RGBA{R: 220, A: 255}}).encode(t),
			50: (fanartJPEGFixture{width: 3, height: 4, fill: color.RGBA{G: 220, A: 255}}).encode(t),
			75: (fanartJPEGFixture{width: 4, height: 5, fill: color.RGBA{B: 220, A: 255}}).encode(t),
		},
	}
	driver := newFanartDriver(media, func(_ context.Context, key string) (string, error) {
		return "https://example.test/video/" + key, nil
	})
	film := createFanartWork(t, "portrait-test", "view-portrait", 0, time.Time{})

	driver.scanFilmFanart(context.Background(), &film)

	firstPath, err := virtual_file.FanartPath(DriverName, film.PrimaryDir, film.Code, 1)
	if err != nil {
		t.Fatal(err)
	}
	secondPath, err := virtual_file.FanartPath(DriverName, film.PrimaryDir, film.Code, 2)
	if err != nil {
		t.Fatal(err)
	}
	firstWidth, firstHeight, firstColor := fanartJPEGDetails(t, firstPath)
	if firstWidth != 2 || firstHeight != 3 || firstColor != (color.RGBA{R: 220, A: 255}) {
		t.Fatalf("fanart1 changed to (%d, %d, %v), want original portrait red frame", firstWidth, firstHeight, firstColor)
	}
	secondWidth, secondHeight, secondColor := fanartJPEGDetails(t, secondPath)
	if secondWidth != 3 || secondHeight != 4 || secondColor != (color.RGBA{G: 220, A: 255}) {
		t.Fatalf("fanart2 changed to (%d, %d, %v), want original portrait green frame", secondWidth, secondHeight, secondColor)
	}
	stored := loadFanartWork(t, film.ID)
	if stored.SampleImageCount != 3 || !stored.SampleImageComplete {
		t.Fatalf("progress = (%d, %t), want (3, true)", stored.SampleImageCount, stored.SampleImageComplete)
	}
}

func TestScanFanartRetriesPromotionFailureThroughQuery(t *testing.T) {
	setupPornhubFanartTest(t)
	originalPromote := promoteLandscapeFanartCandidate
	t.Cleanup(func() { promoteLandscapeFanartCandidate = originalPromote })
	promoteLandscapeFanartCandidate = func(virtual_file.MediaIdentity, int) (bool, error) {
		return false, errors.New("promotion unavailable")
	}
	driver := newFanartDriver(&mockFanartMedia{
		duration: 100,
		frames: map[float64][]byte{
			25: (fanartJPEGFixture{width: 2, height: 3, fill: color.RGBA{R: 220, A: 255}}).encode(t),
			50: (fanartJPEGFixture{width: 4, height: 2, fill: color.RGBA{B: 220, A: 255}}).encode(t),
			75: (fanartJPEGFixture{width: 3, height: 4, fill: color.RGBA{G: 220, A: 255}}).encode(t),
		},
	}, func(_ context.Context, key string) (string, error) {
		return "https://example.test/video/" + key, nil
	})
	film := createFanartWork(t, "retry-test", "view-retry", 0, time.Time{})
	started := time.Now()

	driver.scanFilmFanart(context.Background(), &film)

	stored := loadFanartWork(t, film.ID)
	if stored.SampleImageCount != 2 || stored.SampleImageComplete {
		t.Fatalf("failed progress = (%d, %t), want (2, false)", stored.SampleImageCount, stored.SampleImageComplete)
	}
	if stored.SampleImageScanAt == nil || stored.SampleImageScanAt.Before(started) {
		t.Fatalf("failed scan time = %v, want at or after %s", stored.SampleImageScanAt, started)
	}

	promoteLandscapeFanartCandidate = originalPromote
	if err := db.GetDb().Model(&stored).Update("sample_image_scan_at", time.Now().Add(-73*time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	driver.scanFanart(context.Background())

	stored = loadFanartWork(t, film.ID)
	if stored.SampleImageCount != 3 || !stored.SampleImageComplete {
		t.Fatalf("retried progress = (%d, %t), want (3, true)", stored.SampleImageCount, stored.SampleImageComplete)
	}
	firstPath, err := virtual_file.FanartPath(DriverName, film.PrimaryDir, film.Code, 1)
	if err != nil {
		t.Fatal(err)
	}
	width, height, gotColor := fanartJPEGDetails(t, firstPath)
	if width != 4 || height != 2 || gotColor != (color.RGBA{B: 220, A: 255}) {
		t.Fatalf("retried fanart1 = (%d, %d, %v), want landscape blue frame", width, height, gotColor)
	}
}

func TestScanFanartSkipsCompletedWork(t *testing.T) {
	setupPornhubFanartTest(t)
	videoCalls := 0
	driver := newFanartDriver(&mockFanartMedia{}, func(_ context.Context, _ string) (string, error) {
		videoCalls++
		return "", errors.New("completed work should not resolve video")
	})
	film := createFanartWork(t, "completed", "view-completed", 3, time.Now().Add(-73*time.Hour))
	if err := db.GetDb().Model(&film).Update("sample_image_complete", true).Error; err != nil {
		t.Fatal(err)
	}

	driver.scanFanart(context.Background())

	if videoCalls != 0 {
		t.Fatalf("video resolution calls = %d, want 0 for completed work", videoCalls)
	}
}

type fanartJPEGFixture struct {
	width  int
	height int
	fill   color.RGBA
}

func (f fanartJPEGFixture) encode(t *testing.T) []byte {
	t.Helper()
	imageData := image.NewRGBA(image.Rect(0, 0, f.width, f.height))
	for y := 0; y < f.height; y++ {
		for x := 0; x < f.width; x++ {
			imageData.SetRGBA(x, y, f.fill)
		}
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, imageData, &jpeg.Options{Quality: 100}); err != nil {
		t.Fatalf("encode JPEG: %v", err)
	}
	return encoded.Bytes()
}

func fanartJPEGDetails(t *testing.T, path string) (int, int, color.RGBA) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer file.Close()
	decoded, err := jpeg.Decode(file)
	if err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	r, g, b, a := decoded.At(0, 0).RGBA()
	return decoded.Bounds().Dx(), decoded.Bounds().Dy(), color.RGBA{
		R: uint8(r >> 8),
		G: uint8(g >> 8),
		B: uint8(b >> 8),
		A: uint8(a >> 8),
	}
}
