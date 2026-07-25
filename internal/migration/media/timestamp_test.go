package media

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"gorm.io/gorm"
)

type timestampScenario struct {
	legacyCreatedAt   time.Time
	existingCreatedAt time.Time
	existingUpdatedAt time.Time
}

type timestampFixture struct {
	database *gorm.DB
	file     model.FilmFile
}

func TestMigrateLegacyMediaPreservesRicherExistingFileTimesWhenLegacyTimeUnknown(t *testing.T) {
	scenario := timestampScenario{
		existingCreatedAt: time.Date(2021, time.March, 4, 5, 6, 7, 0, time.UTC),
		existingUpdatedAt: time.Date(2022, time.April, 5, 6, 7, 8, 0, time.UTC),
	}
	for _, mode := range []MigrationMode{MigrationDryRun, MigrationApply} {
		t.Run(string(mode), func(t *testing.T) {
			// Given
			fixture := seedTimestampFixture(t, scenario)

			// When
			_, err := MigrateLegacyMediaWithOptions(context.Background(), fixture.database, MigrationOptions{Mode: mode, DataDir: t.TempDir()})

			// Then
			if err != nil {
				t.Fatalf("%s migration with unknown legacy timestamp: %v", mode, err)
			}
			var stored model.FilmFile
			if err := fixture.database.First(&stored, fixture.file.ID).Error; err != nil {
				t.Fatalf("load preserved normalized file: %v", err)
			}
			if !stored.CreatedAt.Equal(scenario.existingCreatedAt) || !stored.UpdatedAt.Equal(scenario.existingUpdatedAt) {
				t.Fatalf("%s preserved timestamps = created %v, updated %v; want created %v, updated %v", mode, stored.CreatedAt, stored.UpdatedAt, scenario.existingCreatedAt, scenario.existingUpdatedAt)
			}
		})
	}
}

func TestMigrateLegacyMediaDryRunRejectsConflictingKnownLegacyFileTimes(t *testing.T) {
	// Given
	scenario := timestampScenario{
		legacyCreatedAt:   time.Date(2020, time.January, 2, 3, 4, 5, 0, time.UTC),
		existingCreatedAt: time.Date(2021, time.March, 4, 5, 6, 7, 0, time.UTC),
		existingUpdatedAt: time.Date(2022, time.April, 5, 6, 7, 8, 0, time.UTC),
	}
	fixture := seedTimestampFixture(t, scenario)

	// When
	_, err := MigrateLegacyMediaWithOptions(context.Background(), fixture.database, MigrationOptions{Mode: MigrationDryRun, DataDir: t.TempDir()})

	// Then
	if !errors.Is(err, ErrIdentityCollision) {
		t.Fatalf("known legacy timestamp conflict = %v, want ErrIdentityCollision", err)
	}
}

func seedTimestampFixture(t *testing.T, scenario timestampScenario) timestampFixture {
	t.Helper()
	database := newMigrationTestDB(t)
	createStorages(t, database, model.Storage{ID: 1, Driver: "Javdb", MountPath: "/javdb"})
	film := model.Film{
		ID: 51, Source: "javdb", Actor: "Actor A", Name: "ABP-123 title.mp4", Url: "https://javdb.test/v/abp-123",
	}
	if err := database.Create(&film).Error; err != nil {
		t.Fatalf("seed legacy film: %v", err)
	}
	if err := database.Model(&film).UpdateColumn("created_at", scenario.legacyCreatedAt).Error; err != nil {
		t.Fatalf("set legacy timestamp: %v", err)
	}
	work := model.FilmWork{
		StorageID: 1, Source: "javdb", Code: "ABP-123", SourceRef: film.Url, SourceURL: film.Url, PrimaryDir: film.Actor,
	}
	if err := database.Create(&work).Error; err != nil {
		t.Fatalf("seed normalized work: %v", err)
	}
	file := model.FilmFile{
		ID: film.ID, WorkID: work.ID, PartIndex: 1, PartCount: 1, SourcePath: film.Name,
		SourceSize: legacyListingFileSize, CreatedAt: scenario.existingCreatedAt, UpdatedAt: scenario.existingUpdatedAt,
	}
	if err := database.Create(&file).Error; err != nil {
		t.Fatalf("seed normalized file: %v", err)
	}
	return timestampFixture{database: database, file: file}
}
