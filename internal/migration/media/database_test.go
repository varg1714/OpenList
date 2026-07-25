package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestMigrateLegacyMediaMapsSourcesFilesAndCaches(t *testing.T) {
	database := newMigrationTestDB(t)
	fixture := seedCompleteLegacyFixture(t, database)

	report, err := MigrateLegacyMedia(context.Background(), database)
	if err != nil {
		t.Fatalf("migrate legacy media: %v", err)
	}
	if report.LegacyFilms != 5 || report.LegacyMagnetCaches != 7 {
		t.Fatalf("legacy counts = films %d, caches %d", report.LegacyFilms, report.LegacyMagnetCaches)
	}
	if report.WorksCreated != 3 || report.FilesCreated != 5 || report.SourceMagnetsCreated != 2 {
		t.Fatalf("created counts = %+v", report)
	}

	javdb := getWork(t, database, 1, "javdb", "ABP-123")
	if javdb.PrimaryDir != "Actor A" || javdb.SourceRef != fixture.javdbURL || javdb.SourceURL != fixture.javdbURL {
		t.Fatalf("JavDB identity = %+v", javdb)
	}
	if javdb.RawTitle != "Original title" || javdb.TranslatedTitle != "Translated title" || javdb.TranslationStatus != "success" || javdb.TranslationVersion != model.CurrentTranslationVersion || javdb.Synopsis != "Synopsis" || javdb.ImageURL != "https://img.test/abp.jpg" {
		t.Fatalf("JavDB text metadata = %+v", javdb)
	}
	if !reflect.DeepEqual(javdb.Actors, model.StringArray{"Actor A", "Actor B"}) || !reflect.DeepEqual(javdb.Tags, model.StringArray{"tag-a", model.TagSubtitle}) {
		t.Fatalf("JavDB arrays = actors %#v, tags %#v", javdb.Actors, javdb.Tags)
	}
	if !javdb.ReleaseDate.Equal(fixture.releaseDate) || !javdb.SynopsisExcluded || !javdb.SampleImageComplete || javdb.SampleImageCount != 4 || javdb.SampleImageScanAt == nil || javdb.DMMPosterScanAt == nil {
		t.Fatalf("JavDB stage state = %+v", javdb)
	}

	fc2 := getWork(t, database, 2, "fc2", "FC2-PPV-100")
	if fc2.SourceRef != "FC2-PPV-100" || fc2.SourceURL != fixture.fc2URL || fc2.PrimaryDir != "Seller A" || fc2.TranslatedTitle != "FC2 translated" {
		t.Fatalf("FC2 work = %+v", fc2)
	}
	fc2Files := listFiles(t, database, fc2.ID)
	if len(fc2Files) != 3 {
		t.Fatalf("FC2 files = %+v", fc2Files)
	}
	for index, file := range fc2Files {
		if file.PartIndex != index+1 || file.PartCount != 3 || file.SourcePath != fmt.Sprintf("FC2-100-cd%d.mp4", index+1) {
			t.Fatalf("FC2 file %d = %+v", index, file)
		}
	}

	pornhub := getWork(t, database, 3, "pornhub", "view-key-9")
	if pornhub.SourceRef != "view-key-9" || pornhub.SourceURL != fixture.pornhubURL || pornhub.RawTitle != "" || pornhub.TranslatedTitle != "Porn title" {
		t.Fatalf("Pornhub work = %+v", pornhub)
	}

	javdbMagnets := listMagnets(t, database, javdb.ID)
	if len(javdbMagnets) != 1 || !javdbMagnets[0].Selected || !javdbMagnets[0].Subtitle {
		t.Fatalf("JavDB magnets = %+v", javdbMagnets)
	}
	wantJavFingerprint := fingerprint(fixture.javdbMagnet)
	if javdbMagnets[0].Fingerprint != wantJavFingerprint {
		t.Fatalf("JavDB magnet = %+v", javdbMagnets[0])
	}
	fc2Magnets := listMagnets(t, database, fc2.ID)
	if len(fc2Magnets) != 1 {
		t.Fatalf("FC2 magnets = %+v", fc2Magnets)
	}

	if _, err := ValidateLegacyMediaMigration(context.Background(), database); err != nil {
		t.Fatalf("validate migrated identity: %v", err)
	}
	assertCount(t, database, &model.Film{}, 5)
	assertCount(t, database, &model.MagnetCache{}, 11)
}

func TestMarkVerifiedNFOStateUpdatesMigratedWorkAfterArtifactMove(t *testing.T) {
	database := newMigrationTestDB(t)
	work := model.FilmWork{StorageID: 1, Source: "javdb", Code: "ABP-123", PrimaryDir: "Actor A", MetadataVersion: 4}
	if err := database.Create(&work).Error; err != nil {
		t.Fatalf("create work: %v", err)
	}
	dataDir := t.TempDir()
	nfoPath := filepath.Join(dataDir, "emby", "javdb", "Actor A", "ABP-123", "ABP-123.nfo")
	if err := os.MkdirAll(filepath.Dir(nfoPath), 0o755); err != nil {
		t.Fatalf("create NFO directory: %v", err)
	}
	if err := os.WriteFile(nfoPath, []byte("<movie/>"), 0o644); err != nil {
		t.Fatalf("create NFO: %v", err)
	}
	plan := &migrationPlan{works: []*plannedWork{{identity: Identity{StorageID: 1, Source: "javdb", Code: "ABP-123"}, work: work}}}
	if err := database.Transaction(func(tx *gorm.DB) error { return markVerifiedNFOState(tx, plan, dataDir) }); err != nil {
		t.Fatalf("mark verified NFO state: %v", err)
	}
	stored := getWork(t, database, 1, "javdb", "ABP-123")
	if stored.NfoVersion != stored.MetadataVersion || stored.NfoLastError != "" {
		t.Fatalf("NFO state = version %d, error %q", stored.NfoVersion, stored.NfoLastError)
	}
}

