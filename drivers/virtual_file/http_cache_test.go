package virtual_file

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-resty/resty/v2"
)

func TestCacheHTTPFileStreamsAndPublishesAtomically(t *testing.T) {
	requestStarted := make(chan struct{})
	finishResponse := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("X-Cache-Test"); got != "preserved" {
			t.Errorf("X-Cache-Test header = %q, want preserved", got)
		}
		_, _ = io.WriteString(writer, "first-")
		writer.(http.Flusher).Flush()
		close(requestStarted)
		<-finishResponse
		_, _ = io.WriteString(writer, "second")
	}))
	t.Cleanup(server.Close)

	destination := filepath.Join(t.TempDir(), "nested", "cached.jpg")
	type cacheCallResult struct {
		result HTTPFileCacheResult
		err    error
	}
	completed := make(chan cacheCallResult, 1)
	go func() {
		result, err := CacheHTTPFile(context.Background(), HTTPFileCacheRequest{
			URL:         server.URL,
			Destination: destination,
			Headers:     map[string]string{"X-Cache-Test": "preserved"},
			Client:      resty.New(),
		})
		completed <- cacheCallResult{result: result, err: err}
	}()

	<-requestStarted
	directory := filepath.Dir(destination)
	waitForDirectoryEntry(t, directory)
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		t.Fatalf("destination exists before response completes: %v", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("temporary entries = %d, want 1", len(entries))
	}

	close(finishResponse)
	call := <-completed
	if call.err != nil {
		t.Fatal(call.err)
	}
	if call.result.Existing {
		t.Fatal("Existing = true, want false")
	}
	content, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(content); got != "first-second" {
		t.Fatalf("content = %q, want first-second", got)
	}
	assertMode(t, directory, 0o755)
	assertMode(t, destination, 0o644)
	assertNoSymlinks(t, directory)
}

func TestCacheHTTPFileSkipsExistingRegularFile(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	t.Cleanup(server.Close)

	destination := filepath.Join(t.TempDir(), "cached.jpg")
	if err := os.WriteFile(destination, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := CacheHTTPFile(context.Background(), HTTPFileCacheRequest{
		URL:         server.URL,
		Destination: destination,
		Client:      resty.New(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Existing {
		t.Fatal("Existing = false, want true")
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("requests = %d, want 0", got)
	}
	content, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(content); got != "existing" {
		t.Fatalf("content = %q, want existing", got)
	}
}

func TestCacheHTTPFileReturnsTypedStatusErrorAndClosesBody(t *testing.T) {
	body := &trackingReadCloser{Reader: http.NoBody}
	client := resty.NewWithClient(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Context().Value(contextKey{}) != "preserved" {
			t.Error("request context value was not preserved")
		}
		if got := request.Header.Get("Authorization"); got != "Bearer token" {
			t.Errorf("Authorization header = %q, want Bearer token", got)
		}
		return &http.Response{
			StatusCode: http.StatusBadGateway,
			Status:     "502 Bad Gateway",
			Header:     make(http.Header),
			Body:       body,
			Request:    request,
		}, nil
	})})

	destination := filepath.Join(t.TempDir(), "nested", "cached.jpg")
	ctx := context.WithValue(context.Background(), contextKey{}, "preserved")
	_, err := CacheHTTPFile(ctx, HTTPFileCacheRequest{
		URL:         "https://cache.test/file",
		Destination: destination,
		Headers:     map[string]string{"Authorization": "Bearer token"},
		Client:      client,
	})
	var statusErr *HTTPStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("error = %v, want *HTTPStatusError", err)
	}
	if statusErr.StatusCode != http.StatusBadGateway {
		t.Fatalf("StatusCode = %d, want %d", statusErr.StatusCode, http.StatusBadGateway)
	}
	if statusErr.URL != "https://cache.test/file" {
		t.Fatalf("URL = %q, want https://cache.test/file", statusErr.URL)
	}
	if got := statusErr.Headers.Get("Authorization"); got != "Bearer token" {
		t.Fatalf("Authorization header = %q, want Bearer token", got)
	}
	message := statusErr.Error()
	if !strings.Contains(message, "https://cache.test/file") || !strings.Contains(message, "Authorization: Bearer token") {
		t.Fatalf("Error() = %q, want url and request headers", message)
	}
	if !body.closed.Load() {
		t.Fatal("response body was not closed")
	}
	assertDirectoryEmpty(t, filepath.Dir(destination))
}

func TestCacheHTTPFileRejectsRedirectResponse(t *testing.T) {
	body := &trackingReadCloser{Reader: http.NoBody}
	client := resty.NewWithClient(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusFound,
			Status:     "302 Found",
			Header:     make(http.Header),
			Body:       body,
			Request:    request,
		}, nil
	})})

	destination := filepath.Join(t.TempDir(), "cached.jpg")
	_, err := CacheHTTPFile(context.Background(), HTTPFileCacheRequest{
		URL:         "https://cache.test/file",
		Destination: destination,
		Client:      client,
	})
	var statusErr *HTTPStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("error = %v, want *HTTPStatusError", err)
	}
	if statusErr.StatusCode != http.StatusFound {
		t.Fatalf("StatusCode = %d, want %d", statusErr.StatusCode, http.StatusFound)
	}
	if !body.closed.Load() {
		t.Fatal("response body was not closed")
	}
	if _, statErr := os.Stat(destination); !os.IsNotExist(statErr) {
		t.Fatalf("destination exists after redirect response: %v", statErr)
	}
}

