package javdb

import (
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"gorm.io/gorm"
)

func TestSubtitleScanRetriesRecentEmptyResultAndCompletesOldEmptyResult(t *testing.T) {
	// Given
	for _, value := range []interface{}{&model.SourceMagnet{}, &model.FilmFile{}, &model.FilmWork{}} {
		if err := db.GetDb().Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(value).Error; err != nil {
			t.Fatalf("reset %T: %v", value, err)
		}
	}
	now := time.Now()
	works := []model.FilmWork{
		{StorageID: 87, Source: DriverName, Code: "ABP-870", SourceRef: "recent", PrimaryDir: "actor", ReleaseDate: now.AddDate(0, -1, 0)},
		{StorageID: 87, Source: DriverName, Code: "ABP-871", SourceRef: "old", PrimaryDir: "actor", ReleaseDate: now.AddDate(-2, 0, 0)},
	}
	if err := db.GetDb().Create(&works).Error; err != nil {
		t.Fatalf("seed subtitle works: %v", err)
	}
	oldMatch := matchMediaSubtitles
	t.Cleanup(func() { matchMediaSubtitles = oldMatch })
	matchMediaSubtitles = func(string) ([]string, error) { return nil, nil }

	// When
	started := time.Now()
	(&Javdb{Addition: Addition{SubtitlesScanLimit: 2}}).scanMediaSubtitles()

	// Then
	recent, err := db.GetFilmWork(works[0].ID)
	if err != nil {
		t.Fatalf("get recent work: %v", err)
	}
	if recent.SubtitleScanAt == nil || recent.SubtitleNextRetryAt == nil || recent.SubtitleNextRetryAt.Before(started.AddDate(0, 0, 7)) || recent.SubtitleLastError != "" {
		t.Fatalf("recent subtitle state = (scan=%v retry=%v error=%q), want seven-day retry", recent.SubtitleScanAt, recent.SubtitleNextRetryAt, recent.SubtitleLastError)
	}
	old, err := db.GetFilmWork(works[1].ID)
	if err != nil {
		t.Fatalf("get old work: %v", err)
	}
	if old.SubtitleScanAt == nil || old.SubtitleNextRetryAt != nil || old.SubtitleLastError != "" {
		t.Fatalf("old subtitle state = (scan=%v retry=%v error=%q), want terminal completion", old.SubtitleScanAt, old.SubtitleNextRetryAt, old.SubtitleLastError)
	}
}
