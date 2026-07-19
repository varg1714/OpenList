package tool

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"gorm.io/gorm"
)

func TestCloudPlayMediaUsesFingerprintScopedCacheHit(t *testing.T) {
	request, deps, state := newMediaCloudPlayTest(t)
	deps.getCloudFileCache = func(storageIdentity string, filmFileID uint, fingerprint string) (model.CloudFileCache, error) {
		if storageIdentity != "42" || filmFileID != request.File.ID || fingerprint != "magnet-current" {
			t.Fatalf("cache lookup = (%q, %d, %q)", storageIdentity, filmFileID, fingerprint)
		}
		return model.CloudFileCache{
			FilmFileID:        request.File.ID,
			StorageIdentity:   storageIdentity,
			RemoteFileID:      "cached-remote",
			MagnetFingerprint: fingerprint,
		}, nil
	}

	result, err := (&mediaCloudPlayer{deps: deps}).play(context.Background(), model.LinkArgs{}, request)
	if err != nil {
		t.Fatalf("play cached media: %v", err)
	}
	if !result.CacheHit || result.Requested.RemoteFileID != "cached-remote" {
		t.Fatalf("result = %+v, want cache hit", result)
	}
	if state.downloadCalls != 0 {
		t.Fatalf("download calls = %d, want 0", state.downloadCalls)
	}
}

func TestCloudPlayMediaTreatsStaleFingerprintAsMiss(t *testing.T) {
	request, deps, state := newMediaCloudPlayTest(t)
	var lookupFingerprint string
	deps.getCloudFileCache = func(_ string, _ uint, fingerprint string) (model.CloudFileCache, error) {
		lookupFingerprint = fingerprint
		if fingerprint == "magnet-stale" {
			return model.CloudFileCache{RemoteFileID: "stale-remote"}, nil
		}
		return model.CloudFileCache{}, gorm.ErrRecordNotFound
	}

	result, err := (&mediaCloudPlayer{deps: deps}).play(context.Background(), model.LinkArgs{}, request)
	if err != nil {
		t.Fatalf("play stale cache media: %v", err)
	}
	if lookupFingerprint != "magnet-current" {
		t.Fatalf("lookup fingerprint = %q, want current fingerprint", lookupFingerprint)
	}
	if result.CacheHit || state.downloadCalls != 1 {
		t.Fatalf("result = %+v, download calls = %d; want provider fetch", result, state.downloadCalls)
	}
}

func TestCloudPlayMediaFetchesProviderWhenCacheMissing(t *testing.T) {
	request, deps, state := newMediaCloudPlayTest(t)
	deps.getCloudFileCache = func(string, uint, string) (model.CloudFileCache, error) {
		return model.CloudFileCache{}, gorm.ErrRecordNotFound
	}

	result, err := (&mediaCloudPlayer{deps: deps}).play(context.Background(), model.LinkArgs{}, request)
	if err != nil {
		t.Fatalf("play uncached media: %v", err)
	}
	if state.downloadCalls != 1 || state.listCalls != 1 || state.replaceCalls != 1 {
		t.Fatalf("provider calls = download %d, list %d, replace %d; want one each", state.downloadCalls, state.listCalls, state.replaceCalls)
	}
	if result.CacheHit || len(result.Candidates) != 2 {
		t.Fatalf("result = %+v, want two fresh sibling candidates", result)
	}
}

