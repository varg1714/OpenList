package tool

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strconv"
	"time"

	_115 "github.com/OpenListTeam/OpenList/v4/drivers/115"
	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/internal/setting"
	driver115 "github.com/SheltonZhu/115driver/pkg/driver"
	"gorm.io/gorm"
)

var (
	ErrCloudPlayStorageNotFound    = errors.New("cloud play storage not found")
	ErrCloudPlayMagnetNotFound     = errors.New("cloud play source magnet not found")
	ErrCloudPlayRemoteListEmpty    = errors.New("cloud play remote listing is empty")
	ErrCloudPlayManifestEnrichment = errors.New("cloud play manifest enrichment is not unambiguous")
)

type CloudPlayRequest struct {
	Provider        string
	DriverPath      string
	File            model.FilmFileWithWork
	SelectedMagnet  *model.SourceMagnet
	ProviderOptions map[string]string
	MagnetGetter    func(context.Context, model.FilmWork) ([]model.SourceMagnet, error)
}

type CloudPlayProviderRequest struct {
	Provider    string
	Destination string
	MagnetURI   string
	FileName    string
	Options     map[string]string
}

type CloudPlayProviderDownload struct {
	RootRemoteID string
}

type CloudPlayMediaResult struct {
	Link       *model.Link
	Requested  model.CloudFileCache
	Candidates []model.CloudFileCache
	CacheHit   bool
}

type mediaCloudPlayDeps struct {
	resolveStorage          func(string) driver.Driver
	getSelectedSourceMagnet func(uint) (model.SourceMagnet, error)
	upsertSourceMagnets     func(uint, []model.SourceMagnet) error
	listFilmFiles           func(uint) ([]model.FilmFile, error)
	getCloudFileCache       func(string, uint, string) (model.CloudFileCache, error)
	replaceCloudFileCaches  func(string, string, []model.CloudFileCache) error
	deleteCloudFileCache    func(string, uint) error
	download                func(context.Context, CloudPlayProviderRequest) (CloudPlayProviderDownload, error)
	listRemote              func(context.Context, driver.Driver, string) ([]RemoteFileEvidence, error)
	linkRemote              func(context.Context, driver.Driver, model.CloudFileCache, model.LinkArgs) (*model.Link, error)
	now                     func() time.Time
}

type mediaCloudPlayer struct {
	deps mediaCloudPlayDeps
}

func CloudPlayMedia(ctx context.Context, args model.LinkArgs, req CloudPlayRequest) (*model.Link, error) {
	result, err := (&mediaCloudPlayer{deps: defaultMediaCloudPlayDeps()}).play(ctx, args, req)
	if err != nil {
		return nil, err
	}
	return result.Link, nil
}

