package db

import (
	"slices"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

func TestQueryPendingMediaWorksSkipsWorksWithoutSourceURL(t *testing.T) {
	setupMediaRepositoryTestDB(t)
	works := []model.FilmWork{
		{StorageID: 1, Source: "javdb", Code: "ABP-201", SourceRef: "https://javdb.test/v/201", SourceURL: "https://javdb.test/v/201", PrimaryDir: "个人收藏"},
		{StorageID: 1, Source: "javdb", Code: "ABP-202", SourceRef: "ABP-202", PrimaryDir: "个人收藏"},
	}
	if err := db.Create(&works).Error; err != nil {
		t.Fatalf("seed works: %v", err)
	}

	selected, err := QueryPendingMediaWorks("javdb", MediaWorkScanAll, 0)
	if err != nil {
		t.Fatalf("query pending works: %v", err)
	}
	if len(selected) != 1 || selected[0].ID != works[0].ID {
		t.Fatalf("selected IDs = %v, want only %d", selectedWorkIDs(selected), works[0].ID)
	}
}

func TestQueryTranslationMediaWorksSkipsEmptyRawTitle(t *testing.T) {
	setupMediaRepositoryTestDB(t)
	works := []model.FilmWork{
		{StorageID: 1, Source: "javdb", Code: "ABP-211", SourceRef: "ABP-211", PrimaryDir: "个人收藏", RawTitle: "Original title"},
		{StorageID: 1, Source: "javdb", Code: "ABP-212", SourceRef: "ABP-212", PrimaryDir: "个人收藏"},
	}
	if err := db.Create(&works).Error; err != nil {
		t.Fatalf("seed works: %v", err)
	}

	selected, err := QueryTranslationMediaWorks("javdb", model.CurrentTranslationVersion, 0)
	if err != nil {
		t.Fatalf("query translation works: %v", err)
	}
	if len(selected) != 1 || selected[0].ID != works[0].ID {
		t.Fatalf("selected IDs = %v, want only %d", selectedWorkIDs(selected), works[0].ID)
	}
}

func TestQueryUnresolvedSourceMediaWorksSelectsDuePlaceholders(t *testing.T) {
	setupMediaRepositoryTestDB(t)
	now := time.Now()
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)
	works := []model.FilmWork{
		{StorageID: 1, Source: "javdb", Code: "ABP-221", SourceRef: "ABP-221", PrimaryDir: "个人收藏"},
		{StorageID: 1, Source: "javdb", Code: "ABP-222", SourceRef: "ABP-222", PrimaryDir: "个人收藏", SourceNextRetryAt: &future},
		{StorageID: 1, Source: "javdb", Code: "ABP-223", SourceRef: "ABP-223", PrimaryDir: "个人收藏", SourceNextRetryAt: &past},
		{StorageID: 1, Source: "javdb", Code: "ABP-224", SourceRef: "https://javdb.test/v/224", SourceURL: "https://javdb.test/v/224", PrimaryDir: "个人收藏"},
		{StorageID: 2, Source: "fc2", Code: "FC2-PPV-225", SourceRef: "225", PrimaryDir: "个人收藏"},
	}
	if err := db.Create(&works).Error; err != nil {
		t.Fatalf("seed works: %v", err)
	}

	selected, err := QueryUnresolvedSourceMediaWorks("javdb", 0)
	if err != nil {
		t.Fatalf("query unresolved sources: %v", err)
	}
	got := selectedWorkIDs(selected)
	want := []uint{works[2].ID, works[0].ID}
	if !slices.Equal(got, want) {
		t.Fatalf("selected IDs = %v, want %v", got, want)
	}
}

func selectedWorkIDs(works []model.FilmWork) []uint {
	ids := make([]uint, len(works))
	for index, work := range works {
		ids[index] = work.ID
	}
	return ids
}
