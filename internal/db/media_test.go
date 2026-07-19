package db

import (
	"reflect"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func setupMediaRepositoryTestDB(t *testing.T) {
	t.Helper()

	if err := AutoMigrate(new(model.FilmWork), new(model.FilmFile)); err != nil {
		t.Fatalf("migrate media tables: %v", err)
	}
	if err := db.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&model.FilmFile{}).Error; err != nil {
		t.Fatalf("reset film files: %v", err)
	}
	if err := db.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&model.FilmWork{}).Error; err != nil {
		t.Fatalf("reset film works: %v", err)
	}
}

func TestUpsertDiscoveredWorkPreservesPrimaryDirAndState(t *testing.T) {
	setupMediaRepositoryTestDB(t)

	retryAt := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	releaseDate := time.Date(2025, time.January, 2, 0, 0, 0, 0, time.UTC)
	original := model.FilmWork{
		StorageID:              1,
		Source:                 "javdb",
		Code:                   "ABP-123",
		SourceRef:              "v/old",
		SourceURL:              "https://old.example/work",
		PrimaryDir:             "original-dir",
		RawTitle:               "old title",
		ImageURL:               "https://old.example/image.jpg",
		ReleaseDate:            releaseDate,
		TranslationStatus:      "success",
		TranslationAttempts:    3,
		TranslationNextRetryAt: &retryAt,
		TranslationLastError:   "old error",
		TranslationVersion:     4,
		SampleImageComplete:    true,
		MetadataVersion:        7,
	}
	if err := db.Create(&original).Error; err != nil {
		t.Fatalf("create original work: %v", err)
	}

	newReleaseDate := releaseDate.AddDate(0, 1, 0)
	discovered := model.FilmWork{
		StorageID:   original.StorageID,
		Source:      original.Source,
		Code:        original.Code,
		SourceRef:   "v/new",
		SourceURL:   "https://new.example/work",
		PrimaryDir:  "rediscovered-dir",
		RawTitle:    "new title",
		ImageURL:    "https://new.example/image.jpg",
		ReleaseDate: newReleaseDate,
	}
	if err := UpsertDiscoveredWork(&discovered); err != nil {
		t.Fatalf("upsert discovered work: %v", err)
	}

	stored, err := GetFilmWork(original.ID)
	if err != nil {
		t.Fatalf("get stored work: %v", err)
	}
	if stored.SourceRef != discovered.SourceRef || stored.SourceURL != discovered.SourceURL || stored.RawTitle != discovered.RawTitle || stored.ImageURL != discovered.ImageURL || !stored.ReleaseDate.Equal(newReleaseDate) {
		t.Fatalf("discovery fields were not updated: %+v", stored)
	}
	if stored.PrimaryDir != original.PrimaryDir {
		t.Fatalf("primary dir = %q, want preserved %q", stored.PrimaryDir, original.PrimaryDir)
	}
	if stored.TranslationStatus != original.TranslationStatus || stored.TranslationAttempts != original.TranslationAttempts || stored.TranslationNextRetryAt == nil || !stored.TranslationNextRetryAt.Equal(retryAt) || stored.TranslationLastError != original.TranslationLastError || stored.TranslationVersion != original.TranslationVersion {
		t.Fatalf("translation state changed during rediscovery: %+v", stored)
	}
	if !stored.SampleImageComplete || stored.MetadataVersion != original.MetadataVersion {
		t.Fatalf("successful stage state changed during rediscovery: %+v", stored)
	}
}

