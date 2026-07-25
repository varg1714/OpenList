package javdb

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/cmd/flags"
	"github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/open_ai"
	"github.com/go-resty/resty/v2"
	"gorm.io/gorm"
)

func TestScanMediaDMMPosterPersistsNormalizedDecision(t *testing.T) {
	tests := []struct {
		name       string
		statuses   []int
		wantStatus string
		wantPoster bool
	}{
		{name: "first candidate success", statuses: []int{http.StatusOK}, wantStatus: model.DMMPosterStatusSuccess, wantPoster: true},
		{name: "all candidates not found", statuses: []int{http.StatusNotFound, http.StatusGone}, wantStatus: model.DMMPosterStatusNotFound},
		{name: "mixed not found and transient", statuses: []int{http.StatusNotFound, http.StatusTooManyRequests}, wantStatus: model.DMMPosterStatusTransientError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resetJavdbMediaWorks(t)
			previousDataDir := flags.DataDir
			flags.DataDir = t.TempDir()
			t.Cleanup(func() { flags.DataDir = previousDataDir })

			validImage := mediaJobJPEGBytes(t, 4, 3, color.RGBA{R: 220, A: 255})
			requestCount := 0
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				index := requestCount
				requestCount++
				if index >= len(test.statuses) {
					switch {
					case strings.HasPrefix(request.URL.Path, "/mono/movie/adult/"):
						response.WriteHeader(http.StatusNotFound)
					case strings.HasPrefix(request.URL.Path, "/search/=/searchstr="):
						response.WriteHeader(http.StatusOK)
					default:
						t.Errorf("unexpected request %s", request.URL.Path)
						response.WriteHeader(http.StatusNotFound)
					}
					return
				}
				response.WriteHeader(test.statuses[index])
				if test.statuses[index] == http.StatusOK {
					_, _ = response.Write(validImage)
				}
			}))
			defer server.Close()

			work := model.FilmWork{
				StorageID: 91, Source: DriverName, Code: "MIDV-169", SourceRef: "midv-169",
				SourceURL: "https://javdb.test/v/midv-169", PrimaryDir: "Actor A",
			}
			if err := db.GetDb().Create(&work).Error; err != nil {
				t.Fatal(err)
			}
			paths, err := virtual_file.ResolveMediaArtifactPaths(mediaIdentity(work))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(paths.Root, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(paths.LegacyPoster, []byte("old poster"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Base(paths.LegacyPoster), paths.Background); err != nil {
				t.Fatal(err)
			}

			newMediaJobDriver(t, server).scanMediaDMMPosters()

			stored, err := db.GetFilmWork(work.ID)
			if err != nil {
				t.Fatal(err)
			}
			if stored.DMMPosterStatus != test.wantStatus || stored.DMMPosterScanAt == nil {
				t.Fatalf("stored DMM state = (%q, %v), want (%q, non-nil)", stored.DMMPosterStatus, stored.DMMPosterScanAt, test.wantStatus)
			}
			if test.wantPoster {
				if _, err := os.Lstat(paths.Poster); err != nil {
					t.Fatalf("poster not published: %v", err)
				}
				if _, err := os.Lstat(paths.LegacyPoster); !os.IsNotExist(err) {
					t.Fatalf("legacy poster retained: %v", err)
				}
				return
			}
			if _, err := os.Lstat(paths.Poster); !os.IsNotExist(err) {
				t.Fatalf("poster created for failed scan: %v", err)
			}
		})
	}
}

