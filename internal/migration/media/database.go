package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"gorm.io/gorm"
)

var (
	ErrIdentityCollision     = errors.New("legacy media identity collision")
	ErrUnresolvedIdentity    = errors.New("legacy media identity is unresolved")
	ErrIncompleteMigration   = errors.New("legacy media migration is incomplete")
	ErrArtifactRootCollision = errors.New("media artifact root has multiple storage owners")
)

var (
	multipartPattern = regexp.MustCompile(`(?i)^(.*?)-cd([1-9][0-9]*)$`)
	fc2CodePattern   = regexp.MustCompile(`(?i)^FC2(?:-PPV)?-?([A-Z0-9]+(?:[-_][A-Z0-9]+)*)$`)
	fc2URLPattern    = regexp.MustCompile(`(?i)(?:FC2(?:-PPV)?-?|/article/)([0-9]+)(?:/|$)`)
)

type Identity struct {
	StorageID uint
	Source    string
	Code      string
}

func (i Identity) String() string {
	return fmt.Sprintf("%d/%s/%s", i.StorageID, i.Source, i.Code)
}

type IdentityCollisionError struct {
	Identity      Identity
	LegacyFilmIDs []uint
	Reason        string
}

func (e *IdentityCollisionError) Error() string {
	return fmt.Sprintf("%s for %s (legacy films %v): %s", ErrIdentityCollision, e.Identity, e.LegacyFilmIDs, e.Reason)
}

func (e *IdentityCollisionError) Unwrap() error {
	return ErrIdentityCollision
}

type UnresolvedIdentityError struct {
	Entity    string
	LegacyIDs []uint
	Reason    string
}

func (e *UnresolvedIdentityError) Error() string {
	return fmt.Sprintf("%s for %s %v: %s", ErrUnresolvedIdentity, e.Entity, e.LegacyIDs, e.Reason)
}

func (e *UnresolvedIdentityError) Unwrap() error {
	return ErrUnresolvedIdentity
}

type ValidationError struct {
	Identity Identity
	Reason   string
}

type ArtifactRootCollisionError struct {
	Source     string
	PrimaryDir string
	Code       string
	StorageIDs []uint
}

func (e *ArtifactRootCollisionError) Error() string {
	return fmt.Sprintf("%s for %s/%s/%s (storage IDs %v)", ErrArtifactRootCollision, e.Source, e.PrimaryDir, e.Code, e.StorageIDs)
}

func (e *ArtifactRootCollisionError) Unwrap() error { return ErrArtifactRootCollision }

func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s for %s: %s", ErrIncompleteMigration, e.Identity, e.Reason)
}

func (e *ValidationError) Unwrap() error {
	return ErrIncompleteMigration
}

type MigrationReport struct {
	LegacyFilms         int
	LegacyMagnetCaches  int
	SkippedLegacyFilms  []uint
	SkippedMagnetCaches []uint

	WorksCreated               int
	WorksExisting              int
	FilesCreated               int
	FilesExisting              int
	SourceMagnetsCreated       int
	SourceMagnetsExisting      int
	ArtifactsPlanned           int
	ArtifactMovesPlanned       int
	ArtifactDeletesPlanned     int
	ArtifactDirectoriesPlanned int
	ArtifactsMoved             int
	ArtifactsCopied            int
	ArtifactsDeleted           int
	ArtifactDirectoriesRemoved int
	ArtifactsExisting          int
}

type migrationPlan struct {
	works        []*plannedWork
	magnets      []*plannedMagnet
	cacheAliases []plannedCacheAlias
	report       MigrationReport
}

type plannedWork struct {
	identity        Identity
	work            model.FilmWork
	files           []plannedFile
	artifactSources []plannedArtifactSource
	filmIDs         []uint
	workID          uint
}

type plannedArtifactSource struct {
	name      string
	partIndex int
}

type plannedFile struct {
	partIndex  int
	partCount  int
	sourcePath string
	sourceSize int64
	createdAt  time.Time
	updatedAt  time.Time
	legacyID   uint
	fileID     uint
}

type plannedMagnet struct {
	work        *plannedWork
	magnetURI   string
	fingerprint string
	provider    string
	priority    int
	selected    bool
	subtitle    bool
	scanAt      *time.Time
	cacheIDs    []uint
}

type parsedLegacyFilm struct {
	code       string
	rawTitle   string
	partIndex  int
	multipart  bool
	sourcePath string
}

// MigrateLegacyMedia is the explicit, idempotent stop-the-world migration entrypoint.
// Legacy tables are read-only; all normalized writes and final validation share one transaction.
func MigrateLegacyMedia(ctx context.Context, database *gorm.DB) (MigrationReport, error) {
	return MigrateLegacyMediaWithOptions(ctx, database, defaultMigrationOptions())
}

