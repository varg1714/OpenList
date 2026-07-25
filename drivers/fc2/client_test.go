package fc2

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/go-resty/resty/v2"
)

func TestFC2HTTPClientIsUnexportedTestSeam(t *testing.T) {
	field, ok := reflect.TypeOf(FC2{}).FieldByName("client")
	if !ok || field.PkgPath == "" {
		t.Fatal("FC2.client must be present and unexported")
	}
}

func TestFC2HTTPClientDisablesRetriesAndRedirects(t *testing.T) {
	if got := newFC2HTTPClient().RetryCount; got != 0 {
		t.Fatalf("retry count = %d, want 0", got)
	}
	redirectedRequests := 0
	target := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		redirectedRequests++
		response.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, target.URL, http.StatusFound)
	}))
	defer redirect.Close()

	response, err := newFC2HTTPClient().R().Get(redirect.URL)
	if err == nil || response == nil || response.StatusCode() != http.StatusFound || redirectedRequests != 0 {
		t.Fatalf("redirect response = %v, error = %v, redirected requests = %d", response, err, redirectedRequests)
	}
}

func TestGetWhatLinkInfoChecksFailuresAndDecodesScreenshots(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		body      string
		transport error
		wantErr   bool
	}{
		{name: "transport", transport: errors.New("offline"), wantErr: true},
		{name: "status", status: http.StatusBadGateway, body: `{}`, wantErr: true},
		{name: "decode", status: http.StatusOK, body: `{`, wantErr: true},
		{name: "api error", status: http.StatusOK, body: `{"error":"not found"}`, wantErr: true},
		{name: "success", status: http.StatusOK, body: `{"screenshots":[{"time":2,"screenshot":"https://shots.test/two"}]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			driver := &FC2{client: resty.New().SetTransport(fc2ClientRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				if test.transport != nil {
					return nil, test.transport
				}
				return fc2TestResponse(request, test.status, test.body), nil
			}))}
			info, err := driver.getWhatLinkInfo("magnet:test")
			if test.wantErr {
				if err == nil {
					t.Fatal("getWhatLinkInfo error = nil")
				}
				return
			}
			if err != nil || len(info.Screenshots) != 1 || info.Screenshots[0].Screenshot != "https://shots.test/two" {
				t.Fatalf("screenshots = %+v, error = %v", info.Screenshots, err)
			}
		})
	}
}

type fc2ClientRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn fc2ClientRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func fc2TestResponse(request *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    request,
	}
}
