package fc2

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/cmd/flags"
	"github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"gorm.io/gorm"
)

func TestBuildDiscoveredWorkAcceptsBareAndCanonicalFC2Codes(t *testing.T) {
	for _, input := range []string{"123", "fc2-ppv-123"} {
		work, err := buildDiscoveredWork(9, "个人收藏", input, "https://example.test/fc2/123", "Original title", "https://example.test/cover.jpg")
		if err != nil {
			t.Fatal(err)
		}
		if work.Code != "FC2-PPV-123" || work.SourceRef != "FC2-PPV-123" || work.SourceURL != "https://example.test/fc2/123" {
			t.Fatalf("input %q produced %+v", input, work)
		}
	}
}

func TestBuildDiscoveredWorkDoesNotCarryEnrichmentState(t *testing.T) {
	work, err := buildDiscoveredWork(9, "actor", "123", "https://example.test/fc2/123", "Raw", "")
	if err != nil {
		t.Fatal(err)
	}
	if work.TranslationStatus != "" || work.TranslationAttempts != 0 || work.SampleImageComplete || work.NfoVersion != 0 {
		t.Fatalf("discovery payload carried enrichment state: %+v", work)
	}
	if work.Actors != nil || work.Tags != nil || work.TranslatedTitle != "" {
		t.Fatalf("discovery payload carried enrichment metadata: %+v", work)
	}
}