func TestUpdateMediaWorkTranslationPreservesOtherStageState(t *testing.T) {
	setupMediaRepositoryTestDB(t)

	retryAt := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	synopsisScanAt := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	work := model.FilmWork{
		StorageID:              1,
		Source:                 "javdb",
		Code:                   "ABP-123",
		SourceRef:              "v/1",
		PrimaryDir:             "actor",
		RawTitle:               "raw title",
		Synopsis:               "preserve synopsis",
		TranslationStatus:      "retry_wait",
		TranslationAttempts:    2,
		TranslationNextRetryAt: &retryAt,
		TranslationLastError:   "temporary failure",
		TranslationVersion:     4,
		SynopsisScanAt:         &synopsisScanAt,
		SampleImageCount:       3,
		MetadataVersion:        7,
		NfoVersion:             6,
	}
	if err := db.Create(&work).Error; err != nil {
		t.Fatalf("create work: %v", err)
	}

	if err := UpdateMediaWorkTranslation(work.ID, "translated title", 5); err != nil {
		t.Fatalf("update translation: %v", err)
	}

	stored, err := GetFilmWork(work.ID)
	if err != nil {
		t.Fatalf("get work: %v", err)
	}
	if stored.TranslatedTitle != "translated title" || stored.TranslationStatus != "success" {
		t.Fatalf("translation result = (%q, %q)", stored.TranslatedTitle, stored.TranslationStatus)
	}
	if stored.TranslationAttempts != 2 || stored.TranslationNextRetryAt != nil || stored.TranslationLastError != "" || stored.TranslationVersion != 5 {
		t.Fatalf("translation stage state = %+v", stored)
	}
	if stored.MetadataVersion != 8 || stored.NfoVersion != 6 {
		t.Fatalf("metadata/NFO versions = %d/%d, want 8/6", stored.MetadataVersion, stored.NfoVersion)
	}
	if stored.Synopsis != work.Synopsis || stored.SynopsisScanAt == nil || !stored.SynopsisScanAt.Equal(synopsisScanAt) || stored.SampleImageCount != work.SampleImageCount {
		t.Fatalf("unowned stage state changed: %+v", stored)
	}
}

func TestUpdateMediaWorkTagsPreservesCallerTags(t *testing.T) {
	setupMediaRepositoryTestDB(t)
	work := createMediaTestWork(t, 1, "javdb", "ABP-123", "actor")
	work.Tags = model.StringArray{"JavDB-TOP250-2026"}
	if err := db.Model(&work).Update("tags", work.Tags).Error; err != nil {
		t.Fatalf("seed caller tags: %v", err)
	}
	if err := UpdateMediaWorkTags(work.ID, model.StringArray{"high-definition", "JavDB-TOP250-2026"}, 1); err != nil {
		t.Fatalf("update tags: %v", err)
	}
	stored, err := GetFilmWork(work.ID)
	if err != nil {
		t.Fatalf("get tagged work: %v", err)
	}
	want := model.StringArray{"JavDB-TOP250-2026", "high-definition"}
	if !reflect.DeepEqual(stored.Tags, want) {
		t.Fatalf("merged tags = %#v, want %#v", stored.Tags, want)
	}
}

func TestUpdateMediaWorkSynopsisRetryRecordsStageStateOnly(t *testing.T) {
	setupMediaRepositoryTestDB(t)

	work := createMediaTestWork(t, 1, "javdb", "ABP-123", "actor")
	work.TranslatedTitle = "translated"
	work.MetadataVersion = 9
	if err := db.Model(&work).Updates(map[string]interface{}{
		"translated_title": work.TranslatedTitle,
		"metadata_version": work.MetadataVersion,
	}).Error; err != nil {
		t.Fatalf("seed work state: %v", err)
	}
	nextRetryAt := time.Now().Add(6 * time.Hour).UTC().Truncate(time.Second)
	started := time.Now()
	if err := UpdateMediaWorkSynopsisRetry(work.ID, nextRetryAt, "upstream unavailable"); err != nil {
		t.Fatalf("update synopsis retry: %v", err)
	}

	stored, err := GetFilmWork(work.ID)
	if err != nil {
		t.Fatalf("get work: %v", err)
	}
	if stored.SynopsisScanAt == nil || stored.SynopsisScanAt.Before(started) {
		t.Fatalf("synopsis scan time = %v, want at or after %v", stored.SynopsisScanAt, started)
	}
	if stored.SynopsisNextRetryAt == nil || !stored.SynopsisNextRetryAt.Equal(nextRetryAt) || stored.SynopsisLastError != "upstream unavailable" {
		t.Fatalf("synopsis retry state = %+v", stored)
	}
	if stored.TranslatedTitle != work.TranslatedTitle || stored.MetadataVersion != work.MetadataVersion {
		t.Fatalf("retry update changed unowned fields: %+v", stored)
	}
}

