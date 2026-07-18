package javdb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

func TestDMMPosterCIDAndCandidateOrder(t *testing.T) {
	cid, err := dmmPosterCID("MIDV-169.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if cid != "midv00169" {
		t.Fatalf("CID = %q, want midv00169", cid)
	}
	translatedCID, err := dmmPosterCID("MIDV-169 translated title.mp4")
	if err != nil || translatedCID != "midv00169" {
		t.Fatalf("translated-title CID = %q, error = %v, want midv00169", translatedCID, err)
	}
	want := []string{
		"https://awsimgsrc.dmm.co.jp/pics_dig/digital/video/1midv00169/1midv00169ps.jpg",
		"https://awsimgsrc.dmm.co.jp/pics_dig/digital/video/midv00169/midv00169ps.jpg",
	}
	if got := dmmPosterCandidates(cid); !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
	monoCID, err := dmmMonoPosterCID("ABF-007.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if monoCID != "abf007" {
		t.Fatalf("mono CID = %q, want abf007", monoCID)
	}
	wantMono := []string{
		"https://pics.dmm.co.jp/mono/movie/adult/118abf007/118abf007pl.jpg",
		"https://pics.dmm.co.jp/mono/movie/adult/1abf007/1abf007pl.jpg",
	}
	if got := dmmMonoPosterCandidates(monoCID); !reflect.DeepEqual(got, wantMono) {
		t.Fatalf("mono candidates = %v, want %v", got, wantMono)
	}

	longCID, err := dmmPosterCID("Ab12-123456.jpg")
	if err != nil || longCID != "ab12123456" {
		t.Fatalf("long CID = %q, error = %v", longCID, err)
	}
	for _, invalid := range []string{"MIDV.mp4", "MIDV-ABC.mp4", "MIDV-169-extra.mp4"} {
		if _, err := dmmPosterCID(invalid); err == nil {
			t.Errorf("dmmPosterCID(%q) error = nil", invalid)
		}
	}
}

func TestDMMPosterSearchImageURL(t *testing.T) {
	html := `
		<div class="border-b border-dotted border-gray-300">
			<img src="https://pics.dmm.co.jp/mono/movie/adult/other001/other001ps.jpg">
		</div>
		<div class="border-b border-dotted border-gray-300 extra-class">
			<img src="https://pics.dmm.co.jp/common/icon.png">
			<img src="https://pics.dmm.co.jp/mono/movie/adult/1abf007/1abf007ps.jpg?cache=1">
		</div>`

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(html))
	}))
	defer server.Close()

	got, err := newSampleImageDriver(server).fetchDmmPosterSearchImageURL("ABF-007")
	if err != nil {
		t.Fatal(err)
	}
	want := "https://pics.dmm.co.jp/mono/movie/adult/1abf007/1abf007pl.jpg?cache=1"
	if got != want {
		t.Fatalf("search image URL = %q, want %q", got, want)
	}
}

func TestDMMPosterSearchImageURLLiveREBD1046(t *testing.T) {
	if os.Getenv("DMM_LIVE_TEST") != "1" {
		t.Skip("set DMM_LIVE_TEST=1 to query the live DMM search page")
	}

	got, err := (&Javdb{}).fetchDmmPosterSearchImageURL("REBD-1046")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("fetchDmmPosterSearchImageURL(%q) = %q", "REBD-1046", got)
}

