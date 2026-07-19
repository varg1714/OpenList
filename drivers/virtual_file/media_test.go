package virtual_file

import (
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

func TestConvertMediaFileToEmbyFileProjectsSingleFile(t *testing.T) {
	item := model.FilmFileWithWork{
		FilmFile: model.FilmFile{ID: 9, WorkID: 3, PartIndex: 1, PartCount: 1},
		Work: model.FilmWork{
			ID:              3,
			Code:            "ABP-123",
			SourceRef:       "javdb:ABP-123",
			SourceURL:       "https://example.test/ABP-123",
			PrimaryDir:      "Actor A",
			RawTitle:        "Original title",
			TranslatedTitle: "Translated title",
			Synopsis:        "Synopsis",
			ImageURL:        "https://example.test/cover.jpg",
			Actors:          model.StringArray{"Actor A", "Actor B"},
			Tags:            model.StringArray{"tag-a", "tag-b"},
		},
	}

	got, err := ConvertMediaFileToEmbyFile(item)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "ABP-123.mp4" {
		t.Fatalf("Name = %q, want %q", got.Name, "ABP-123.mp4")
	}
	if got.Title != "ABP-123 Translated title" {
		t.Fatalf("Title = %q, want translated title", got.Title)
	}
	if got.ID != "9" || got.Path != "Actor A" {
		t.Fatalf("object identity = ID %q, Path %q", got.ID, got.Path)
	}
	if got.WorkID != 3 || got.FilmFileID != 9 || got.Code != "ABP-123" {
		t.Fatalf("typed identity lost: %+v", got)
	}
	if got.PartIndex != 1 || got.PartCount != 1 {
		t.Fatalf("part identity = %d/%d, want 1/1", got.PartIndex, got.PartCount)
	}
	if got.SourceRef != item.Work.SourceRef || got.SourceURL != item.Work.SourceURL || got.Url != item.Work.SourceURL {
		t.Fatalf("source identity lost: SourceRef=%q SourceURL=%q Url=%q", got.SourceRef, got.SourceURL, got.Url)
	}
	if got.Thumb() != item.Work.ImageURL || got.Synopsis != item.Work.Synopsis || !got.Translated {
		t.Fatalf("metadata projection incomplete: %+v", got)
	}
	if len(got.Actors) != 2 || len(got.Tags) != 2 {
		t.Fatalf("actors/tags projection incomplete: %+v", got)
	}
}

func TestConvertMediaFileToEmbyFileBuildsMultipartNames(t *testing.T) {
	for _, part := range []struct {
		index int
		want  string
	}{
		{index: 1, want: "ABP-123-cd1.mp4"},
		{index: 2, want: "ABP-123-cd2.mp4"},
	} {
		item := model.FilmFileWithWork{
			FilmFile: model.FilmFile{ID: uint(part.index), WorkID: 3, PartIndex: part.index, PartCount: 2},
			Work:     model.FilmWork{ID: 3, Code: "ABP-123"},
		}

		got, err := ConvertMediaFileToEmbyFile(item)
		if err != nil {
			t.Fatal(err)
		}
		if got.Name != part.want {
			t.Fatalf("part %d Name = %q, want %q", part.index, got.Name, part.want)
		}
	}
}

func TestConvertMediaFileToEmbyFileUsesMediaTitleFallback(t *testing.T) {
	tests := []struct {
		name       string
		raw        string
		translated string
		want       string
	}{
		{name: "translated", raw: "Raw", translated: "Translated", want: "ABP-123 Translated"},
		{name: "raw", raw: "Raw", want: "ABP-123 Raw"},
		{name: "code", want: "ABP-123"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := model.FilmFileWithWork{
				FilmFile: model.FilmFile{ID: 9, WorkID: 3, PartIndex: 1, PartCount: 1},
				Work: model.FilmWork{
					ID: 3, Code: "ABP-123", RawTitle: tt.raw, TranslatedTitle: tt.translated,
				},
			}

			got, err := ConvertMediaFileToEmbyFile(item)
			if err != nil {
				t.Fatal(err)
			}
			if got.Title != tt.want {
				t.Fatalf("Title = %q, want %q", got.Title, tt.want)
			}
		})
	}
}

func TestWrapMediaFilesGroupsByWorkID(t *testing.T) {
	files := []model.EmbyFileObj{
		{WorkID: 10, Code: "SAME", ObjThumb: model.ObjThumb{Object: model.Object{Name: "alpha-cd1.mp4"}}},
		{WorkID: 10, Code: "SAME", ObjThumb: model.ObjThumb{Object: model.Object{Name: "different-cd2.mp4"}}},
		{WorkID: 20, Code: "SAME", ObjThumb: model.ObjThumb{Object: model.Object{Name: "alpha-cd1.mp4"}}},
	}

	wrapped := WrapMediaFiles(files)
	if len(wrapped) != 2 {
		t.Fatalf("len(WrapMediaFiles) = %d, want 2: %+v", len(wrapped), wrapped)
	}
	groups := make(map[uint]int, len(wrapped))
	for _, wrapper := range wrapped {
		if len(wrapper.EmbyFiles) == 0 {
			t.Fatal("WrapMediaFiles returned an empty group")
		}
		workID := wrapper.EmbyFiles[0].WorkID
		groups[workID] = len(wrapper.EmbyFiles)
		for _, file := range wrapper.EmbyFiles {
			if file.WorkID != workID {
				t.Fatalf("group %d contains work %d", workID, file.WorkID)
			}
		}
	}
	if groups[10] != 2 || groups[20] != 1 {
		t.Fatalf("group sizes = %+v, want map[10:2 20:1]", groups)
	}
}

func TestConvertMediaFileToEmbyFileRejectsInvalidFilmFile(t *testing.T) {
	item := model.FilmFileWithWork{
		FilmFile: model.FilmFile{ID: 9, WorkID: 3, PartIndex: 2, PartCount: 1},
		Work:     model.FilmWork{ID: 3, Code: "ABP-123"},
	}

	if _, err := ConvertMediaFileToEmbyFile(item); err == nil {
		t.Fatal("ConvertMediaFileToEmbyFile() error = nil, want invalid part error")
	}
}
