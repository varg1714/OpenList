package javdb

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/cmd/flags"
	"github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"
	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/go-resty/resty/v2"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestMain(m *testing.M) {
	dataDir, err := os.MkdirTemp("", "javdb-test-")
	if err != nil {
		panic(err)
	}
	conf.Conf = conf.DefaultConfig(dataDir)
	testDB, err := gorm.Open(sqlite.Open(filepath.Join(dataDir, "javdb-test.db")), &gorm.Config{})
	if err != nil {
		panic(err)
	}
	db.Init(testDB)

	code := m.Run()
	if sqlDB, sqlErr := testDB.DB(); sqlErr == nil {
		_ = sqlDB.Close()
	}
	_ = os.RemoveAll(dataDir)
	os.Exit(code)
}

func TestJavdbHTTPClientIsUnexportedTestSeam(t *testing.T) {
	field, ok := reflect.TypeOf(Javdb{}).FieldByName("client")
	if !ok {
		t.Fatal("Javdb.client field is missing")
	}
	if field.PkgPath == "" {
		t.Fatal("Javdb.client must be unexported")
	}
}

func TestNewSampleImageClientDoesNotFollowRedirects(t *testing.T) {
	var redirected atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/redirected" {
			redirected.Store(true)
			response.WriteHeader(http.StatusOK)
			return
		}
		http.Redirect(response, request, "/redirected", http.StatusFound)
	}))
	defer server.Close()

	response, err := newSampleImageClient().R().Get(server.URL)
	if err == nil {
		t.Fatal("redirect request error = nil, want redirect refusal")
	}
	if response == nil || response.StatusCode() != http.StatusFound {
		t.Fatalf("redirect response = %v, want status %d", response, http.StatusFound)
	}
	if redirected.Load() {
		t.Fatal("redirect target was requested")
	}
}

func TestNewSampleImageClientEnforcesTLSVerification(t *testing.T) {
	previous := conf.Conf.TlsInsecureSkipVerify
	conf.Conf.TlsInsecureSkipVerify = true
	t.Cleanup(func() {
		conf.Conf.TlsInsecureSkipVerify = previous
	})

	transport, ok := newSampleImageClient().GetClient().Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T, want *http.Transport", newSampleImageClient().GetClient().Transport)
	}
	if transport.TLSClientConfig == nil {
		t.Fatal("TLSClientConfig = nil")
	}
	if transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("InsecureSkipVerify = true, want false")
	}
}

func TestNewSampleImageClientDisablesRetries(t *testing.T) {
	var attempts atomic.Int32
	client := newSampleImageClient().SetTransport(roundTripFunc(func(request *http.Request) (*http.Response, error) {
		attempts.Add(1)
		return nil, errors.New("temporary transport failure")
	}))

	_, _ = client.R().Get("https://img.jdbstatic.com/covers/movie.jpg")
	if got := attempts.Load(); got != 1 {
		t.Fatalf("transport attempts = %d, want 1", got)
	}
}

