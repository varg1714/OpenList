package tool

import (
	"context"
	"errors"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/stretchr/testify/require"
)

type cloudPlayTestDriver struct {
	driver.Driver
}

type staleCloudPlayTestDriver struct {
	driver.Driver
}

func (staleCloudPlayTestDriver) Link(context.Context, model.Obj, model.LinkArgs) (*model.Link, error) {
	return &model.Link{URL: "https://stale.example/video"}, errors.New("remote file missing")
}

func TestCloudPlayUsesCanonicalCacheBeforeSourceMagnets(t *testing.T) {
	oldStorage := getCloudPlayStorage
	oldQuery := queryCloudCache
	oldResolve := resolveCloudCacheLink
	oldAttempt := attemptCloudMagnet
	t.Cleanup(func() {
		getCloudPlayStorage = oldStorage
		queryCloudCache = oldQuery
		resolveCloudCacheLink = oldResolve
		attemptCloudMagnet = oldAttempt
	})

	getCloudPlayStorage = func(string) driver.Driver { return cloudPlayTestDriver{} }
	queryCloudCache = func(driverType, name string) model.MagnetCache {
		require.Equal(t, "PikPak", driverType)
		require.Equal(t, "ABC-001.mp4", name)
		return model.MagnetCache{FileId: "cached-file", Name: name, DriverType: driverType}
	}
	resolveCloudCacheLink = func(context.Context, model.LinkArgs, string, driver.Driver, model.MagnetCache) (*model.Link, error) {
		return &model.Link{URL: "https://cache.example/video"}, nil
	}
	attempts := 0
	attemptCloudMagnet = func(context.Context, cloudPlaySession, model.SourceMagnet) (*model.Link, error) {
		attempts++
		return nil, errors.New("unexpected magnet attempt")
	}

	link, err := CloudPlay(context.Background(), model.LinkArgs{}, "PikPak", "/cloud", playbackFile("ABC-001.mp4"), []model.SourceMagnet{{MagnetURI: "magnet:?xt=one", Fingerprint: "one"}})

	require.NoError(t, err)
	require.Equal(t, "https://cache.example/video", link.URL)
	require.Zero(t, attempts)
}

func TestCloudPlayKeepsStaleRemoteCacheAndTriesSourceMagnets(t *testing.T) {
	oldStorage := getCloudPlayStorage
	oldQuery := queryCloudCache
	oldResolve := resolveCloudCacheLink
	oldAttempt := attemptCloudMagnet
	oldUpdate := updateSourceMagnetError
	oldSelect := selectPlaybackMagnet
	t.Cleanup(func() {
		getCloudPlayStorage = oldStorage
		queryCloudCache = oldQuery
		resolveCloudCacheLink = oldResolve
		attemptCloudMagnet = oldAttempt
		updateSourceMagnetError = oldUpdate
		selectPlaybackMagnet = oldSelect
	})

	getCloudPlayStorage = func(string) driver.Driver { return cloudPlayTestDriver{} }
	cache := model.MagnetCache{ID: 7, FileId: "stale-file", Name: "ABC-001.mp4", DriverType: "115 Cloud"}
	queryCloudCache = func(string, string) model.MagnetCache { return cache }
	resolveCloudCacheLink = func(context.Context, model.LinkArgs, string, driver.Driver, model.MagnetCache) (*model.Link, error) {
		return nil, errors.New("remote file no longer exists")
	}
	attemptCloudMagnet = func(_ context.Context, _ cloudPlaySession, magnet model.SourceMagnet) (*model.Link, error) {
		return &model.Link{URL: "https://download.example/" + magnet.Fingerprint}, nil
	}
	updateSourceMagnetError = func(uint, string) error { return nil }
	selectPlaybackMagnet = func(uint, uint) error { return nil }

	link, err := CloudPlay(context.Background(), model.LinkArgs{}, "115 Cloud", "/cloud", playbackFile("ABC-001.mp4"), []model.SourceMagnet{{ID: 11, WorkID: 3, MagnetURI: "magnet:?xt=one", Fingerprint: "one"}})

	require.NoError(t, err)
	require.Equal(t, "https://download.example/one", link.URL)
	require.Equal(t, uint(7), cache.ID)
}

func TestGetLinkByCacheDiscardsLinkReturnedWithError(t *testing.T) {
	for _, driverType := range []string{"PikPak", "115 Cloud"} {
		t.Run(driverType, func(t *testing.T) {
			link, err := getLinkByCache(
				context.Background(),
				model.LinkArgs{},
				driverType,
				staleCloudPlayTestDriver{},
				model.MagnetCache{FileId: "stale", Option: map[string]string{"pickCode": "stale"}},
			)

			require.NoError(t, err)
			require.Nil(t, link)
		})
	}
}

func TestCloudPlayRetriesDistinctMagnetsWithOneConfiguredProvider(t *testing.T) {
	oldStorage := getCloudPlayStorage
	oldQuery := queryCloudCache
	oldAttempt := attemptCloudMagnet
	oldUpdate := updateSourceMagnetError
	oldSelect := selectPlaybackMagnet
	t.Cleanup(func() {
		getCloudPlayStorage = oldStorage
		queryCloudCache = oldQuery
		attemptCloudMagnet = oldAttempt
		updateSourceMagnetError = oldUpdate
		selectPlaybackMagnet = oldSelect
	})

	getCloudPlayStorage = func(string) driver.Driver { return cloudPlayTestDriver{} }
	queryCloudCache = func(string, string) model.MagnetCache { return model.MagnetCache{} }
	var attempted []string
	attemptCloudMagnet = func(_ context.Context, session cloudPlaySession, magnet model.SourceMagnet) (*model.Link, error) {
		require.Equal(t, "PikPak", session.driverType)
		attempted = append(attempted, magnet.Fingerprint)
		if magnet.Fingerprint == "selected" {
			return nil, errors.New("selected magnet failed")
		}
		return &model.Link{URL: "https://download.example/success"}, nil
	}
	errorsByID := make(map[uint]string)
	updateSourceMagnetError = func(id uint, message string) error {
		errorsByID[id] = message
		return nil
	}
	var selectedID uint
	selectPlaybackMagnet = func(workID, magnetID uint) error {
		require.Equal(t, uint(9), workID)
		selectedID = magnetID
		return nil
	}

	link, err := CloudPlay(context.Background(), model.LinkArgs{}, "PikPak", "/cloud", playbackFile("ABC-001.mp4"), []model.SourceMagnet{
		{ID: 1, WorkID: 9, MagnetURI: "magnet:?xt=selected", Fingerprint: "selected", Selected: true},
		{ID: 2, WorkID: 9, MagnetURI: "magnet:?xt=next", Fingerprint: "next", Priority: 1},
		{ID: 3, WorkID: 9, MagnetURI: "magnet:?xt=duplicate", Fingerprint: "selected", Priority: 2},
	})

	require.NoError(t, err)
	require.Equal(t, "https://download.example/success", link.URL)
	require.Equal(t, []string{"selected", "next"}, attempted)
	require.Equal(t, "selected magnet failed", errorsByID[1])
	require.Empty(t, errorsByID[2])
	require.Equal(t, uint(2), selectedID)
}

func playbackFile(name string) *model.EmbyFileObj {
	return &model.EmbyFileObj{ObjThumb: model.ObjThumb{Object: model.Object{Name: name, Path: "actor"}}}
}