func (p *mediaCloudPlayer) play(ctx context.Context, args model.LinkArgs, req CloudPlayRequest) (CloudPlayMediaResult, error) {
	if req.Provider == "" {
		return CloudPlayMediaResult{}, errors.New("cloud play provider is empty")
	}
	req.DriverPath = cloudPlayDriverPath(req.Provider, req.DriverPath)
	if req.DriverPath == "" {
		return CloudPlayMediaResult{}, fmt.Errorf("%w: driver path is empty", ErrCloudPlayStorageNotFound)
	}

	magnet, err := p.selectedMagnet(ctx, req)
	if err != nil {
		return CloudPlayMediaResult{}, err
	}
	storage := p.deps.resolveStorage(req.DriverPath)
	if storage == nil || storage.GetStorage() == nil {
		return CloudPlayMediaResult{}, fmt.Errorf("%w: %s", ErrCloudPlayStorageNotFound, req.DriverPath)
	}
	storageIdentity := strconv.FormatUint(uint64(storage.GetStorage().ID), 10)

	cache, cacheErr := p.deps.getCloudFileCache(storageIdentity, req.File.ID, magnet.Fingerprint)
	if cacheErr == nil {
		link, linkErr := p.deps.linkRemote(ctx, storage, cache, args)
		if linkErr == nil {
			return CloudPlayMediaResult{Link: link, Requested: cache, CacheHit: true}, nil
		}
		if delErr := p.deps.deleteCloudFileCache(storageIdentity, req.File.ID); delErr != nil {
			return CloudPlayMediaResult{}, delErr
		}
	} else if !errors.Is(cacheErr, gorm.ErrRecordNotFound) {
		return CloudPlayMediaResult{}, cacheErr
	}

	files, err := p.deps.listFilmFiles(req.File.WorkID)
	if err != nil {
		return CloudPlayMediaResult{}, err
	}
	fileName, err := model.BuildMediaFileName(req.File.Work.Code, req.File.PartIndex, req.File.PartCount)
	if err != nil {
		return CloudPlayMediaResult{}, err
	}
	download, err := p.deps.download(ctx, CloudPlayProviderRequest{
		Provider:    req.Provider,
		Destination: path.Join(req.DriverPath, req.File.Work.PrimaryDir),
		MagnetURI:   magnet.MagnetURI,
		FileName:    fileName,
		Options:     cloneStringMap(req.ProviderOptions),
	})
	if err != nil {
		return CloudPlayMediaResult{}, err
	}
	remotes, err := p.deps.listRemote(ctx, storage, download.RootRemoteID)
	if err != nil {
		return CloudPlayMediaResult{}, err
	}
	if len(remotes) == 0 {
		remotes, err = rootRemoteFallback(req.Provider, download.RootRemoteID, files, magnet.FileManifest)
		if err != nil {
			return CloudPlayMediaResult{}, err
		}
	}

	files, err = enrichFilmFileEvidence(files, magnet.FileManifest)
	if err != nil {
		return CloudPlayMediaResult{}, err
	}
	matches, err := MatchRemoteMediaFiles(files, magnet.FileManifest, remotes)
	if err != nil {
		return CloudPlayMediaResult{}, err
	}
	verifiedAt := p.deps.now()
	candidates := make([]model.CloudFileCache, 0, len(files))
	for _, file := range files {
		remote, ok := matches[file.ID]
		if !ok {
			return CloudPlayMediaResult{}, fmt.Errorf("film file %d has no matched remote evidence", file.ID)
		}
		candidates = append(candidates, model.CloudFileCache{
			FilmFileID:        file.ID,
			StorageIdentity:   storageIdentity,
			Provider:          req.Provider,
			RemoteFileID:      remote.ID,
			ProviderOptions:   mergeStringMaps(req.ProviderOptions, remote.Options),
			MagnetFingerprint: magnet.Fingerprint,
			VerifiedAt:        &verifiedAt,
		})
	}
	if err := p.deps.replaceCloudFileCaches(storageIdentity, magnet.Fingerprint, candidates); err != nil {
		return CloudPlayMediaResult{}, err
	}

	requested, ok := cloudFileCacheByFilmFileID(candidates, req.File.ID)
	if !ok {
		return CloudPlayMediaResult{}, fmt.Errorf("requested film file %d is not a sibling of work %d", req.File.ID, req.File.WorkID)
	}
	link, err := p.deps.linkRemote(ctx, storage, requested, args)
	if err != nil {
		return CloudPlayMediaResult{}, err
	}
	return CloudPlayMediaResult{Link: link, Requested: requested, Candidates: candidates}, nil
}

func enrichFilmFileEvidence(files []model.FilmFile, manifest model.MagnetFileManifest) ([]model.FilmFile, error) {
	if len(manifest) == 0 || !hasMissingFilmFileEvidence(files) {
		return files, nil
	}
	if len(files) != len(manifest) {
		return nil, fmt.Errorf("%w: %d film files cannot map to %d manifest entries", ErrCloudPlayManifestEnrichment, len(files), len(manifest))
	}

	byPart := make([]model.FilmFile, len(files))
	seenParts := make([]bool, len(files))
	for _, file := range files {
		if file.PartCount != len(files) || file.PartIndex < 1 || file.PartIndex > len(files) {
			return nil, fmt.Errorf("%w: invalid part topology for film file %d (%d/%d)", ErrCloudPlayManifestEnrichment, file.ID, file.PartIndex, file.PartCount)
		}
		partIndex := file.PartIndex - 1
		if seenParts[partIndex] {
			return nil, fmt.Errorf("%w: duplicate part index %d", ErrCloudPlayManifestEnrichment, file.PartIndex)
		}
		seenParts[partIndex] = true
		byPart[partIndex] = file
	}
	for partIndex, file := range byPart {
		if !seenParts[partIndex] {
			return nil, fmt.Errorf("%w: missing part index %d", ErrCloudPlayManifestEnrichment, partIndex+1)
		}
		entry := manifest[partIndex]
		if file.SourcePath == "" {
			file.SourcePath = entry.Path
		}
		if file.SourceSize == 0 {
			file.SourceSize = entry.Size
		}
		if file.SourceFileFingerprint == "" {
			file.SourceFileFingerprint = entry.Fingerprint
		}
		byPart[partIndex] = file
	}
	return byPart, nil
}

func hasMissingFilmFileEvidence(files []model.FilmFile) bool {
	for _, file := range files {
		if file.SourcePath == "" || file.SourceSize == 0 || file.SourceFileFingerprint == "" {
			return true
		}
	}
	return false
}