func MigrateLegacyMediaWithOptions(ctx context.Context, database *gorm.DB, options MigrationOptions) (MigrationReport, error) {
	options, err := options.normalized()
	if err != nil {
		return MigrationReport{}, err
	}
	plan, err := buildMigrationPlan(database.WithContext(ctx), options)
	if err != nil {
		return MigrationReport{}, err
	}
	report := plan.report
	artifactPlan, err := inspectArtifacts(plan, options, &report)
	if err != nil || options.Mode == MigrationDryRun {
		return report, err
	}
	err = database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := populatePlan(tx, plan, &report); err != nil {
			return err
		}
		return validatePlan(tx, plan)
	})
	if err != nil {
		return report, err
	}
	if err := migrateArtifacts(artifactPlan, options, &report); err != nil {
		return report, err
	}
	if err := database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return markVerifiedNFOState(tx, plan, options.DataDir)
	}); err != nil {
		return report, err
	}
	return report, nil
}

// ValidateLegacyMediaMigration rebuilds the expected identity graph from legacy rows and
// verifies that every resolvable work, file, and magnet exists in normalized tables.
func ValidateLegacyMediaMigration(ctx context.Context, database *gorm.DB) (MigrationReport, error) {
	var report MigrationReport
	err := database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		plan, err := buildMigrationPlan(tx, defaultMigrationOptions())
		if err != nil {
			return err
		}
		report = plan.report
		return validatePlan(tx, plan)
	})
	return report, err
}

func buildMigrationPlan(tx *gorm.DB, options MigrationOptions) (*migrationPlan, error) {
	var films []model.Film
	filmTable := tx.NamingStrategy.TableName("Film")
	if err := tx.Table(filmTable).Order("id ASC").Find(&films).Error; err != nil {
		return nil, fmt.Errorf("load legacy films: %w", err)
	}
	var caches []model.MagnetCache
	cacheTable := tx.NamingStrategy.TableName("MagnetCache")
	if err := tx.Table(cacheTable).Order("id ASC").Find(&caches).Error; err != nil {
		return nil, fmt.Errorf("load legacy magnet caches: %w", err)
	}
	var storages []model.Storage
	if err := tx.Order("id ASC").Find(&storages).Error; err != nil {
		return nil, fmt.Errorf("load storages: %w", err)
	}

	plan := &migrationPlan{report: MigrationReport{LegacyFilms: len(films), LegacyMagnetCaches: len(caches)}}
	mediaStorages := indexMediaStorages(storages)
	workByKey := make(map[string]*plannedWork)
	parsedByFilmID := make(map[uint]parsedLegacyFilm)
	for index := range films {
		film := films[index]
		source, supported := canonicalMediaSource(film.Source)
		if !supported {
			if strings.EqualFold(strings.TrimSpace(film.Source), "airav") {
				plan.report.SkippedLegacyFilms = append(plan.report.SkippedLegacyFilms, film.ID)
				continue
			}
			return nil, &UnresolvedIdentityError{Entity: "film", LegacyIDs: []uint{film.ID}, Reason: fmt.Sprintf("unsupported source %q", film.Source)}
		}
		parsed, err := parseLegacyFilm(source, film)
		if err != nil {
			return nil, &UnresolvedIdentityError{Entity: "film", LegacyIDs: []uint{film.ID}, Reason: err.Error()}
		}
		parsedByFilmID[film.ID] = parsed
		primaryDir := strings.TrimSpace(film.Actor)
		if primaryDir == "" {
			primaryDir = strings.TrimSpace(film.ActorId)
		}
		if primaryDir == "" {
			return nil, &UnresolvedIdentityError{Entity: "film", LegacyIDs: []uint{film.ID}, Reason: "primary directory is empty"}
		}
		storageID, err := resolveMediaStorage(source, primaryDir, mediaStorages[source], options.StorageMapping)
		if err != nil {
			return nil, &UnresolvedIdentityError{Entity: "film", LegacyIDs: []uint{film.ID}, Reason: err.Error()}
		}
		identity := Identity{StorageID: storageID, Source: source, Code: parsed.code}
		key := identity.String()
		planned := workByKey[key]
		if planned == nil {
			planned = &plannedWork{
				identity: identity,
				work: model.FilmWork{
					StorageID: storageID, Source: source, Code: parsed.code,
					SourceRef: sourceRef(source, parsed.code, film.Url), SourceURL: strings.TrimSpace(film.Url), PrimaryDir: primaryDir,
					RawTitle: parsed.rawTitle, TranslatedTitle: normalizeLegacyTranslatedTitle(source, parsed.code, film.Title), Synopsis: film.Synopsis, ImageURL: film.Image,
					ReleaseDate: film.Date, Actors: unionArrays(nil, film.Actors), Tags: unionArrays(nil, film.Tags),
					SynopsisExcluded: film.SynopsisExcluded, SampleImageCount: film.SampleImageCount,
					SampleImageComplete: film.SampleImageComplete, DMMPosterStatus: film.DMMPosterStatus,
					MetadataVersion: 1, CreatedAt: film.CreatedAt,
				},
			}
			applyDerivedTranslationState(&planned.work)
			copyLegacyTimes(&planned.work, film)
			workByKey[key] = planned
			plan.works = append(plan.works, planned)
		} else {
			if err := mergeLegacyFilm(planned, film, parsed, primaryDir); err != nil {
				return nil, err
			}
		}
		planned.artifactSources = append(planned.artifactSources, plannedArtifactSource{name: film.Name, partIndex: parsed.partIndex})
		planned.filmIDs = append(planned.filmIDs, film.ID)
	}

	for _, work := range plan.works {
		if err := buildFileTopology(work, films, parsedByFilmID); err != nil {
			return nil, err
		}
	}
	slices.SortFunc(plan.works, func(a, b *plannedWork) int {
		return strings.Compare(a.identity.String(), b.identity.String())
	})
	if err := buildCachePlan(plan, caches); err != nil {
		return nil, err
	}
	if err := validateNormalizedCompatibility(tx, plan); err != nil {
		return nil, err
	}
	if err := validateArtifactRootOwnership(tx, plan); err != nil {
		return nil, err
	}
	return plan, nil
}

