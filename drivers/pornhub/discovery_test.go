package pornhub

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"
	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestMain(m *testing.M) {
	dataDir, err := os.MkdirTemp("", "pornhub-test-")
	if err != nil {
		os.Exit(1)
	}
	conf.Conf = conf.DefaultConfig(dataDir)
	testDB, err := gorm.Open(sqlite.Open(filepath.Join(dataDir, "pornhub-test.db")), &gorm.Config{})
	if err != nil {
		_ = os.RemoveAll(dataDir)
		os.Exit(1)
	}
	if err := db.Init(testDB); err != nil {
		_ = os.RemoveAll(dataDir)
		os.Exit(1)
	}
	code := m.Run()
	if sqlDB, sqlErr := testDB.DB(); sqlErr == nil {
		_ = sqlDB.Close()
	}
	_ = os.RemoveAll(dataDir)
	os.Exit(code)
}

func TestBuildDiscoveredWorkUsesNormalizedViewKeyIdentity(t *testing.T) {
	work, err := buildDiscoveredWork(12, "Actor A", PornFilm{
		ViewKey:   "  abc123  ",
		Title:     "Original title",
		Image:     "https://example.test/cover.jpg",
		SourceURL: "https://www.pornhub.com/view_video.php?viewkey=abc123",
	})
	if err != nil {
		t.Fatal(err)
	}
	if work.StorageID != 12 || work.Source != DriverName || work.Code != "abc123" || work.PrimaryDir != "Actor A" {
		t.Fatalf("identity = %+v", work)
	}
	if work.SourceRef != "abc123" || work.SourceURL != "https://www.pornhub.com/view_video.php?viewkey=abc123" {
		t.Fatalf("source identity = %+v", work)
	}
	if work.RawTitle != "Original title" || work.ImageURL != "https://example.test/cover.jpg" {
		t.Fatalf("discovery fields = %+v", work)
	}
}

func TestBuildDiscoveredWorkDeduplicatesEquivalentViewKeys(t *testing.T) {
	film := PornFilm{ViewKey: "abc123", SourceURL: "https://www.pornhub.com/view_video.php?viewkey=abc123"}
	first, err := buildDiscoveredWork(7, "actor", film)
	if err != nil {
		t.Fatal(err)
	}
	film.ViewKey = "  abc123  "
	second, err := buildDiscoveredWork(7, "actor", film)
	if err != nil {
		t.Fatal(err)
	}
	if first.StorageID != second.StorageID || first.Source != second.Source || first.Code != second.Code {
		t.Fatalf("equivalent view keys produced different identity: first=%+v second=%+v", first, second)
	}
}

func TestBuildDiscoveredWorkRejectsMissingOrInvalidURL(t *testing.T) {
	tests := []PornFilm{
		{ViewKey: "abc123"},
		{ViewKey: "abc123", SourceURL: "://invalid"},
		{ViewKey: "abc123", SourceURL: "ftp://files.example/video.mp4"},
	}
	for _, film := range tests {
		if _, err := buildDiscoveredWork(1, "actor", film); err == nil {
			t.Fatalf("buildDiscoveredWork(%+v) accepted invalid URL", film)
		}
	}
}

