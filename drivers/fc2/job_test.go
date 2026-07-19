package fc2

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
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
	dataDir, err := os.MkdirTemp("", "fc2-test-")
	if err != nil {
		panic(err)
	}
	conf.Conf = conf.DefaultConfig(dataDir)
	testDB, err := gorm.Open(sqlite.Open(filepath.Join(dataDir, "fc2-test.db")), &gorm.Config{})
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

func TestFC2HTTPClientIsUnexportedTestSeam(t *testing.T) {
	field, ok := reflect.TypeOf(FC2{}).FieldByName("client")
	if !ok {
		t.Fatal("FC2.client field is missing")
	}
	if field.PkgPath == "" {
		t.Fatal("FC2.client must be unexported")
	}
}

func TestFC2HTTPClientDisablesRetries(t *testing.T) {
	if got := newFC2HTTPClient().RetryCount; got != 0 {
		t.Fatalf("retry count = %d, want 0", got)
	}
}

func TestFC2HTTPClientDoesNotFollowRedirects(t *testing.T) {
	redirectedRequests := 0
	target := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		redirectedRequests++
		response.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(target.Close)
	redirect := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, target.URL, http.StatusFound)
	}))
	t.Cleanup(redirect.Close)

	response, err := newFC2HTTPClient().R().Get(redirect.URL)
	if err == nil {
		t.Fatal("redirect request error = nil")
	}
	if response == nil || response.StatusCode() != http.StatusFound {
		t.Fatalf("redirect status = %v, want %d", response, http.StatusFound)
	}
	if redirectedRequests != 0 {
		t.Fatalf("redirected requests = %d, want 0", redirectedRequests)
	}
}