func TestCacheHTTPFileRejectsNonOKSuccessResponses(t *testing.T) {
	for _, statusCode := range []int{http.StatusNoContent, http.StatusPartialContent} {
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			body := &trackingReadCloser{Reader: http.NoBody}
			client := resty.NewWithClient(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: statusCode,
					Status:     http.StatusText(statusCode),
					Header:     make(http.Header),
					Body:       body,
					Request:    request,
				}, nil
			})})

			destination := filepath.Join(t.TempDir(), "cached.jpg")
			_, err := CacheHTTPFile(context.Background(), HTTPFileCacheRequest{
				URL:         "https://cache.test/file",
				Destination: destination,
				Client:      client,
			})
			var statusErr *HTTPStatusError
			if !errors.As(err, &statusErr) {
				t.Fatalf("error = %v, want *HTTPStatusError", err)
			}
			if statusErr.StatusCode != statusCode {
				t.Fatalf("StatusCode = %d, want %d", statusErr.StatusCode, statusCode)
			}
			if !body.closed.Load() {
				t.Fatal("response body was not closed")
			}
			assertDirectoryEmpty(t, filepath.Dir(destination))
		})
	}
}

func TestCacheHTTPFileRejectsOversizedContentLengthBeforeTempCreation(t *testing.T) {
	body := &trackingReadCloser{Reader: strings.NewReader("abcdef")}
	client := resty.NewWithClient(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusOK,
			Status:        "200 OK",
			Header:        http.Header{"Content-Length": []string{"6"}},
			Body:          body,
			ContentLength: 6,
			Request:       request,
		}, nil
	})})

	destination := filepath.Join(t.TempDir(), "nested", "cached.jpg")
	_, err := CacheHTTPFile(context.Background(), HTTPFileCacheRequest{
		URL:         "https://cache.test/file",
		Destination: destination,
		Client:      client,
		MaxBytes:    5,
	})
	if err == nil || !strings.Contains(err.Error(), "exceeds maximum") {
		t.Fatalf("error = %v, want response size error", err)
	}
	if !body.closed.Load() {
		t.Fatal("response body was not closed")
	}
	assertDirectoryEmpty(t, filepath.Dir(destination))
}

func TestCacheHTTPFileRejectsOversizedChunkedBodyAndCleansTemp(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, "abc")
		writer.(http.Flusher).Flush()
		_, _ = io.WriteString(writer, "def")
	}))
	t.Cleanup(server.Close)

	destination := filepath.Join(t.TempDir(), "nested", "cached.jpg")
	_, err := CacheHTTPFile(context.Background(), HTTPFileCacheRequest{
		URL:         server.URL,
		Destination: destination,
		Client:      resty.New(),
		MaxBytes:    5,
	})
	if err == nil || !strings.Contains(err.Error(), "exceeds maximum") {
		t.Fatalf("error = %v, want response size error", err)
	}
	assertDirectoryEmpty(t, filepath.Dir(destination))
}

func TestCacheHTTPFileDoesNotClobberConcurrentlyCreatedDestination(t *testing.T) {
	responseStarted := make(chan struct{})
	finishResponse := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, "downloaded")
		writer.(http.Flusher).Flush()
		close(responseStarted)
		<-finishResponse
	}))
	t.Cleanup(server.Close)

	destination := filepath.Join(t.TempDir(), "nested", "cached.jpg")
	type cacheCallResult struct {
		result HTTPFileCacheResult
		err    error
	}
	completed := make(chan cacheCallResult, 1)
	go func() {
		result, err := CacheHTTPFile(context.Background(), HTTPFileCacheRequest{
			URL:         server.URL,
			Destination: destination,
			Client:      resty.New(),
		})
		completed <- cacheCallResult{result: result, err: err}
	}()

	<-responseStarted
	if err := os.WriteFile(destination, []byte("winner"), 0o644); err != nil {
		t.Fatal(err)
	}
	close(finishResponse)
	call := <-completed
	if call.err != nil {
		t.Fatal(call.err)
	}
	if !call.result.Existing {
		t.Fatal("Existing = false, want true")
	}
	content, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(content); got != "winner" {
		t.Fatalf("content = %q, want winner", got)
	}
	assertMode(t, destination, 0o644)
	assertNoTemporaryFiles(t, filepath.Dir(destination))
}

func TestCacheHTTPFileCancellationCleansPartialFile(t *testing.T) {
	requestStarted := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(writer, "partial")
		writer.(http.Flusher).Flush()
		close(requestStarted)
		<-request.Context().Done()
	}))
	t.Cleanup(server.Close)

	destination := filepath.Join(t.TempDir(), "nested", "cached.jpg")
	ctx, cancel := context.WithCancel(context.Background())
	completed := make(chan error, 1)
	go func() {
		_, err := CacheHTTPFile(ctx, HTTPFileCacheRequest{
			URL:         server.URL,
			Destination: destination,
			Client:      resty.New(),
		})
		completed <- err
	}()

	<-requestStarted
	waitForDirectoryEntry(t, filepath.Dir(destination))
	cancel()
	if err := <-completed; err == nil {
		t.Fatal("error = nil, want cancellation error")
	}
	assertDirectoryEmpty(t, filepath.Dir(destination))
}

type contextKey struct{}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

type trackingReadCloser struct {
	io.Reader
	closed atomic.Bool
}

func (body *trackingReadCloser) Close() error {
	body.closed.Store(true)
	return nil
}

func waitForDirectoryEntry(t *testing.T, directory string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		entries, err := os.ReadDir(directory)
		if err == nil && len(entries) > 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("temporary file was not created in %s", directory)
}

func assertDirectoryEmpty(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("directory entries = %v, want empty", entries)
	}
}

func assertNoTemporaryFiles(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".http-cache-") {
			t.Fatalf("temporary file remains: %s", entry.Name())
		}
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode for %s = %o, want %o", path, got, want)
	}
}

func assertNoSymlinks(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			t.Fatalf("unexpected symlink %s", entry.Name())
		}
	}
}
