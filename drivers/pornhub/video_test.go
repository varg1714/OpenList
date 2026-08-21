package pornhub

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetVideoLinkReturnsVideoDisabledWithoutRunningScript(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests++
		if request.URL.Path != "/view_video.php" {
			t.Errorf("unexpected path %q", request.URL.Path)
		}
		_, _ = response.Write([]byte(`<!DOCTYPE html>
<html>
<head><title>Video Disabled - Pornhub.com</title></head>
<body>
<script>flashvars_123 = { mediaDefinitions: foo?.bar };</script>
</body>
</html>`))
	}))
	t.Cleanup(server.Close)

	_, err := (&Pornhub{Addition: Addition{ServerUrl: server.URL}}).getVideoLink(context.Background(), "disabled-key")
	if !errors.Is(err, ErrVideoDisabled) {
		t.Fatalf("getVideoLink error = %v, want ErrVideoDisabled", err)
	}
	if requests != 1 {
		t.Fatalf("HTTP requests = %d, want 1", requests)
	}
}

func TestGetVideoLinkKeepsScriptErrorsDistinctFromDisabledVideo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(`<!DOCTYPE html>
<html>
<head><title>Some Video - Pornhub.com</title></head>
<body>
<script>flashvars_123 = { mediaDefinitions: foo?.bar };</script>
</body>
</html>`))
	}))
	t.Cleanup(server.Close)

	_, err := (&Pornhub{Addition: Addition{ServerUrl: server.URL}}).getVideoLink(context.Background(), "live-key")
	if err == nil {
		t.Fatal("getVideoLink error = nil, want script error")
	}
	if errors.Is(err, ErrVideoDisabled) {
		t.Fatalf("getVideoLink error = %v, want a script error not ErrVideoDisabled", err)
	}
}
