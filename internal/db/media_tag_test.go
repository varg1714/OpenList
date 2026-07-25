package db

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

func TestQueryPendingMediaWorksReturnsRequestedUnscannedAndDueStages(t *testing.T) {
	// Given
	setupMediaRepositoryTestDB(t)
	now := time.Now()
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)
	works := []model.FilmWork{
		{StorageID: 1, Source: "javdb", Code: "ABP-101", SourceRef: "1", PrimaryDir: "actor", Actors: model.StringArray{"legacy actor"}, Tags: model.StringArray{"legacy"}},
		{StorageID: 1, Source: "javdb", Code: "ABP-102", SourceRef: "2", PrimaryDir: "actor", TagScanAt: &now, ActorScanAt: &now},
		{StorageID: 1, Source: "javdb", Code: "ABP-103", SourceRef: "3", PrimaryDir: "actor"},
		{StorageID: 1, Source: "javdb", Code: "ABP-104", SourceRef: "4", PrimaryDir: "actor", Actors: model.StringArray{"actor"}, Tags: model.StringArray{"partial"}, TagScanAt: &now, TagNextRetryAt: &past},
		{StorageID: 1, Source: "javdb", Code: "ABP-105", SourceRef: "5", PrimaryDir: "actor", TagScanAt: &now, TagNextRetryAt: &future, ActorScanAt: &now},
		{StorageID: 2, Source: "pornhub", Code: "view-key", SourceRef: "6", PrimaryDir: "actor"},
		{StorageID: 1, Source: "javdb", Code: "ABP-106", SourceRef: "7", PrimaryDir: "actor", Tags: model.StringArray{"tagged"}, TagScanAt: &now},
		{StorageID: 1, Source: "javdb", Code: "ABP-107", SourceRef: "8", PrimaryDir: "actor", Actors: model.StringArray{"partial actor"}, Tags: model.StringArray{"tagged"}, TagScanAt: &now, ActorScanAt: &now, ActorNextRetryAt: &past},
		{StorageID: 2, Source: "pornhub", Code: "completed-key", SourceRef: "9", PrimaryDir: "actor", Tags: model.StringArray{"tagged"}, TagScanAt: &now},
	}
	if err := db.Create(&works).Error; err != nil {
		t.Fatalf("seed tag scan works: %v", err)
	}

	// When
	selected, err := QueryPendingMediaWorks("javdb", MediaWorkScanTags|MediaWorkScanActors, 0)

	// Then
	if err != nil {
		t.Fatalf("query tag works: %v", err)
	}
	got := make([]uint, len(selected))
	for index := range selected {
		got[index] = selected[index].ID
	}
	want := []uint{works[7].ID, works[6].ID, works[3].ID, works[2].ID}
	if !slices.Equal(got, want) {
		t.Fatalf("selected work IDs = %v, want %v", got, want)
	}
	pornhubSelected, err := QueryPendingMediaWorks("pornhub", MediaWorkScanTags, 0)
	if err != nil {
		t.Fatalf("query Pornhub tag works: %v", err)
	}
	if len(pornhubSelected) != 1 || pornhubSelected[0].ID != works[5].ID {
		t.Fatalf("Pornhub selected works = %v, want only %d", pornhubSelected, works[5].ID)
	}
	allSelected, err := QueryPendingMediaWorks("javdb", MediaWorkScanAll, 0)
	if err != nil {
		t.Fatalf("query all pending stages: %v", err)
	}
	allIDs := make([]uint, len(allSelected))
	for index := range allSelected {
		allIDs[index] = allSelected[index].ID
	}
	if !slices.Equal(allIDs, want) {
		t.Fatalf("all-stage work IDs = %v, want %v", allIDs, want)
	}
	if _, err := QueryPendingMediaWorks("javdb", MediaWorkScanStages(1<<7), 0); !errors.Is(err, ErrInvalidMediaWorkScanStages) {
		t.Fatalf("unknown scan stages error = %v, want ErrInvalidMediaWorkScanStages", err)
	}
}
