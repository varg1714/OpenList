package db

import (
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

func TestQueryFanartMediaWorksUsesNormalizedCandidates(t *testing.T) {
	// Given
	setupMediaRepositoryTestDB(t)
	stale := time.Now().Add(-73 * time.Hour)
	works := []model.FilmWork{
		{StorageID: 1, Source: "pornhub", Code: "pending", SourceRef: "pending", PrimaryDir: "actor", SampleImageScanAt: &stale},
		{StorageID: 1, Source: "pornhub", Code: "completed", SourceRef: "completed", PrimaryDir: "actor", SampleImageCount: 3, SampleImageComplete: true, SampleImageScanAt: &stale},
		{StorageID: 1, Source: "pornhub", Code: "missing-ref", PrimaryDir: "actor", SampleImageScanAt: &stale},
		{StorageID: 2, Source: "pornhub", Code: "other-storage", SourceRef: "other-storage", PrimaryDir: "actor", SampleImageScanAt: &stale},
		{StorageID: 1, Source: "javdb", Code: "other-source", SourceRef: "other-source", PrimaryDir: "actor", SampleImageScanAt: &stale},
	}
	if err := db.Create(&works).Error; err != nil {
		t.Fatal(err)
	}

	// When
	candidates, err := QueryFanartMediaWorks(1, "pornhub", 72*time.Hour, 10, 3)

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 || candidates[0].Code != "completed" || candidates[1].Code != "pending" {
		t.Fatalf("fanart candidates = %+v, want completed and pending normalized works", candidates)
	}
}

func TestUpdateMediaWorkSampleScanAtPreservesCompletion(t *testing.T) {
	// Given
	setupMediaRepositoryTestDB(t)
	work := model.FilmWork{
		StorageID: 1, Source: "pornhub", Code: "complete", SourceRef: "complete", PrimaryDir: "actor",
		SampleImageCount: 3, SampleImageComplete: true,
	}
	if err := db.Create(&work).Error; err != nil {
		t.Fatal(err)
	}

	// When
	if err := UpdateMediaWorkSampleScanAt(work.ID); err != nil {
		t.Fatal(err)
	}

	// Then
	stored, err := GetFilmWork(work.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !stored.SampleImageComplete || stored.SampleImageCount != 3 || stored.SampleImageScanAt == nil {
		t.Fatalf("sample-image state = (%d, %t, %v), want preserved completion with scan time", stored.SampleImageCount, stored.SampleImageComplete, stored.SampleImageScanAt)
	}
}
