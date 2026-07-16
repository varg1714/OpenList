package db

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestMain(m *testing.M) {
	dataDir, err := os.MkdirTemp("", "film-sample-image-test-")
	if err != nil {
		panic(err)
	}
	conf.Conf = conf.DefaultConfig(dataDir)
	testDB, err := gorm.Open(sqlite.Open(filepath.Join(dataDir, "test.db")), &gorm.Config{})
	if err != nil {
		panic(err)
	}
	db = testDB
	if err := AutoMigrate(new(model.Film)); err != nil {
		panic(err)
	}

	code := m.Run()
	if sqlDB, sqlErr := testDB.DB(); sqlErr == nil {
		_ = sqlDB.Close()
	}
	_ = os.RemoveAll(dataDir)
	os.Exit(code)
}

func setupFilmSampleImageTestDB(t *testing.T) {
	t.Helper()

	previousConf := conf.Conf
	conf.Conf = conf.DefaultConfig("data")
	if err := db.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&model.Film{}).Error; err != nil {
		t.Fatalf("reset films: %v", err)
	}

	t.Cleanup(func() {
		conf.Conf = previousConf
	})
}

func TestQuerySampleImageFilms(t *testing.T) {
	setupFilmSampleImageTestDB(t)

	now := time.Now()
	films := []model.Film{
		{Name: "older", Source: "target", Image: "older.jpg", Date: now.Add(-time.Hour), SampleImageScanAt: now.Add(-2 * time.Hour)},
		{Name: "newer-first", Source: "target", Image: "first.jpg", Date: now, SampleImageScanAt: now.Add(-2 * time.Hour)},
		{Name: "newer-second", Source: "target", Image: "second.jpg", Date: now},
		{Name: "wrong-source", Source: "other", Image: "other.jpg", Date: now.Add(time.Hour), SampleImageScanAt: now.Add(-2 * time.Hour)},
		{Name: "empty-image", Source: "target", Date: now.Add(time.Hour), SampleImageScanAt: now.Add(-2 * time.Hour)},
		{Name: "complete", Source: "target", Image: "complete.jpg", Date: now.Add(time.Hour), SampleImageComplete: true, SampleImageScanAt: now.Add(-2 * time.Hour)},
		{Name: "fresh", Source: "target", Image: "fresh.jpg", Date: now.Add(time.Hour), SampleImageScanAt: now},
	}
	if err := db.Create(&films).Error; err != nil {
		t.Fatalf("create films: %v", err)
	}
	if err := db.Model(&model.Film{}).Where("id = ?", films[2].ID).Update("sample_image_scan_at", nil).Error; err != nil {
		t.Fatalf("set null scan time: %v", err)
	}

	got, err := QuerySampleImageFilms("target", time.Hour, 2)
	if err != nil {
		t.Fatalf("query sample-image films: %v", err)
	}
	wantIDs := []uint{films[2].ID, films[1].ID}
	if len(got) != len(wantIDs) {
		t.Fatalf("got %d films, want %d", len(got), len(wantIDs))
	}
	for i, wantID := range wantIDs {
		if got[i].ID != wantID {
			t.Errorf("film %d ID = %d, want %d", i, got[i].ID, wantID)
		}
	}
}

func TestSampleImageUpdateHelpers(t *testing.T) {
	setupFilmSampleImageTestDB(t)

	oldScanAt := time.Now().Add(-24 * time.Hour)
	film := model.Film{
		Name:                "sample",
		Source:              "target",
		Image:               "sample.jpg",
		SampleImageCount:    9,
		SampleImageComplete: true,
		SampleImageScanAt:   oldScanAt,
	}
	if err := db.Create(&film).Error; err != nil {
		t.Fatalf("create film: %v", err)
	}

	if err := UpdateSampleImageProgress(film.ID, 0, false); err != nil {
		t.Fatalf("update progress: %v", err)
	}
	progress := loadFilmForSampleImageTest(t, film.ID)
	if progress.SampleImageCount != 9 {
		t.Errorf("sample image count = %d, want 9", progress.SampleImageCount)
	}
	if !progress.SampleImageComplete {
		t.Error("sample image complete = false, want true")
	}
	if !progress.SampleImageScanAt.Equal(oldScanAt) {
		t.Errorf("progress scan time = %s, want unchanged %s", progress.SampleImageScanAt, oldScanAt)
	}

	if err := db.Model(&model.Film{}).Where("id = ?", film.ID).Updates(map[string]interface{}{
		"sample_image_complete": false,
		"sample_image_scan_at":  oldScanAt,
	}).Error; err != nil {
		t.Fatalf("reset progress state: %v", err)
	}
	if err := UpdateSampleImageProgress(film.ID, 12, false); err != nil {
		t.Fatalf("advance progress: %v", err)
	}
	if err := UpdateSampleImageProgress(film.ID, 10, true); err != nil {
		t.Fatalf("complete progress: %v", err)
	}
	progress = loadFilmForSampleImageTest(t, film.ID)
	if progress.SampleImageCount != 12 || !progress.SampleImageComplete {
		t.Errorf("monotonic progress = (%d, %t), want (12, true)", progress.SampleImageCount, progress.SampleImageComplete)
	}
	if !progress.SampleImageScanAt.Equal(oldScanAt) {
		t.Errorf("advanced progress scan time = %s, want unchanged %s", progress.SampleImageScanAt, oldScanAt)
	}

	if err := db.Model(&model.Film{}).Where("id = ?", film.ID).Update("sample_image_scan_at", oldScanAt).Error; err != nil {
		t.Fatalf("reset scan time: %v", err)
	}
	beforeComplete := time.Now()
	if err := MarkSampleImageComplete(film.ID); err != nil {
		t.Fatalf("mark complete: %v", err)
	}
	complete := loadFilmForSampleImageTest(t, film.ID)
	if !complete.SampleImageComplete {
		t.Error("sample image complete = false, want true")
	}
	assertScanTimeUpdated(t, complete.SampleImageScanAt, beforeComplete)

	if err := db.Model(&model.Film{}).Where("id = ?", film.ID).Update("sample_image_scan_at", oldScanAt).Error; err != nil {
		t.Fatalf("reset scan time: %v", err)
	}
	beforeScan := time.Now()
	if err := UpdateSampleImageScanAt(film.ID); err != nil {
		t.Fatalf("update scan time: %v", err)
	}
	scanned := loadFilmForSampleImageTest(t, film.ID)
	if scanned.SampleImageCount != 12 || !scanned.SampleImageComplete {
		t.Errorf("scan-only update changed progress: count=%d complete=%t", scanned.SampleImageCount, scanned.SampleImageComplete)
	}
	assertScanTimeUpdated(t, scanned.SampleImageScanAt, beforeScan)
}

func loadFilmForSampleImageTest(t *testing.T, filmID uint) model.Film {
	t.Helper()

	var film model.Film
	if err := db.First(&film, filmID).Error; err != nil {
		t.Fatalf("load film: %v", err)
	}
	return film
}

func assertScanTimeUpdated(t *testing.T, scanAt, earliest time.Time) {
	t.Helper()

	if scanAt.Before(earliest) {
		t.Errorf("scan time %s is before update started at %s", scanAt, earliest)
	}
}