func TestQuerySubtitleMediaWorksIncludesDueRetriesOnly(t *testing.T) {
	setupMediaRepositoryTestDB(t)

	now := time.Now()
	completedAt := now.Add(-2 * time.Hour)
	futureRetry := now.Add(time.Hour)
	dueRetry := now.Add(-time.Hour)
	works := []model.FilmWork{
		{StorageID: 1, Source: "javdb", Code: "NEW", PrimaryDir: "actor"},
		{StorageID: 1, Source: "javdb", Code: "DONE", PrimaryDir: "actor", SubtitleScanAt: &completedAt},
		{StorageID: 1, Source: "javdb", Code: "DUE", PrimaryDir: "actor", SubtitleScanAt: &completedAt, SubtitleNextRetryAt: &dueRetry},
		{StorageID: 1, Source: "javdb", Code: "WAIT", PrimaryDir: "actor", SubtitleScanAt: &completedAt, SubtitleNextRetryAt: &futureRetry},
	}
	for index := range works {
		if err := db.Create(&works[index]).Error; err != nil {
			t.Fatalf("create subtitle fixture %s: %v", works[index].Code, err)
		}
	}

	selected, err := QuerySubtitleMediaWorks("javdb", 0)
	if err != nil {
		t.Fatalf("query subtitle works: %v", err)
	}
	if len(selected) != 2 || selected[0].Code != "NEW" || selected[1].Code != "DUE" {
		t.Fatalf("selected subtitle works = %#v, want NEW and DUE", selected)
	}
}

func TestEnsureSingleFilmFileIsIdempotent(t *testing.T) {
	setupMediaRepositoryTestDB(t)

	work := createMediaTestWork(t, 1, "javdb", "ABP-123", "actor-a")
	first, err := EnsureSingleFilmFile(work.ID)
	if err != nil {
		t.Fatalf("ensure first film file: %v", err)
	}
	second, err := EnsureSingleFilmFile(work.ID)
	if err != nil {
		t.Fatalf("ensure film file again: %v", err)
	}
	if first.ID == 0 || second.ID != first.ID || second.WorkID != work.ID || second.PartIndex != 1 || second.PartCount != 1 {
		t.Fatalf("idempotent result mismatch: first=%+v second=%+v", first, second)
	}

	stored, err := GetFilmFile(first.ID)
	if err != nil {
		t.Fatalf("get film file: %v", err)
	}
	files, err := ListFilmFiles(work.ID)
	if err != nil {
		t.Fatalf("list film files: %v", err)
	}
	if stored.ID != first.ID || len(files) != 1 || files[0].ID != first.ID {
		t.Fatalf("stored files = %+v, direct get = %+v", files, stored)
	}

	multipartWork := createMediaTestWork(t, 1, "fc2", "FC2-PPV-100", "actor-a")
	if err := ReplaceFilmFiles(multipartWork.ID, []model.FilmFile{{PartIndex: 1, PartCount: 2}, {PartIndex: 2, PartCount: 2}}); err != nil {
		t.Fatalf("create multipart topology: %v", err)
	}
	if _, err := EnsureSingleFilmFile(multipartWork.ID); err == nil {
		t.Fatal("EnsureSingleFilmFile accepted an existing multipart topology")
	}
	multipart, err := ListFilmFiles(multipartWork.ID)
	if err != nil {
		t.Fatalf("list multipart files: %v", err)
	}
	if len(multipart) != 2 || multipart[0].PartIndex != 1 || multipart[1].PartIndex != 2 {
		t.Fatalf("multipart topology was overwritten: %+v", multipart)
	}
}

func TestUpsertDiscoveredWorkDoesNotClearExistingDiscoveryFields(t *testing.T) {
	setupMediaRepositoryTestDB(t)

	releaseDate := time.Date(2025, time.January, 2, 0, 0, 0, 0, time.UTC)
	original := model.FilmWork{
		StorageID:   1,
		Source:      "javdb",
		Code:        "ABP-123",
		SourceRef:   "v/1",
		SourceURL:   "https://example/work",
		PrimaryDir:  "actor-a",
		RawTitle:    "原题",
		ImageURL:    "https://example/image.jpg",
		ReleaseDate: releaseDate,
	}
	if err := db.Create(&original).Error; err != nil {
		t.Fatalf("create original work: %v", err)
	}

	discovered := model.FilmWork{
		StorageID:  1,
		Source:     "javdb",
		Code:       "ABP-123",
		SourceRef:  "v/1",
		PrimaryDir: "actor-a",
		RawTitle:   "新原题",
	}
	if err := UpsertDiscoveredWork(&discovered); err != nil {
		t.Fatalf("upsert discovered work: %v", err)
	}

	stored, err := GetFilmWork(original.ID)
	if err != nil {
		t.Fatalf("get stored work: %v", err)
	}
	if !stored.ReleaseDate.Equal(releaseDate) || stored.ImageURL != original.ImageURL {
		t.Fatalf("empty discovery fields cleared persisted data: %+v", stored)
	}
	if stored.RawTitle != discovered.RawTitle {
		t.Fatalf("raw title = %q, want %q", stored.RawTitle, discovered.RawTitle)
	}
}

