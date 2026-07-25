package fc2

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/cmd/flags"
	"github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/go-resty/resty/v2"
	"gorm.io/gorm"
)

func TestScanMediaSampleImagesUsesFilmWorkAndSortsScreenshots(t *testing.T) {
	resetFC2MediaWorks(t)
	previousDataDir := flags.DataDir
	flags.DataDir = t.TempDir()
	t.Cleanup(func() { flags.DataDir = previousDataDir })

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/link":
			response.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(response).Encode(WhatLinkInfo{Screenshots: []WhatLinkScreenshot{
				{Time: 20, Screenshot: "https://screens.test/frame2.jpg"},
				{Time: 10, Screenshot: "https://screens.test/frame1.jpg"},
			}})
		case "/frame1.jpg":
			_, _ = response.Write([]byte("first"))
		case "/frame2.jpg":
			_, _ = response.Write([]byte("second"))
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	work := createFC2MediaWork(t, "FC2-PPV-123")
	driver := newFC2MediaJobDriver(t, server)
	driver.scanMediaSampleImages()

	stored, err := db.GetFilmWork(work.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.SampleImageCount != 2 || !stored.SampleImageComplete {
		t.Fatalf("sample progress = (%d, %t), want (2, true)", stored.SampleImageCount, stored.SampleImageComplete)
	}
	first, err := virtual_file.MediaFanartPath(fc2MediaIdentity(stored), 1)
	if err != nil {
		t.Fatal(err)
	}
	second, err := virtual_file.MediaFanartPath(fc2MediaIdentity(stored), 2)
	if err != nil {
		t.Fatal(err)
	}
	assertFC2MediaContent(t, first, "first")
	assertFC2MediaContent(t, second, "second")
}

func TestScanMediaSampleImagesCompletesEmptyScreenshotList(t *testing.T) {
	resetFC2MediaWorks(t)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(WhatLinkInfo{})
	}))
	defer server.Close()
	work := createFC2MediaWork(t, "FC2-PPV-124")

	newFC2MediaJobDriver(t, server).scanMediaSampleImages()

	stored, err := db.GetFilmWork(work.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !stored.SampleImageComplete || stored.SampleImageCount != 0 {
		t.Fatalf("empty sample progress = (%d, %t), want (0, true)", stored.SampleImageCount, stored.SampleImageComplete)
	}
}

func TestScanMediaSampleImagesRecordsRetryForWhatLinkFailure(t *testing.T) {
	resetFC2MediaWorks(t)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()
	work := createFC2MediaWork(t, "FC2-PPV-125")

	newFC2MediaJobDriver(t, server).scanMediaSampleImages()

	stored, err := db.GetFilmWork(work.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.SampleImageComplete || stored.SampleImageScanAt == nil {
		t.Fatalf("retry state = (%t, %v), want (false, non-nil)", stored.SampleImageComplete, stored.SampleImageScanAt)
	}
}

func resetFC2MediaWorks(t *testing.T) {
	t.Helper()
	for _, value := range []any{&model.SourceMagnet{}, &model.FilmFile{}, &model.FilmWork{}} {
		if err := db.GetDb().Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(value).Error; err != nil {
			t.Fatalf("reset %T: %v", value, err)
		}
	}
}

func createFC2MediaWork(t *testing.T, code string) model.FilmWork {
	t.Helper()
	work := model.FilmWork{
		StorageID: 71, Source: "fc2", Code: code, SourceRef: code,
		SourceURL: code, PrimaryDir: "actor", ImageURL: "https://images.test/poster.jpg",
	}
	if err := db.GetDb().Create(&work).Error; err != nil {
		t.Fatal(err)
	}
	magnet := model.SourceMagnet{
		WorkID: work.ID, MagnetURI: "magnet:?xt=urn:btih:" + code,
		Fingerprint: "fingerprint-" + code, Selected: true,
	}
	if err := db.GetDb().Create(&magnet).Error; err != nil {
		t.Fatal(err)
	}
	return work
}

type fc2MediaRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn fc2MediaRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func newFC2MediaJobDriver(t *testing.T, server *httptest.Server) *FC2 {
	t.Helper()
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := server.Client().Transport
	client := resty.NewWithClient(&http.Client{Transport: fc2MediaRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		forwarded := request.Clone(request.Context())
		forwardedURL := *request.URL
		forwardedURL.Scheme = target.Scheme
		forwardedURL.Host = target.Host
		forwarded.URL = &forwardedURL
		return transport.RoundTrip(forwarded)
	})})
	return &FC2{Storage: model.Storage{ID: 71}, client: client}
}

func assertFC2MediaContent(t *testing.T, path, want string) {
	t.Helper()
	content, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != want {
		t.Fatalf("content at %s = %q, want %q", path, content, want)
	}
}