func TestMigrateLegacyMediaRerunIsIdempotent(t *testing.T) {
	database := newMigrationTestDB(t)
	seedCompleteLegacyFixture(t, database)

	first, err := MigrateLegacyMedia(context.Background(), database)
	if err != nil {
		t.Fatalf("first migration: %v", err)
	}
	if err := database.Model(&model.FilmWork{}).
		Where("source = ? AND code = ?", "javdb", "ABP-123").
		Update("translated_title", "ABP-123 Translated title").Error; err != nil {
		t.Fatalf("seed previously migrated prefixed title: %v", err)
	}
	second, err := MigrateLegacyMedia(context.Background(), database)
	if err != nil {
		t.Fatalf("second migration: %v", err)
	}
	if first.WorksCreated == 0 || second.WorksCreated != 0 || second.FilesCreated != 0 || second.SourceMagnetsCreated != 0 {
		t.Fatalf("rerun reports = first %+v, second %+v", first, second)
	}
	if second.WorksExisting != 3 || second.FilesExisting != 5 || second.SourceMagnetsExisting != 2 {
		t.Fatalf("rerun existing counts = %+v", second)
	}
	assertCount(t, database, &model.FilmWork{}, 3)
	assertCount(t, database, &model.FilmFile{}, 5)
	assertCount(t, database, &model.SourceMagnet{}, 2)
	javdb := getWork(t, database, 1, "javdb", "ABP-123")
	if javdb.TranslatedTitle != "Translated title" {
		t.Fatalf("rerun translated title = %q, want title without code prefix", javdb.TranslatedTitle)
	}
}

func TestMigrateLegacyMediaKeepsFirstDirectoryAndStableMergesMetadata(t *testing.T) {
	// Given
	database := newMigrationTestDB(t)
	createStorages(t, database, model.Storage{ID: 1, Driver: "Javdb", MountPath: "/javdb"})
	films := []model.Film{
		{
			ID: 42, Source: "javdb", Actor: "Later Directory", Name: "ABP-123 title.mp4", Url: "https://javdb.test/v/later",
			Actors: model.StringArray{"Shared", "Later Cast"}, Tags: model.StringArray{"shared-tag", "later-tag"},
		},
		{
			ID: 7, Source: "javdb", Actor: "First Directory", Name: "ABP-123 title.mp4", Url: "https://javdb.test/v/first",
			Actors: model.StringArray{"First Cast", "Shared"}, Tags: model.StringArray{"first-tag", "shared-tag"},
		},
	}
	if err := database.Create(&films).Error; err != nil {
		t.Fatalf("seed shared-directory films: %v", err)
	}

	// When
	_, err := MigrateLegacyMedia(context.Background(), database)

	// Then
	if err != nil {
		t.Fatalf("migrate shared-directory films: %v", err)
	}
	work := getWork(t, database, 1, "javdb", "ABP-123")
	if work.PrimaryDir != "First Directory" || work.SourceURL != "https://javdb.test/v/first" {
		t.Fatalf("first legacy projection = directory %q, URL %q", work.PrimaryDir, work.SourceURL)
	}
	wantActors := model.StringArray{"First Cast", "Shared", "Later Cast", "Later Directory"}
	wantTags := model.StringArray{"first-tag", "shared-tag", "later-tag"}
	if !reflect.DeepEqual(work.Actors, wantActors) || !reflect.DeepEqual(work.Tags, wantTags) {
		t.Fatalf("merged metadata = actors %#v, tags %#v; want actors %#v, tags %#v", work.Actors, work.Tags, wantActors, wantTags)
	}
}

func TestMigrateLegacyMediaRejectsDistinctPlainSourcePathsBeforeWrites(t *testing.T) {
	// Given
	database := newMigrationTestDB(t)
	createStorages(t, database, model.Storage{ID: 1, Driver: "Javdb", MountPath: "/javdb"})
	films := []model.Film{
		{Source: "javdb", Actor: "Actor A", Name: "ABP-123 first.mp4", Url: "https://javdb.test/v/abp-123"},
		{Source: "javdb", Actor: "Actor A", Name: "ABP-123 second.mp4", Url: "https://javdb.test/v/abp-123"},
	}
	if err := database.Create(&films).Error; err != nil {
		t.Fatalf("seed distinct plain source paths: %v", err)
	}

	// When
	_, err := MigrateLegacyMedia(context.Background(), database)

	// Then
	if !errors.Is(err, ErrIdentityCollision) {
		t.Fatalf("plain source path collision = %v, want ErrIdentityCollision", err)
	}
	assertCount(t, database, &model.FilmWork{}, 0)
	assertCount(t, database, &model.FilmFile{}, 0)
}