func TestCropDMMMonoPoster(t *testing.T) {
	if _, err := cropDMMMonoPoster(bytes.Repeat([]byte{1}, minDMMMonoPosterBytes-1)); err == nil {
		t.Fatal("small response body accepted")
	}
	if _, err := cropDMMMonoPoster(testCompositeJPEG(t, 799, 541)); err == nil {
		t.Fatal("799px-wide image accepted")
	}

	cropped, err := cropDMMMonoPoster(testSegmentedCompositeJPEG(t, 541))
	if err != nil {
		t.Fatal(err)
	}
	croppedImage, format, err := image.Decode(bytes.NewReader(cropped))
	if err != nil {
		t.Fatal(err)
	}
	if format != "jpeg" || croppedImage.Bounds().Dx() != 380 || croppedImage.Bounds().Dy() != 541 {
		t.Fatalf("cropped image = %s %dx%d, want jpeg 380x541", format, croppedImage.Bounds().Dx(), croppedImage.Bounds().Dy())
	}
	r, g, b, _ := croppedImage.At(190, 270).RGBA()
	if b < 0xc000 || r > 0x3000 || g > 0x3000 {
		t.Fatalf("cropped center color = (%04x, %04x, %04x), want blue from third segment", r, g, b)
	}
}

func testSegmentedCompositeJPEG(t *testing.T, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 800, height))
	for y := 0; y < height; y++ {
		for x := 0; x < 800; x++ {
			pixel := color.RGBA{R: 255, A: 255}
			if x >= 380 && x < 420 {
				pixel = color.RGBA{G: 255, A: 255}
			} else if x >= 420 {
				pixel = color.RGBA{B: 255, A: 255}
			}
			img.SetRGBA(x, y, pixel)
		}
	}
	var output bytes.Buffer
	if err := jpeg.Encode(&output, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	if output.Len() < minDMMMonoPosterBytes {
		output.Write(make([]byte, minDMMMonoPosterBytes-output.Len()))
	}
	return output.Bytes()
}

func testCompositeJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetRGBA(x, y, color.RGBA{
				R: uint8((x*17 + y*31) % 256),
				G: uint8((x*43 + y*7) % 256),
				B: uint8((x*3 + y*61) % 256),
				A: 255,
			})
		}
	}
	var output bytes.Buffer
	if err := jpeg.Encode(&output, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	if output.Len() < minDMMMonoPosterBytes {
		t.Fatalf("test JPEG size = %d, want at least %d", output.Len(), minDMMMonoPosterBytes)
	}
	return output.Bytes()
}