func TestSampleImageURL(t *testing.T) {
	tests := []struct {
		name    string
		image   string
		index   int
		want    string
		wantErr bool
	}{
		{
			name:  "maps covers segment and preserves URL components",
			image: "https://img.jdbstatic.com/cdn/covers/abc.jpg?token=secret#preview",
			index: 1,
			want:  "https://img.jdbstatic.com/cdn/samples/abc_l_1.jpg?token=secret#preview",
		},
		{name: "accepts exact CDN host", image: "https://jdbstatic.com/covers/abc.jpg", index: 50, want: "https://jdbstatic.com/samples/abc_l_50.jpg"},
		{name: "rejects index zero", image: "https://jdbstatic.com/covers/abc.jpg", index: 0, wantErr: true},
		{name: "rejects index above cap", image: "https://jdbstatic.com/covers/abc.jpg", index: 51, wantErr: true},
		{name: "rejects HTTP", image: "http://jdbstatic.com/covers/abc.jpg", index: 1, wantErr: true},
		{name: "rejects untrusted host", image: "https://example.com/covers/abc.jpg", index: 1, wantErr: true},
		{name: "rejects host suffix trick", image: "https://eviljdbstatic.com/covers/abc.jpg", index: 1, wantErr: true},
		{name: "rejects unsupported path", image: "https://jdbstatic.com/posters/abc.jpg", index: 1, wantErr: true},
		{name: "rejects covers substring", image: "https://jdbstatic.com/mycovers/abc.jpg", index: 1, wantErr: true},
		{name: "rejects unsupported extension", image: "https://jdbstatic.com/covers/abc.png", index: 1, wantErr: true},
		{name: "rejects escaped slash", image: "https://jdbstatic.com/covers%2Fnested/abc.jpg?token=secret", index: 1, wantErr: true},
		{name: "rejects escaped path segment", image: "https://jdbstatic.com/%63overs/abc.jpg", index: 1, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := sampleImageURL(test.image, test.index)
			if test.wantErr {
				if err == nil {
					t.Fatalf("sampleImageURL(%q, %d) error = nil, want error", test.image, test.index)
				}
				return
			}
			if err != nil {
				t.Fatalf("sampleImageURL(%q, %d): %v", test.image, test.index, err)
			}
			if got != test.want {
				t.Errorf("sampleImageURL(%q, %d) = %q, want %q", test.image, test.index, got, test.want)
			}
		})
	}
}

func TestScanFilmSampleImagesDownloadsUntil403(t *testing.T) {
	setupJavdbSampleImageTest(t)

	var requests []string
	var requestsMu sync.Mutex
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requestsMu.Lock()
		requests = append(requests, request.URL.Path)
		requestsMu.Unlock()
		if request.Referer() != serverFilmURL(server.URL, "movie") {
			t.Errorf("Referer = %q, want film URL", request.Referer())
		}
		if request.URL.Path == "/samples/movie_l_1.jpg" {
			_, _ = response.Write([]byte("first image"))
			return
		}
		response.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	film := createSampleImageFilm(t, "https://img.jdbstatic.com", "movie", 0, time.Time{})
	film.Url = server.URL + "/movies/movie"
	if err := db.GetDb().Model(&film).Update("url", film.Url).Error; err != nil {
		t.Fatalf("update film URL: %v", err)
	}

	newSampleImageDriver(server).scanFilmSampleImages(context.Background(), &film)

	stored := loadSampleImageFilm(t, film.ID)
	if stored.SampleImageCount != 1 || !stored.SampleImageComplete {
		t.Errorf("progress = (%d, %t), want (1, true)", stored.SampleImageCount, stored.SampleImageComplete)
	}
	wantRequests := []string{"/samples/movie_l_1.jpg", "/samples/movie_l_2.jpg"}
	if !reflect.DeepEqual(requests, wantRequests) {
		t.Errorf("requests = %v, want %v", requests, wantRequests)
	}
	content, err := os.ReadFile(sampleImagePath(film, 1))
	if err != nil {
		t.Fatalf("read fanart1.jpg: %v", err)
	}
	if string(content) != "first image" {
		t.Errorf("fanart1.jpg = %q, want first image", content)
	}
}

func TestScanFilmSampleImagesStopsForTransientError(t *testing.T) {
	setupJavdbSampleImageTest(t)

	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requestCount.Add(1)
		response.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	film := createSampleImageFilm(t, "https://img.jdbstatic.com", "transient", 0, time.Time{})
	started := time.Now()
	newSampleImageDriver(server).scanFilmSampleImages(context.Background(), &film)

	stored := loadSampleImageFilm(t, film.ID)
	if stored.SampleImageCount != 0 || stored.SampleImageComplete {
		t.Errorf("progress = (%d, %t), want (0, false)", stored.SampleImageCount, stored.SampleImageComplete)
	}
	if stored.SampleImageScanAt.Before(started) {
		t.Errorf("scan time = %s, want at or after %s", stored.SampleImageScanAt, started)
	}
	if requestCount.Load() != 1 {
		t.Errorf("request count = %d, want 1", requestCount.Load())
	}
}