func TestCloudPlayMediaEnrichesSingleEmptyFilmFileFromManifest(t *testing.T) {
	request, deps, state := newMediaCloudPlayTest(t)
	request.File.FilmFile = model.FilmFile{ID: 101, WorkID: 10, PartIndex: 1, PartCount: 1}
	request.SelectedMagnet = &model.SourceMagnet{
		WorkID:      request.File.WorkID,
		MagnetURI:   "magnet:?xt=urn:btih:single-empty",
		Fingerprint: "single-empty-fingerprint",
		FileManifest: model.MagnetFileManifest{
			{Path: "release/movie.mp4", Size: 1234, Fingerprint: "single-file-fingerprint"},
		},
	}
	deps.listFilmFiles = func(uint) ([]model.FilmFile, error) {
		return []model.FilmFile{request.File.FilmFile}, nil
	}
	deps.download = func(context.Context, CloudPlayProviderRequest) (CloudPlayProviderDownload, error) {
		state.downloadCalls++
		return CloudPlayProviderDownload{RootRemoteID: "download-root"}, nil
	}
	deps.replaceCloudFileCaches = func(string, string, []model.CloudFileCache) error {
		state.replaceCalls++
		return nil
	}
	deps.listRemote = func(context.Context, driver.Driver, string) ([]RemoteFileEvidence, error) {
		return []RemoteFileEvidence{{ID: "remote-single", Path: "release/movie.mp4", Size: 1234}}, nil
	}

	result, err := (&mediaCloudPlayer{deps: deps}).play(context.Background(), model.LinkArgs{}, request)
	if err != nil {
		t.Fatalf("play single empty-evidence media: %v", err)
	}
	if result.Requested.RemoteFileID != "remote-single" || state.linkedCache.RemoteFileID != "remote-single" {
		t.Fatalf("cached/link IDs = %q/%q, want remote-single", result.Requested.RemoteFileID, state.linkedCache.RemoteFileID)
	}
}

func TestCloudPlayMediaEnrichesMultipartEmptyFilmFilesFromManifest(t *testing.T) {
	request, deps, state := newMediaCloudPlayTest(t)
	request.File.FilmFile = model.FilmFile{ID: 101, WorkID: 10, PartIndex: 1, PartCount: 2}
	request.SelectedMagnet = &model.SourceMagnet{
		WorkID:      request.File.WorkID,
		MagnetURI:   "magnet:?xt=urn:btih=multipart-empty",
		Fingerprint: "multipart-empty-fingerprint",
		FileManifest: model.MagnetFileManifest{
			{Path: "release/part-1.mkv", Size: 1111, Fingerprint: "part-1-fingerprint"},
			{Path: "release/part-2.mkv", Size: 2222, Fingerprint: "part-2-fingerprint"},
		},
	}
	files := []model.FilmFile{
		{ID: 101, WorkID: 10, PartIndex: 1, PartCount: 2},
		{ID: 102, WorkID: 10, PartIndex: 2, PartCount: 2},
	}
	deps.listFilmFiles = func(uint) ([]model.FilmFile, error) { return files, nil }
	deps.download = func(context.Context, CloudPlayProviderRequest) (CloudPlayProviderDownload, error) {
		state.downloadCalls++
		return CloudPlayProviderDownload{RootRemoteID: "download-root"}, nil
	}
	deps.replaceCloudFileCaches = func(string, string, []model.CloudFileCache) error {
		state.replaceCalls++
		return nil
	}
	deps.listRemote = func(context.Context, driver.Driver, string) ([]RemoteFileEvidence, error) {
		return []RemoteFileEvidence{
			{ID: "remote-part-2", Path: "release/part-2.mkv", Size: 2222},
			{ID: "remote-part-1", Path: "release/part-1.mkv", Size: 1111},
		}, nil
	}

	result, err := (&mediaCloudPlayer{deps: deps}).play(context.Background(), model.LinkArgs{}, request)
	if err != nil {
		t.Fatalf("play multipart empty-evidence media: %v", err)
	}
	if result.Requested.RemoteFileID != "remote-part-1" || state.linkedCache.RemoteFileID != "remote-part-1" {
		t.Fatalf("requested/link IDs = %q/%q, want remote-part-1", result.Requested.RemoteFileID, state.linkedCache.RemoteFileID)
	}
	if len(result.Candidates) != 2 || result.Candidates[1].RemoteFileID != "remote-part-2" {
		t.Fatalf("candidates = %#v, want both manifest-derived siblings", result.Candidates)
	}
}