func validateArtifactRootOwnership(tx *gorm.DB, plan *migrationPlan) error {
	type rootOwner struct {
		storageID  uint
		source     string
		primaryDir string
		code       string
	}
	owners := make([]rootOwner, 0, len(plan.works))
	var existing []model.FilmWork
	if tx.Migrator().HasTable(&model.FilmWork{}) {
		if err := tx.Order("id ASC").Find(&existing).Error; err != nil {
			return fmt.Errorf("load normalized works for artifact root preflight: %w", err)
		}
	}
	for _, work := range existing {
		owners = append(owners, rootOwner{storageID: work.StorageID, source: work.Source, primaryDir: work.PrimaryDir, code: work.Code})
	}
	for _, work := range plan.works {
		owners = append(owners, rootOwner{storageID: work.identity.StorageID, source: work.identity.Source, primaryDir: work.work.PrimaryDir, code: work.identity.Code})
	}
	storageByRoot := make(map[string]map[uint]struct{})
	ownerByRoot := make(map[string]rootOwner)
	for _, owner := range owners {
		key := owner.source + "\x00" + owner.primaryDir + "\x00" + owner.code
		if storageByRoot[key] == nil {
			storageByRoot[key] = make(map[uint]struct{})
			ownerByRoot[key] = owner
		}
		storageByRoot[key][owner.storageID] = struct{}{}
	}
	for key, storageSet := range storageByRoot {
		if len(storageSet) < 2 {
			continue
		}
		storageIDs := make([]uint, 0, len(storageSet))
		for storageID := range storageSet {
			storageIDs = append(storageIDs, storageID)
		}
		slices.Sort(storageIDs)
		owner := ownerByRoot[key]
		return &ArtifactRootCollisionError{Source: owner.source, PrimaryDir: owner.primaryDir, Code: owner.code, StorageIDs: storageIDs}
	}
	return nil
}

func indexMediaStorages(storages []model.Storage) map[string][]model.Storage {
	result := make(map[string][]model.Storage)
	for _, storage := range storages {
		if source, ok := canonicalMediaSource(storage.Driver); ok {
			result[source] = append(result[source], storage)
		}
	}
	return result
}

func resolveMediaStorage(source, primaryDir string, candidates []model.Storage, mapping map[string]uint) (uint, error) {
	if mappedID, ok := mapping[source+":"+primaryDir]; ok {
		return validateMappedStorage(source, mappedID, candidates)
	}
	if mappedID, ok := mapping[source]; ok {
		return validateMappedStorage(source, mappedID, candidates)
	}
	if len(candidates) == 0 {
		return 0, fmt.Errorf("no storage configured for source %q", source)
	}
	if len(candidates) != 1 {
		return 0, fmt.Errorf("source %q has %d storage candidates", source, len(candidates))
	}
	return candidates[0].ID, nil
}

func validateMappedStorage(source string, mappedID uint, candidates []model.Storage) (uint, error) {
	for _, candidate := range candidates {
		if candidate.ID == mappedID {
			return mappedID, nil
		}
	}
	return 0, fmt.Errorf("storage mapping for source %q points to storage %d with a different driver", source, mappedID)
}

func canonicalMediaSource(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "javdb":
		return "javdb", true
	case "fc2":
		return "fc2", true
	case "pornhub":
		return "pornhub", true
	default:
		return "", false
	}
}

func parseLegacyFilm(source string, film model.Film) (parsedLegacyFilm, error) {
	stem := stripMediaExtension(strings.TrimSpace(film.Name))
	base, partIndex, multipart, err := parseMultipartStem(source, stem)
	if err != nil {
		return parsedLegacyFilm{}, err
	}
	parsed := parsedLegacyFilm{partIndex: partIndex, multipart: multipart, sourcePath: film.Name}
	switch source {
	case "javdb":
		fields := strings.Fields(base)
		if len(fields) == 0 {
			return parsedLegacyFilm{}, errors.New("JavDB filename is empty")
		}
		parsed.code, err = model.NormalizeMediaCode(source, fields[0])
		if len(fields) > 1 {
			parsed.rawTitle = strings.TrimSpace(strings.TrimPrefix(base, fields[0]))
		}
	case "fc2":
		candidate := strings.Fields(base)
		if len(candidate) > 0 {
			parsed.code, err = normalizeFC2Code(candidate[0])
		}
		if err != nil || parsed.code == "" {
			parsed.code, err = fc2CodeFromURL(film.Url)
		}
	case "pornhub":
		parsed.code = pornhubCodeFromURL(film.Url)
		if parsed.code == "" {
			parsed.code = base
		}
		parsed.code, err = model.NormalizeMediaCode(source, parsed.code)
	}
	if err != nil {
		return parsedLegacyFilm{}, err
	}
	if parsed.sourcePath == "" {
		parsed.sourcePath = film.Name
	}
	return parsed, nil
}