func TestMigrateLegacyMediaAcceptsIdenticalPlainSourcePathsAcrossDirectories(t *testing.T) {
	// Given
	database := newMigrationTestDB(t)
	createStorages(t, database, model.Storage{ID: 1, Driver: "Javdb", MountPath: "/javdb"})
	films := []model.Film{
		{Source: "javdb", Actor: "Actor A", Name: "ABP-123 title.mp4", Url: "https://javdb.test/v/abp-123"},
		{Source: "javdb", Actor: "Actor B", Name: "ABP-123 title.mp4", Url: "https://javdb.test/v/abp-123"},
	}
	if err := database.Create(&films).Error; err != nil {
		t.Fatalf("seed identical plain source paths: %v", err)
	}

	// When
	_, err := MigrateLegacyMedia(context.Background(), database)

	// Then
	if err != nil {
		t.Fatalf("migrate identical plain source paths: %v", err)
	}
	work := getWork(t, database, 1, "javdb", "ABP-123")
	files := listFiles(t, database, work.ID)
	if len(files) != 1 || files[0].SourcePath != films[0].Name {
		t.Fatalf("merged identical file rows = %+v", files)
	}
}

func TestMigrateLegacyMediaPreservesLegacyFileProjection(t *testing.T) {
	// Given
	database := newMigrationTestDB(t)
	createStorages(t, database, model.Storage{ID: 1, Driver: "Javdb", MountPath: "/javdb"})
	createdAt := time.Date(2023, time.December, 4, 5, 6, 7, 0, time.UTC)
	film := model.Film{
		ID: 701, Source: "javdb", Actor: "Actor A", Name: "ABP-123 title.mp4",
		Url: "https://javdb.test/v/abp-123", CreatedAt: createdAt,
	}
	if err := database.Create(&film).Error; err != nil {
		t.Fatalf("seed legacy file projection: %v", err)
	}

	// When
	_, err := MigrateLegacyMedia(context.Background(), database)

	// Then
	if err != nil {
		t.Fatalf("migrate legacy file projection: %v", err)
	}
	work := getWork(t, database, 1, "javdb", "ABP-123")
	files := listFiles(t, database, work.ID)
	if len(files) != 1 {
		t.Fatalf("migrated files = %+v", files)
	}
	file := files[0]
	if file.ID != film.ID || file.SourcePath != film.Name || file.SourceSize != 1417381701 {
		t.Fatalf("legacy file projection = %+v", file)
	}
	if !file.CreatedAt.Equal(createdAt) || !file.UpdatedAt.Equal(createdAt) {
		t.Fatalf("legacy file times = created %v, updated %v; want %v", file.CreatedAt, file.UpdatedAt, createdAt)
	}
}

func TestMigrateLegacyMediaPreassignsSyntheticFileIDsAroundExplicitLegacyIDs(t *testing.T) {
	// Given
	database := newMigrationTestDB(t)
	createStorages(t, database, model.Storage{ID: 1, Driver: "Javdb", MountPath: "/javdb"})
	existingWork := model.FilmWork{StorageID: 99, Source: "pornhub", Code: "existing", SourceRef: "existing", PrimaryDir: "Existing"}
	if err := database.Create(&existingWork).Error; err != nil {
		t.Fatalf("seed existing normalized work: %v", err)
	}
	if err := database.Create(&model.FilmFile{ID: 11, WorkID: existingWork.ID, PartIndex: 1, PartCount: 1}).Error; err != nil {
		t.Fatalf("seed existing normalized file: %v", err)
	}
	films := []model.Film{
		{ID: 1, Source: "javdb", Actor: "Actor A", Name: "AAA-001 title.mp4", Url: "https://javdb.test/v/aaa-001"},
		{ID: 2, Source: "javdb", Actor: "Actor B", Name: "AAA-001 title.mp4", Url: "https://javdb.test/v/aaa-001"},
		{ID: 12, Source: "javdb", Actor: "Actor Z", Name: "ZZZ-012 title.mp4", Url: "https://javdb.test/v/zzz-012"},
	}
	if err := database.Create(&films).Error; err != nil {
		t.Fatalf("seed synthetic and explicit legacy files: %v", err)
	}
	dataDir := t.TempDir()
	dryRun := MigrationOptions{Mode: MigrationDryRun, DataDir: dataDir}
	apply := MigrationOptions{Mode: MigrationApply, DataDir: dataDir}

	// When
	_, dryRunErr := MigrateLegacyMediaWithOptions(context.Background(), database, dryRun)
	_, applyErr := MigrateLegacyMediaWithOptions(context.Background(), database, apply)

	// Then
	if dryRunErr != nil || applyErr != nil {
		t.Fatalf("synthetic ID migration errors = dry-run %v, apply %v", dryRunErr, applyErr)
	}
	syntheticWork := getWork(t, database, 1, "javdb", "AAA-001")
	explicitWork := getWork(t, database, 1, "javdb", "ZZZ-012")
	syntheticFiles := listFiles(t, database, syntheticWork.ID)
	explicitFiles := listFiles(t, database, explicitWork.ID)
	if len(syntheticFiles) != 1 || syntheticFiles[0].ID != 13 {
		t.Fatalf("synthetic file IDs = %+v, want deterministic ID 13", syntheticFiles)
	}
	if len(explicitFiles) != 1 || explicitFiles[0].ID != films[2].ID {
		t.Fatalf("explicit file IDs = %+v, want legacy ID %d", explicitFiles, films[2].ID)
	}

	// When
	_, rerunErr := MigrateLegacyMediaWithOptions(context.Background(), database, apply)

	// Then
	if rerunErr != nil {
		t.Fatalf("rerun synthetic ID migration: %v", rerunErr)
	}
	if got := listFiles(t, database, syntheticWork.ID); len(got) != 1 || got[0].ID != syntheticFiles[0].ID {
		t.Fatalf("rerun synthetic file IDs = %+v, want ID %d", got, syntheticFiles[0].ID)
	}
}