func TestCloudPlayMediaRejectsAmbiguousManifestEnrichmentTopology(t *testing.T) {
	request, deps, state := newMediaCloudPlayTest(t)
	request.File.FilmFile = model.FilmFile{ID: 101, WorkID: 10, PartIndex: 1, PartCount: 2}
	files := []model.FilmFile{
		{ID: 101, WorkID: 10, PartIndex: 1, PartCount: 2},
		{ID: 102, WorkID: 10, PartIndex: 1, PartCount: 2},
	}
	deps.listFilmFiles = func(uint) ([]model.FilmFile, error) { return files, nil }
	deps.download = func(context.Context, CloudPlayProviderRequest) (CloudPlayProviderDownload, error) {
		return CloudPlayProviderDownload{RootRemoteID: "download-root"}, nil
	}
	deps.listRemote = func(context.Context, driver.Driver, string) ([]RemoteFileEvidence, error) {
		return []RemoteFileEvidence{
			{ID: "remote-part-1", Path: "disc-1.mp4", Size: 100},
			{ID: "remote-part-2", Path: "disc-2.mp4", Size: 200},
		}, nil
	}

	_, err := (&mediaCloudPlayer{deps: deps}).play(context.Background(), model.LinkArgs{}, request)
	if !errors.Is(err, ErrCloudPlayManifestEnrichment) {
		t.Fatalf("error = %v, want explicit enrichment ambiguity", err)
	}
	if state.replaceCalls != 0 {
		t.Fatalf("replace calls = %d, want no cache write", state.replaceCalls)
	}
}

func TestCloudPlayMediaRejectsManifestMismatchWithoutCaching(t *testing.T) {
	request, deps, state := newMediaCloudPlayTest(t)
	deps.getCloudFileCache = func(string, uint, string) (model.CloudFileCache, error) {
		return model.CloudFileCache{}, gorm.ErrRecordNotFound
	}
	deps.listRemote = func(context.Context, driver.Driver, string) ([]RemoteFileEvidence, error) {
		state.listCalls++
		return []RemoteFileEvidence{{ID: "wrong", Path: "disc-1.mp4", Size: 999}}, nil
	}

	_, err := (&mediaCloudPlayer{deps: deps}).play(context.Background(), model.LinkArgs{}, request)
	if !errors.Is(err, ErrRemoteFileNotMatched) {
		t.Fatalf("error = %v, want manifest mismatch", err)
	}
	if state.replaceCalls != 0 {
		t.Fatalf("replace calls = %d, want no cache write", state.replaceCalls)
	}
}

func TestCloudPlayMediaPreservesExactRemoteIDs(t *testing.T) {
	request, deps, state := newMediaCloudPlayTest(t)
	deps.getCloudFileCache = func(string, uint, string) (model.CloudFileCache, error) {
		return model.CloudFileCache{}, gorm.ErrRecordNotFound
	}
	exactID := "01HZX/opaque:remote-id"
	deps.listRemote = func(context.Context, driver.Driver, string) ([]RemoteFileEvidence, error) {
		return []RemoteFileEvidence{
			{ID: "sibling-id", Path: "disc-2.mp4", Size: 200},
			{ID: exactID, Path: "disc-1.mp4", Size: 100},
		}, nil
	}

	result, err := (&mediaCloudPlayer{deps: deps}).play(context.Background(), model.LinkArgs{}, request)
	if err != nil {
		t.Fatalf("play exact remote ID media: %v", err)
	}
	if result.Requested.RemoteFileID != exactID || state.linkedCache.RemoteFileID != exactID {
		t.Fatalf("requested/link IDs = %q/%q, want %q", result.Requested.RemoteFileID, state.linkedCache.RemoteFileID, exactID)
	}
}

