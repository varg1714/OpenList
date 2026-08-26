package pornhub

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
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

func TestGetVideoLinkLogsStatusCodeAndHTMLWhenScriptFails(t *testing.T) {
	var logBuffer bytes.Buffer
	oldOut := utils.Log.Out
	utils.Log.Out = &logBuffer
	t.Cleanup(func() { utils.Log.Out = oldOut })

	rawHTML := `<!DOCTYPE html>
<html>
<head><title>Some Video - Pornhub.com</title></head>
<body>
<script>flashvars_123 = { mediaDefinitions: foo?.bar };</script>
</body>
</html>`
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusForbidden)
		_, _ = response.Write([]byte(rawHTML))
	}))
	t.Cleanup(server.Close)

	_, err := (&Pornhub{Addition: Addition{ServerUrl: server.URL}}).getVideoLink(context.Background(), "live-key")
	if err == nil {
		t.Fatal("getVideoLink error = nil, want script error")
	}
	logged := logBuffer.String()
	if !strings.Contains(logged, "403") {
		t.Fatalf("log does not contain response status code 403:\n%s", logged)
	}
	if !strings.Contains(logged, "flashvars_123") {
		t.Fatalf("log does not contain raw html:\n%s", logged)
	}
}

func TestIsPornhubVideoDisabledReadsConfigKeywords(t *testing.T) {
	tests := []struct {
		name         string
		keywords     string
		html         string
		wantDisabled bool
	}{
		{"default english keyword", "", "Video Disabled - Pornhub.com", true},
		{"default chinese keyword", "", "此视频已下架", true},
		{"default keyword absent", "", "Some Video - Pornhub.com", false},
		{"config keyword matches case-insensitively", "custom disabled", "CUSTOM DISABLED", true},
		{"config keyword replaces defaults", "custom disabled", "Video Disabled", false},
		{"config keyword absent", "custom disabled", "Some Video", false},
		{"blank config falls back to defaults", " , ", "video disabled", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &Pornhub{Addition: Addition{DisabledKeywords: tt.keywords}}
			if got := d.isPornhubVideoDisabled(tt.html); got != tt.wantDisabled {
				t.Fatalf("isPornhubVideoDisabled(%q) with keywords %q = %v, want %v", tt.html, tt.keywords, got, tt.wantDisabled)
			}
		})
	}
}