func TestMigrateLegacyMediaStableUnionsExistingWorkMetadata(t *testing.T) {
	// Given
	database := newMigrationTestDB(t)
	createStorages(t, database, model.Storage{ID: 1, Driver: "Javdb", MountPath: "/javdb"})
	film := model.Film{
		ID: 31, Source: "javdb", Actor: "Actor A", Name: "ABP-123 title.mp4", Url: "https://javdb.test/v/abp-123",
		Actors: model.StringArray{"Shared Actor", "Planned Actor", "Planned Actor"},
		Tags:   model.StringArray{"shared-tag", "planned-tag", "planned-tag"},
	}
	if err := database.Create(&film).Error; err != nil {
		t.Fatalf("seed legacy metadata: %v", err)
	}
	existing := model.FilmWork{
		StorageID: 1, Source: "javdb", Code: "ABP-123", SourceRef: film.Url, SourceURL: film.Url, PrimaryDir: film.Actor,
		Actors: model.StringArray{"Existing Actor", "Shared Actor", "Existing Actor"},
		Tags:   model.StringArray{"existing-tag", "shared-tag", "existing-tag"},
	}
	if err := database.Create(&existing).Error; err != nil {
		t.Fatalf("seed partial normalized work: %v", err)
	}

	// When
	_, err := MigrateLegacyMedia(context.Background(), database)

	// Then
	if err != nil {
		t.Fatalf("migrate partial normalized work: %v", err)
	}
	stored := getWork(t, database, 1, "javdb", "ABP-123")
	wantActors := model.StringArray{"Existing Actor", "Shared Actor", "Planned Actor"}
	wantTags := model.StringArray{"existing-tag", "shared-tag", "planned-tag"}
	if !reflect.DeepEqual(stored.Actors, wantActors) || !reflect.DeepEqual(stored.Tags, wantTags) {
		t.Fatalf("existing-first metadata = actors %#v, tags %#v; want actors %#v, tags %#v", stored.Actors, stored.Tags, wantActors, wantTags)
	}
}

func TestValidateLegacyMediaMigrationRejectsMissingPlannedMetadata(t *testing.T) {
	// Given
	database := newMigrationTestDB(t)
	createStorages(t, database, model.Storage{ID: 1, Driver: "Javdb", MountPath: "/javdb"})
	createdAt := time.Date(2024, time.February, 3, 4, 5, 6, 0, time.UTC)
	film := model.Film{
		ID: 41, Source: "javdb", Actor: "Actor A", Name: "ABP-123 title.mp4", Url: "https://javdb.test/v/abp-123",
		CreatedAt: createdAt, Actors: model.StringArray{"Planned Actor"}, Tags: model.StringArray{"planned-tag"},
	}
	if err := database.Create(&film).Error; err != nil {
		t.Fatalf("seed legacy metadata: %v", err)
	}
	existing := model.FilmWork{
		StorageID: 1, Source: "javdb", Code: "ABP-123", SourceRef: film.Url, SourceURL: film.Url, PrimaryDir: film.Actor,
		Actors: model.StringArray{"Existing Actor"}, Tags: model.StringArray{"existing-tag"},
	}
	if err := database.Create(&existing).Error; err != nil {
		t.Fatalf("seed normalized work without planned metadata: %v", err)
	}
	file := model.FilmFile{
		ID: film.ID, WorkID: existing.ID, PartIndex: 1, PartCount: 1, SourcePath: film.Name,
		SourceSize: 1417381701, CreatedAt: createdAt, UpdatedAt: createdAt,
	}
	if err := database.Create(&file).Error; err != nil {
		t.Fatalf("seed normalized file: %v", err)
	}

	// When
	_, err := ValidateLegacyMediaMigration(context.Background(), database)

	// Then
	if !errors.Is(err, ErrIncompleteMigration) {
		t.Fatalf("missing planned metadata validation = %v, want ErrIncompleteMigration", err)
	}
}

func TestMigrateLegacyMediaDryRunRejectsLegacyFileIDCollision(t *testing.T) {
	// Given
	database := newMigrationTestDB(t)
	createStorages(t, database, model.Storage{ID: 1, Driver: "Javdb", MountPath: "/javdb"})
	film := model.Film{ID: 91, Source: "javdb", Actor: "Actor A", Name: "ABP-123 title.mp4", Url: "https://javdb.test/v/abp-123"}
	if err := database.Create(&film).Error; err != nil {
		t.Fatalf("seed legacy film: %v", err)
	}
	existingWork := model.FilmWork{StorageID: 99, Source: "pornhub", Code: "other", SourceRef: "other", PrimaryDir: "Other"}
	if err := database.Create(&existingWork).Error; err != nil {
		t.Fatalf("seed existing normalized work: %v", err)
	}
	existingFile := model.FilmFile{ID: film.ID, WorkID: existingWork.ID, PartIndex: 1, PartCount: 1, SourcePath: "other.mp4"}
	if err := database.Create(&existingFile).Error; err != nil {
		t.Fatalf("seed colliding normalized file: %v", err)
	}

	// When
	_, err := MigrateLegacyMediaWithOptions(context.Background(), database, MigrationOptions{Mode: MigrationDryRun, DataDir: t.TempDir()})

	// Then
	if !errors.Is(err, ErrIdentityCollision) {
		t.Fatalf("legacy file ID collision = %v, want ErrIdentityCollision", err)
	}
	assertCount(t, database, &model.FilmWork{}, 1)
	assertCount(t, database, &model.FilmFile{}, 1)
}

