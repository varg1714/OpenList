package javdb

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
)

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
	if err == nil || response == nil || response.StatusCode() != http.StatusFound {
		t.Fatalf("redirect response = %v, error = %v", response, err)
	}
	if redirected.Load() {
		t.Fatal("redirect target was requested")
	}
}

func TestNewSampleImageClientEnforcesTLSVerification(t *testing.T) {
	previous := conf.Conf.TlsInsecureSkipVerify
	conf.Conf.TlsInsecureSkipVerify = true
	t.Cleanup(func() { conf.Conf.TlsInsecureSkipVerify = previous })

	transport, ok := newSampleImageClient().GetClient().Transport.(*http.Transport)
	if !ok || transport.TLSClientConfig == nil || transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatalf("sample transport = %#v, want verified TLS", transport)
	}
}

func TestNewSampleImageClientDisablesRetries(t *testing.T) {
	var attempts atomic.Int32
	client := newSampleImageClient().SetTransport(sampleRoundTripFunc(func(*http.Request) (*http.Response, error) {
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
		{name: "maps covers", image: "https://img.jdbstatic.com/cdn/covers/abc.jpg?token=secret", index: 1, want: "https://img.jdbstatic.com/cdn/samples/abc_l_1.jpg?token=secret"},
		{name: "last supported index", image: "https://jdbstatic.com/covers/abc.jpg", index: 50, want: "https://jdbstatic.com/samples/abc_l_50.jpg"},
		{name: "rejects zero", image: "https://jdbstatic.com/covers/abc.jpg", index: 0, wantErr: true},
		{name: "rejects untrusted host", image: "https://example.com/covers/abc.jpg", index: 1, wantErr: true},
		{name: "rejects escaped path", image: "https://jdbstatic.com/covers%2Fnested/abc.jpg", index: 1, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := sampleImageURL(test.image, test.index)
			if test.wantErr {
				if err == nil {
					t.Fatal("error = nil, want error")
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("sample URL = %q, error = %v, want %q", got, err, test.want)
			}
		})
	}
}

type sampleRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn sampleRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}
