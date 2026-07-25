package virtual_file

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/cmd/flags"
	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/go-resty/resty/v2"
	"github.com/stretchr/testify/require"
)

func TestCacheImageReturnsDetailedErrorWhenPosterDownloadFails(t *testing.T) {
	// Given
	oldDataDir := flags.DataDir
	oldClient := base.RestyClient
	flags.DataDir = t.TempDir()
	base.RestyClient = resty.New()
	t.Cleanup(func() {
		flags.DataDir = oldDataDir
		base.RestyClient = oldClient
	})
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	identity := MediaIdentity{StorageID: 7, Source: "fc2", PrimaryDir: "actor", Code: "FC2-PPV-7"}

	// When
	result, err := CacheImageWithError(MediaInfo{Identity: &identity, ImgUrl: server.URL + "/poster.jpg"})

	// Then
	require.Equal(t, CreatedFailed, result)
	require.ErrorContains(t, err, "HTTP 503")
}