func TestMigrateLegacyMediaDryRunRejectsIncompatibleNormalizedRows(t *testing.T) {
	// Given
	database := newMigrationTestDB(t)
	createStorages(t, database, model.Storage{ID: 1, Driver: "Javdb", MountPath: "/javdb"})
	film := model.Film{ID: 92, Source: "javdb", Actor: "Actor A", Name: "ABP-123 title.mp4", Url: "https://javdb.test/v/abp-123"}
	if err := database.Create(&film).Error; err != nil {
		t.Fatalf("seed legacy film: %v", err)
	}
	existing := model.FilmWork{
		StorageID: 1, Source: "javdb", Code: "ABP-123", SourceRef: film.Url,
		SourceURL: film.Url, PrimaryDir: "Different Directory",
	}
	if err := database.Create(&existing).Error; err != nil {
		t.Fatalf("seed incompatible normalized work: %v", err)
	}

	// When
	_, err := MigrateLegacyMediaWithOptions(context.Background(), database, MigrationOptions{Mode: MigrationDryRun, DataDir: t.TempDir()})

	// Then
	if !errors.Is(err, ErrIdentityCollision) {
		t.Fatalf("normalized row collision = %v, want ErrIdentityCollision", err)
	}
	assertCount(t, database, &model.FilmFile{}, 0)
}

func TestMigrateLegacyMediaCreatesCanonicalCloudCacheAliases(t *testing.T) {
	// Given
	database := newMigrationTestDB(t)
	createStorages(t, database, model.Storage{ID: 1, Driver: "Javdb", MountPath: "/javdb"})
	film := model.Film{Source: "javdb", Actor: "Actor A", Name: "ABP-123 legacy title.mp4", Url: "https://javdb.test/v/abp-123"}
	if err := database.Create(&film).Error; err != nil {
		t.Fatalf("seed legacy film: %v", err)
	}
	scanAt := time.Date(2024, time.January, 2, 3, 4, 5, 0, time.UTC)
	originals := []model.MagnetCache{
		{
			DriverType: "115 Cloud", Magnet: "magnet:?xt=urn:btih:115", FileId: "remote-115", Name: film.Name, Code: "ABP-123",
			Option: map[string]string{"pickCode": "pick-115", "opaque": "keep"}, Subtitle: true, ScanAt: scanAt,
			ScanCount: 7, SubtitleUrls: model.StringArray{"https://subtitle.test/one", "https://subtitle.test/two"},
		},
		{
			DriverType: "PikPak", Magnet: "magnet:?xt=urn:btih:pikpak", FileId: "remote-pikpak", Name: film.Name, Code: "ABP-123",
			Option: map[string]string{"opaque": "pikpak"}, Subtitle: true, ScanAt: scanAt.Add(time.Hour),
			ScanCount: 9, SubtitleUrls: model.StringArray{"https://subtitle.test/pikpak"},
		},
	}
	if err := database.Create(&originals).Error; err != nil {
		t.Fatalf("seed cloud caches: %v", err)
	}

	// When
	_, err := MigrateLegacyMedia(context.Background(), database)

	// Then
	if err != nil {
		t.Fatalf("migrate cloud caches: %v", err)
	}
	for _, original := range originals {
		var alias model.MagnetCache
		if err := database.Where("driver_type = ? AND name = ?", original.DriverType, "ABP-123.mp4").First(&alias).Error; err != nil {
			t.Fatalf("lookup %s canonical alias: %v", original.DriverType, err)
		}
		if alias.ID == original.ID || alias.DriverType != original.DriverType || alias.Magnet != original.Magnet || alias.FileId != original.FileId || alias.Code != original.Code {
			t.Fatalf("%s canonical alias identity = %+v", original.DriverType, alias)
		}
		if !reflect.DeepEqual(alias.Option, original.Option) || !reflect.DeepEqual(alias.SubtitleUrls, original.SubtitleUrls) || alias.Subtitle != original.Subtitle || alias.ScanCount != original.ScanCount || !alias.ScanAt.Equal(original.ScanAt) {
			t.Fatalf("%s canonical alias fields = %+v; want %+v", original.DriverType, alias, original)
		}
		var preserved model.MagnetCache
		if err := database.First(&preserved, original.ID).Error; err != nil || preserved.Name != film.Name {
			t.Fatalf("preserved %s legacy cache = %+v, error %v", original.DriverType, preserved, err)
		}
	}
	assertCount(t, database, &model.MagnetCache{}, 4)
}