func parseMultipartStem(source, stem string) (string, int, bool, error) {
	if source == "pornhub" {
		return stem, 1, false, nil
	}
	match := multipartPattern.FindStringSubmatch(stem)
	if len(match) == 0 {
		return stem, 1, false, nil
	}
	part, err := strconv.Atoi(match[2])
	if err != nil || part < 1 {
		return "", 0, false, fmt.Errorf("invalid multipart filename %q", stem)
	}
	return match[1], part, true, nil
}

func stripMediaExtension(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".mp4", ".mkv", ".avi", ".mov", ".wmv", ".m4v", ".ts":
		return strings.TrimSuffix(name, filepath.Ext(name))
	default:
		return name
	}
}

func normalizeFC2Code(value string) (string, error) {
	value = strings.TrimSpace(value)
	if match := fc2CodePattern.FindStringSubmatch(value); len(match) != 0 {
		value = match[1]
	}
	return model.NormalizeMediaCode("fc2", value)
}

func fc2CodeFromURL(value string) (string, error) {
	match := fc2URLPattern.FindStringSubmatch(strings.TrimSpace(value))
	if len(match) == 0 {
		return "", fmt.Errorf("cannot derive FC2 code from filename or URL %q", value)
	}
	return model.NormalizeMediaCode("fc2", match[1])
}

func pornhubCodeFromURL(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(parsed.Query().Get("viewkey"))
}

func sourceRef(source, code, sourceURL string) string {
	if source == "javdb" && strings.TrimSpace(sourceURL) != "" {
		return strings.TrimSpace(sourceURL)
	}
	return code
}

func normalizeLegacyTranslatedTitle(source, code, value string) string {
	title := strings.TrimSpace(value)
	fields := strings.Fields(title)
	if len(fields) == 0 {
		return ""
	}
	prefix := fields[0]
	var (
		normalized string
		err        error
	)
	if source == "fc2" {
		normalized, err = normalizeFC2Code(prefix)
	} else {
		normalized, err = model.NormalizeMediaCode(source, prefix)
	}
	if err != nil || normalized != code {
		return title
	}
	return strings.TrimSpace(strings.TrimPrefix(title, prefix))
}

func mergeLegacyFilm(work *plannedWork, film model.Film, parsed parsedLegacyFilm, primaryDir string) error {
	legacyURL := strings.TrimSpace(film.Url)
	fillString(&work.work.SourceURL, legacyURL)
	fillString(&work.work.SourceRef, sourceRef(work.identity.Source, work.identity.Code, legacyURL))
	fillString(&work.work.RawTitle, parsed.rawTitle)
	fillString(&work.work.TranslatedTitle, normalizeLegacyTranslatedTitle(work.identity.Source, work.identity.Code, film.Title))
	fillString(&work.work.Synopsis, film.Synopsis)
	fillString(&work.work.ImageURL, film.Image)
	if work.work.ReleaseDate.IsZero() && !film.Date.IsZero() {
		work.work.ReleaseDate = film.Date
	}
	work.work.Actors = unionArrays(work.work.Actors, film.Actors)
	if primaryDir != work.work.PrimaryDir {
		work.work.Actors = unionArrays(work.work.Actors, model.StringArray{primaryDir})
	}
	work.work.Tags = unionArrays(work.work.Tags, film.Tags)
	work.work.SynopsisExcluded = work.work.SynopsisExcluded || film.SynopsisExcluded
	if film.SampleImageCount > work.work.SampleImageCount {
		work.work.SampleImageCount = film.SampleImageCount
	}
	work.work.SampleImageComplete = work.work.SampleImageComplete || film.SampleImageComplete
	fillString(&work.work.DMMPosterStatus, film.DMMPosterStatus)
	copyLegacyTimes(&work.work, film)
	if work.work.CreatedAt.IsZero() || (!film.CreatedAt.IsZero() && film.CreatedAt.Before(work.work.CreatedAt)) {
		work.work.CreatedAt = film.CreatedAt
	}
	applyDerivedTranslationState(&work.work)
	return nil
}

func copyLegacyTimes(work *model.FilmWork, film model.Film) {
	work.SynopsisScanAt = latestTime(work.SynopsisScanAt, film.SynopsisScanAt)
	work.SampleImageScanAt = latestTime(work.SampleImageScanAt, film.SampleImageScanAt)
	work.DMMPosterScanAt = latestTime(work.DMMPosterScanAt, film.DMMPosterScanAt)
}

func applyDerivedTranslationState(work *model.FilmWork) {
	if strings.TrimSpace(work.TranslatedTitle) == "" {
		return
	}
	work.TranslationStatus = "success"
	work.TranslationAttempts = 0
	work.TranslationNextRetryAt = nil
	work.TranslationLastError = ""
	work.TranslationVersion = model.CurrentTranslationVersion
}

