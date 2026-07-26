package pornhub

import (
	"errors"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestScanMediaArtifactsUsesStableIdentityAndReferer(t *testing.T) {
	// Given
	require.NoError(t, db.GetDb().Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&model.FilmWork{}).Error)
	original := cachePornhubMediaImage
	t.Cleanup(func() { cachePornhubMediaImage = original })
	var captured virtual_file.MediaInfo
	cachePornhubMediaImage = func(info virtual_file.MediaInfo) (int, error) {
		captured = info
		return virtual_file.CreatedSuccess, nil
	}
	work := model.FilmWork{
		StorageID: 12, Source: DriverName, Code: "abc123", PrimaryDir: "Actor A",
		RawTitle: "Original title", TranslatedTitle: "Translated title",
		ImageURL: "https://example.test/cover.jpg", Actors: model.StringArray{"Actor A"}, Tags: model.StringArray{"tag"},
	}
	require.NoError(t, db.GetDb().Create(&work).Error)
	driver := Pornhub{Storage: model.Storage{ID: 12}, Addition: Addition{ServerUrl: "https://www.pornhub.com"}}

	// When
	err := driver.scanMediaArtifacts()

	// Then
	require.NoError(t, err)
	require.NotNil(t, captured.Identity)
	require.Equal(t, virtual_file.MediaIdentity{StorageID: 12, Source: DriverName, PrimaryDir: "Actor A", Code: "abc123"}, *captured.Identity)
	require.Equal(t, "abc123 Translated title", captured.Title)
	require.Equal(t, work.ImageURL, captured.ImgUrl)
	require.Equal(t, "https://www.pornhub.com", captured.ImgUrlHeaders["Referer"])
	stored, err := db.GetFilmWork(work.ID)
	require.NoError(t, err)
	require.Equal(t, model.DMMPosterStatusSuccess, stored.DMMPosterStatus)
	require.NotNil(t, stored.DMMPosterScanAt)
}

func TestScanMediaArtifactsClassifiesHTTPNotFoundAsTerminal(t *testing.T) {
	tests := []struct {
		name       string
		cacheErr   error
		wantStatus string
	}{
		{name: "HTTP 404", cacheErr: errors.New("download media poster: HTTP 404"), wantStatus: model.DMMPosterStatusNotFound},
		{name: "HTTP 410", cacheErr: errors.New("download media poster: HTTP 410"), wantStatus: model.DMMPosterStatusNotFound},
		{name: "other error", cacheErr: errors.New("connection refused"), wantStatus: model.DMMPosterStatusTransientError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			require.NoError(t, db.GetDb().Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&model.FilmWork{}).Error)
			original := cachePornhubMediaImage
			t.Cleanup(func() { cachePornhubMediaImage = original })
			cachePornhubMediaImage = func(virtual_file.MediaInfo) (int, error) {
				return virtual_file.CreatedFailed, test.cacheErr
			}
			work := model.FilmWork{
				StorageID: 13, Source: DriverName, Code: "def456", PrimaryDir: "Actor B",
				ImageURL: "https://image.test/poster.jpg",
			}
			require.NoError(t, db.GetDb().Create(&work).Error)
			driver := Pornhub{Storage: model.Storage{ID: 13}, Addition: Addition{ServerUrl: "https://www.pornhub.com"}}

			// When
			err := driver.scanMediaArtifacts()

			// Then
			require.NoError(t, err)
			stored, err := db.GetFilmWork(work.ID)
			require.NoError(t, err)
			require.Equal(t, test.wantStatus, stored.DMMPosterStatus)
			require.NotNil(t, stored.DMMPosterScanAt)
		})
	}
}