func TestListFilmWorksScopesStorageSourceAndDir(t *testing.T) {
	setupMediaRepositoryTestDB(t)

	want := createMediaTestWork(t, 1, "javdb", "ABP-123", "actor-a")
	createMediaTestWork(t, 2, "javdb", "ABP-124", "actor-a")
	createMediaTestWork(t, 1, "fc2", "FC2-PPV-100", "actor-a")
	createMediaTestWork(t, 1, "javdb", "ABP-125", "actor-b")

	works, err := ListFilmWorks(1, "javdb", "actor-a")
	if err != nil {
		t.Fatalf("list scoped film works: %v", err)
	}
	if len(works) != 1 || works[0].ID != want.ID {
		t.Fatalf("scoped works = %+v, want only work %d", works, want.ID)
	}
}

func TestGetFilmFileWithWorkPreloadsParent(t *testing.T) {
	setupMediaRepositoryTestDB(t)

	work := createMediaTestWork(t, 1, "javdb", "ABP-123", "actor-a")
	file, err := EnsureSingleFilmFile(work.ID)
	if err != nil {
		t.Fatalf("ensure film file: %v", err)
	}

	got, err := GetFilmFileWithWork(file.ID)
	if err != nil {
		t.Fatalf("get film file with work: %v", err)
	}
	if got.ID != file.ID || got.WorkID != work.ID || got.Work.ID != work.ID || got.Work.Code != work.Code || got.Work.PrimaryDir != work.PrimaryDir {
		t.Fatalf("film file parent was not preloaded: %+v", got)
	}
}

func TestGetFilmFileWithWorkUsesConfiguredTablePrefix(t *testing.T) {
	previousDB := db
	previousConf := conf.Conf
	t.Cleanup(func() {
		db = previousDB
		conf.Conf = previousConf
	})

	testDB, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "pref_"},
	})
	if err != nil {
		t.Fatalf("open prefixed SQLite database: %v", err)
	}
	db = testDB
	conf.Conf = conf.DefaultConfig(t.TempDir())
	conf.Conf.Database.TablePrefix = "pref_"
	if err := AutoMigrate(new(model.FilmWork), new(model.FilmFile)); err != nil {
		t.Fatalf("migrate prefixed media tables: %v", err)
	}
	work := createMediaTestWork(t, 1, "javdb", "ABP-PREFIX", "actor")
	file := model.FilmFile{WorkID: work.ID, PartIndex: 1, PartCount: 1}
	if err := db.Create(&file).Error; err != nil {
		t.Fatalf("create prefixed film file: %v", err)
	}

	got, err := GetFilmFileWithWork(file.ID)
	if err != nil {
		t.Fatalf("get prefixed film file with work: %v", err)
	}
	if got.ID != file.ID || got.Work.ID != work.ID {
		t.Fatalf("prefixed lookup = %+v, want file %d with work %d", got, file.ID, work.ID)
	}
}

func TestReplaceFilmFilesPreservesIDs(t *testing.T) {
	setupMediaRepositoryTestDB(t)
	work := createMediaTestWork(t, 1, "fc2", "FC2-PPV-STABLE", "actor")
	if err := ReplaceFilmFiles(work.ID, []model.FilmFile{
		{PartIndex: 1, PartCount: 2, SourcePath: "part-1"},
		{PartIndex: 2, PartCount: 2, SourcePath: "part-2"},
	}); err != nil {
		t.Fatalf("create initial parts: %v", err)
	}
	initial, err := ListFilmFiles(work.ID)
	if err != nil {
		t.Fatalf("list initial parts: %v", err)
	}
	if err := ReplaceFilmFiles(work.ID, []model.FilmFile{
		{PartIndex: 1, PartCount: 2, SourcePath: "part-1-updated"},
		{PartIndex: 2, PartCount: 2, SourcePath: "part-2"},
	}); err != nil {
		t.Fatalf("replace unchanged topology: %v", err)
	}
	unchanged, err := ListFilmFiles(work.ID)
	if err != nil {
		t.Fatalf("list unchanged topology: %v", err)
	}
	if unchanged[0].ID != initial[0].ID || unchanged[1].ID != initial[1].ID {
		t.Fatalf("film file IDs changed: initial=%+v replacement=%+v", initial, unchanged)
	}
	if err := ReplaceFilmFiles(work.ID, []model.FilmFile{{PartIndex: 1, PartCount: 1, SourcePath: "part-1-final"}}); err != nil {
		t.Fatalf("remove second part: %v", err)
	}
	final, err := ListFilmFiles(work.ID)
	if err != nil {
		t.Fatalf("list final topology: %v", err)
	}
	if len(final) != 1 || final[0].ID != initial[0].ID {
		t.Fatalf("final film files = %+v, want preserved part 1 ID %d", final, initial[0].ID)
	}
}

