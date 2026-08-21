package virtual_file

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/go-resty/resty/v2"
)

const defaultHTTPFileCacheMaxBytes int64 = 32 << 20

type HTTPFileCacheRequest struct {
	URL         string
	Destination string
	Headers     map[string]string
	Client      *resty.Client
	MaxBytes    int64
}

type HTTPFileCacheResult struct {
	Existing bool
}

type HTTPStatusError struct {
	StatusCode int
	URL        string
	Headers    http.Header
}

func (err *HTTPStatusError) Error() string {
	message := fmt.Sprintf("unexpected HTTP status: %d", err.StatusCode)
	if err.URL != "" {
		message += "; url=" + err.URL
	}
	if formatted := formatHTTPRequestHeaders(err.Headers); formatted != "" {
		message += "; headers=" + formatted
	}
	return message
}

func formatHTTPRequestHeaders(headers http.Header) string {
	if len(headers) == 0 {
		return ""
	}
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+": "+strings.Join(headers[key], ", "))
	}
	return strings.Join(parts, "; ")
}

func newHTTPStatusError(response *resty.Response, request HTTPFileCacheRequest) *HTTPStatusError {
	statusErr := &HTTPStatusError{StatusCode: response.StatusCode(), URL: request.URL}
	if raw := response.RawResponse; raw != nil && raw.Request != nil {
		if raw.Request.URL != nil {
			statusErr.URL = raw.Request.URL.String()
		}
		statusErr.Headers = raw.Request.Header.Clone()
	} else if len(request.Headers) > 0 {
		statusErr.Headers = make(http.Header, len(request.Headers))
		for key, value := range request.Headers {
			statusErr.Headers.Set(key, value)
		}
	}
	return statusErr
}

func CacheHTTPFile(ctx context.Context, request HTTPFileCacheRequest) (HTTPFileCacheResult, error) {
	info, err := os.Lstat(request.Destination)
	if err == nil {
		if info.Mode().IsRegular() {
			return HTTPFileCacheResult{Existing: true}, nil
		}
		return HTTPFileCacheResult{}, fmt.Errorf("cache destination is not a regular file: %s", request.Destination)
	}
	if !os.IsNotExist(err) {
		return HTTPFileCacheResult{}, err
	}

	directory := filepath.Dir(request.Destination)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return HTTPFileCacheResult{}, err
	}

	client := request.Client
	if client == nil {
		client = base.RestyClient
	}
	if client == nil {
		client = base.NewRestyClient()
	}
	response, err := client.R().
		SetContext(ctx).
		SetHeaders(request.Headers).
		SetDoNotParseResponse(true).
		Get(request.URL)
	if err != nil {
		return HTTPFileCacheResult{}, err
	}
	body := response.RawBody()
	defer body.Close()
	if response.StatusCode() != 200 {
		return HTTPFileCacheResult{}, newHTTPStatusError(response, request)
	}
	maxBytes := request.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultHTTPFileCacheMaxBytes
	}
	if response.RawResponse.ContentLength > maxBytes {
		return HTTPFileCacheResult{}, fmt.Errorf("HTTP response size %d exceeds maximum %d", response.RawResponse.ContentLength, maxBytes)
	}

	temporary, err := os.CreateTemp(directory, ".http-cache-*")
	if err != nil {
		return HTTPFileCacheResult{}, err
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()

	written, err := io.Copy(temporary, io.LimitReader(body, maxBytes+1))
	if err != nil {
		return HTTPFileCacheResult{}, err
	}
	if written > maxBytes {
		return HTTPFileCacheResult{}, fmt.Errorf("HTTP response size exceeds maximum %d", maxBytes)
	}
	if err := temporary.Chmod(0o644); err != nil {
		return HTTPFileCacheResult{}, err
	}
	if err := temporary.Close(); err != nil {
		return HTTPFileCacheResult{}, err
	}
	if err := os.Link(temporaryPath, request.Destination); err != nil {
		if errors.Is(err, os.ErrExist) {
			info, statErr := os.Lstat(request.Destination)
			if statErr == nil && info.Mode().IsRegular() {
				return HTTPFileCacheResult{Existing: true}, nil
			}
			if statErr == nil {
				return HTTPFileCacheResult{}, fmt.Errorf("cache destination is not a regular file: %s", request.Destination)
			}
		}
		return HTTPFileCacheResult{}, err
	}
	return HTTPFileCacheResult{}, nil
}
