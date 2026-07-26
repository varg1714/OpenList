package javdb

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/cmd/flags"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/go-resty/resty/v2"
	"github.com/stretchr/testify/require"
)

func TestScanMediaDMMPosterMarksNoImageFallbackAsNotFound(t *testing.T) {
	// Given
	resetJavdbMediaWorks(t)
	previousDataDir := flags.DataDir
	flags.DataDir = t.TempDir()
	t.Cleanup(func() { flags.DataDir = previousDataDir })

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case strings.HasPrefix(request.URL.Path, "/pics_dig/digital/video/"):
			response.WriteHeader(http.StatusNotFound)
		case strings.HasPrefix(request.URL.Path, "/mono/movie/adult/"):
			http.Redirect(response, request, "https://pics.dmm.com/mono/noimage/movie/adult_pl.jpg", http.StatusFound)
		case strings.HasPrefix(request.URL.Path, "/search/=/searchstr="):
			response.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected request %s", request.URL.Path)
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	driver := newMediaJobDriver(t, server)
	driver.client.SetRedirectPolicy(resty.NoRedirectPolicy())
	work := model.FilmWork{
		StorageID: 91, Source: DriverName, Code: "SUKE-089", SourceRef: "suke-089",
		SourceURL: "https://javdb.test/v/suke-089", PrimaryDir: "Actor A",
	}
	if err := db.GetDb().Create(&work).Error; err != nil {
		t.Fatal(err)
	}

	// When
	driver.scanMediaDMMPosters()

	// Then
	stored, err := db.GetFilmWork(work.ID)
	require.NoError(t, err)
	require.Equal(t, model.DMMPosterStatusNotFound, stored.DMMPosterStatus)
	require.NotNil(t, stored.DMMPosterScanAt)
}