func TestConvertFilmsUsesCodeOnlyObjectName(t *testing.T) {
	films, err := convertFilms("Actor A", []PornFilm{{ViewKey: "abc123", Title: "Original title"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(films) != 1 {
		t.Fatalf("len(convertFilms) = %d, want 1", len(films))
	}
	if films[0].Name != "abc123" || films[0].Code != "abc123" || films[0].Title != "Original title" {
		t.Fatalf("projection = %+v", films[0])
	}
}

func TestLinkUsesCanonicalSourceURLDirectly(t *testing.T) {
	d := &Pornhub{}
	file := &model.EmbyFileObj{SourceURL: "https://www.pornhub.com/view_video.php?viewkey=abc123"}

	link, err := d.Link(nil, file, model.LinkArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if link.URL != file.SourceURL {
		t.Fatalf("link URL = %q, want %q", link.URL, file.SourceURL)
	}
}

func TestLinkRejectsMissingCanonicalSourceURL(t *testing.T) {
	if _, err := (&Pornhub{}).Link(nil, &model.EmbyFileObj{}, model.LinkArgs{}); err == nil {
		t.Fatal("Link accepted missing canonical source URL")
	}
}

func TestCacheDiscoveredWorkArtifactsUsesStableIdentity(t *testing.T) {
	original := cacheDiscoveredImageAndNFO
	t.Cleanup(func() { cacheDiscoveredImageAndNFO = original })

	var captured virtual_file.MediaInfo
	cacheDiscoveredImageAndNFO = func(info virtual_file.MediaInfo) int {
		captured = info
		return virtual_file.CreatedSuccess
	}
	work := model.FilmWork{
		StorageID: 12, Source: DriverName, Code: "abc123", PrimaryDir: "Actor A",
		RawTitle: "Original title", TranslatedTitle: "Translated title",
		ImageURL: "https://example.test/cover.jpg", Actors: model.StringArray{"Actor A"}, Tags: model.StringArray{"tag"},
	}
	cacheDiscoveredWorkArtifacts(work)

	if captured.Identity == nil {
		t.Fatal("artifact call omitted media identity")
	}
	identity := *captured.Identity
	if identity.StorageID != 12 || identity.Source != DriverName || identity.PrimaryDir != "Actor A" || identity.Code != "abc123" {
		t.Fatalf("artifact identity = %+v", identity)
	}
	if captured.Title != "abc123 Translated title" || captured.ImgUrl != work.ImageURL {
		t.Fatalf("artifact metadata = %+v", captured)
	}
}

func TestSyncNfoUpdatesIdentityWhenTagMatchingDisabled(t *testing.T) {
	original := updateDiscoveredMediaNFO
	t.Cleanup(func() { updateDiscoveredMediaNFO = original })

	var captured virtual_file.MediaInfo
	updateDiscoveredMediaNFO = func(info virtual_file.MediaInfo) error {
		captured = info
		return nil
	}
	d := &Pornhub{Addition: Addition{SyncNfo: true, MatchFilmTagLimit: 0}}
	files := []model.EmbyFileObj{{
		WorkID: 12, FilmFileID: 22, Code: "abc123", SourceRef: "abc123", SourceURL: "https://www.pornhub.com/view_video.php?viewkey=abc123",
		ObjThumb: model.ObjThumb{Object: model.Object{Name: "abc123.mp4", Path: "Actor A"}},
		Title:    "Existing title", Synopsis: "Synopsis", Actors: []string{"Actor A"}, Tags: []string{"tag"},
	}}
	if err := d.syncDiscoveredNFO(files); err != nil {
		t.Fatal(err)
	}
	if captured.Identity == nil {
		t.Fatal("SyncNfo omitted media identity")
	}
	identity := *captured.Identity
	if identity.StorageID != d.ID || identity.Source != DriverName || identity.PrimaryDir != "Actor A" || identity.Code != "abc123" {
		t.Fatalf("synced identity = %+v", identity)
	}
	if captured.Title != "abc123 Existing title" || captured.Synopsis != "Synopsis" {
		t.Fatalf("synced metadata = %+v", captured)
	}
}

func TestRemoveIndividualMediaFilePreservesSiblingParts(t *testing.T) {
	work := model.FilmWork{StorageID: 43, Source: DriverName, Code: "abc-remove", SourceRef: "abc-remove", PrimaryDir: "actor"}
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

	if err := (&Pornhub{Storage: model.Storage{ID: 43}}).Remove(context.Background(), &model.EmbyFileObj{WorkID: work.ID, FilmFileID: files[0].ID}); err != nil {
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