func TestCloudPlayMediaPropagatesProviderOptions(t *testing.T) {
	request, deps, state := newMediaCloudPlayTest(t)
	request.ProviderOptions = map[string]string{"quality": "original", "pickCode": "request-default"}
	deps.getCloudFileCache = func(string, uint, string) (model.CloudFileCache, error) {
		return model.CloudFileCache{}, gorm.ErrRecordNotFound
	}
	deps.download = func(_ context.Context, got CloudPlayProviderRequest) (CloudPlayProviderDownload, error) {
		state.downloadCalls++
		if !reflect.DeepEqual(got.Options, request.ProviderOptions) {
			t.Fatalf("download options = %#v, want %#v", got.Options, request.ProviderOptions)
		}
		return CloudPlayProviderDownload{RootRemoteID: "download-root"}, nil
	}
	deps.listRemote = func(context.Context, driver.Driver, string) ([]RemoteFileEvidence, error) {
		return []RemoteFileEvidence{
			{ID: "remote-1", Path: "disc-1.mp4", Size: 100, Options: map[string]string{"pickCode": "exact-pick"}},
			{ID: "remote-2", Path: "disc-2.mp4", Size: 200},
		}, nil
	}

	result, err := (&mediaCloudPlayer{deps: deps}).play(context.Background(), model.LinkArgs{}, request)
	if err != nil {
		t.Fatalf("play media with provider options: %v", err)
	}
	want := map[string]string{"quality": "original", "pickCode": "exact-pick"}
	if !reflect.DeepEqual(result.Requested.ProviderOptions, want) || !reflect.DeepEqual(state.linkedCache.ProviderOptions, want) {
		t.Fatalf("cached/link options = %#v/%#v, want %#v", result.Requested.ProviderOptions, state.linkedCache.ProviderOptions, want)
	}
}

func TestCloudPlayMediaFetchesAndSelectsMissingSourceMagnet(t *testing.T) {
	request, deps, state := newMediaCloudPlayTest(t)
	request.SelectedMagnet = nil
	getCalls := 0
	deps.getSelectedSourceMagnet = func(uint) (model.SourceMagnet, error) {
		getCalls++
		if getCalls == 1 {
			return model.SourceMagnet{}, gorm.ErrRecordNotFound
		}
		return testSelectedMagnet(request.File.WorkID), nil
	}
	request.MagnetGetter = func(_ context.Context, work model.FilmWork) ([]model.SourceMagnet, error) {
		if work.ID != request.File.WorkID {
			t.Fatalf("getter work ID = %d, want %d", work.ID, request.File.WorkID)
		}
		return []model.SourceMagnet{testSelectedMagnet(work.ID)}, nil
	}
	deps.upsertSourceMagnets = func(workID uint, magnets []model.SourceMagnet) error {
		state.upsertCalls++
		if workID != request.File.WorkID || len(magnets) != 1 {
			t.Fatalf("upsert = work %d, magnets %#v", workID, magnets)
		}
		return nil
	}
	deps.getCloudFileCache = func(string, uint, string) (model.CloudFileCache, error) {
		return model.CloudFileCache{}, gorm.ErrRecordNotFound
	}

	if _, err := (&mediaCloudPlayer{deps: deps}).play(context.Background(), model.LinkArgs{}, request); err != nil {
		t.Fatalf("play media after magnet fetch: %v", err)
	}
	if getCalls != 2 || state.upsertCalls != 1 {
		t.Fatalf("selected reads = %d, upserts = %d; want 2/1", getCalls, state.upsertCalls)
	}
}

func TestCloudPlayMediaRetriesDownloadAfterCacheLinkNotFound(t *testing.T) {
	request, deps, state := newMediaCloudPlayTest(t)
	var cacheLinkCalls int
	cacheHit := model.CloudFileCache{
		FilmFileID:        request.File.ID,
		StorageIdentity:   "42",
		RemoteFileID:      "stale-remote",
		MagnetFingerprint: request.SelectedMagnet.Fingerprint,
	}
	deps.getCloudFileCache = func(string, uint, string) (model.CloudFileCache, error) {
		return cacheHit, nil
	}
	deleteCalls := 0
	var deletedStorageIdentity string
	var deletedFilmFileID uint
	deps.deleteCloudFileCache = func(storageIdentity string, filmFileID uint) error {
		deleteCalls++
		deletedStorageIdentity = storageIdentity
		deletedFilmFileID = filmFileID
		return nil
	}
	deps.linkRemote = func(_ context.Context, _ driver.Driver, cache model.CloudFileCache, _ model.LinkArgs) (*model.Link, error) {
		cacheLinkCalls++
		if cacheLinkCalls == 1 {
			return nil, errors.New("remote file not found")
		}
		state.linkedCache = cache
		return &model.Link{URL: "https://example.test/" + cache.RemoteFileID}, nil
	}

	result, err := (&mediaCloudPlayer{deps: deps}).play(context.Background(), model.LinkArgs{}, request)
	if err != nil {
		t.Fatalf("play with one link retry: %v", err)
	}
	if deleteCalls != 1 {
		t.Fatalf("delete calls = %d, want 1", deleteCalls)
	}
	if deletedStorageIdentity != "42" || deletedFilmFileID != request.File.ID {
		t.Fatalf("delete scope = (%q, %d), want (%q, %d)", deletedStorageIdentity, deletedFilmFileID, "42", request.File.ID)
	}
	if cacheLinkCalls != 2 {
		t.Fatalf("link calls = %d, want 2 (one failure, one success)", cacheLinkCalls)
	}
	if state.downloadCalls != 1 {
		t.Fatalf("download calls = %d, want 1 re-download", state.downloadCalls)
	}
	if result.CacheHit {
		t.Fatalf("result.CacheHit = true, want false after re-download")
	}
}