func TestScanMediaSampleImagesPromotesLandscapeBeforeCompletion(t *testing.T) {
	resetJavdbMediaWorks(t)
	previousDataDir := flags.DataDir
	flags.DataDir = t.TempDir()
	t.Cleanup(func() { flags.DataDir = previousDataDir })

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case strings.HasSuffix(request.URL.Path, "_l_1.jpg"):
			writeMediaJobJPEG(t, response, 2, 3, color.RGBA{R: 220, A: 255})
		case strings.HasSuffix(request.URL.Path, "_l_2.jpg"):
			writeMediaJobJPEG(t, response, 4, 2, color.RGBA{B: 220, A: 255})
		default:
			response.WriteHeader(http.StatusForbidden)
		}
	}))
	defer server.Close()

	work := model.FilmWork{
		StorageID: 91, Source: DriverName, Code: "MIDV-169", SourceRef: "midv-169",
		SourceURL: "https://javdb.test/v/midv-169", PrimaryDir: "Actor A",
		ImageURL: "https://jdbstatic.com/covers/midv-169.jpg",
	}
	if err := db.GetDb().Create(&work).Error; err != nil {
		t.Fatal(err)
	}

	newMediaJobDriver(t, server).scanMediaSampleImages()

	stored, err := db.GetFilmWork(work.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.SampleImageCount != 2 || !stored.SampleImageComplete {
		t.Fatalf("sample progress = (%d, %t), want (2, true)", stored.SampleImageCount, stored.SampleImageComplete)
	}
	first, err := virtual_file.MediaFanartPath(mediaIdentity(stored), 1)
	if err != nil {
		t.Fatal(err)
	}
	second, err := virtual_file.MediaFanartPath(mediaIdentity(stored), 2)
	if err != nil {
		t.Fatal(err)
	}
	assertMediaJobDimensions(t, first, 4, 2)
	assertMediaJobDimensions(t, second, 2, 3)
}

func TestScanMediaSampleImagesRetriesWhenPromotionRecoveryFails(t *testing.T) {
	resetJavdbMediaWorks(t)
	previousDataDir := flags.DataDir
	flags.DataDir = t.TempDir()
	t.Cleanup(func() { flags.DataDir = previousDataDir })

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	work := model.FilmWork{
		StorageID: 91, Source: DriverName, Code: "MIDV-170", SourceRef: "midv-170",
		SourceURL: "https://javdb.test/v/midv-170", PrimaryDir: "Actor A",
		ImageURL: "https://jdbstatic.com/covers/midv-170.jpg", SampleImageCount: 2,
	}
	if err := db.GetDb().Create(&work).Error; err != nil {
		t.Fatal(err)
	}
	first, err := virtual_file.MediaFanartPath(mediaIdentity(work), 1)
	if err != nil {
		t.Fatal(err)
	}
	second, err := virtual_file.MediaFanartPath(mediaIdentity(work), 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(first), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(first, []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("second"), 0o644); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(filepath.Dir(first), ".fanart1.jpg-fanart2.jpg.swap-old")
	if err := os.Mkdir(marker, 0o755); err != nil {
		t.Fatal(err)
	}

	newMediaJobDriver(t, server).scanMediaSampleImages()

	stored, err := db.GetFilmWork(work.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.SampleImageComplete {
		t.Fatal("sample work completed despite failed promotion recovery")
	}
	if stored.SampleImageCount != 2 || stored.SampleImageScanAt == nil {
		t.Fatalf("sample retry state = (%d, %v), want (2, non-nil)", stored.SampleImageCount, stored.SampleImageScanAt)
	}
}

func resetJavdbMediaWorks(t *testing.T) {
	t.Helper()
	for _, value := range []any{&model.SourceMagnet{}, &model.FilmFile{}, &model.FilmWork{}} {
		if err := db.GetDb().Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(value).Error; err != nil {
			t.Fatalf("reset %T: %v", value, err)
		}
	}
}

type mediaJobRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn mediaJobRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func newMediaJobDriver(t *testing.T, server *httptest.Server) *Javdb {
	t.Helper()
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := server.Client().Transport
	client := resty.NewWithClient(&http.Client{Transport: mediaJobRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		forwarded := request.Clone(request.Context())
		forwardedURL := *request.URL
		forwardedURL.Scheme = target.Scheme
		forwardedURL.Host = target.Host
		forwarded.URL = &forwardedURL
		return transport.RoundTrip(forwarded)
	})})
	return &Javdb{Storage: model.Storage{ID: 91}, client: client}
}

func writeMediaJobJPEG(t *testing.T, response http.ResponseWriter, width, height int, fill color.RGBA) {
	t.Helper()
	encodeMediaJobJPEG(t, response, width, height, fill)
}

func encodeMediaJobJPEG(t *testing.T, destination io.Writer, width, height int, fill color.RGBA) {
	t.Helper()
	frame := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			frame.SetRGBA(x, y, fill)
		}
	}
	if err := jpeg.Encode(destination, frame, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
}

func mediaJobJPEGBytes(t *testing.T, width, height int, fill color.RGBA) []byte {
	t.Helper()
	var content bytes.Buffer
	encodeMediaJobJPEG(t, &content, width, height, fill)
	return content.Bytes()
}