func markVerifiedNFOState(tx *gorm.DB, plan *migrationPlan, dataDir string) error {
	for _, planned := range plan.works {
		targetRoot, err := safeArtifactPath(dataDir, "emby", planned.identity.Source, planned.work.PrimaryDir, planned.identity.Code)
		if err != nil {
			return err
		}
		nfoPath := filepath.Join(targetRoot, planned.identity.Code+".nfo")
		info, err := os.Lstat(nfoPath)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return &ArtifactMigrationError{Path: nfoPath, Reason: err.Error()}
		}
		if !info.Mode().IsRegular() {
			return &ArtifactMigrationError{Path: nfoPath, Reason: "verified NFO target is not a regular file"}
		}
		var existing model.FilmWork
		if err := tx.Where("storage_id = ? AND source = ? AND code = ?", planned.identity.StorageID, planned.identity.Source, planned.identity.Code).First(&existing).Error; err != nil {
			return err
		}
		if err := tx.Model(&existing).Updates(map[string]interface{}{
			"nfo_version":    existing.MetadataVersion,
			"nfo_last_error": "",
		}).Error; err != nil {
			return err
		}
	}
	return nil
}

func latestTime(current *time.Time, candidate time.Time) *time.Time {
	if candidate.IsZero() || (current != nil && !candidate.After(*current)) {
		return current
	}
	value := candidate
	return &value
}

func buildFileTopology(work *plannedWork, films []model.Film, parsed map[uint]parsedLegacyFilm) error {
	rows := make(map[uint]model.Film, len(work.filmIDs))
	for _, film := range films {
		rows[film.ID] = film
	}
	hasMultipart := false
	for _, id := range work.filmIDs {
		hasMultipart = hasMultipart || parsed[id].multipart
	}
	if !hasMultipart {
		firstID := work.filmIDs[0]
		first := parsed[firstID]
		for _, id := range work.filmIDs[1:] {
			if parsed[id].sourcePath != first.sourcePath {
				return &IdentityCollisionError{Identity: work.identity, LegacyFilmIDs: append([]uint(nil), work.filmIDs...), Reason: "plain work has multiple source paths"}
			}
		}
		legacyID := firstID
		if len(work.filmIDs) > 1 {
			legacyID = 0
		}
		work.files = []plannedFile{{
			partIndex: 1, partCount: 1, sourcePath: first.sourcePath, sourceSize: legacyListingFileSize,
			createdAt: rows[firstID].CreatedAt, updatedAt: rows[firstID].CreatedAt, legacyID: legacyID,
		}}
		return nil
	}
	parts := make(map[int]plannedFile)
	for _, id := range work.filmIDs {
		item := parsed[id]
		if !item.multipart {
			return &UnresolvedIdentityError{Entity: "film", LegacyIDs: append([]uint(nil), work.filmIDs...), Reason: "multipart work contains a filename without a -cdN part"}
		}
		if existing, ok := parts[item.partIndex]; ok {
			if existing.sourcePath != item.sourcePath {
				return &IdentityCollisionError{Identity: work.identity, LegacyFilmIDs: append([]uint(nil), work.filmIDs...), Reason: fmt.Sprintf("part %d has multiple source paths", item.partIndex)}
			}
			existing.legacyID = 0
			parts[item.partIndex] = existing
			continue
		}
		parts[item.partIndex] = plannedFile{
			partIndex: item.partIndex, sourcePath: rows[id].Name, sourceSize: legacyListingFileSize,
			createdAt: rows[id].CreatedAt, updatedAt: rows[id].CreatedAt, legacyID: id,
		}
	}
	partCount := len(parts)
	for index := 1; index <= partCount; index++ {
		file, ok := parts[index]
		if !ok {
			return &UnresolvedIdentityError{Entity: "film", LegacyIDs: append([]uint(nil), work.filmIDs...), Reason: fmt.Sprintf("multipart topology has a gap at part %d", index)}
		}
		file.partCount = partCount
		work.files = append(work.files, file)
	}
	return nil
}

func buildCachePlan(plan *migrationPlan, caches []model.MagnetCache) error {
	workBySourceCode := make(map[string][]*plannedWork)
	for _, work := range plan.works {
		key := work.identity.Source + "\x00" + work.identity.Code
		workBySourceCode[key] = append(workBySourceCode[key], work)
	}

	magnetByKey := make(map[string]*plannedMagnet)
	aliasCandidates := make([]plannedCacheAlias, 0)
	for index := range caches {
		cache := caches[index]
		work, err := resolveCacheWork(cache, workBySourceCode)
		if err != nil {
			plan.report.SkippedMagnetCaches = append(plan.report.SkippedMagnetCaches, cache.ID)
			continue
		}
		partIndex, err := cachePartIndex(cache, work)
		if err != nil {
			plan.report.SkippedMagnetCaches = append(plan.report.SkippedMagnetCaches, cache.ID)
			continue
		}
		if isCloudCacheWithRemoteHandle(cache) {
			name, err := model.BuildMediaFileName(work.identity.Code, partIndex, len(work.files))
			if err != nil {
				return fmt.Errorf("build canonical cache alias for %s: %w", work.identity, err)
			}
			aliasCandidates = append(aliasCandidates, plannedCacheAlias{work: work, row: cache, name: name})
		}
		if strings.TrimSpace(cache.Magnet) == "" {
			plan.report.SkippedMagnetCaches = append(plan.report.SkippedMagnetCaches, cache.ID)
			continue
		}
		fingerprint := magnetFingerprint(cache.Magnet)
		magnetKey := work.identity.String() + "\x00" + fingerprint
		magnet := magnetByKey[magnetKey]
		if magnet == nil {
			magnet = &plannedMagnet{
				work: work, magnetURI: cache.Magnet, fingerprint: fingerprint, provider: work.identity.Source,
				priority: len(plan.magnets), selected: true,
			}
			magnetByKey[magnetKey] = magnet
			plan.magnets = append(plan.magnets, magnet)
		}
		magnet.subtitle = magnet.subtitle || cache.Subtitle
		magnet.scanAt = latestTime(magnet.scanAt, cache.ScanAt)
		magnet.cacheIDs = append(magnet.cacheIDs, cache.ID)
	}

	for _, work := range plan.works {
		selected := false
		priority := 0
		for _, magnet := range plan.magnets {
			if magnet.work != work {
				continue
			}
			magnet.priority = priority
			magnet.selected = !selected
			selected = true
			priority++
		}
	}
	aliases, err := planCacheAliases(caches, aliasCandidates)
	if err != nil {
		return err
	}
	plan.cacheAliases = aliases
	return nil
}