func TestCloudPlayMediaErrorsAfterSecondLinkFailureNoLoop(t *testing.T) {
	request, deps, _ := newMediaCloudPlayTest(t)
	cacheHit := model.CloudFileCache{
		FilmFileID:        request.File.ID,
		StorageIdentity:   "42",
		RemoteFileID:      "orphan-remote",
		MagnetFingerprint: request.SelectedMagnet.Fingerprint,
	}
	deps.getCloudFileCache = func(string, uint, string) (model.CloudFileCache, error) {
		return cacheHit, nil
	}
	deps.deleteCloudFileCache = func(string, uint) error { return nil }
	linkCalls := 0
	deps.linkRemote = func(_ context.Context, _ driver.Driver, _ model.CloudFileCache, _ model.LinkArgs) (*model.Link, error) {
		linkCalls++
		return nil, errors.New("permanent not found")
	}

	_, err := (&mediaCloudPlayer{deps: deps}).play(context.Background(), model.LinkArgs{}, request)
	if err == nil {
		t.Fatal("expected error after second link failure")
	}
	if linkCalls != 2 {
		t.Fatalf("link calls = %d, want exactly 2 (no infinite loop)", linkCalls)
	}
}

func TestCloudPlayMediaAllowsPikPakRootIDOnlyForSingleFile(t *testing.T) {
	request, deps, state := newMediaCloudPlayTest(t)
	request.Provider = "PikPak"
	request.File.FilmFile.PartCount = 1
	request.SelectedMagnet = &model.SourceMagnet{
		WorkID:      request.File.WorkID,
		MagnetURI:   "magnet:?xt=single",
		Fingerprint: "single-fingerprint",
	}
	deps.listFilmFiles = func(uint) ([]model.FilmFile, error) {
		return []model.FilmFile{request.File.FilmFile}, nil
	}
	deps.getCloudFileCache = func(string, uint, string) (model.CloudFileCache, error) {
		return model.CloudFileCache{}, gorm.ErrRecordNotFound
	}
	deps.download = func(_ context.Context, got CloudPlayProviderRequest) (CloudPlayProviderDownload, error) {
		state.downloadCalls++
		if got.Provider != "PikPak" || got.MagnetURI != request.SelectedMagnet.MagnetURI {
			t.Fatalf("download request = %+v", got)
		}
		return CloudPlayProviderDownload{RootRemoteID: "download-root"}, nil
	}
	deps.replaceCloudFileCaches = func(storageIdentity, fingerprint string, caches []model.CloudFileCache) error {
		state.replaceCalls++
		if storageIdentity != "42" || fingerprint != "single-fingerprint" || len(caches) != 1 {
			t.Fatalf("replace = storage %q, fingerprint %q, caches %#v", storageIdentity, fingerprint, caches)
		}
		return nil
	}
	deps.listRemote = func(context.Context, driver.Driver, string) ([]RemoteFileEvidence, error) {
		return nil, nil
	}

	result, err := (&mediaCloudPlayer{deps: deps}).play(context.Background(), model.LinkArgs{}, request)
	if err != nil {
		t.Fatalf("play PikPak root-only media: %v", err)
	}
	if result.Requested.RemoteFileID != "download-root" || state.linkedCache.RemoteFileID != "download-root" {
		t.Fatalf("root fallback IDs = %q/%q", result.Requested.RemoteFileID, state.linkedCache.RemoteFileID)
	}
}

