package fc2

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/cmd/flags"
	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/go-resty/resty/v2"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestScanMediaArtifactsDownloadsPosterOnceThenSkipsSuccessfulWork(t *testing.T) {
	// Given
	resetFC2PosterWorks(t)
	oldDataDir := flags.DataDir
	oldClient := base.RestyClient
	oldCache := cacheFC2MediaImage
	flags.DataDir = t.TempDir()
	base.RestyClient = resty.New()
	cacheFC2MediaImage = virtual_file.CacheImageWithError
	t.Cleanup(func() {
		flags.DataDir = oldDataDir
		base.RestyClient = oldClient
		cacheFC2MediaImage = oldCache
	})
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = response.Write([]byte("downloaded poster"))
	}))
	t.Cleanup(server.Close)
	work := model.FilmWork{
		StorageID: 90, Source: "fc2", Code: "FC2-PPV-990", PrimaryDir: "actor", ImageURL: server.URL + "/poster.jpg",
	}
	require.NoError(t, db.GetDb().Create(&work).Error)
	driver := &FC2{Storage: model.Storage{ID: work.StorageID}}

	// When
	require.NoError(t, driver.scanMediaArtifacts())
	require.NoError(t, driver.scanMediaArtifacts())

	// Then
	require.Equal(t, int32(1), requests.Load())
	stored, err := db.GetFilmWork(work.ID)
	require.NoError(t, err)
	require.Equal(t, model.DMMPosterStatusSuccess, stored.DMMPosterStatus)
	require.NotNil(t, stored.DMMPosterScanAt)
	paths, err := virtual_file.ResolveMediaArtifactPaths(virtual_file.MediaIdentity{
		StorageID: work.StorageID, Source: work.Source, PrimaryDir: work.PrimaryDir, Code: work.Code,
	})
	require.NoError(t, err)
	content, err := os.ReadFile(paths.Poster)
	require.NoError(t, err)
	require.Equal(t, "downloaded poster", string(content))
}

func TestScanMediaArtifactsMarksExistingPosterSuccessful(t *testing.T) {
	// Given
	resetFC2PosterWorks(t)
	oldDataDir := flags.DataDir
	flags.DataDir = t.TempDir()
	t.Cleanup(func() { flags.DataDir = oldDataDir })
	work := model.FilmWork{
		StorageID: 91, Source: "fc2", Code: "FC2-PPV-991", PrimaryDir: "actor",
		ImageURL: "http://127.0.0.1:1/must-not-be-requested.jpg",
	}
	require.NoError(t, db.GetDb().Create(&work).Error)
	paths, err := virtual_file.ResolveMediaArtifactPaths(virtual_file.MediaIdentity{
		StorageID: work.StorageID, Source: work.Source, PrimaryDir: work.PrimaryDir, Code: work.Code,
	})
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(paths.Poster), 0o755))
	require.NoError(t, os.WriteFile(paths.Poster, []byte("existing poster"), 0o644))

	// When
	err = (&FC2{Storage: model.Storage{ID: work.StorageID}}).scanMediaArtifacts()

	// Then
	require.NoError(t, err)
	stored, err := db.GetFilmWork(work.ID)
	require.NoError(t, err)
	require.Equal(t, model.DMMPosterStatusSuccess, stored.DMMPosterStatus)
	require.NotNil(t, stored.DMMPosterScanAt)
}

func TestScanMediaArtifactsMarksDownloadFailureTransient(t *testing.T) {
	// Given
	resetFC2PosterWorks(t)
	work := model.FilmWork{
		StorageID: 92, Source: "fc2", Code: "FC2-PPV-992", PrimaryDir: "actor",
		ImageURL: "https://image.test/failure.jpg",
	}
	require.NoError(t, db.GetDb().Create(&work).Error)
	oldCache := cacheFC2MediaImage
	cacheFC2MediaImage = func(virtual_file.MediaInfo) (int, error) {
		return virtual_file.CreatedFailed, errors.New("poster upstream unavailable")
	}
	t.Cleanup(func() { cacheFC2MediaImage = oldCache })

	// When
	err := (&FC2{Storage: model.Storage{ID: work.StorageID}}).scanMediaArtifacts()

	// Then
	require.NoError(t, err)
	stored, err := db.GetFilmWork(work.ID)
	require.NoError(t, err)
	require.Equal(t, model.DMMPosterStatusTransientError, stored.DMMPosterStatus)
	require.Equal(t, uint(1), stored.DMMPosterRetryCount)
	require.NotNil(t, stored.DMMPosterScanAt)
}

func TestScanMediaArtifactsSkipsRetryExhaustedWork(t *testing.T) {
	// Given
	resetFC2PosterWorks(t)
	oldDataDir := flags.DataDir
	flags.DataDir = t.TempDir()
	t.Cleanup(func() { flags.DataDir = oldDataDir })
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		t.Error("request must not be issued for retry-exhausted work")
	}))
	t.Cleanup(server.Close)
	oldScan := time.Now().Add(-73 * time.Hour)
	work := model.FilmWork{
		StorageID: 93, Source: "fc2", Code: "FC2-PPV-993", PrimaryDir: "actor",
		ImageURL:            server.URL + "/poster.jpg",
		DMMPosterStatus:     model.DMMPosterStatusTransientError,
		DMMPosterScanAt:     &oldScan,
		DMMPosterRetryCount: 3,
	}
	require.NoError(t, db.GetDb().Create(&work).Error)

	// When
	require.NoError(t, (&FC2{Storage: model.Storage{ID: work.StorageID}}).scanMediaArtifacts())

	// Then
	stored, err := db.GetFilmWork(work.ID)
	require.NoError(t, err)
	require.Equal(t, uint(3), stored.DMMPosterRetryCount)
	require.NotNil(t, stored.DMMPosterScanAt)
}

func resetFC2PosterWorks(t *testing.T) {
	t.Helper()
	require.NoError(t, db.GetDb().Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&model.FilmWork{}).Error)
}
