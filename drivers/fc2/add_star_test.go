package fc2

import (
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/av"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

func TestAddStarStoresRawOriginalAndTranslatedTitleSeparately(t *testing.T) {
	resetFC2MediaWorks(t)
	oldSuke := getFC2SukeMeta
	oldTranslate := translateFC2Title
	oldFetch := fetchFC2DailyFilm
	t.Cleanup(func() {
		getFC2SukeMeta = oldSuke
		translateFC2Title = oldTranslate
		fetchFC2DailyFilm = oldFetch
	})
	getFC2SukeMeta = func(string) (av.Meta, error) {
		return av.Meta{Magnets: []av.Magnet{fc2TestMagnet{
			uri:   "magnet:?xt=urn:btih:abc",
			name:  "FC2-PPV-123 オリジナルタイトル.mp4",
			files: []av.File{{Name: "FC2-PPV-123 オリジナルタイトル.mp4", Size: 300 * 1024 * 1024}},
		}}}, nil
	}
	translateFC2Title = func(string) string { return "中文标题" }
	fetchFC2DailyFilm = func(*FC2, string) (model.EmbyFileObj, error) {
		return model.EmbyFileObj{
			Actors:      []string{"女优"},
			ReleaseTime: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
		}, nil
	}

	if _, err := (&FC2{Storage: model.Storage{ID: 71}}).addStar("123", nil); err != nil {
		t.Fatal(err)
	}

	stored, err := db.GetFilmWorkByIdentity(71, "fc2", "FC2-PPV-123")
	if err != nil {
		t.Fatal(err)
	}
	if stored.RawTitle != "オリジナルタイトル" {
		t.Fatalf("raw title = %q, want %q", stored.RawTitle, "オリジナルタイトル")
	}
	if stored.TranslatedTitle != "中文标题" {
		t.Fatalf("translated title = %q, want %q", stored.TranslatedTitle, "中文标题")
	}
}
