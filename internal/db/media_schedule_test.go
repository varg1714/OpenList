package db

import (
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

func TestQuerySampleImageMediaWorksExcludesRetryExhaustedWorks(t *testing.T) {
	// Given
	setupMediaRepositoryTestDB(t)
	exhausted := model.FilmWork{
		StorageID: 94, Source: "javdb", Code: "ABP-940", SourceRef: "940", PrimaryDir: "actor",
		ImageURL: "https://jdbstatic.com/covers/abp-940.jpg", SampleImageRetryCount: sampleImageRetryLimit,
	}
	if err := db.Create(&exhausted).Error; err != nil {
		t.Fatalf("seed exhausted work: %v", err)
	}
	retryable := model.FilmWork{
		StorageID: 95, Source: "javdb", Code: "ABP-950", SourceRef: "950", PrimaryDir: "actor",
		ImageURL: "https://jdbstatic.com/covers/abp-950.jpg", SampleImageRetryCount: sampleImageRetryLimit - 1,
	}
	if err := db.Create(&retryable).Error; err != nil {
		t.Fatalf("seed retryable work: %v", err)
	}

	// When
	selected, err := QuerySampleImageMediaWorks("javdb", 72*time.Hour, 20)

	// Then
	if err != nil {
		t.Fatalf("query sample works: %v", err)
	}
	if len(selected) != 1 || selected[0].ID != retryable.ID {
		t.Fatalf("selected works = %v, want only retryable work %d", selected, retryable.ID)
	}
}

func TestIncrementMediaWorkSampleRetryRecordsScan(t *testing.T) {
	// Given
	setupMediaRepositoryTestDB(t)
	work := model.FilmWork{
		StorageID: 96, Source: "javdb", Code: "ABP-960", SourceRef: "960", PrimaryDir: "actor",
		ImageURL: "https://jdbstatic.com/covers/abp-960.jpg", SampleImageRetryCount: 1,
	}
	if err := db.Create(&work).Error; err != nil {
		t.Fatalf("seed work: %v", err)
	}

	// When
	if err := IncrementMediaWorkSampleRetry(work.ID); err != nil {
		t.Fatalf("increment retry: %v", err)
	}

	// Then
	stored, err := GetFilmWork(work.ID)
	if err != nil {
		t.Fatalf("get stored work: %v", err)
	}
	if stored.SampleImageRetryCount != 2 {
		t.Fatalf("retry count = %d, want 2", stored.SampleImageRetryCount)
	}
	if stored.SampleImageScanAt == nil {
		t.Fatal("scan must be recorded after retry increment")
	}
	if stored.SampleImageComplete {
		t.Fatal("retry increment must not mark the work complete")
	}
}

func TestQueryGeneratedSampleImageMediaWorksIncludesImageLessWork(t *testing.T) {
	// Given
	setupMediaRepositoryTestDB(t)
	work := model.FilmWork{
		StorageID: 93, Source: "fc2", Code: "FC2-PPV-930", SourceRef: "930", PrimaryDir: "actor",
	}
	if err := db.Create(&work).Error; err != nil {
		t.Fatalf("seed image-less work: %v", err)
	}

	// When
	selected, err := QueryGeneratedSampleImageMediaWorks("fc2", 72*time.Hour, 20)

	// Then
	if err != nil {
		t.Fatalf("query generated sample works: %v", err)
	}
	if len(selected) != 1 || selected[0].ID != work.ID {
		t.Fatalf("selected works = %v, want image-less work %d", selected, work.ID)
	}
}
