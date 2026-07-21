package javdb

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
)

func TestScanFilmSampleImagesRemovesRegularBackground(t *testing.T) {
	setupJavdbSampleImageTest(t)

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/samples/bg-regular_l_1.jpg" {
			_, _ = response.Write([]byte("first image"))
			return
		}
		response.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	film := createSampleImageFilm(t, "https://img.jdbstatic.com", "bg-regular", 0, time.Time{})
	film.Url = server.URL + "/movies/bg-regular"
	if err := db.GetDb().Model(&film).Update("url", film.Url).Error; err != nil {
		t.Fatalf("update film URL: %v", err)
	}

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

	newSampleImageDriver(server).scanFilmSampleImages(context.Background(), &film)

	if _, err := os.Lstat(paths.Background); !os.IsNotExist(err) {
		t.Fatalf("regular background not removed: %v", err)
	}
	if _, err := os.Lstat(paths.LegacyPoster); err != nil {
		t.Fatalf("legacy poster lost: %v", err)
	}
}

func TestScanFilmSampleImagesRemovesBackgroundSymlink(t *testing.T) {
	setupJavdbSampleImageTest(t)

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/samples/bg-symlink_l_1.jpg" {
			_, _ = response.Write([]byte("first image"))
			return
		}
		response.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	film := createSampleImageFilm(t, "https://img.jdbstatic.com", "bg-symlink", 0, time.Time{})
	film.Url = server.URL + "/movies/bg-symlink"
	if err := db.GetDb().Model(&film).Update("url", film.Url).Error; err != nil {
		t.Fatalf("update film URL: %v", err)
	}

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
	if err := os.Symlink(filepath.Base(paths.LegacyPoster), paths.Background); err != nil {
		t.Fatal(err)
	}

	newSampleImageDriver(server).scanFilmSampleImages(context.Background(), &film)

	if _, err := os.Lstat(paths.Background); !os.IsNotExist(err) {
		t.Fatalf("background symlink not removed: %v", err)
	}
	content, err := os.ReadFile(paths.LegacyPoster)
	if err != nil {
		t.Fatalf("symlink target lost: %v", err)
	}
	if string(content) != "old poster" {
		t.Fatalf("symlink target content = %q, want old poster", content)
	}
}

func TestScanFilmSampleImagesCleanupFailurePreservesRetry(t *testing.T) {
	setupJavdbSampleImageTest(t)

	var removeCalls int
	removeStub := func(source, dir, name string) error {
		removeCalls++
		return fmt.Errorf("cleanup denied")
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/samples/cleanup-fail_l_1.jpg" {
			_, _ = response.Write([]byte("first image"))
			return
		}
		response.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	film := createSampleImageFilm(t, "https://img.jdbstatic.com", "cleanup-fail", 0, time.Time{})
	film.Url = server.URL + "/movies/cleanup-fail"
	if err := db.GetDb().Model(&film).Update("url", film.Url).Error; err != nil {
		t.Fatalf("update film URL: %v", err)
	}

	started := time.Now()
	d := newSampleImageDriver(server)
	d.removeBackground = removeStub
	d.scanFilmSampleImages(context.Background(), &film)

	if removeCalls != 1 {
		t.Fatalf("RemoveBackground calls = %d, want 1", removeCalls)
	}

	stored := loadSampleImageFilm(t, film.ID)
	if stored.SampleImageCount != 0 || stored.SampleImageComplete {
		t.Fatalf("progress = (%d, %t), want (0, false) - should not advance on cleanup failure", stored.SampleImageCount, stored.SampleImageComplete)
	}
	if stored.SampleImageScanAt.Before(started) {
		t.Errorf("scan time = %s, want at or after %s", stored.SampleImageScanAt, started)
	}
}

func TestScanFilmSampleImagesPersistedProgressRemovesBackgroundBeforeTerminal403(t *testing.T) {
	setupJavdbSampleImageTest(t)

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	film := createSampleImageFilm(t, "https://img.jdbstatic.com", "persisted-background", 1, time.Time{})
	paths, err := virtual_file.PosterPaths(DriverName, film.Actor, film.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.Background), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Background, []byte("old background"), 0o644); err != nil {
		t.Fatal(err)
	}
	fanartPath, err := virtual_file.FanartPath(DriverName, film.Actor, film.Name, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fanartPath, []byte("existing fanart"), 0o644); err != nil {
		t.Fatal(err)
	}

	newSampleImageDriver(server).scanFilmSampleImages(context.Background(), &film)

	if _, err := os.Lstat(paths.Background); !os.IsNotExist(err) {
		t.Fatalf("background still exists after persisted fanart progress: %v", err)
	}
	stored := loadSampleImageFilm(t, film.ID)
	if stored.SampleImageCount != 1 || !stored.SampleImageComplete {
		t.Fatalf("progress = (%d, %t), want (1, true)", stored.SampleImageCount, stored.SampleImageComplete)
	}
}

func TestScanFilmSampleImagesStaleProgressKeepsBackgroundWithoutFanart(t *testing.T) {
	setupJavdbSampleImageTest(t)

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	film := createSampleImageFilm(t, "https://img.jdbstatic.com", "stale-background", 1, time.Time{})
	paths, err := virtual_file.PosterPaths(DriverName, film.Actor, film.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.Background), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Background, []byte("fallback background"), 0o644); err != nil {
		t.Fatal(err)
	}

	newSampleImageDriver(server).scanFilmSampleImages(context.Background(), &film)

	content, err := os.ReadFile(paths.Background)
	if err != nil {
		t.Fatalf("background removed without a real fanart file: %v", err)
	}
	if string(content) != "fallback background" {
		t.Fatalf("background content = %q, want fallback background", content)
	}
}