func TestFavoritesUseTypedMediaProjection(t *testing.T) {
	file, err := virtual_file.ConvertMediaFileToEmbyFile(model.FilmFileWithWork{
		FilmFile: model.FilmFile{ID: 31, WorkID: 9, PartIndex: 1, PartCount: 1},
		Work:     model.FilmWork{ID: 9, Code: "FC2-PPV-123", PrimaryDir: "个人收藏"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if file.WorkID != 9 || file.FilmFileID != 31 || file.Name != "FC2-PPV-123.mp4" {
		t.Fatalf("favorites projection = %+v", file)
	}
	wrapped := virtual_file.WrapMediaFiles([]model.EmbyFileObj{file})
	if len(wrapped) != 1 || wrapped[0].GetName() != "FC2-PPV-123" {
		t.Fatalf("favorites wrapper = %+v", wrapped)
	}
}

func TestCustomDirectoryUsesTypedMediaProjection(t *testing.T) {
	files := []model.EmbyFileObj{
		{WorkID: 9, FilmFileID: 41, Code: "FC2-PPV-123", ObjThumb: model.ObjThumb{Object: model.Object{Name: "FC2-PPV-123-cd1.mp4"}}},
		{WorkID: 9, FilmFileID: 42, Code: "FC2-PPV-123", ObjThumb: model.ObjThumb{Object: model.Object{Name: "FC2-PPV-123-cd2.mp4"}}, PartIndex: 2, PartCount: 2},
	}
	wrapped := virtual_file.WrapMediaFiles(files)
	if len(wrapped) != 1 || len(wrapped[0].EmbyFiles) != 2 || wrapped[0].GetName() != "FC2-PPV-123" {
		t.Fatalf("custom directory wrapper = %+v", wrapped)
	}
	if wrapped[0].EmbyFiles[0].WorkID != 9 || wrapped[0].EmbyFiles[0].FilmFileID != 41 || wrapped[0].EmbyFiles[1].FilmFileID != 42 {
		t.Fatalf("typed identity lost in custom wrapper: %+v", wrapped[0].EmbyFiles)
	}
	if wrapped[0].EmbyFiles[0].Name != "FC2-PPV-123-cd1.mp4" || wrapped[0].EmbyFiles[1].Name != "FC2-PPV-123-cd2.mp4" {
		t.Fatalf("custom wrapper names = %+v", wrapped[0].EmbyFiles)
	}
}

func TestWrapAddedStarPreservesTypedIdentity(t *testing.T) {
	wrapped, err := wrapAddedStar(model.EmbyFileObj{
		WorkID: 9, FilmFileID: 31, Code: "FC2-PPV-123",
		ObjThumb: model.ObjThumb{Object: model.Object{Name: "FC2-PPV-123.mp4", Path: "个人收藏"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(wrapped.EmbyFiles) != 1 {
		t.Fatalf("wrapped files = %+v", wrapped.EmbyFiles)
	}
	file := wrapped.EmbyFiles[0]
	if wrapped.GetName() != "FC2-PPV-123" || file.WorkID != 9 || file.FilmFileID != 31 || file.Name != "FC2-PPV-123.mp4" {
		t.Fatalf("typed Put wrapper = %+v", wrapped)
	}
}

func TestRemoveIndividualMediaFileDeletesWholeAggregate(t *testing.T) {
	oldDataDir := flags.DataDir
	flags.DataDir = t.TempDir()
	t.Cleanup(func() { flags.DataDir = oldDataDir })
	work := model.FilmWork{StorageID: 42, Source: "fc2", Code: "FC2-PPV-REMOVE", SourceRef: "FC2-PPV-REMOVE", PrimaryDir: "actor"}
	if err := db.GetDb().Create(&work).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceFilmFiles(work.ID, []model.FilmFile{{PartIndex: 1, PartCount: 2}, {PartIndex: 2, PartCount: 2}}); err != nil {
		t.Fatal(err)
	}
	files, err := db.ListFilmFiles(work.ID)
	if err != nil {
		t.Fatal(err)
	}
	paths, err := virtual_file.ResolveMediaArtifactPaths(virtual_file.MediaIdentity{
		StorageID: work.StorageID, Source: work.Source, PrimaryDir: work.PrimaryDir, Code: work.Code,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.Root, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := (&FC2{Storage: model.Storage{ID: 42}}).Remove(context.Background(), &model.EmbyFileObj{WorkID: work.ID, FilmFileID: files[0].ID}); err != nil {
		t.Fatalf("remove individual file: %v", err)
	}
	var workCount int64
	if err := db.GetDb().Model(&model.FilmWork{}).Where("id = ?", work.ID).Count(&workCount).Error; err != nil {
		t.Fatal(err)
	}
	if workCount != 0 {
		t.Fatalf("remaining work count = %d", workCount)
	}
	var tombstoneCount int64
	if err := db.GetDb().Model(&model.MissedFilm{}).Where("code = ?", work.Code).Count(&tombstoneCount).Error; err != nil {
		t.Fatal(err)
	}
	if tombstoneCount != 1 {
		t.Fatalf("tombstone count = %d, want 1", tombstoneCount)
	}
	if _, err := os.Stat(paths.Root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("artifact root still exists or stat failed: %v", err)
	}
}

func TestFC2DiscoverySkipsTombstonesAndContinuesAfterBadItem(t *testing.T) {
	resetFC2DiscoveryTables(t)
	if err := db.CreateMissedFilms([]string{"FC2-PPV-700"}); err != nil {
		t.Fatal(err)
	}
	oldFetch := fetchFC2DailyPageFilms
	t.Cleanup(func() { fetchFC2DailyPageFilms = oldFetch })
	page := 0
	fetchFC2DailyPageFilms = func(*FC2, string) ([]string, error) {
		page++
		if page == 1 {
			return []string{"FC2-PPV-700", "", "FC2-PPV-701"}, nil
		}
		return nil, nil
	}
	driver := FC2{Storage: model.Storage{ID: 70}}

	films, err := driver.getFilms("actor", func(index int) string { return "page" })

	if err != nil {
		t.Fatal(err)
	}
	if len(films) != 1 || films[0].Code != "FC2-PPV-701" {
		t.Fatalf("discovered films = %+v", films)
	}
}

func TestFC2DiscoveryRetainsAccumulatedIDsAfterPageFailure(t *testing.T) {
	resetFC2DiscoveryTables(t)
	oldFetch := fetchFC2DailyPageFilms
	t.Cleanup(func() { fetchFC2DailyPageFilms = oldFetch })
	page := 0
	fetchFC2DailyPageFilms = func(*FC2, string) ([]string, error) {
		page++
		if page == 1 {
			return []string{"FC2-PPV-702"}, nil
		}
		return nil, errors.New("page unavailable")
	}
	driver := FC2{Storage: model.Storage{ID: 70}}

	films, err := driver.getFilms("actor", func(index int) string { return "page" })

	if err != nil {
		t.Fatal(err)
	}
	if len(films) != 1 || films[0].Code != "FC2-PPV-702" {
		t.Fatalf("films after page failure = %+v", films)
	}
}

func TestMissAVSyncContinuesAfterInvalidItem(t *testing.T) {
	resetFC2DiscoveryTables(t)
	work := model.FilmWork{
		StorageID: 70, Source: "fc2", Code: "FC2-PPV-703", SourceRef: "FC2-PPV-703",
		SourceURL: "FC2-PPV-703", PrimaryDir: "actor",
	}
	if err := db.GetDb().Create(&work).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := db.EnsureSingleFilmFile(work.ID); err != nil {
		t.Fatal(err)
	}
	driver := FC2{Storage: model.Storage{ID: 70}}

	err := driver.syncMissAvFilms([]model.EmbyFileObj{
		{ObjThumb: model.ObjThumb{Object: model.Object{Name: ""}}},
		{ObjThumb: model.ObjThumb{Object: model.Object{Name: work.Code}}, Tags: []string{"ranked"}},
	})

	if err != nil {
		t.Fatal(err)
	}
	updated, err := db.GetFilmWork(work.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Tags) != 1 || updated.Tags[0] != "ranked" {
		t.Fatalf("updated tags = %#v", updated.Tags)
	}
}

func resetFC2DiscoveryTables(t *testing.T) {
	t.Helper()
	for _, value := range []interface{}{&model.SourceMagnet{}, &model.FilmFile{}, &model.FilmWork{}, &model.MissedFilm{}} {
		if err := db.GetDb().Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(value).Error; err != nil {
			t.Fatalf("reset %T: %v", value, err)
		}
	}
}