func TestScanDMMPosterHTTPDecisions(t *testing.T) {
	validImage := tinyPNG(t)
	truncatedImage := validImage[:33]
	tests := []struct {
		name         string
		statuses     []int
		bodies       [][]byte
		wantRequests int
		wantStatus   string
		wantPoster   bool
	}{
		{name: "first candidate success", statuses: []int{http.StatusOK}, bodies: [][]byte{validImage}, wantRequests: 1, wantStatus: model.DMMPosterStatusSuccess, wantPoster: true},
		{name: "fallback candidate success", statuses: []int{http.StatusNotFound, http.StatusOK}, bodies: [][]byte{nil, validImage}, wantRequests: 2, wantStatus: model.DMMPosterStatusSuccess, wantPoster: true},
		{name: "all candidates not found", statuses: []int{http.StatusNotFound, http.StatusGone}, wantRequests: 5, wantStatus: model.DMMPosterStatusNotFound},
		{name: "mixed not found and transient", statuses: []int{http.StatusNotFound, http.StatusTooManyRequests}, wantRequests: 5, wantStatus: model.DMMPosterStatusTransientError},
		{name: "invalid image falls back then remains transient", statuses: []int{http.StatusOK, http.StatusNotFound}, bodies: [][]byte{[]byte("not an image"), nil}, wantRequests: 5, wantStatus: model.DMMPosterStatusTransientError},
		{name: "truncated image falls back then remains transient", statuses: []int{http.StatusOK, http.StatusNotFound}, bodies: [][]byte{truncatedImage, nil}, wantRequests: 5, wantStatus: model.DMMPosterStatusTransientError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setupJavdbSampleImageTest(t)
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				index := int(requests.Add(1)) - 1
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
				if index < len(test.bodies) {
					_, _ = response.Write(test.bodies[index])
				}
			}))
			defer server.Close()

			film := createSampleImageFilm(t, server.URL, "MIDV-169", 0, time.Time{})
			paths, err := virtual_file.PosterPaths(DriverName, film.Actor, film.Name)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(paths.Poster), 0o755); err != nil {
				t.Fatal(err)
			}
			legacyPoster := paths.LegacyPoster
			if err := os.WriteFile(legacyPoster, []byte("old poster"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Base(legacyPoster), paths.Background); err != nil {
				t.Fatal(err)
			}

			newSampleImageDriver(server).scanDMMPoster(context.Background(), &film)

			stored := loadSampleImageFilm(t, film.ID)
			if stored.DMMPosterStatus != test.wantStatus || stored.DMMPosterScanAt.IsZero() {
				t.Fatalf("stored DMM state = (%q, %s), want (%q, nonzero)", stored.DMMPosterStatus, stored.DMMPosterScanAt, test.wantStatus)
			}
			if got := int(requests.Load()); got != test.wantRequests {
				t.Fatalf("requests = %d, want %d", got, test.wantRequests)
			}
			if test.wantPoster {
				content, err := os.ReadFile(paths.Poster)
				if err != nil {
					t.Fatal(err)
				}
				if bytes.Equal(content, []byte("old poster")) {
					t.Fatal("poster was not replaced")
				}
				if _, err := os.Lstat(legacyPoster); !os.IsNotExist(err) {
					t.Fatalf("legacy poster retained after success: %v", err)
				}
				if _, err := os.Lstat(paths.Background); !os.IsNotExist(err) {
					t.Fatalf("background symlink retained after success: %v", err)
				}
			} else {
				content, err := os.ReadFile(legacyPoster)
				if err != nil {
					t.Fatal(err)
				}
				if string(content) != "old poster" {
					t.Fatalf("poster changed on failure: %q", content)
				}
				if _, err := os.Lstat(paths.Poster); !os.IsNotExist(err) {
					t.Fatalf("poster.jpg created on failure: %v", err)
				}
				if info, err := os.Lstat(paths.Background); err != nil || info.Mode()&os.ModeSymlink == 0 {
					t.Fatalf("background symlink changed on failure: %v", err)
				}
			}
		})
	}
}

func TestScanDMMPosterMonoFallbackOrder(t *testing.T) {
	setupJavdbSampleImageTest(t)
	validComposite := testCompositeJPEG(t, 800, 541)
	var requestedPaths []string
	var requestsMu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requestsMu.Lock()
		requestedPaths = append(requestedPaths, request.URL.Path)
		requestsMu.Unlock()
		switch request.URL.Path {
		case "/pics_dig/digital/video/1abf00007/1abf00007ps.jpg",
			"/pics_dig/digital/video/abf00007/abf00007ps.jpg":
			response.WriteHeader(http.StatusNotFound)
		case "/mono/movie/adult/118abf007/118abf007pl.jpg":
			_, _ = response.Write(bytes.Repeat([]byte{1}, 3180))
		case "/mono/movie/adult/1abf007/1abf007pl.jpg":
			_, _ = response.Write(validComposite)
		default:
			t.Fatalf("unexpected request %s", request.URL.Path)
		}
	}))
	defer server.Close()

	film, paths := prepareDMMPosterTestFilm(t, server.URL, "ABF-007")
	newSampleImageDriver(server).scanDMMPoster(context.Background(), &film)

	wantPaths := []string{
		"/pics_dig/digital/video/1abf00007/1abf00007ps.jpg",
		"/pics_dig/digital/video/abf00007/abf00007ps.jpg",
		"/mono/movie/adult/118abf007/118abf007pl.jpg",
		"/mono/movie/adult/1abf007/1abf007pl.jpg",
	}
	if !reflect.DeepEqual(requestedPaths, wantPaths) {
		t.Fatalf("request paths = %v, want %v", requestedPaths, wantPaths)
	}
	assertCroppedDMMPoster(t, film, paths)
}