func assertMediaJobDimensions(t *testing.T, path string, wantWidth, wantHeight int) {
	t.Helper()
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	config, _, err := image.DecodeConfig(file)
	if err != nil {
		t.Fatal(err)
	}
	if config.Width != wantWidth || config.Height != wantHeight {
		t.Fatalf("dimensions at %s = %dx%d, want %dx%d", path, config.Width, config.Height, wantWidth, wantHeight)
	}
}

func TestPersistMediaSynopsesBatchesAllThroughSingleTranslateCall(t *testing.T) {
	resetJavdbMediaWorks(t)
	works := createMediaWorksForSynopsis(t, "airav-code", "dmm-code")

	var translateArgs []open_ai.TranslateItem
	original := batchSynopsisMediaTranslate
	t.Cleanup(func() { batchSynopsisMediaTranslate = original })
	batchSynopsisMediaTranslate = func(items []open_ai.TranslateItem) []string {
		translateArgs = items
		return []string{"airav-translated", "dmm-translated"}
	}

	persistMediaSynopses([]mediaSynopsisCandidate{
		{workID: works[0].ID, code: works[0].Code, origin: "airav-raw"},
		{workID: works[1].ID, code: works[1].Code, origin: "dmm-raw"},
	})

	if len(translateArgs) != 2 {
		t.Fatalf("BatchTranslate items = %d, want 2", len(translateArgs))
	}
	if translateArgs[0].Origin != "airav-raw" || translateArgs[1].Origin != "dmm-raw" {
		t.Fatalf("BatchTranslate origins = %q, %q", translateArgs[0].Origin, translateArgs[1].Origin)
	}
	first, err := db.GetFilmWork(works[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if first.Synopsis != "airav-translated" {
		t.Fatalf("first synopsis = %q, want airav-translated", first.Synopsis)
	}
	second, err := db.GetFilmWork(works[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	if second.Synopsis != "dmm-translated" {
		t.Fatalf("second synopsis = %q, want dmm-translated", second.Synopsis)
	}
}

func TestPersistMediaSynopsesRetriesWhenTranslationEmpty(t *testing.T) {
	resetJavdbMediaWorks(t)
	work := createMediaWorksForSynopsis(t, "empty-translation")[0]

	original := batchSynopsisMediaTranslate
	t.Cleanup(func() { batchSynopsisMediaTranslate = original })
	batchSynopsisMediaTranslate = func(items []open_ai.TranslateItem) []string {
		return []string{""}
	}

	persistMediaSynopses([]mediaSynopsisCandidate{
		{workID: work.ID, code: work.Code, origin: "raw-synopsis"},
	})

	stored, err := db.GetFilmWork(work.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Synopsis != "" {
		t.Fatalf("synopsis = %q, want empty for retry", stored.Synopsis)
	}
	if stored.SynopsisScanAt == nil || stored.SynopsisNextRetryAt == nil {
		t.Fatalf("retry state = (scan=%v, retry=%v), want both non-nil", stored.SynopsisScanAt, stored.SynopsisNextRetryAt)
	}
	if stored.SynopsisLastError != "translation returned an empty result" {
		t.Fatalf("last error = %q, want translation returned an empty result", stored.SynopsisLastError)
	}
}

func TestPersistMediaSynopsesSkipsWhenNoCollected(t *testing.T) {
	resetJavdbMediaWorks(t)

	called := false
	original := batchSynopsisMediaTranslate
	t.Cleanup(func() { batchSynopsisMediaTranslate = original })
	batchSynopsisMediaTranslate = func([]open_ai.TranslateItem) []string {
		called = true
		return nil
	}

	persistMediaSynopses(nil)
	if called {
		t.Fatal("BatchTranslate called with no collected synopses")
	}
}

func createMediaWorksForSynopsis(t *testing.T, codes ...string) []model.FilmWork {
	t.Helper()
	works := make([]model.FilmWork, len(codes))
	for i, code := range codes {
		works[i] = model.FilmWork{
			StorageID: 91, Source: DriverName, Code: code,
			SourceRef: code, SourceURL: code, PrimaryDir: "Actor A",
		}
		if err := db.GetDb().Create(&works[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	return works
}