func TestListFilmFilesWithWorksOrdersAndProjectsTypedRows(t *testing.T) {
	setupMediaRepositoryTestDB(t)
	first := createMediaTestWork(t, 1, "javdb", "ABP-001", "actor")
	second := createMediaTestWork(t, 1, "javdb", "ABP-002", "actor")
	if err := ReplaceFilmFiles(first.ID, []model.FilmFile{{PartIndex: 1, PartCount: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceFilmFiles(second.ID, []model.FilmFile{{PartIndex: 1, PartCount: 2}, {PartIndex: 2, PartCount: 2}}); err != nil {
		t.Fatal(err)
	}

	rows, err := ListFilmFilesWithWorks(1, "javdb", "actor")
	if err != nil {
		t.Fatalf("list film files with works: %v", err)
	}
	if len(rows) != 3 || rows[0].Work.ID != first.ID || rows[1].Work.ID != second.ID || rows[2].PartIndex != 2 {
		t.Fatalf("bulk media rows = %+v", rows)
	}
}

func TestDeleteMediaFileDeletesOnlyOneFile(t *testing.T) {
	setupMediaRepositoryTestDB(t)
	work := createMediaTestWork(t, 1, "javdb", "ABP-DELETE", "actor")
	if err := ReplaceFilmFiles(work.ID, []model.FilmFile{{PartIndex: 1, PartCount: 2}, {PartIndex: 2, PartCount: 2}}); err != nil {
		t.Fatal(err)
	}
	files, err := ListFilmFiles(work.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := DeleteMediaFile(files[0].ID); err != nil {
		t.Fatalf("delete media file: %v", err)
	}
	remaining, err := ListFilmFiles(work.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0].ID != files[1].ID {
		t.Fatalf("remaining files = %+v", remaining)
	}
}

func TestReplaceFilmFilesRejectsNonContiguousParts(t *testing.T) {
	setupMediaRepositoryTestDB(t)

	work := createMediaTestWork(t, 1, "fc2", "FC2-PPV-100", "actor-a")
	original, err := EnsureSingleFilmFile(work.ID)
	if err != nil {
		t.Fatalf("ensure original film file: %v", err)
	}

	invalidTopologies := [][]model.FilmFile{
		{{PartIndex: 1, PartCount: 3}, {PartIndex: 3, PartCount: 3}},
		{{PartIndex: 1, PartCount: 2}, {PartIndex: 2, PartCount: 3}},
		{{PartIndex: 1, PartCount: 2}, {PartIndex: 1, PartCount: 2}},
	}
	for _, files := range invalidTopologies {
		if err := ReplaceFilmFiles(work.ID, files); err == nil {
			t.Fatalf("accepted invalid topology: %+v", files)
		}
	}
	if err := ReplaceFilmFiles(work.ID, nil); err == nil {
		t.Fatal("accepted empty film file topology")
	}

	files, err := ListFilmFiles(work.ID)
	if err != nil {
		t.Fatalf("list files after rejected replacements: %v", err)
	}
	if len(files) != 1 || files[0].ID != original.ID || files[0].PartIndex != 1 || files[0].PartCount != 1 {
		t.Fatalf("rejected replacement changed existing topology: %+v", files)
	}
}

func TestLegacyAndNewTablesCoexist(t *testing.T) {
	tables := []string{"films", "magnet_caches", "film_works", "film_files", "source_magnets"}
	for _, name := range tables {
		if !db.Migrator().HasTable(name) {
			t.Fatalf("table %q does not exist after migration", name)
		}
	}
}

func createMediaTestWork(t *testing.T, storageID uint, source, code, primaryDir string) model.FilmWork {
	t.Helper()

	work := model.FilmWork{
		StorageID:  storageID,
		Source:     source,
		Code:       code,
		SourceRef:  source + "/" + code,
		PrimaryDir: primaryDir,
	}
	if err := db.Create(&work).Error; err != nil {
		t.Fatalf("create media work: %v", err)
	}
	return work
}
