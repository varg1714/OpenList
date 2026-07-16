package virtual_file

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

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
}

func (err *HTTPStatusError) Error() string {
	return fmt.Sprintf("unexpected HTTP status: %d", err.StatusCode)
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
		return HTTPFileCacheResult{}, &HTTPStatusError{StatusCode: response.StatusCode()}
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