func (p *mediaCloudPlayer) selectedMagnet(ctx context.Context, req CloudPlayRequest) (model.SourceMagnet, error) {
	if req.SelectedMagnet != nil {
		if req.SelectedMagnet.WorkID != req.File.WorkID {
			return model.SourceMagnet{}, fmt.Errorf("selected magnet work %d does not match film work %d", req.SelectedMagnet.WorkID, req.File.WorkID)
		}
		return *req.SelectedMagnet, nil
	}

	magnet, err := p.deps.getSelectedSourceMagnet(req.File.WorkID)
	if err == nil {
		return magnet, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return model.SourceMagnet{}, err
	}
	if req.MagnetGetter == nil {
		return model.SourceMagnet{}, ErrCloudPlayMagnetNotFound
	}
	candidates, err := req.MagnetGetter(ctx, req.File.Work)
	if err != nil {
		return model.SourceMagnet{}, err
	}
	if len(candidates) == 0 {
		return model.SourceMagnet{}, ErrCloudPlayMagnetNotFound
	}
	if err := p.deps.upsertSourceMagnets(req.File.WorkID, candidates); err != nil {
		return model.SourceMagnet{}, err
	}
	return p.deps.getSelectedSourceMagnet(req.File.WorkID)
}

func defaultMediaCloudPlayDeps() mediaCloudPlayDeps {
	return mediaCloudPlayDeps{
		resolveStorage:          op.GetBalancedStorage,
		getSelectedSourceMagnet: db.GetSelectedSourceMagnet,
		upsertSourceMagnets:     db.UpsertSourceMagnets,
		listFilmFiles:           db.ListFilmFiles,
		getCloudFileCache:       db.GetCloudFileCache,
		replaceCloudFileCaches:  db.ReplaceCloudFileCaches,
		deleteCloudFileCache:    db.DeleteCloudFileCache,
		download: func(ctx context.Context, req CloudPlayProviderRequest) (CloudPlayProviderDownload, error) {
			status, _, err := downloadMagnet(ctx, req.Provider, req.Destination, req.MagnetURI, req.FileName)
			if err != nil {
				return CloudPlayProviderDownload{}, err
			}
			return CloudPlayProviderDownload{RootRemoteID: status.FileInfo.FileId}, nil
		},
		listRemote: listCloudPlayRemoteEvidence,
		linkRemote: linkCloudPlayCache,
		now:        time.Now,
	}
}

func listCloudPlayRemoteEvidence(ctx context.Context, storage driver.Driver, rootRemoteID string) ([]RemoteFileEvidence, error) {
	objects, err := storage.List(ctx, &model.ObjThumb{Object: model.Object{ID: rootRemoteID}}, model.ListArgs{})
	if err != nil {
		return nil, err
	}
	remotes := make([]RemoteFileEvidence, 0, len(objects))
	for _, object := range objects {
		remotePath := object.GetPath()
		if remotePath == "" {
			remotePath = object.GetName()
		}
		options := map[string]string(nil)
		if file, ok := object.(*_115.FileObj); ok {
			options = map[string]string{"pickCode": file.PickCode}
		}
		remotes = append(remotes, RemoteFileEvidence{
			ID:      object.GetID(),
			Path:    remotePath,
			Size:    object.GetSize(),
			Options: options,
		})
	}
	return remotes, nil
}

func linkCloudPlayCache(ctx context.Context, storage driver.Driver, cache model.CloudFileCache, args model.LinkArgs) (*model.Link, error) {
	switch cache.Provider {
	case "PikPak":
		return storage.Link(ctx, &model.ObjThumb{Object: model.Object{ID: cache.RemoteFileID}}, args)
	case "115 Cloud":
		return storage.Link(ctx, &_115.FileObj{File: driver115.File{
			FileID:   cache.RemoteFileID,
			PickCode: cache.ProviderOptions["pickCode"],
		}}, args)
	default:
		return nil, fmt.Errorf("unsupported cloud play provider %q", cache.Provider)
	}
}

func rootRemoteFallback(provider, rootRemoteID string, files []model.FilmFile, manifest model.MagnetFileManifest) ([]RemoteFileEvidence, error) {
	if provider != "PikPak" || rootRemoteID == "" || len(files) != 1 {
		return nil, ErrCloudPlayRemoteListEmpty
	}
	evidence := RemoteFileEvidence{ID: rootRemoteID, Path: files[0].SourcePath, Size: files[0].SourceSize}
	if len(manifest) == 1 {
		evidence.Path = manifest[0].Path
		evidence.Size = manifest[0].Size
		evidence.Fingerprint = manifest[0].Fingerprint
	}
	return []RemoteFileEvidence{evidence}, nil
}

func cloudPlayDriverPath(provider, configured string) string {
	if configured != "" {
		return configured
	}
	switch provider {
	case "115 Cloud":
		return setting.GetStr(conf.Pan115TempDir)
	case "PikPak":
		return setting.GetStr(conf.PikPakTempDir)
	default:
		return ""
	}
}

func cloudFileCacheByFilmFileID(caches []model.CloudFileCache, filmFileID uint) (model.CloudFileCache, bool) {
	for _, cache := range caches {
		if cache.FilmFileID == filmFileID {
			return cache, true
		}
	}
	return model.CloudFileCache{}, false
}

func mergeStringMaps(base, override map[string]string) map[string]string {
	merged := cloneStringMap(base)
	if len(override) > 0 && merged == nil {
		merged = make(map[string]string, len(override))
	}
	for key, value := range override {
		merged[key] = value
	}
	return merged
}

func cloneStringMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}