func TestCloudPlayMediaWritesNewFingerprintAfterMagnetSwitch(t *testing.T) {
	request, deps, state := newMediaCloudPlayTest(t)
	staleMagnet := model.SourceMagnet{
		ID:          99,
		WorkID:      request.File.WorkID,
		MagnetURI:   "magnet:?xt=urn:btih:stale",
		Fingerprint: "magnet-stale",
		Selected:    true,
	}
	newMagnet := *request.SelectedMagnet
	request.SelectedMagnet = &newMagnet

	var cacheLookupFingerprints []string
	deps.getCloudFileCache = func(_ string, _ uint, fingerprint string) (model.CloudFileCache, error) {
		cacheLookupFingerprints = append(cacheLookupFingerprints, fingerprint)
		if fingerprint == staleMagnet.Fingerprint {
			return model.CloudFileCache{
				FilmFileID:        request.File.ID,
				StorageIdentity:   "42",
				RemoteFileID:      "stale-remote",
				MagnetFingerprint: staleMagnet.Fingerprint,
			}, nil
		}
		return model.CloudFileCache{}, gorm.ErrRecordNotFound
	}

	deps.download = func(_ context.Context, got CloudPlayProviderRequest) (CloudPlayProviderDownload, error) {
		state.downloadCalls++
		if got.MagnetURI != newMagnet.MagnetURI {
			t.Fatalf("download magnet = %q, want new magnet %q", got.MagnetURI, newMagnet.MagnetURI)
		}
		return CloudPlayProviderDownload{RootRemoteID: "download-root"}, nil
	}
	var replacedFingerprint string
	deps.replaceCloudFileCaches = func(storageIdentity, fingerprint string, caches []model.CloudFileCache) error {
		state.replaceCalls++
		replacedFingerprint = fingerprint
		if len(caches) != 2 {
			t.Fatalf("replace caches = %d, want 2 siblings", len(caches))
		}
		for _, c := range caches {
			if c.MagnetFingerprint != newMagnet.Fingerprint {
				t.Fatalf("cache fingerprint = %q, want %q", c.MagnetFingerprint, newMagnet.Fingerprint)
			}
		}
		return nil
	}

	result, err := (&mediaCloudPlayer{deps: deps}).play(context.Background(), model.LinkArgs{}, request)
	if err != nil {
		t.Fatalf("play after magnet switch: %v", err)
	}
	if len(cacheLookupFingerprints) < 1 || cacheLookupFingerprints[0] != newMagnet.Fingerprint {
		t.Fatalf("first cache lookup fingerprint = %q, want %q", cacheLookupFingerprints[0], newMagnet.Fingerprint)
	}
	if replacedFingerprint != newMagnet.Fingerprint {
		t.Fatalf("replaced fingerprint = %q, want %q", replacedFingerprint, newMagnet.Fingerprint)
	}
	if result.CacheHit || state.downloadCalls != 1 {
		t.Fatalf("result.CacheHit=%v, downloadCalls=%d; want miss with fetch", result.CacheHit, state.downloadCalls)
	}
}

type mediaCloudPlayTestState struct {
	downloadCalls int
	listCalls     int
	replaceCalls  int
	upsertCalls   int
	linkedCache   model.CloudFileCache
}