func resolveCacheWork(cache model.MagnetCache, workBySourceCode map[string][]*plannedWork) (*plannedWork, error) {
	var matches []*plannedWork
	if source, ok := canonicalMediaSource(cache.DriverType); ok {
		code, err := normalizeCacheCode(source, cache.Code)
		if err != nil {
			return nil, &UnresolvedIdentityError{Entity: "magnet cache", LegacyIDs: []uint{cache.ID}, Reason: err.Error()}
		}
		matches = append(matches, workBySourceCode[source+"\x00"+code]...)
	} else {
		for _, source := range []string{"javdb", "fc2", "pornhub"} {
			code, err := normalizeCacheCode(source, cache.Code)
			if err != nil {
				continue
			}
			matches = append(matches, workBySourceCode[source+"\x00"+code]...)
		}
	}
	matches = uniqueWorks(matches)
	if len(matches) != 1 {
		return nil, &UnresolvedIdentityError{Entity: "magnet cache", LegacyIDs: []uint{cache.ID}, Reason: fmt.Sprintf("code %q resolves to %d works", cache.Code, len(matches))}
	}
	return matches[0], nil
}

func normalizeCacheCode(source, value string) (string, error) {
	if source == "fc2" {
		return normalizeFC2Code(value)
	}
	return model.NormalizeMediaCode(source, value)
}

func uniqueWorks(values []*plannedWork) []*plannedWork {
	seen := make(map[*plannedWork]bool)
	result := make([]*plannedWork, 0, len(values))
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func cachePartIndex(cache model.MagnetCache, work *plannedWork) (int, error) {
	film := model.Film{Name: cache.Name, Source: work.identity.Source, Url: cache.Code}
	parsed, err := parseLegacyFilm(work.identity.Source, film)
	if err != nil || parsed.code != work.identity.Code {
		return 0, fmt.Errorf("cache name %q does not match work %s", cache.Name, work.identity)
	}
	if len(work.files) == 1 {
		if parsed.multipart && parsed.partIndex != 1 {
			return 0, fmt.Errorf("cache name %q names part %d for a single file", cache.Name, parsed.partIndex)
		}
		return 1, nil
	}
	if !parsed.multipart || parsed.partIndex > len(work.files) {
		return 0, fmt.Errorf("cache name %q lacks a valid multipart suffix", cache.Name)
	}
	return parsed.partIndex, nil
}

func populatePlan(tx *gorm.DB, plan *migrationPlan, report *MigrationReport) error {
	for _, planned := range plan.works {
		var existing model.FilmWork
		err := tx.Where("storage_id = ? AND source = ? AND code = ?", planned.identity.StorageID, planned.identity.Source, planned.identity.Code).First(&existing).Error
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			if err := tx.Create(&planned.work).Error; err != nil {
				return fmt.Errorf("create work %s: %w", planned.identity, err)
			}
			planned.workID = planned.work.ID
			report.WorksCreated++
		case err != nil:
			return err
		default:
			if err := ensureExistingWorkCompatible(tx, &existing, planned); err != nil {
				return err
			}
			planned.workID = existing.ID
			report.WorksExisting++
		}
		if err := populateFiles(tx, planned, report); err != nil {
			return err
		}
	}
	for _, magnet := range plan.magnets {
		var existing model.SourceMagnet
		err := tx.Where("work_id = ? AND fingerprint = ?", magnet.work.workID, magnet.fingerprint).First(&existing).Error
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			row := model.SourceMagnet{
				WorkID: magnet.work.workID, MagnetURI: magnet.magnetURI, Fingerprint: magnet.fingerprint,
				Provider: magnet.provider, Priority: magnet.priority, Selected: magnet.selected,
				Subtitle: magnet.subtitle, ScanAt: magnet.scanAt,
			}
			if err := tx.Create(&row).Error; err != nil {
				return fmt.Errorf("create source magnet for %s: %w", magnet.work.identity, err)
			}
			report.SourceMagnetsCreated++
		case err != nil:
			return err
		default:
			if existing.MagnetURI != magnet.magnetURI {
				return &IdentityCollisionError{Identity: magnet.work.identity, LegacyFilmIDs: append([]uint(nil), magnet.work.filmIDs...), Reason: "magnet fingerprint is attached to a different URI"}
			}
			updates := model.SourceMagnet{
				Provider: magnet.provider, Priority: magnet.priority, Selected: magnet.selected,
				Subtitle: magnet.subtitle, ScanAt: magnet.scanAt,
			}
			if err := tx.Model(&existing).
				Select("Provider", "Priority", "Selected", "Subtitle", "ScanAt").
				Updates(updates).Error; err != nil {
				return err
			}
			report.SourceMagnetsExisting++
		}
	}
	return populateCacheAliases(tx, plan)
}