func TestScanFilmSampleImagesRecoversExistingFile(t *testing.T) {
	setupJavdbSampleImageTest(t)

	film := createSampleImageFilm(t, "https://img.jdbstatic.com", "existing", 0, time.Time{})
	if err := os.MkdirAll(filepath.Dir(sampleImagePath(film, 1)), 0o755); err != nil {
		t.Fatalf("create film directory: %v", err)
	}
	if err := os.WriteFile(sampleImagePath(film, 1), []byte("cached"), 0o644); err != nil {
		t.Fatalf("create fanart1.jpg: %v", err)
	}

	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		paths = append(paths, request.URL.Path)
		response.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	film.Image = "https://img.jdbstatic.com/covers/existing.jpg"

	newSampleImageDriver(server).scanFilmSampleImages(context.Background(), &film)

	stored := loadSampleImageFilm(t, film.ID)
	if stored.SampleImageCount != 1 || !stored.SampleImageComplete {
		t.Errorf("progress = (%d, %t), want (1, true)", stored.SampleImageCount, stored.SampleImageComplete)
	}
	if want := []string{"/samples/existing_l_2.jpg"}; !reflect.DeepEqual(paths, want) {
		t.Errorf("requests = %v, want %v", paths, want)
	}
}

func TestScanFilmSampleImagesResumesPersistedCount(t *testing.T) {
	setupJavdbSampleImageTest(t)

	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		paths = append(paths, request.URL.Path)
		response.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	film := createSampleImageFilm(t, "https://img.jdbstatic.com", "resume", 2, time.Time{})
	newSampleImageDriver(server).scanFilmSampleImages(context.Background(), &film)

	stored := loadSampleImageFilm(t, film.ID)
	if stored.SampleImageCount != 2 || !stored.SampleImageComplete {
		t.Errorf("progress = (%d, %t), want (2, true)", stored.SampleImageCount, stored.SampleImageComplete)
	}
	if want := []string{"/samples/resume_l_3.jpg"}; !reflect.DeepEqual(paths, want) {
		t.Errorf("requests = %v, want %v", paths, want)
	}
}

func TestScanFilmSampleImagesUsesSequentialFanartNames(t *testing.T) {
	setupJavdbSampleImageTest(t)

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/samples/names_l_1.jpg":
			_, _ = response.Write([]byte("one"))
		case "/samples/names_l_2.jpg":
			_, _ = response.Write([]byte("two"))
		default:
			response.WriteHeader(http.StatusForbidden)
		}
	}))
	defer server.Close()

	film := createSampleImageFilm(t, "https://img.jdbstatic.com", "names", 0, time.Time{})
	newSampleImageDriver(server).scanFilmSampleImages(context.Background(), &film)

	for index, want := range []string{"one", "two"} {
		content, err := os.ReadFile(sampleImagePath(film, index+1))
		if err != nil {
			t.Fatalf("read fanart%d.jpg: %v", index+1, err)
		}
		if string(content) != want {
			t.Errorf("fanart%d.jpg = %q, want %q", index+1, content, want)
		}
	}
}

func TestScanFilmSampleImagesCompletesAtCap(t *testing.T) {
	setupJavdbSampleImageTest(t)

	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requestCount.Add(1)
		_, _ = response.Write([]byte("fifty"))
	}))
	defer server.Close()

	film := createSampleImageFilm(t, "https://img.jdbstatic.com", "cap", 49, time.Time{})
	newSampleImageDriver(server).scanFilmSampleImages(context.Background(), &film)

	stored := loadSampleImageFilm(t, film.ID)
	if stored.SampleImageCount != 50 || !stored.SampleImageComplete {
		t.Errorf("progress = (%d, %t), want (50, true)", stored.SampleImageCount, stored.SampleImageComplete)
	}
	if requestCount.Load() != 1 {
		t.Errorf("request count = %d, want 1", requestCount.Load())
	}
}