func TestScanDMMPosterSearchFallback(t *testing.T) {
	setupJavdbSampleImageTest(t)
	validComposite := testCompositeJPEG(t, 800, 541)
	var requestedPaths []string
	var requestsMu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requestsMu.Lock()
		requestedPaths = append(requestedPaths, request.URL.Path)
		requestsMu.Unlock()
		switch {
		case strings.HasPrefix(request.URL.Path, "/pics_dig/digital/video/"),
			request.URL.Path == "/mono/movie/adult/118abf007/118abf007pl.jpg",
			request.URL.Path == "/mono/movie/adult/1abf007/1abf007pl.jpg":
			response.WriteHeader(http.StatusNotFound)
		case strings.HasPrefix(request.URL.Path, "/search/=/searchstr=abf 007/"):
			_, _ = response.Write([]byte(`
				<div class="border-b border-dotted border-gray-300">
					<img src="https://pics.dmm.co.jp/mono/movie/adult/other001/other001ps.jpg">
				</div>
				<div class="border-b border-dotted border-gray-300">
					<img src="https://pics.dmm.co.jp/mono/movie/adult/searchabf007/searchabf007ps.jpg">
				</div>`))
		case request.URL.Path == "/mono/movie/adult/searchabf007/searchabf007pl.jpg":
			_, _ = response.Write(validComposite)
		default:
			t.Fatalf("unexpected request %s", request.URL.Path)
		}
	}))
	defer server.Close()

	film, paths := prepareDMMPosterTestFilm(t, server.URL, "ABF-007")
	newSampleImageDriver(server).scanDMMPoster(context.Background(), &film)

	if len(requestedPaths) != 6 {
		t.Fatalf("request paths = %v, want 6 requests", requestedPaths)
	}
	if !strings.HasPrefix(requestedPaths[4], "/search/=/searchstr=abf 007/") {
		t.Fatalf("fifth request = %q, want DMM search", requestedPaths[4])
	}
	if requestedPaths[5] != "/mono/movie/adult/searchabf007/searchabf007pl.jpg" {
		t.Fatalf("sixth request = %q, want matched pl.jpg", requestedPaths[5])
	}
	assertCroppedDMMPoster(t, film, paths)
}

func TestScanDMMPosterCorruptMonoRemainsTransient(t *testing.T) {
	setupJavdbSampleImageTest(t)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case strings.HasPrefix(request.URL.Path, "/pics_dig/digital/video/"):
			response.WriteHeader(http.StatusNotFound)
		case request.URL.Path == "/mono/movie/adult/118abf007/118abf007pl.jpg":
			_, _ = response.Write(bytes.Repeat([]byte("corrupt"), minDMMMonoPosterBytes/7+1))
		case request.URL.Path == "/mono/movie/adult/1abf007/1abf007pl.jpg":
			response.WriteHeader(http.StatusNotFound)
		case strings.HasPrefix(request.URL.Path, "/search/=/searchstr=abf 007/"):
			response.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected request %s", request.URL.Path)
		}
	}))
	defer server.Close()

	film, paths := prepareDMMPosterTestFilm(t, server.URL, "ABF-007")
	newSampleImageDriver(server).scanDMMPoster(context.Background(), &film)

	stored := loadSampleImageFilm(t, film.ID)
	if stored.DMMPosterStatus != model.DMMPosterStatusTransientError {
		t.Fatalf("DMM status = %q, want transient_error", stored.DMMPosterStatus)
	}
	if content, err := os.ReadFile(paths.LegacyPoster); err != nil || string(content) != "old poster" {
		t.Fatalf("legacy poster changed: content=%q error=%v", content, err)
	}
	if _, err := os.Lstat(paths.Poster); !os.IsNotExist(err) {
		t.Fatalf("poster.jpg created for corrupt mono response: %v", err)
	}
}

func prepareDMMPosterTestFilm(t *testing.T, serverURL, name string) (model.Film, virtual_file.PosterPathSet) {
	t.Helper()
	film := createSampleImageFilm(t, serverURL, name, 0, time.Time{})
	paths, err := virtual_file.PosterPaths(DriverName, film.Actor, film.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.Poster), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.LegacyPoster, []byte("old poster"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Base(paths.LegacyPoster), paths.Background); err != nil {
		t.Fatal(err)
	}
	return film, paths
}