func ensureExistingWorkCompatible(tx *gorm.DB, existing *model.FilmWork, planned *plannedWork) error {
	if err := validateExistingWorkCompatibility(existing, planned); err != nil {
		return err
	}
	updates := make(map[string]interface{})
	fillEmptyUpdate(updates, "source_ref", existing.SourceRef, planned.work.SourceRef)
	fillEmptyUpdate(updates, "source_url", existing.SourceURL, planned.work.SourceURL)
	fillEmptyUpdate(updates, "raw_title", existing.RawTitle, planned.work.RawTitle)
	normalizedExistingTitle := normalizeLegacyTranslatedTitle(existing.Source, existing.Code, existing.TranslatedTitle)
	if normalizedExistingTitle != strings.TrimSpace(existing.TranslatedTitle) {
		updates["translated_title"] = normalizedExistingTitle
	} else {
		fillEmptyUpdate(updates, "translated_title", existing.TranslatedTitle, planned.work.TranslatedTitle)
	}
	fillEmptyUpdate(updates, "synopsis", existing.Synopsis, planned.work.Synopsis)
	fillEmptyUpdate(updates, "image_url", existing.ImageURL, planned.work.ImageURL)
	fillEmptyUpdate(updates, "dmm_poster_status", existing.DMMPosterStatus, planned.work.DMMPosterStatus)
	if existing.TranslationStatus == "" && strings.TrimSpace(planned.work.TranslatedTitle) != "" {
		updates["translation_status"] = "success"
		updates["translation_attempts"] = 0
		updates["translation_next_retry_at"] = nil
		updates["translation_last_error"] = ""
		updates["translation_version"] = model.CurrentTranslationVersion
	}
	if planned.work.NfoVersion > existing.NfoVersion {
		updates["nfo_version"] = planned.work.NfoVersion
		updates["nfo_last_error"] = ""
	}
	if existing.ReleaseDate.IsZero() && !planned.work.ReleaseDate.IsZero() {
		updates["release_date"] = planned.work.ReleaseDate
	}
	actors := unionArrays(existing.Actors, planned.work.Actors)
	if !slices.Equal(existing.Actors, actors) {
		updates["actors"] = actors
	}
	tags := unionArrays(existing.Tags, planned.work.Tags)
	if !slices.Equal(existing.Tags, tags) {
		updates["tags"] = tags
	}
	if existing.SynopsisScanAt == nil && planned.work.SynopsisScanAt != nil {
		updates["synopsis_scan_at"] = planned.work.SynopsisScanAt
	}
	if existing.SampleImageScanAt == nil && planned.work.SampleImageScanAt != nil {
		updates["sample_image_scan_at"] = planned.work.SampleImageScanAt
	}
	if existing.DMMPosterScanAt == nil && planned.work.DMMPosterScanAt != nil {
		updates["dmm_poster_scan_at"] = planned.work.DMMPosterScanAt
	}
	if planned.work.SynopsisExcluded && !existing.SynopsisExcluded {
		updates["synopsis_excluded"] = true
	}
	if planned.work.SampleImageCount > existing.SampleImageCount {
		updates["sample_image_count"] = planned.work.SampleImageCount
	}
	if planned.work.SampleImageComplete && !existing.SampleImageComplete {
		updates["sample_image_complete"] = true
	}
	if len(updates) > 0 {
		return tx.Model(existing).Updates(updates).Error
	}
	return nil
}