func TestMigrateLegacyMediaCloudCacheAliasRerunIsIdempotent(t *testing.T) {
	// Given
	database := newMigrationTestDB(t)
	createStorages(t, database, model.Storage{ID: 1, Driver: "Javdb", MountPath: "/javdb"})
	film := model.Film{Source: "javdb", Actor: "Actor A", Name: "ABP-123 title.mp4", Url: "https://javdb.test/v/abp-123"}
	if err := database.Create(&film).Error; err != nil {
		t.Fatalf("seed legacy film: %v", err)
	}
	cache := model.MagnetCache{
		DriverType: "115 Cloud", Magnet: "magnet:?xt=urn:btih:115", FileId: "remote-115", Name: film.Name,
		Code: "ABP-123", Option: map[string]string{"pickCode": "pick-115"},
	}
	if err := database.Create(&cache).Error; err != nil {
		t.Fatalf("seed cloud cache: %v", err)
	}

	// When
	_, firstErr := MigrateLegacyMedia(context.Background(), database)
	_, secondErr := MigrateLegacyMedia(context.Background(), database)

	// Then
	if firstErr != nil || secondErr != nil {
		t.Fatalf("cloud cache alias rerun errors = first %v, second %v", firstErr, secondErr)
	}
	var aliases int64
	if err := database.Model(&model.MagnetCache{}).
		Where("driver_type = ? AND name = ?", "115 Cloud", "ABP-123.mp4").Count(&aliases).Error; err != nil {
		t.Fatalf("count canonical aliases: %v", err)
	}
	if aliases != 1 {
		t.Fatalf("canonical alias count = %d, want 1", aliases)
	}
	assertCount(t, database, &model.MagnetCache{}, 2)
}

func TestMigrateLegacyMediaRejectsConflictingCloudCacheAliasBeforeWrites(t *testing.T) {
	// Given
	database := newMigrationTestDB(t)
	createStorages(t, database, model.Storage{ID: 1, Driver: "Javdb", MountPath: "/javdb"})
	film := model.Film{Source: "javdb", Actor: "Actor A", Name: "ABP-123 legacy title.mp4", Url: "https://javdb.test/v/abp-123"}
	if err := database.Create(&film).Error; err != nil {
		t.Fatalf("seed legacy film: %v", err)
	}
	caches := []model.MagnetCache{
		{DriverType: "115 Cloud", Magnet: "magnet:?xt=urn:btih:115", FileId: "remote-original", Name: film.Name, Code: "ABP-123", Option: map[string]string{"pickCode": "pick-original"}},
		{DriverType: "115 Cloud", Magnet: "magnet:?xt=urn:btih:115", FileId: "remote-conflict", Name: "ABP-123.mp4", Code: "ABP-123", Option: map[string]string{"pickCode": "pick-conflict"}},
	}
	if err := database.Create(&caches).Error; err != nil {
		t.Fatalf("seed conflicting cloud caches: %v", err)
	}

	// When
	_, err := MigrateLegacyMedia(context.Background(), database)

	// Then
	if !errors.Is(err, ErrIdentityCollision) {
		t.Fatalf("cloud cache alias conflict = %v, want ErrIdentityCollision", err)
	}
	assertCount(t, database, &model.FilmWork{}, 0)
	assertCount(t, database, &model.FilmFile{}, 0)
	assertCount(t, database, &model.SourceMagnet{}, 0)
	assertCount(t, database, &model.MagnetCache{}, 2)
}

func TestMigrateLegacyMediaRejectsIdentityCollisionAndRollsBack(t *testing.T) {
	database := newMigrationTestDB(t)
	createStorages(t, database, model.Storage{ID: 1, Driver: "Javdb", MountPath: "/javdb"})
	films := []model.Film{
		{Source: "javdb", Actor: "Actor A", Name: "ABP-123 first.mp4", Url: "https://javdb.test/v/one"},
		{Source: "JAVDB", Actor: "Actor B", Name: "abp-123 second.mp4", Url: "https://javdb.test/v/two"},
	}
	if err := database.Create(&films).Error; err != nil {
		t.Fatalf("seed colliding films: %v", err)
	}

	_, err := MigrateLegacyMedia(context.Background(), database)
	if !errors.Is(err, ErrIdentityCollision) {
		t.Fatalf("collision error = %v, want ErrIdentityCollision", err)
	}
	var collision *IdentityCollisionError
	if !errors.As(err, &collision) || collision.Identity.Code != "ABP-123" || !reflect.DeepEqual(collision.LegacyFilmIDs, []uint{films[0].ID, films[1].ID}) {
		t.Fatalf("typed collision = %#v", collision)
	}
	assertCount(t, database, &model.FilmWork{}, 0)
	assertCount(t, database, &model.FilmFile{}, 0)
	assertCount(t, database, &model.SourceMagnet{}, 0)
	assertCount(t, database, &model.Film{}, 2)
}

func TestMigrateLegacyMediaRejectsCrossStorageArtifactRootBeforeWrites(t *testing.T) {
	database := newMigrationTestDB(t)
	dataDir := t.TempDir()
	journalPath := filepath.Join(dataDir, "journal.json")
	createStorages(t, database,
		model.Storage{ID: 1, Driver: "Javdb", MountPath: "/javdb-one"},
		model.Storage{ID: 2, Driver: "Javdb", MountPath: "/javdb-two"},
	)
	legacy := model.Film{Source: "javdb", Actor: "Actor A", Name: "ABP-123 title.mp4", Url: "https://javdb.test/v/abp-123"}
	if err := database.Create(&legacy).Error; err != nil {
		t.Fatalf("seed legacy film: %v", err)
	}
	existing := model.FilmWork{StorageID: 2, Source: "javdb", Code: "ABP-123", PrimaryDir: "Actor A", SourceRef: legacy.Url}
	if err := database.Create(&existing).Error; err != nil {
		t.Fatalf("seed existing work: %v", err)
	}

	_, err := MigrateLegacyMediaWithOptions(context.Background(), database, MigrationOptions{
		Mode: MigrationApply, DataDir: dataDir, JournalPath: journalPath,
		StorageMapping: map[string]uint{"javdb:Actor A": 1},
	})
	if !errors.Is(err, ErrArtifactRootCollision) {
		t.Fatalf("cross-storage root error = %v, want ErrArtifactRootCollision", err)
	}
	assertCount(t, database, &model.FilmWork{}, 1)
	assertCount(t, database, &model.FilmFile{}, 0)
	if _, statErr := os.Stat(journalPath); !os.IsNotExist(statErr) {
		t.Fatalf("preflight wrote journal: %v", statErr)
	}
}