func newMediaCloudPlayTest(t *testing.T) (CloudPlayRequest, mediaCloudPlayDeps, *mediaCloudPlayTestState) {
	t.Helper()
	file1 := model.FilmFile{ID: 101, WorkID: 10, PartIndex: 1, PartCount: 2, SourcePath: "disc-1.mp4", SourceSize: 100}
	file2 := model.FilmFile{ID: 102, WorkID: 10, PartIndex: 2, PartCount: 2, SourcePath: "disc-2.mp4", SourceSize: 200}
	work := model.FilmWork{ID: 10, Code: "ABC-123", PrimaryDir: "ABC-123"}
	magnet := testSelectedMagnet(work.ID)
	request := CloudPlayRequest{
		Provider:       "115 Cloud",
		DriverPath:     "/cloud",
		File:           model.FilmFileWithWork{FilmFile: file1, Work: work},
		SelectedMagnet: &magnet,
	}
	state := &mediaCloudPlayTestState{}
	storage := &mediaCloudPlayTestDriver{storage: model.Storage{ID: 42}}
	deps := mediaCloudPlayDeps{
		resolveStorage: func(path string) driver.Driver {
			if path != request.DriverPath {
				t.Fatalf("resolve path = %q, want %q", path, request.DriverPath)
			}
			return storage
		},
		getSelectedSourceMagnet: func(uint) (model.SourceMagnet, error) {
			return magnet, nil
		},
		upsertSourceMagnets: func(uint, []model.SourceMagnet) error { return nil },
		listFilmFiles: func(workID uint) ([]model.FilmFile, error) {
			if workID != work.ID {
				t.Fatalf("list work ID = %d, want %d", workID, work.ID)
			}
			return []model.FilmFile{file1, file2}, nil
		},
		getCloudFileCache: func(string, uint, string) (model.CloudFileCache, error) {
			return model.CloudFileCache{}, gorm.ErrRecordNotFound
		},
		deleteCloudFileCache: func(string, uint) error { return nil },
		replaceCloudFileCaches: func(storageIdentity, fingerprint string, caches []model.CloudFileCache) error {
			state.replaceCalls++
			if storageIdentity != "42" || fingerprint != magnet.Fingerprint {
				t.Fatalf("replace scope = (%q, %q)", storageIdentity, fingerprint)
			}
			return nil
		},
		download: func(_ context.Context, got CloudPlayProviderRequest) (CloudPlayProviderDownload, error) {
			state.downloadCalls++
			if got.Provider != request.Provider || got.MagnetURI != magnet.MagnetURI {
				t.Fatalf("download request = %+v", got)
			}
			return CloudPlayProviderDownload{RootRemoteID: "download-root"}, nil
		},
		listRemote: func(_ context.Context, _ driver.Driver, rootRemoteID string) ([]RemoteFileEvidence, error) {
			state.listCalls++
			if rootRemoteID != "download-root" {
				t.Fatalf("list root ID = %q", rootRemoteID)
			}
			return []RemoteFileEvidence{
				{ID: "remote-1", Path: "disc-1.mp4", Size: 100},
				{ID: "remote-2", Path: "disc-2.mp4", Size: 200},
			}, nil
		},
		linkRemote: func(_ context.Context, _ driver.Driver, cache model.CloudFileCache, _ model.LinkArgs) (*model.Link, error) {
			state.linkedCache = cache
			return &model.Link{URL: "https://example.test/" + cache.RemoteFileID}, nil
		},
		now: func() time.Time { return time.Unix(123, 0) },
	}
	return request, deps, state
}

func testSelectedMagnet(workID uint) model.SourceMagnet {
	return model.SourceMagnet{
		ID:          77,
		WorkID:      workID,
		MagnetURI:   "magnet:?xt=urn:btih:current",
		Fingerprint: "magnet-current",
		Selected:    true,
		FileManifest: model.MagnetFileManifest{
			{Path: "disc-1.mp4", Size: 100},
			{Path: "disc-2.mp4", Size: 200},
		},
	}
}

type mediaCloudPlayTestDriver struct {
	storage model.Storage
}

func (d *mediaCloudPlayTestDriver) Config() driver.Config            { return driver.Config{} }
func (d *mediaCloudPlayTestDriver) GetStorage() *model.Storage       { return &d.storage }
func (d *mediaCloudPlayTestDriver) SetStorage(storage model.Storage) { d.storage = storage }
func (d *mediaCloudPlayTestDriver) GetAddition() driver.Additional   { return nil }
func (d *mediaCloudPlayTestDriver) Init(context.Context) error       { return nil }
func (d *mediaCloudPlayTestDriver) Drop(context.Context) error       { return nil }
func (d *mediaCloudPlayTestDriver) List(context.Context, model.Obj, model.ListArgs) ([]model.Obj, error) {
	return nil, nil
}
func (d *mediaCloudPlayTestDriver) Link(context.Context, model.Obj, model.LinkArgs) (*model.Link, error) {
	return nil, nil
}
