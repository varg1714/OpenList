package javdb

import (
	"context"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

func TestBuildDiscoveredWorkNormalizesIdentityAndPreservesDiscoveryScope(t *testing.T) {
	work, err := buildDiscoveredWork(12, "Actor A", model.EmbyFileObj{
		ObjThumb:    model.ObjThumb{Object: model.Object{Name: "abp-123 Original title"}, Thumbnail: model.Thumbnail{Thumbnail: "https://example.test/cover.jpg"}},
		Url:         "https://javdb.com/v/abp-123",
		Title:       "abp-123 Original title",
		ReleaseTime: model.EmbyFileObj{}.ReleaseTime,
	})
	if err != nil {
		t.Fatal(err)
	}
	if work.StorageID != 12 || work.Source != DriverName || work.Code != "ABP-123" || work.PrimaryDir != "Actor A" {
		t.Fatalf("identity = %+v", work)
	}
	if work.SourceRef != "https://javdb.com/v/abp-123" || work.SourceURL != work.SourceRef || work.RawTitle != "Original title" {
		t.Fatalf("discovery fields = %+v", work)
	}
}

func TestJavDBDiscoveryStopMarkerUsesFilmWorkCode(t *testing.T) {
	if !isExistingDiscoveredWork(model.EmbyFileObj{ObjThumb: model.ObjThumb{Object: model.Object{Name: "abp-123 title"}}}, map[string]bool{"ABP-123": true}) {
		t.Fatal("canonical existing FilmWork code was not recognized")
	}
	if isExistingDiscoveredWork(model.EmbyFileObj{ObjThumb: model.ObjThumb{Object: model.Object{Name: "abp-124 title"}}}, map[string]bool{"ABP-123": true}) {
		t.Fatal("different FilmWork code incorrectly stopped discovery")
	}
}

func TestDiscoveredWorkTopologySupportsSingleAndMultipartFiles(t *testing.T) {
	single, err := discoveredFilmFiles(1)
	if err != nil || len(single) != 1 || single[0].PartIndex != 1 || single[0].PartCount != 1 {
		t.Fatalf("single topology = %+v, err=%v", single, err)
	}
	multipart, err := discoveredFilmFiles(3)
	if err != nil || len(multipart) != 3 {
		t.Fatalf("multipart topology = %+v, err=%v", multipart, err)
	}
	for index, file := range multipart {
		if file.PartIndex != index+1 || file.PartCount != 3 {
			t.Fatalf("multipart file %d = %+v", index, file)
		}
	}
}

func TestFavoritesUseTypedMediaProjection(t *testing.T) {
	file, err := virtual_file.ConvertMediaFileToEmbyFile(model.FilmFileWithWork{
		FilmFile: model.FilmFile{ID: 22, WorkID: 12, PartIndex: 1, PartCount: 1},
		Work:     model.FilmWork{ID: 12, Code: "ABP-123", PrimaryDir: "个人收藏"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if file.WorkID != 12 || file.FilmFileID != 22 || file.Name != "ABP-123.mp4" {
		t.Fatalf("favorites projection = %+v", file)
	}
	wrapped := virtual_file.WrapMediaFiles([]model.EmbyFileObj{file})
	if len(wrapped) != 1 || wrapped[0].GetName() != "ABP-123" {
		t.Fatalf("favorites wrapper = %+v", wrapped)
	}
}

func TestWrapAddedStarPreservesTypedIdentity(t *testing.T) {
	wrapped, err := wrapAddedStar(model.EmbyFileObj{
		WorkID: 12, FilmFileID: 22, Code: "ABP-123",
		ObjThumb: model.ObjThumb{Object: model.Object{Name: "ABP-123.mp4", Path: "个人收藏"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(wrapped.EmbyFiles) != 1 {
		t.Fatalf("wrapped files = %+v", wrapped.EmbyFiles)
	}
	file := wrapped.EmbyFiles[0]
	if wrapped.GetName() != "ABP-123" || file.WorkID != 12 || file.FilmFileID != 22 || file.Name != "ABP-123.mp4" {
		t.Fatalf("typed Put wrapper = %+v", wrapped)
	}
}

func TestRemoveIndividualMediaFilePreservesSiblingParts(t *testing.T) {
	work := model.FilmWork{StorageID: 41, Source: DriverName, Code: "ABP-REMOVE", SourceRef: "ABP-REMOVE", PrimaryDir: "actor"}
	if err := db.GetDb().Create(&work).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = virtual_file.DeleteMediaWork(work.ID) })
	if err := db.ReplaceFilmFiles(work.ID, []model.FilmFile{{PartIndex: 1, PartCount: 2}, {PartIndex: 2, PartCount: 2}}); err != nil {
		t.Fatal(err)
	}
	files, err := db.ListFilmFiles(work.ID)
	if err != nil {
		t.Fatal(err)
	}

	if err := (&Javdb{Storage: model.Storage{ID: 41}}).Remove(context.Background(), &model.EmbyFileObj{WorkID: work.ID, FilmFileID: files[0].ID}); err != nil {
		t.Fatalf("remove individual file: %v", err)
	}
	remaining, err := db.ListFilmFiles(work.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0].ID != files[1].ID {
		t.Fatalf("remaining files = %+v", remaining)
	}
}