func assertCroppedDMMPoster(t *testing.T, film model.Film, paths virtual_file.PosterPathSet) {
	t.Helper()
	stored := loadSampleImageFilm(t, film.ID)
	if stored.DMMPosterStatus != model.DMMPosterStatusSuccess || stored.DMMPosterScanAt.IsZero() {
		t.Fatalf("stored DMM state = (%q, %s), want success", stored.DMMPosterStatus, stored.DMMPosterScanAt)
	}
	content, err := os.ReadFile(paths.Poster)
	if err != nil {
		t.Fatal(err)
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	if format != "jpeg" || config.Width != 380 || config.Height != 541 {
		t.Fatalf("poster = %s %dx%d, want jpeg 380x541", format, config.Width, config.Height)
	}
	if _, err := os.Lstat(paths.LegacyPoster); !os.IsNotExist(err) {
		t.Fatalf("legacy poster retained after success: %v", err)
	}
	if _, err := os.Lstat(paths.Background); !os.IsNotExist(err) {
		t.Fatalf("background symlink retained after success: %v", err)
	}
}

func TestScanDMMPosterInvalidCodeMakesNoRequestOrFilesystemMutation(t *testing.T) {
	setupJavdbSampleImageTest(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()
	film := createSampleImageFilm(t, server.URL, "invalid-code-extra", 0, time.Time{})

	newSampleImageDriver(server).scanDMMPoster(context.Background(), &film)

	stored := loadSampleImageFilm(t, film.ID)
	if stored.DMMPosterStatus != model.DMMPosterStatusTransientError || stored.DMMPosterScanAt.IsZero() {
		t.Fatalf("stored DMM state = (%q, %s)", stored.DMMPosterStatus, stored.DMMPosterScanAt)
	}
	if requests.Load() != 0 {
		t.Fatalf("requests = %d, want 0", requests.Load())
	}
	paths, err := virtual_file.PosterPaths(DriverName, film.Actor, film.Name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Dir(paths.Poster)); !os.IsNotExist(err) {
		t.Fatalf("filesystem mutated for invalid code: %v", err)
	}
}

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	var output bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 1, G: 2, B: 3, A: 255})
	if err := png.Encode(&output, img); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
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

func TestScanFilmSampleImagesPromotesFirstLandscape(t *testing.T) {
	setupJavdbSampleImageTest(t)
	portrait := testCompositeJPEG(t, 400, 600)
	landscape := testCompositeJPEG(t, 600, 400)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/samples/orientation_l_1.jpg":
			_, _ = response.Write(portrait)
		case "/samples/orientation_l_2.jpg":
			_, _ = response.Write(landscape)
		default:
			response.WriteHeader(http.StatusForbidden)
		}
	}))
	defer server.Close()

	film := createSampleImageFilm(t, "https://img.jdbstatic.com", "orientation", 0, time.Time{})
	newSampleImageDriver(server).scanFilmSampleImages(context.Background(), &film)

	assertSampleImageDimensions(t, sampleImagePath(film, 1), 600, 400)
	assertSampleImageDimensions(t, sampleImagePath(film, 2), 400, 600)
	stored := loadSampleImageFilm(t, film.ID)
	if stored.SampleImageCount != 2 || !stored.SampleImageComplete {
		t.Fatalf("progress = (%d, %t), want (2, true)", stored.SampleImageCount, stored.SampleImageComplete)
	}
}

