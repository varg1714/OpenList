package pornhub

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/cmd/flags"
	"github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"gorm.io/gorm"
)

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

func TestBuildDiscoveredWorkUsesPrimaryDirActorAndPlaylistTags(t *testing.T) {
	work, err := buildDiscoveredWork(12, "Playlist A", PornFilm{
		ViewKey: "abc124", Title: "Original title", SourceURL: "https://www.pornhub.com/view_video.php?viewkey=abc124",
		Tags: []string{"Playlist A"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(work.Actors) != 1 || work.Actors[0] != "Playlist A" {
		t.Fatalf("fallback actors = %#v", work.Actors)
	}
	if len(work.Tags) != 1 || work.Tags[0] != "Playlist A" {
		t.Fatalf("playlist tags = %#v", work.Tags)
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
	films, err := convertFilms("Actor A", []PornFilm{{ViewKey: "abc123", Title: "Original title", Tags: []string{"playlist"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(films) != 1 {
		t.Fatalf("len(convertFilms) = %d, want 1", len(films))
	}
	if films[0].Name != "abc123" || films[0].Code != "abc123" || films[0].Title != "Original title" {
		t.Fatalf("projection = %+v", films[0])
	}
	if len(films[0].Tags) != 1 || films[0].Tags[0] != "playlist" {
		t.Fatalf("projection tags = %#v", films[0].Tags)
	}
}

func TestLinkUsesCanonicalSourceURLDirectly(t *testing.T) {
	d := &Pornhub{Addition: Addition{ServerUrl: "https://www.pornhub.com"}}
	file := &model.EmbyFileObj{SourceURL: "https://www.pornhub.com/view_video.php?viewkey=abc123"}

	link, err := d.Link(nil, file, model.LinkArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if link.URL != file.SourceURL {
		t.Fatalf("link URL = %q, want %q", link.URL, file.SourceURL)
	}
	if link.Header.Get("Referer") != d.ServerUrl {
		t.Fatalf("link Referer = %q", link.Header.Get("Referer"))
	}
}

func TestLinkAddsRefererToResolvedPornhubVideo(t *testing.T) {
	oldResolve := resolvePornhubVideoLink
	t.Cleanup(func() { resolvePornhubVideoLink = oldResolve })
	resolvePornhubVideoLink = func(context.Context, *Pornhub, string) (string, error) {
		return "https://cdn.pornhub.test/video.mp4", nil
	}
	driver := Pornhub{Addition: Addition{ServerUrl: "https://www.pornhub.com"}}

	link, err := driver.Link(context.Background(), &model.EmbyFileObj{SourceRef: "abc123"}, model.LinkArgs{})

	if err != nil {
		t.Fatal(err)
	}
	if link.Header.Get("Referer") != driver.ServerUrl {
		t.Fatalf("link Referer = %q", link.Header.Get("Referer"))
	}
}

func TestLinkReturnsMockedLinkWithoutResolutionWhenMockedEnabled(t *testing.T) {
	oldResolve := resolvePornhubVideoLink
	t.Cleanup(func() { resolvePornhubVideoLink = oldResolve })
	calls := 0
	resolvePornhubVideoLink = func(context.Context, *Pornhub, string) (string, error) {
		calls++
		return "", errors.New("unexpected resolution")
	}
	driver := Pornhub{Addition: Addition{Mocked: true, MockedLink: "https://mock.test/video.mp4"}}

	link, err := driver.Link(context.Background(), &model.EmbyFileObj{SourceRef: "abc123"}, model.LinkArgs{})

	if err != nil {
		t.Fatal(err)
	}
	if link.URL != driver.MockedLink || calls != 0 {
		t.Fatalf("mock link = %q, resolver calls = %d", link.URL, calls)
	}
	if _, exists := link.Header["Referer"]; exists {
		t.Fatalf("mock link leaked Referer header %#v", link.Header)
	}
}

func TestLinkReturnsResolutionErrorWhenMockedDisabled(t *testing.T) {
	oldResolve := resolvePornhubVideoLink
	t.Cleanup(func() { resolvePornhubVideoLink = oldResolve })
	wantErr := errors.New("resolution failed")
	resolvePornhubVideoLink = func(context.Context, *Pornhub, string) (string, error) { return "", wantErr }
	driver := Pornhub{Addition: Addition{MockedLink: "https://dormant.test/video.mp4"}}

	_, err := driver.Link(context.Background(), &model.EmbyFileObj{SourceRef: "abc123"}, model.LinkArgs{})

	if !errors.Is(err, wantErr) {
		t.Fatalf("resolution error = %v, want %v", err, wantErr)
	}
}

func TestLinkRejectsMissingCanonicalSourceURL(t *testing.T) {
	if _, err := (&Pornhub{}).Link(nil, &model.EmbyFileObj{}, model.LinkArgs{}); err == nil {
		t.Fatal("Link accepted missing canonical source URL")
	}
}

func TestReMatchTagsLeavesNFOStaleForScheduledSync(t *testing.T) {
	if err := db.GetDb().Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&model.FilmWork{}).Error; err != nil {
		t.Fatal(err)
	}
	oldDataDir := flags.DataDir
	flags.DataDir = t.TempDir()
	t.Cleanup(func() { flags.DataDir = oldDataDir })
	oldWait := waitPornhubTagScan
	waitPornhubTagScan = func(time.Duration) {}
	t.Cleanup(func() { waitPornhubTagScan = oldWait })
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_, _ = response.Write([]byte(`<div class="tagsWrapper"><div class="gtm-event-video-underplayer item"><span>tag-a</span></div></div>`))
	}))
	t.Cleanup(server.Close)
	work := model.FilmWork{
		StorageID: 86, Source: DriverName, Code: "abc860", SourceRef: "abc860", SourceURL: server.URL,
		PrimaryDir: "actor", RawTitle: "title", MetadataVersion: 1, NfoVersion: 1,
	}
	if err := db.GetDb().Create(&work).Error; err != nil {
		t.Fatal(err)
	}
	driver := Pornhub{
		Storage:  model.Storage{ID: 86},
		Addition: Addition{ServerUrl: server.URL, MatchFilmTagLimit: 1},
	}

	driver.reMatchTags()

	updated, err := db.GetFilmWork(work.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.MetadataVersion <= updated.NfoVersion {
		t.Fatalf("metadata version = %d, NFO version = %d", updated.MetadataVersion, updated.NfoVersion)
	}
	paths, err := virtual_file.ResolveMediaArtifactPaths(virtual_file.MediaIdentity{
		StorageID: work.StorageID, Source: work.Source, PrimaryDir: work.PrimaryDir, Code: work.Code,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(paths.NFO); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("enrichment wrote NFO directly: %v", err)
	}
}

func TestRemoveIndividualMediaFileDeletesWholeAggregate(t *testing.T) {
	work := model.FilmWork{StorageID: 43, Source: DriverName, Code: "abc-remove", SourceRef: "abc-remove", PrimaryDir: "actor"}
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

	if err := (&Pornhub{Storage: model.Storage{ID: 43}}).Remove(context.Background(), &model.EmbyFileObj{WorkID: work.ID, FilmFileID: files[0].ID}); err != nil {
		t.Fatalf("remove individual file: %v", err)
	}
	var workCount int64
	if err := db.GetDb().Model(&model.FilmWork{}).Where("id = ?", work.ID).Count(&workCount).Error; err != nil {
		t.Fatal(err)
	}
	if workCount != 0 {
		t.Fatalf("remaining work count = %d", workCount)
	}
}