func TestMigrateLegacyMediaAcceptsAlphanumericFC2Code(t *testing.T) {
	database := newMigrationTestDB(t)
	createStorages(t, database, model.Storage{ID: 2, Driver: "FC2", MountPath: "/fc2"})
	film := model.Film{
		Source: "fc2", Actor: "个人收藏", Name: "050525_01-10MU", Url: "https://adult.contents.fc2.com/article/050525_01-10MU/",
	}
	if err := database.Create(&film).Error; err != nil {
		t.Fatalf("seed alphanumeric FC2 film: %v", err)
	}

	if _, err := MigrateLegacyMedia(context.Background(), database); err != nil {
		t.Fatalf("migrate alphanumeric FC2 film: %v", err)
	}
	work := getWork(t, database, 2, "fc2", "FC2-PPV-050525_01-10MU")
	if work.PrimaryDir != "个人收藏" || work.SourceURL != film.Url {
		t.Fatalf("migrated FC2 work = %+v", work)
	}
}

func TestMigrateLegacyMediaSkipsCacheWithIncompatiblePartTopology(t *testing.T) {
	database := newMigrationTestDB(t)
	createStorages(t, database, model.Storage{ID: 2, Driver: "FC2", MountPath: "/fc2"})
	film := model.Film{
		Source: "fc2", Actor: "个人收藏", Name: "FC2-PPV-1628569.mp4", Url: "https://adult.contents.fc2.com/article/1628569/",
	}
	if err := database.Create(&film).Error; err != nil {
		t.Fatalf("seed single-file FC2 film: %v", err)
	}
	cache := model.MagnetCache{
		DriverType: "fc2", Magnet: "magnet:?xt=urn:btih:incompatible-part",
		Name: "FC2-PPV-1628569-cd2.mp4", Code: "FC2-PPV-1628569",
	}
	if err := database.Create(&cache).Error; err != nil {
		t.Fatalf("seed incompatible cache: %v", err)
	}

	report, err := MigrateLegacyMedia(context.Background(), database)
	if err != nil {
		t.Fatalf("migrate incompatible cache: %v", err)
	}
	if !reflect.DeepEqual(report.SkippedMagnetCaches, []uint{cache.ID}) {
		t.Fatalf("skipped caches = %v, want [%d]", report.SkippedMagnetCaches, cache.ID)
	}
	assertCount(t, database, &model.MagnetCache{}, 1)
	getWork(t, database, 2, "fc2", "FC2-PPV-1628569")
}

func TestMigrateLegacyMediaSkipsCacheWithoutMatchingWork(t *testing.T) {
	database := newMigrationTestDB(t)
	cache := model.MagnetCache{
		DriverType: "javdb", Magnet: "magnet:?xt=urn:btih:orphan-cache",
		Name: "MIDV-899.mp4", Code: "MIDV-899",
	}
	if err := database.Create(&cache).Error; err != nil {
		t.Fatalf("seed orphan cache: %v", err)
	}

	report, err := MigrateLegacyMedia(context.Background(), database)
	if err != nil {
		t.Fatalf("migrate orphan cache: %v", err)
	}
	if !reflect.DeepEqual(report.SkippedMagnetCaches, []uint{cache.ID}) {
		t.Fatalf("skipped caches = %v, want [%d]", report.SkippedMagnetCaches, cache.ID)
	}
	assertCount(t, database, &model.MagnetCache{}, 1)
}

type completeFixture struct {
	javdbURL, fc2URL, pornhubURL string
	javdbMagnet, fc2Magnet       string
	releaseDate, scanAt          time.Time
}

