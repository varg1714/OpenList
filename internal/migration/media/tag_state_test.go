package media

import (
	"context"
	"slices"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

func TestMigrateLegacyMediaPreservesTagsWithUnsetTagScanState(t *testing.T) {
	// Given
	database := newMigrationTestDB(t)
	createStorages(t, database, model.Storage{ID: 15, Driver: "Javdb", MountPath: "/javdb"})
	film := model.Film{
		Source: "javdb",
		Actor:  "actor",
		Name:   "ABP-123 title.mp4",
		Url:    "https://javdb.test/v/abp-123",
		Tags:   model.StringArray{"legacy", model.TagSubtitle},
	}
	if err := database.Create(&film).Error; err != nil {
		t.Fatalf("seed tagged film: %v", err)
	}

	// When
	_, err := MigrateLegacyMediaWithOptions(context.Background(), database, MigrationOptions{
		Mode: MigrationApply, DataDir: t.TempDir(),
	})

	// Then
	if err != nil {
		t.Fatalf("migrate tagged film: %v", err)
	}
	work := getWork(t, database, 15, "javdb", "ABP-123")
	if !slices.Equal([]string(work.Tags), []string(film.Tags)) {
		t.Fatalf("migrated tags = %v, want %v", work.Tags, film.Tags)
	}
	if work.TagScanAt != nil || work.TagNextRetryAt != nil || work.TagVersion != 0 {
		t.Fatalf("migrated tag state = (scan=%v retry=%v version=%d), want (nil nil 0)", work.TagScanAt, work.TagNextRetryAt, work.TagVersion)
	}
}