func TestGetWhatLinkInfoChecksResponseFailures(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		body      string
		transport error
	}{
		{name: "transport", transport: errors.New("offline")},
		{name: "status", status: http.StatusBadGateway, body: `{}`},
		{name: "decode", status: http.StatusOK, body: `{`},
		{name: "api error", status: http.StatusOK, body: `{"error":"not found"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			driver := &FC2{client: resty.New().SetTransport(roundTripFunc(func(request *http.Request) (*http.Response, error) {
				if test.transport != nil {
					return nil, test.transport
				}
				return testResponse(request, test.status, test.body), nil
			}))}
			if _, err := driver.getWhatLinkInfo("magnet:test"); err == nil {
				t.Fatal("getWhatLinkInfo error = nil")
			}
		})
	}
}

func TestGetWhatLinkInfoReturnsDecodedScreenshots(t *testing.T) {
	driver := &FC2{client: resty.New().SetTransport(roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if got := request.URL.Query().Get("url"); got != "magnet:test" {
			t.Errorf("magnet query = %q, want magnet:test", got)
		}
		return testResponse(request, http.StatusOK, `{"screenshots":[{"time":2,"screenshot":"https://shots.test/two"}]}`), nil
	}))}
	info, err := driver.getWhatLinkInfo("magnet:test")
	if err != nil {
		t.Fatalf("getWhatLinkInfo: %v", err)
	}
	if len(info.Screenshots) != 1 || info.Screenshots[0].Screenshot != "https://shots.test/two" {
		t.Fatalf("screenshots = %+v", info.Screenshots)
	}
}

func TestSyncMissAvFilmsUsesFilmWorkIdentityWithoutLegacyMagnetCache(t *testing.T) {
	setupFC2SampleImageTest(t)
	for _, value := range []interface{}{&model.SourceMagnet{}, &model.FilmFile{}, &model.FilmWork{}} {
		if err := db.GetDb().Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(value).Error; err != nil {
			t.Fatalf("reset %T: %v", value, err)
		}
	}
	work := model.FilmWork{
		StorageID: 71, Source: "fc2", Code: "FC2-PPV-123", SourceRef: "FC2-PPV-123",
		PrimaryDir: "Ranked", Tags: model.StringArray{"existing"},
	}
	if err := db.GetDb().Create(&work).Error; err != nil {
		t.Fatalf("create existing work: %v", err)
	}
	if err := db.GetDb().Create(&model.FilmFile{WorkID: work.ID, PartIndex: 1, PartCount: 1}).Error; err != nil {
		t.Fatalf("create existing file: %v", err)
	}
	driver := FC2{Storage: model.Storage{ID: 71}}
	queried := []model.EmbyFileObj{{
		ObjThumb: model.ObjThumb{Object: model.Object{Name: "FC2-PPV-123"}},
		Tags:     []string{"Ranked-Top30", "Ranked"},
	}}
	if err := driver.syncMissAvFilms(queried); err != nil {
		t.Fatalf("sync MissAV films: %v", err)
	}
	stored, err := db.GetFilmWorkByIdentity(71, "fc2", "FC2-PPV-123")
	if err != nil {
		t.Fatalf("load synced work: %v", err)
	}
	if !reflect.DeepEqual(stored.Tags, model.StringArray{"existing", "Ranked-Top30", "Ranked"}) {
		t.Fatalf("synced tags = %#v", stored.Tags)
	}
	var legacyCount int64
	if err := db.GetDb().Model(&model.MagnetCache{}).Count(&legacyCount).Error; err != nil {
		t.Fatalf("count legacy caches: %v", err)
	}
	if legacyCount != 0 {
		t.Fatalf("MissAV sync wrote %d legacy magnet caches", legacyCount)
	}
}

func TestScanSampleImagesGroupsSiblingsAndSortsScreenshots(t *testing.T) {
	setupFC2SampleImageTest(t)
	createFC2Group(t, "actor", "FC2-100", 3, 0, time.Time{})
	createMagnet(t, "javdb", "FC2-100", "magnet:wrong")
	createMagnet(t, "fc2", "FC2-100", "magnet:right")

	var mu sync.Mutex
	var requests []string
	driver := newFC2SampleImageDriver(func(request *http.Request) (*http.Response, error) {
		mu.Lock()
		requests = append(requests, request.URL.String())
		mu.Unlock()
		if request.URL.Host == "whatslink.info" {
			if got := request.URL.Query().Get("url"); got != "magnet:right" {
				t.Errorf("WhatsLink magnet = %q, want driver-scoped magnet", got)
			}
			body := `{"screenshots":[` +
				`{"time":20,"screenshot":"https://shots.test/b"},` +
				`{"time":10,"screenshot":"https://shots.test/c"},` +
				`{"time":10,"screenshot":"https://shots.test/a"}]}`
			return testResponse(request, http.StatusOK, body), nil
		}
		if request.Referer() != "https://mypikpak.com/" {
			t.Errorf("screenshot Referer = %q", request.Referer())
		}
		return testResponse(request, http.StatusOK, request.URL.Path), nil
	})

	driver.scanSampleImages()

	if got := countRequests(requests, "whatslink.info"); got != 1 {
		t.Fatalf("WhatsLink requests = %d, want 1 for three siblings", got)
	}
	for index, want := range []string{"/a", "/c", "/b"} {
		content, err := os.ReadFile(fc2FanartPath(t, "actor", "FC2-100", index+1))
		if err != nil {
			t.Fatalf("read fanart%d: %v", index+1, err)
		}
		if string(content) != want {
			t.Errorf("fanart%d = %q, want %q", index+1, content, want)
		}
	}
	assertFC2GroupProgress(t, "actor", "FC2-100", 3, true)
}

func TestScanSampleImagesResumesAndRecoversExistingFanart(t *testing.T) {
	setupFC2SampleImageTest(t)
	createFC2Group(t, "actor", "FC2-200", 2, 1, time.Time{})
	createMagnet(t, "fc2", "FC2-200", "magnet:resume")
	existing := fc2FanartPath(t, "actor", "FC2-200", 2)
	if err := os.MkdirAll(filepath.Dir(existing), 0o755); err != nil {
		t.Fatalf("create fanart directory: %v", err)
	}
	if err := os.WriteFile(existing, []byte("existing"), 0o644); err != nil {
		t.Fatalf("create existing fanart: %v", err)
	}

	var screenshotRequests int
	driver := newFC2SampleImageDriver(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "whatslink.info" {
			return testResponse(request, http.StatusOK, `{"screenshots":[`+
				`{"time":1,"screenshot":"https://shots.test/one"},`+
				`{"time":2,"screenshot":"https://shots.test/two"},`+
				`{"time":3,"screenshot":"https://shots.test/three"}]}`), nil
		}
		screenshotRequests++
		return testResponse(request, http.StatusOK, request.URL.Path), nil
	})

	driver.scanSampleImages()

	if screenshotRequests != 1 {
		t.Fatalf("missing screenshot downloads = %d, want 1", screenshotRequests)
	}
	assertFC2GroupProgress(t, "actor", "FC2-200", 3, true)
	content, err := os.ReadFile(fc2FanartPath(t, "actor", "FC2-200", 3))
	if err != nil || string(content) != "/three" {
		t.Fatalf("fanart3 = %q, err %v", content, err)
	}
}

func TestScanSampleImagesSuccessfulEmptyListCompletesSiblings(t *testing.T) {
	setupFC2SampleImageTest(t)
	createFC2Group(t, "actor", "FC2-EMPTY", 3, 0, time.Time{})
	createMagnet(t, "fc2", "FC2-EMPTY", "magnet:empty")
	driver := newFC2SampleImageDriver(func(request *http.Request) (*http.Response, error) {
		return testResponse(request, http.StatusOK, `{"screenshots":[]}`), nil
	})

	driver.scanSampleImages()

	assertFC2GroupProgress(t, "actor", "FC2-EMPTY", 0, true)
}

func TestScanSampleImagesRetriesTransientFailures(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "api status", body: "status"},
		{name: "api error", body: "api-error"},
		{name: "invalid screenshot URL", body: "invalid-url"},
		{name: "screenshot transport", body: "transport-error"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setupFC2SampleImageTest(t)
			code := "FC2-" + strings.ToUpper(strings.ReplaceAll(test.name, " ", "-"))
			createFC2Group(t, "actor", code, 2, 0, time.Time{})
			createMagnet(t, "fc2", code, "magnet:retry")
			driver := newFC2SampleImageDriver(func(request *http.Request) (*http.Response, error) {
				if request.URL.Host == "whatslink.info" {
					switch test.body {
					case "status":
						return testResponse(request, http.StatusServiceUnavailable, `{}`), nil
					case "api-error":
						return testResponse(request, http.StatusOK, `{"error":"retry"}`), nil
					case "invalid-url":
						return testResponse(request, http.StatusOK, `{"screenshots":[{"time":1,"screenshot":"file:///tmp/no"}]}`), nil
					default:
						return testResponse(request, http.StatusOK, `{"screenshots":[{"time":1,"screenshot":"https://shots.test/fail"}]}`), nil
					}
				}
				return nil, errors.New("offline")
			})

			started := time.Now()
			driver.scanSampleImages()
			assertFC2GroupProgress(t, "actor", code, 0, false)
			for _, film := range loadFC2Group(t, "actor", code) {
				if film.SampleImageScanAt.Before(started) {
					t.Errorf("scan-at = %s, want at or after %s", film.SampleImageScanAt, started)
				}
			}
		})
	}
}

func TestScanSampleImagesSkipsHTTPStatusAndContinues(t *testing.T) {
	for _, statusCode := range []int{http.StatusNotFound, http.StatusInternalServerError} {
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			setupFC2SampleImageTest(t)
			code := fmt.Sprintf("FC2-HTTP-%d", statusCode)
			createFC2Group(t, "actor", code, 2, 0, time.Time{})
			createMagnet(t, "fc2", code, "magnet:http-status")
			driver := newFC2SampleImageDriver(func(request *http.Request) (*http.Response, error) {
				if request.URL.Host == "whatslink.info" {
					return testResponse(request, http.StatusOK, `{"screenshots":[`+
						`{"time":1,"screenshot":"https://shots.test/one"},`+
						`{"time":2,"screenshot":"https://shots.test/two"},`+
						`{"time":3,"screenshot":"https://shots.test/three"}]}`), nil
				}
				if request.URL.Path == "/two" {
					return testResponse(request, statusCode, "missing"), nil
				}
				return testResponse(request, http.StatusOK, request.URL.Path), nil
			})

			driver.scanSampleImages()

			assertFC2GroupProgress(t, "actor", code, 3, true)
			for index, want := range map[int]string{1: "/one", 3: "/three"} {
				content, err := os.ReadFile(fc2FanartPath(t, "actor", code, index))
				if err != nil {
					t.Fatalf("read fanart%d: %v", index, err)
				}
				if string(content) != want {
					t.Errorf("fanart%d = %q, want %q", index, content, want)
				}
			}
			if _, err := os.Stat(fc2FanartPath(t, "actor", code, 2)); !os.IsNotExist(err) {
				t.Fatalf("fanart2 should be absent, stat error = %v", err)
			}
		})
	}
}

func TestScanSampleImagesUsesBatchLimit(t *testing.T) {
	setupFC2SampleImageTest(t)
	for index := 0; index < 21; index++ {
		code := fmt.Sprintf("FC2-BATCH-%02d", index)
		createFC2Group(t, "actor", code, 1, 0, time.Time{})
		createMagnet(t, "fc2", code, "magnet:"+code)
	}
	requests := 0
	driver := newFC2SampleImageDriver(func(request *http.Request) (*http.Response, error) {
		requests++
		return testResponse(request, http.StatusOK, `{"screenshots":[]}`), nil
	})

	driver.scanSampleImages()

	if requests != 20 {
		t.Fatalf("WhatsLink requests = %d, want 20", requests)
	}
	if got := countCompleteFC2Films(t); got != 20 {
		t.Fatalf("complete films = %d, want 20", got)
	}
}

func TestScanSampleImagesEnforcesRunBudgetWithoutDelayingPartialGroup(t *testing.T) {
	setupFC2SampleImageTest(t)
	createFC2Group(t, "a", "FC2-BUDGET-A", 1, 0, time.Time{})
	createFC2Group(t, "b", "FC2-BUDGET-B", 1, 0, time.Time{})
	createMagnet(t, "fc2", "FC2-BUDGET-A", "magnet:a")
	createMagnet(t, "fc2", "FC2-BUDGET-B", "magnet:b")

	requests := 0
	driver := newFC2SampleImageDriver(func(request *http.Request) (*http.Response, error) {
		requests++
		if request.URL.Host == "whatslink.info" {
			parts := make([]string, 55)
			for index := range parts {
				parts[index] = fmt.Sprintf(`{"time":%d,"screenshot":"https://shots.test/%02d"}`, index, index)
			}
			return testResponse(request, http.StatusOK, `{"screenshots":[`+strings.Join(parts, ",")+`]}`), nil
		}
		return testResponse(request, http.StatusOK, "image"), nil
	})

	driver.scanSampleImages()

	if requests != 100 {
		t.Fatalf("external requests = %d, want 100", requests)
	}
	assertFC2GroupProgress(t, "a", "FC2-BUDGET-A", 50, true)
	assertFC2GroupProgress(t, "b", "FC2-BUDGET-B", 48, false)
	for _, film := range loadFC2Group(t, "b", "FC2-BUDGET-B") {
		if !film.SampleImageScanAt.IsZero() {
			t.Errorf("partial group scan-at = %s, want zero", film.SampleImageScanAt)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func testResponse(request *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    request,
	}
}

func newFC2SampleImageDriver(transport roundTripFunc) *FC2 {
	return &FC2{client: resty.New().SetTransport(transport)}
}

func setupFC2SampleImageTest(t *testing.T) {
	t.Helper()
	previousDataDir := flags.DataDir
	previousConf := conf.Conf
	flags.DataDir = t.TempDir()
	conf.Conf = conf.DefaultConfig(flags.DataDir)
	if err := db.GetDb().Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&model.Film{}).Error; err != nil {
		t.Fatalf("reset films: %v", err)
	}
	if err := db.GetDb().Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&model.MagnetCache{}).Error; err != nil {
		t.Fatalf("reset magnets: %v", err)
	}
	t.Cleanup(func() {
		flags.DataDir = previousDataDir
		conf.Conf = previousConf
	})
}

func createFC2Group(t *testing.T, actor, code string, siblings, count int, scanAt time.Time) {
	t.Helper()
	films := make([]model.Film, siblings)
	for index := range films {
		films[index] = model.Film{
			Url:                 code,
			Name:                fmt.Sprintf("%s-cd%d.mp4", code, index+1),
			Source:              "fc2",
			Actor:               actor,
			Date:                time.Now(),
			SampleImageCount:    count,
			SampleImageScanAt:   scanAt,
			SampleImageComplete: false,
		}
	}
	if err := db.GetDb().Create(&films).Error; err != nil {
		t.Fatalf("create FC2 group: %v", err)
	}
}

func createMagnet(t *testing.T, driver, code, magnet string) {
	t.Helper()
	if err := db.GetDb().Create(&model.MagnetCache{DriverType: driver, Code: code, Magnet: magnet}).Error; err != nil {
		t.Fatalf("create magnet: %v", err)
	}
}

func loadFC2Group(t *testing.T, actor, code string) []model.Film {
	t.Helper()
	var films []model.Film
	if err := db.GetDb().Where("source = ? AND actor = ? AND url = ?", "fc2", actor, code).Order("id").Find(&films).Error; err != nil {
		t.Fatalf("load FC2 group: %v", err)
	}
	return films
}

func assertFC2GroupProgress(t *testing.T, actor, code string, count int, complete bool) {
	t.Helper()
	films := loadFC2Group(t, actor, code)
	for _, film := range films {
		if film.SampleImageCount != count || film.SampleImageComplete != complete {
			t.Errorf("film %d progress = (%d, %t), want (%d, %t)", film.ID, film.SampleImageCount, film.SampleImageComplete, count, complete)
		}
	}
}

func fc2FanartPath(t *testing.T, actor, code string, index int) string {
	t.Helper()
	path, err := virtual_file.FanartPath("fc2", actor, code, index)
	if err != nil {
		t.Fatalf("fanart path: %v", err)
	}
	return path
}

func countRequests(requests []string, host string) int {
	count := 0
	for _, request := range requests {
		if strings.Contains(request, host) {
			count++
		}
	}
	return count
}

func countCompleteFC2Films(t *testing.T) int64 {
	t.Helper()
	var count int64
	if err := db.GetDb().Model(&model.Film{}).Where("source = ? AND sample_image_complete = ?", "fc2", true).Count(&count).Error; err != nil {
		t.Fatalf("count complete films: %v", err)
	}
	return count
}