func TestScanFilmSampleImagesPromotesCachedLandscapeOnResume(t *testing.T) {
	setupJavdbSampleImageTest(t)
	film := createSampleImageFilm(t, "https://img.jdbstatic.com", "resume-orientation", 2, time.Time{})
	if err := os.MkdirAll(filepath.Dir(sampleImagePath(film, 1)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sampleImagePath(film, 1), testCompositeJPEG(t, 400, 600), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sampleImagePath(film, 2), testCompositeJPEG(t, 600, 400), 0o644); err != nil {
		t.Fatal(err)
	}
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests = append(requests, request.URL.Path)
		response.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	newSampleImageDriver(server).scanFilmSampleImages(context.Background(), &film)

	if want := []string{"/samples/resume-orientation_l_3.jpg"}; !reflect.DeepEqual(requests, want) {
		t.Fatalf("requests = %v, want %v", requests, want)
	}
	assertSampleImageDimensions(t, sampleImagePath(film, 1), 600, 400)
	assertSampleImageDimensions(t, sampleImagePath(film, 2), 400, 600)
}

func TestScanFilmSampleImagesPromotesLandscapeOverInvalidPrimary(t *testing.T) {
	setupJavdbSampleImageTest(t)
	film := createSampleImageFilm(t, "https://img.jdbstatic.com", "invalid-primary", 2, time.Time{})
	if err := os.MkdirAll(filepath.Dir(sampleImagePath(film, 1)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sampleImagePath(film, 1), []byte("not an image"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sampleImagePath(film, 2), testCompositeJPEG(t, 600, 400), 0o644); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	newSampleImageDriver(server).scanFilmSampleImages(context.Background(), &film)

	assertSampleImageDimensions(t, sampleImagePath(film, 1), 600, 400)
	content, err := os.ReadFile(sampleImagePath(film, 2))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "not an image" {
		t.Fatalf("fanart2 content = %q, want displaced invalid primary", content)
	}
}

func TestScanFilmSampleImagesRecoveryFailureDoesNotComplete(t *testing.T) {
	setupJavdbSampleImageTest(t)
	film := createSampleImageFilm(t, "https://img.jdbstatic.com", "recovery-failure", 2, time.Time{})
	directory := filepath.Dir(sampleImagePath(film, 1))
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sampleImagePath(film, 1), testCompositeJPEG(t, 400, 600), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sampleImagePath(film, 2), testCompositeJPEG(t, 600, 400), 0o644); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(directory, ".fanart1.jpg-fanart2.jpg.swap-old")
	if err := os.Mkdir(marker, 0o755); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		response.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	started := time.Now()

	newSampleImageDriver(server).scanFilmSampleImages(context.Background(), &film)

	stored := loadSampleImageFilm(t, film.ID)
	if stored.SampleImageComplete || stored.SampleImageCount != 2 {
		t.Fatalf("progress = (%d, %t), want (2, false)", stored.SampleImageCount, stored.SampleImageComplete)
	}
	if stored.SampleImageScanAt.Before(started) {
		t.Fatalf("scan time = %s, want at or after %s", stored.SampleImageScanAt, started)
	}
	if requests.Load() != 0 {
		t.Fatalf("requests = %d, want 0", requests.Load())
	}
}

func TestScanFilmSampleImagesSkipsLaterInspectionWhenPrimaryIsLandscape(t *testing.T) {
	setupJavdbSampleImageTest(t)
	film := createSampleImageFilm(t, "https://img.jdbstatic.com", "landscape-primary", 3, time.Time{})
	directory := filepath.Dir(sampleImagePath(film, 1))
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sampleImagePath(film, 1), testCompositeJPEG(t, 600, 400), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sampleImagePath(film, 2), testCompositeJPEG(t, 400, 600), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(sampleImagePath(film, 3), 0o755); err != nil {
		t.Fatal(err)
	}
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests = append(requests, request.URL.Path)
		response.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	newSampleImageDriver(server).scanFilmSampleImages(context.Background(), &film)

	if want := []string{"/samples/landscape-primary_l_4.jpg"}; !reflect.DeepEqual(requests, want) {
		t.Fatalf("requests = %v, want %v", requests, want)
	}
	stored := loadSampleImageFilm(t, film.ID)
	if stored.SampleImageCount != 3 || !stored.SampleImageComplete {
		t.Fatalf("progress = (%d, %t), want (3, true)", stored.SampleImageCount, stored.SampleImageComplete)
	}
}

func TestScanFilmSampleImagesCachesLandscapeReadyForCurrentRun(t *testing.T) {
	setupJavdbSampleImageTest(t)
	originalPromote := promoteLandscapeFanartCandidate
	var promotionCalls atomic.Int32
	promoteLandscapeFanartCandidate = func(string, string, string, int) (bool, error) {
		promotionCalls.Add(1)
		return true, nil
	}
	t.Cleanup(func() { promoteLandscapeFanartCandidate = originalPromote })

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/samples/ready-cache_l_1.jpg", "/samples/ready-cache_l_2.jpg", "/samples/ready-cache_l_3.jpg":
			_, _ = response.Write([]byte("sample image"))
		default:
			response.WriteHeader(http.StatusForbidden)
		}
	}))
	defer server.Close()
	film := createSampleImageFilm(t, "https://img.jdbstatic.com", "ready-cache", 0, time.Time{})

	newSampleImageDriver(server).scanFilmSampleImages(context.Background(), &film)

	if got := promotionCalls.Load(); got != 1 {
		t.Fatalf("promotion calls = %d, want 1", got)
	}
	stored := loadSampleImageFilm(t, film.ID)
	if stored.SampleImageCount != 3 || !stored.SampleImageComplete {
		t.Fatalf("progress = (%d, %t), want (3, true)", stored.SampleImageCount, stored.SampleImageComplete)
	}
}