func populateFiles(tx *gorm.DB, planned *plannedWork, report *MigrationReport) error {
	var existing []model.FilmFile
	if err := tx.Where("work_id = ?", planned.workID).Order("part_index ASC").Find(&existing).Error; err != nil {
		return err
	}
	if len(existing) == 0 {
		for index := range planned.files {
			row := model.FilmFile{
				ID: planned.files[index].fileID, WorkID: planned.workID, PartIndex: planned.files[index].partIndex,
				PartCount: planned.files[index].partCount, SourcePath: planned.files[index].sourcePath,
				SourceSize: planned.files[index].sourceSize, CreatedAt: planned.files[index].createdAt,
				UpdatedAt: planned.files[index].updatedAt,
			}
			createTx := tx
			if row.CreatedAt.IsZero() {
				createTx = tx.Session(&gorm.Session{NowFunc: func() time.Time { return time.Time{} }})
			}
			if err := createTx.Create(&row).Error; err != nil {
				return fmt.Errorf("create file for %s part %d: %w", planned.identity, row.PartIndex, err)
			}
			planned.files[index].fileID = row.ID
			report.FilesCreated++
		}
		return nil
	}
	if len(existing) != len(planned.files) {
		return &IdentityCollisionError{Identity: planned.identity, LegacyFilmIDs: append([]uint(nil), planned.filmIDs...), Reason: fmt.Sprintf("normalized file count %d differs from legacy %d", len(existing), len(planned.files))}
	}
	for index := range existing {
		want := &planned.files[index]
		got := existing[index]
		if got.PartIndex != want.partIndex || got.PartCount != want.partCount {
			return &IdentityCollisionError{Identity: planned.identity, LegacyFilmIDs: append([]uint(nil), planned.filmIDs...), Reason: "normalized multipart topology differs from legacy"}
		}
		want.fileID = got.ID
		updates := make(map[string]interface{})
		if got.SourcePath == "" && want.sourcePath != "" {
			updates["source_path"] = want.sourcePath
		}
		if got.SourceSize == 0 && want.sourceSize != 0 {
			updates["source_size"] = want.sourceSize
		}
		if got.CreatedAt.IsZero() && !want.createdAt.IsZero() {
			updates["created_at"] = want.createdAt
		}
		if got.UpdatedAt.IsZero() && !want.updatedAt.IsZero() {
			updates["updated_at"] = want.updatedAt
		}
		if len(updates) > 0 {
			if err := tx.Model(&got).UpdateColumns(updates).Error; err != nil {
				return err
			}
		}
		report.FilesExisting++
	}
	return nil
}

func validatePlan(tx *gorm.DB, plan *migrationPlan) error {
	for _, planned := range plan.works {
		var work model.FilmWork
		if err := tx.Where("storage_id = ? AND source = ? AND code = ?", planned.identity.StorageID, planned.identity.Source, planned.identity.Code).First(&work).Error; err != nil {
			return &ValidationError{Identity: planned.identity, Reason: fmt.Sprintf("work is missing: %v", err)}
		}
		if work.PrimaryDir != planned.work.PrimaryDir || work.SourceRef == "" {
			return &ValidationError{Identity: planned.identity, Reason: "work identity fields are incomplete"}
		}
		if !slices.Equal(unionArrays(nil, work.Actors), unionArrays(work.Actors, planned.work.Actors)) {
			return &ValidationError{Identity: planned.identity, Reason: "work actors omit planned legacy values"}
		}
		if !slices.Equal(unionArrays(nil, work.Tags), unionArrays(work.Tags, planned.work.Tags)) {
			return &ValidationError{Identity: planned.identity, Reason: "work tags omit planned legacy values"}
		}
		var files []model.FilmFile
		if err := tx.Where("work_id = ?", work.ID).Order("part_index ASC").Find(&files).Error; err != nil {
			return &ValidationError{Identity: planned.identity, Reason: err.Error()}
		}
		if len(files) != len(planned.files) {
			return &ValidationError{Identity: planned.identity, Reason: fmt.Sprintf("file count is %d, want %d", len(files), len(planned.files))}
		}
		planned.workID = work.ID
		for index := range files {
			got := files[index]
			want := planned.files[index]
			if got.PartIndex != index+1 || got.PartCount != len(files) {
				return &ValidationError{Identity: planned.identity, Reason: "file parts are not contiguous"}
			}
			if got.ID != want.fileID {
				return &ValidationError{Identity: planned.identity, Reason: "file ID differs from the migration plan"}
			}
			if got.SourcePath != want.sourcePath || got.SourceSize != want.sourceSize || timeDiffers(got.CreatedAt, want.createdAt) || timeDiffers(got.UpdatedAt, want.updatedAt) {
				return &ValidationError{Identity: planned.identity, Reason: "file projection differs from legacy"}
			}
			planned.files[index].fileID = got.ID
		}
	}
	for _, magnet := range plan.magnets {
		var count int64
		if err := tx.Model(&model.SourceMagnet{}).Where("work_id = ? AND fingerprint = ?", magnet.work.workID, magnet.fingerprint).Count(&count).Error; err != nil || count != 1 {
			return &ValidationError{Identity: magnet.work.identity, Reason: fmt.Sprintf("source magnet count is %d: %v", count, err)}
		}
	}
	return validateCacheAliases(tx, plan)
}

func fillString(target *string, value string) {
	if *target == "" && value != "" {
		*target = value
	}
}

func fillEmptyUpdate(updates map[string]interface{}, column, current, value string) {
	if current == "" && value != "" {
		updates[column] = value
	}
}

func unionArrays(current, additional model.StringArray) model.StringArray {
	seen := make(map[string]bool, len(current)+len(additional))
	result := make(model.StringArray, 0, len(current)+len(additional))
	for _, values := range []model.StringArray{current, additional} {
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			if !seen[value] {
				seen[value] = true
				result = append(result, value)
			}
		}
	}
	return result
}

func cloneArray(value model.StringArray) model.StringArray {
	return append(model.StringArray(nil), value...)
}

func magnetFingerprint(uri string) string {
	sum := sha256.Sum256([]byte(uri))
	return hex.EncodeToString(sum[:])
}
