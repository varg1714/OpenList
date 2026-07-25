package db

import (
	"fmt"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/stretchr/testify/require"
)

func TestQueryPendingMediaPosterWorksLimitsBatchToTwenty(t *testing.T) {
	// Given
	setupMediaRepositoryTestDB(t)
	works := make([]model.FilmWork, 21)
	for index := range works {
		works[index] = model.FilmWork{
			StorageID: 9, Source: "pornhub", Code: fmt.Sprintf("view-key-%02d", index), PrimaryDir: "actor",
			ImageURL: fmt.Sprintf("https://image.test/%02d.jpg", index),
		}
	}
	require.NoError(t, db.Create(&works).Error)

	// When
	selected, err := QueryPendingMediaPosterWorks(9, "pornhub", 72*time.Hour)

	// Then
	require.NoError(t, err)
	require.Len(t, selected, 20)
	require.Equal(t, works[0].ID, selected[0].ID)
	require.Equal(t, works[19].ID, selected[19].ID)
}

func TestQueryPendingMediaPosterWorksSelectsInitialAndExpiredRetryRows(t *testing.T) {
	// Given
	setupMediaRepositoryTestDB(t)
	oldScan := time.Now().Add(-73 * time.Hour)
	recentScan := time.Now().Add(-71 * time.Hour)
	works := []model.FilmWork{
		{StorageID: 7, Source: "fc2", Code: "FC2-PPV-1", PrimaryDir: "actor", ImageURL: "https://image.test/1.jpg"},
		{StorageID: 7, Source: "fc2", Code: "FC2-PPV-2", PrimaryDir: "actor", ImageURL: "https://image.test/2.jpg", DMMPosterStatus: model.DMMPosterStatusPending},
		{StorageID: 7, Source: "fc2", Code: "FC2-PPV-3", PrimaryDir: "actor", ImageURL: "https://image.test/3.jpg", DMMPosterStatus: model.DMMPosterStatusTransientError, DMMPosterScanAt: &oldScan},
		{StorageID: 7, Source: "fc2", Code: "FC2-PPV-4", PrimaryDir: "actor", ImageURL: "https://image.test/4.jpg", DMMPosterStatus: model.DMMPosterStatusTransientError, DMMPosterScanAt: &recentScan},
		{StorageID: 7, Source: "fc2", Code: "FC2-PPV-5", PrimaryDir: "actor", ImageURL: "https://image.test/5.jpg", DMMPosterStatus: model.DMMPosterStatusSuccess},
		{StorageID: 8, Source: "fc2", Code: "FC2-PPV-6", PrimaryDir: "actor", ImageURL: "https://image.test/6.jpg"},
		{StorageID: 7, Source: "pornhub", Code: "view-key-7", PrimaryDir: "actor", ImageURL: "https://image.test/7.jpg"},
		{StorageID: 7, Source: "fc2", Code: "FC2-PPV-8", PrimaryDir: "actor"},
	}
	require.NoError(t, db.Create(&works).Error)

	// When
	selected, err := QueryPendingMediaPosterWorks(7, "fc2", 72*time.Hour)

	// Then
	require.NoError(t, err)
	require.Len(t, selected, 3)
	require.Equal(t, []uint{works[0].ID, works[1].ID, works[2].ID}, []uint{selected[0].ID, selected[1].ID, selected[2].ID})
}