func assertSampleImageDimensions(t *testing.T, path string, wantWidth, wantHeight int) {
	t.Helper()
	file, err := os.Open(path)
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

func TestScanFilmSampleImagesUsesSharedCanonicalPathForHistoricalLongName(t *testing.T) {
	setupJavdbSampleImageTest(t)

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if requests.Add(1) == 1 {
			_, _ = response.Write([]byte("historical fanart"))
			return
		}
		response.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	film := createSampleImageFilm(t, "https://img.jdbstatic.com", "historical", 0, time.Time{})
	film.Name = strings.Repeat("界", 100) + ".mp4"
	if err := db.GetDb().Model(&film).Update("name", film.Name).Error; err != nil {
		t.Fatalf("update historical film name: %v", err)
	}

	newSampleImageDriver(server).scanFilmSampleImages(context.Background(), &film)

	wantPath, err := virtual_file.FanartPath(DriverName, film.Actor, film.Name, 1)
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("read canonical fanart path: %v", err)
	}
	if string(content) != "historical fanart" {
		t.Fatalf("fanart content = %q, want historical fanart", content)
	}
	stored := loadSampleImageFilm(t, film.ID)
	if stored.SampleImageCount != 1 || !stored.SampleImageComplete {
		t.Fatalf("progress = (%d, %t), want (1, true)", stored.SampleImageCount, stored.SampleImageComplete)
	}
}

func TestCreateStarFilmPersistsAndReturnsNormalizedName(t *testing.T) {
	setupJavdbSampleImageTest(t)

	film := model.EmbyFileObj{
		ObjThumb: model.ObjThumb{Object: model.Object{
			Name: strings.Repeat("界", 100) + ".jpg",
		}},
		Url: "https://javdb.com/v/test-normalized-star",
	}
	want := virtual_file.AppendFilmName(virtual_file.CutString(virtual_file.ClearFilmName(film.Name)))

	err := createStarFilm(&film)
	if err != nil {
		t.Fatal(err)
	}
	if film.Name != want {
		t.Fatalf("returned cache name = %q, want %q", film.Name, want)
	}

	var stored model.Film
	if err := db.GetDb().Where("url = ?", film.Url).First(&stored).Error; err != nil {
		t.Fatalf("load persisted star: %v", err)
	}
	if stored.Name != want {
		t.Fatalf("persisted name = %q, want %q", stored.Name, want)
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