func TestScanFilmSampleImagesRejectsUnsafePathComponents(t *testing.T) {
	tests := []struct {
		name     string
		actor    string
		filmName string
	}{
		{name: "empty actor", actor: "", filmName: "movie.mp4"},
		{name: "absolute actor", actor: filepath.Join(string(filepath.Separator), "outside"), filmName: "movie.mp4"},
		{name: "dot actor", actor: ".", filmName: "movie.mp4"},
		{name: "parent actor", actor: "..", filmName: "movie.mp4"},
		{name: "slash actor", actor: "nested/actor", filmName: "movie.mp4"},
		{name: "backslash actor", actor: `nested\actor`, filmName: "movie.mp4"},
		{name: "empty derived film directory", actor: "actor", filmName: ""},
		{name: "parent film directory", actor: "actor", filmName: "../outside.mp4"},
		{name: "backslash film directory", actor: "actor", filmName: `..\outside.mp4`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setupJavdbSampleImageTest(t)
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				requests.Add(1)
				response.WriteHeader(http.StatusForbidden)
			}))
			defer server.Close()

			film := createSampleImageFilm(t, "https://img.jdbstatic.com", "safe", 0, time.Time{})
			film.Actor = test.actor
			film.Name = test.filmName
			newSampleImageDriver(server).scanFilmSampleImages(context.Background(), &film)

			if got := requests.Load(); got != 0 {
				t.Errorf("HTTP requests = %d, want 0", got)
			}
		})
	}
}

func TestScanFilmSampleImagesRejectsSymlinkedActorDirectory(t *testing.T) {
	setupJavdbSampleImageTest(t)

	root := filepath.Join(flags.DataDir, "emby", DriverName)
	outside := t.TempDir()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("create cache root: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "actor")); err != nil {
		t.Fatalf("create actor symlink: %v", err)
	}

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		_, _ = response.Write([]byte("image"))
	}))
	defer server.Close()

	film := createSampleImageFilm(t, "https://img.jdbstatic.com", "symlink", 0, time.Time{})
	newSampleImageDriver(server).scanFilmSampleImages(context.Background(), &film)

	if got := requests.Load(); got != 0 {
		t.Fatalf("HTTP requests = %d, want 0", got)
	}
	outsideFile := filepath.Join(outside, "symlink", "fanart1.jpg")
	if _, err := os.Stat(outsideFile); !os.IsNotExist(err) {
		t.Fatalf("outside file exists: %v", err)
	}
}

func TestScanSampleImagesEnforcesTotalRequestBudgetWithoutDelayingPartialFilm(t *testing.T) {
	setupJavdbSampleImageTest(t)

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		_, _ = response.Write([]byte("image"))
	}))
	defer server.Close()

	now := time.Now()
	first := createSampleImageFilm(t, "https://img.jdbstatic.com", "budget-first", 25, time.Time{})
	second := createSampleImageFilm(t, "https://img.jdbstatic.com", "budget-second", 0, time.Time{})
	partial := createSampleImageFilm(t, "https://img.jdbstatic.com", "budget-partial", 0, time.Time{})
	for index, film := range []*model.Film{&first, &second, &partial} {
		film.Date = now.Add(-time.Duration(index) * time.Hour)
		if err := db.GetDb().Model(film).Update("date", film.Date).Error; err != nil {
			t.Fatalf("update film date: %v", err)
		}
	}

	newSampleImageDriver(server).scanSampleImages()

	if got := requests.Load(); got != 100 {
		t.Fatalf("HTTP requests = %d, want 100", got)
	}
	first = loadSampleImageFilm(t, first.ID)
	second = loadSampleImageFilm(t, second.ID)
	partial = loadSampleImageFilm(t, partial.ID)
	if first.SampleImageCount != 50 || !first.SampleImageComplete {
		t.Errorf("first progress = (%d, %t), want (50, true)", first.SampleImageCount, first.SampleImageComplete)
	}
	if second.SampleImageCount != 50 || !second.SampleImageComplete {
		t.Errorf("second progress = (%d, %t), want (50, true)", second.SampleImageCount, second.SampleImageComplete)
	}
	if partial.SampleImageCount != 25 || partial.SampleImageComplete {
		t.Errorf("partial progress = (%d, %t), want (25, false)", partial.SampleImageCount, partial.SampleImageComplete)
	}
	if !partial.SampleImageScanAt.IsZero() {
		t.Errorf("partial scan time = %s, want zero", partial.SampleImageScanAt)
	}
	eligible, err := db.QuerySampleImageFilms(DriverName, 72*time.Hour, 20)
	if err != nil {
		t.Fatalf("query immediately eligible films: %v", err)
	}
	if len(eligible) != 1 || eligible[0].ID != partial.ID {
		t.Errorf("eligible films = %+v, want only film %d", eligible, partial.ID)
	}
}