func seedCompleteLegacyFixture(t *testing.T, database *gorm.DB) completeFixture {
	t.Helper()
	fixture := completeFixture{
		javdbURL:    "https://javdb.test/v/abp-123",
		fc2URL:      "https://adult.contents.fc2.com/article/100/",
		pornhubURL:  "https://www.pornhub.test/view_video.php?viewkey=view-key-9",
		javdbMagnet: "magnet:?xt=urn:btih:javdb",
		fc2Magnet:   "magnet:?xt=urn:btih:fc2",
		releaseDate: time.Date(2024, time.June, 2, 0, 0, 0, 0, time.UTC),
	}
	fixture.scanAt = fixture.releaseDate.Add(48 * time.Hour)
	createStorages(t, database,
		model.Storage{ID: 1, Driver: "Javdb", MountPath: "/javdb"},
		model.Storage{ID: 2, Driver: "FC2", MountPath: "/fc2"},
		model.Storage{ID: 3, Driver: "Pornhub", MountPath: "/pornhub"},
		model.Storage{ID: 10, Driver: "PikPak", MountPath: "/pikpak"},
		model.Storage{ID: 11, Driver: "115 Cloud", MountPath: "/115"},
	)
	scanAt := fixture.scanAt
	films := []model.Film{
		{
			Source: "JavDB", Actor: "Actor A", Name: "abp-123 Original title.mp4", Url: fixture.javdbURL,
			Image: "https://img.test/abp.jpg", Title: "abp-123 Translated title", Synopsis: "Synopsis",
			Actors: model.StringArray{"Actor A", "Actor B"}, Tags: model.StringArray{"tag-a", model.TagSubtitle}, Date: fixture.releaseDate,
			SynopsisScanAt: scanAt, SynopsisExcluded: true, SampleImageCount: 4, SampleImageComplete: true,
			SampleImageScanAt: scanAt, DMMPosterStatus: model.DMMPosterStatusSuccess, DMMPosterScanAt: scanAt,
		},
		{Source: "FC2", Actor: "Seller A", Name: "FC2-100-cd1.mp4", Url: fixture.fc2URL},
		{Source: "fc2", Actor: "Seller A", Name: "FC2-100-cd2.mp4", Url: fixture.fc2URL, Title: "FC2-100 FC2 translated"},
		{Source: "Fc2", Actor: "Seller A", Name: "FC2-100-cd3.mp4", Url: fixture.fc2URL, Title: "FC2 translated"},
		{Source: "PornHub", Actor: "Channel A", Name: "view-key-9", Url: fixture.pornhubURL, Title: "view-key-9 Porn title"},
	}
	if err := database.Create(&films).Error; err != nil {
		t.Fatalf("seed films: %v", err)
	}
	caches := []model.MagnetCache{
		{DriverType: "JavDB", Magnet: fixture.javdbMagnet, Name: films[0].Name, Code: "abp-123", Subtitle: true, ScanAt: scanAt},
		{DriverType: "javdb", Magnet: fixture.javdbMagnet, Name: films[0].Name, Code: "ABP-123", Subtitle: true, ScanAt: scanAt},
		{DriverType: "PikPak", Magnet: fixture.javdbMagnet, FileId: "remote-javdb", Name: films[0].Name, Code: "ABP-123", Option: map[string]string{"opaque": "keep"}, ScanAt: scanAt},
		{DriverType: "FC2", Magnet: fixture.fc2Magnet, Name: films[1].Name, Code: "FC2-100", ScanAt: scanAt},
		{DriverType: "115 Cloud", Magnet: fixture.fc2Magnet, FileId: "remote-fc2-1", Name: films[1].Name, Code: "FC2-100", Option: map[string]string{"pickCode": "pick-1"}, ScanAt: scanAt},
		{DriverType: "115 Cloud", Magnet: fixture.fc2Magnet, FileId: "remote-fc2-2", Name: films[2].Name, Code: "FC2-100", Option: map[string]string{"pickCode": "pick-2"}, ScanAt: scanAt},
		{DriverType: "115 Cloud", Magnet: fixture.fc2Magnet, FileId: "remote-fc2-3", Name: films[3].Name, Code: "FC2-100", Option: map[string]string{"pickCode": "pick-3"}, ScanAt: scanAt},
	}
	if err := database.Create(&caches).Error; err != nil {
		t.Fatalf("seed magnet caches: %v", err)
	}
	return fixture
}

func newMigrationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:media-migration-%s?mode=memory&cache=shared", t.Name())
	database, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open migration database: %v", err)
	}
	if err := database.AutoMigrate(&model.Storage{}, &model.Film{}, &model.MagnetCache{}, &model.FilmWork{}, &model.FilmFile{}, &model.SourceMagnet{}); err != nil {
		t.Fatalf("migrate test schema: %v", err)
	}
	return database
}

func createStorages(t *testing.T, database *gorm.DB, storages ...model.Storage) {
	t.Helper()
	if err := database.Create(&storages).Error; err != nil {
		t.Fatalf("seed storages: %v", err)
	}
}

func getWork(t *testing.T, database *gorm.DB, storageID uint, source, code string) model.FilmWork {
	t.Helper()
	var work model.FilmWork
	if err := database.Where("storage_id = ? AND source = ? AND code = ?", storageID, source, code).First(&work).Error; err != nil {
		t.Fatalf("get work %d/%s/%s: %v", storageID, source, code, err)
	}
	return work
}

func listFiles(t *testing.T, database *gorm.DB, workID uint) []model.FilmFile {
	t.Helper()
	var files []model.FilmFile
	if err := database.Where("work_id = ?", workID).Order("part_index ASC").Find(&files).Error; err != nil {
		t.Fatalf("list files for work %d: %v", workID, err)
	}
	return files
}

func listMagnets(t *testing.T, database *gorm.DB, workID uint) []model.SourceMagnet {
	t.Helper()
	var magnets []model.SourceMagnet
	if err := database.Where("work_id = ?", workID).Order("priority ASC, id ASC").Find(&magnets).Error; err != nil {
		t.Fatalf("list magnets for work %d: %v", workID, err)
	}
	return magnets
}

func assertCount(t *testing.T, database *gorm.DB, value interface{}, want int64) {
	t.Helper()
	var got int64
	if err := database.Model(value).Count(&got).Error; err != nil {
		t.Fatalf("count %T: %v", value, err)
	}
	if got != want {
		t.Fatalf("count %T = %d, want %d", value, got, want)
	}
}

func fingerprint(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