func TestScanSampleImagesUsesBoundedStaleBatch(t *testing.T) {
	setupJavdbSampleImageTest(t)

	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requestCount.Add(1)
		response.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	now := time.Now()
	for index := 0; index < 21; index++ {
		createSampleImageFilm(t, "https://img.jdbstatic.com", fmt.Sprintf("batch-%02d", index), 0, time.Time{})
	}
	fresh := createSampleImageFilm(t, "https://img.jdbstatic.com", "fresh", 0, now.Add(-71*time.Hour))

	newSampleImageDriver(server).scanSampleImages()

	var films []model.Film
	if err := db.GetDb().Order("id").Find(&films).Error; err != nil {
		t.Fatalf("load films: %v", err)
	}
	completeCount := 0
	for _, film := range films {
		if film.SampleImageComplete {
			completeCount++
		}
	}
	if completeCount != 20 {
		t.Errorf("complete film count = %d, want 20", completeCount)
	}
	if requestCount.Load() != 20 {
		t.Errorf("request count = %d, want 20", requestCount.Load())
	}
	if loadSampleImageFilm(t, fresh.ID).SampleImageComplete {
		t.Error("film scanned within 72 hours was processed")
	}
}

func setupJavdbSampleImageTest(t *testing.T) {
	t.Helper()

	dataDir := t.TempDir()
	previousDataDir := flags.DataDir
	previousConf := conf.Conf
	flags.DataDir = dataDir
	conf.Conf = conf.DefaultConfig(dataDir)

	if err := db.GetDb().Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&model.Film{}).Error; err != nil {
		t.Fatalf("reset films: %v", err)
	}

	t.Cleanup(func() {
		flags.DataDir = previousDataDir
		conf.Conf = previousConf
	})
}

func createSampleImageFilm(t *testing.T, serverURL, name string, count int, scanAt time.Time) model.Film {
	t.Helper()

	film := model.Film{
		Url:               serverFilmURL(serverURL, name),
		Name:              name + ".mp4",
		Image:             serverURL + "/covers/" + name + ".jpg",
		Source:            DriverName,
		Actor:             "actor",
		Date:              time.Now(),
		SampleImageCount:  count,
		SampleImageScanAt: scanAt,
	}
	if err := db.GetDb().Create(&film).Error; err != nil {
		t.Fatalf("create film: %v", err)
	}
	return film
}

func loadSampleImageFilm(t *testing.T, filmID uint) model.Film {
	t.Helper()

	var film model.Film
	if err := db.GetDb().First(&film, filmID).Error; err != nil {
		t.Fatalf("load film: %v", err)
	}
	return film
}

func sampleImagePath(film model.Film, index int) string {
	filmDir := virtual_file.GetRealName(virtual_file.AppendImageName(film.Name))
	return filepath.Join(flags.DataDir, "emby", DriverName, film.Actor, filmDir, fmt.Sprintf("fanart%d.jpg", index))
}

func serverFilmURL(serverURL, name string) string {
	return serverURL + "/movies/" + name
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func newSampleImageDriver(server *httptest.Server) *Javdb {
	target, err := url.Parse(server.URL)
	if err != nil {
		panic(err)
	}
	transport := server.Client().Transport
	client := resty.NewWithClient(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		forwarded := request.Clone(request.Context())
		forwardedURL := *request.URL
		forwardedURL.Scheme = target.Scheme
		forwardedURL.Host = target.Host
		forwarded.URL = &forwardedURL
		return transport.RoundTrip(forwarded)
	})})
	return &Javdb{client: client}
}
