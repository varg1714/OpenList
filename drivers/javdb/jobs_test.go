package javdb

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/av"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"gorm.io/gorm"
)

func TestFilterFilmsDeletesNormalizedAggregateByCodePrefix(t *testing.T) {
	resetJavdbMediaWorks(t)
	filtered := model.FilmWork{
		StorageID: 80, Source: DriverName, Code: "FILTER-ID-TEST", SourceRef: "filtered",
		SourceURL: "filtered", PrimaryDir: "actor",
	}
	kept := model.FilmWork{
		StorageID: 80, Source: DriverName, Code: "KEEP-ID-TEST", SourceRef: "kept",
		SourceURL: "kept", PrimaryDir: "actor",
	}
	for _, work := range []*model.FilmWork{&filtered, &kept} {
		if err := db.GetDb().Create(work).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := db.EnsureSingleFilmFile(work.ID); err != nil {
			t.Fatal(err)
		}
	}

	driver := Javdb{Storage: model.Storage{ID: 80}, Addition: Addition{Filter: " FILTER-ID , OTHER "}}
	if err := driver.filterFilms(); err != nil {
		t.Fatal(err)
	}
	var filteredCount, keptCount int64
	if err := db.GetDb().Model(&model.FilmWork{}).Where("id = ?", filtered.ID).Count(&filteredCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.GetDb().Model(&model.FilmWork{}).Where("id = ?", kept.ID).Count(&keptCount).Error; err != nil {
		t.Fatal(err)
	}
	if filteredCount != 0 || keptCount != 1 {
		t.Fatalf("filtered count = %d, kept count = %d", filteredCount, keptCount)
	}
}

func TestAddStarReusesFilmWorkWithoutLegacyMagnetCache(t *testing.T) {
	resetJavdbMediaWorks(t)
	if err := db.GetDb().Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&model.MagnetCache{}).Error; err != nil {
		t.Fatal(err)
	}
	work := model.FilmWork{
		StorageID: 81, Source: DriverName, Code: "ABP-123", SourceRef: "https://javdb.test/v/abp-123",
		SourceURL: "https://javdb.test/v/abp-123", PrimaryDir: "个人收藏", Tags: model.StringArray{"existing"},
	}
	if err := db.GetDb().Create(&work).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.GetDb().Create(&model.FilmFile{WorkID: work.ID, PartIndex: 1, PartCount: 1}).Error; err != nil {
		t.Fatal(err)
	}

	file, err := (&Javdb{Storage: model.Storage{ID: 81}}).addStar("abp-123", []string{"favorite"})
	if err != nil {
		t.Fatal(err)
	}
	if file.WorkID != work.ID || file.FilmFileID == 0 {
		t.Fatalf("favorite identity = work %d, file %d", file.WorkID, file.FilmFileID)
	}
	stored, err := db.GetFilmWork(work.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stored.Tags, model.StringArray{"existing", "favorite"}) {
		t.Fatalf("addStar tags = %#v", stored.Tags)
	}
	var legacyCount int64
	if err := db.GetDb().Model(&model.MagnetCache{}).Count(&legacyCount).Error; err != nil {
		t.Fatal(err)
	}
	if legacyCount != 0 {
		t.Fatalf("addStar wrote %d legacy magnet caches", legacyCount)
	}
}

func TestAddStarPersistsPlaceholderWhenJavDBSearchForbidden(t *testing.T) {
	resetJavdbMediaWorks(t)
	oldSearch := searchJavdbFilms
	t.Cleanup(func() { searchJavdbFilms = oldSearch })
	searchJavdbFilms = func(*Javdb, string) ([]model.EmbyFileObj, error) {
		return nil, errors.New("Forbidden")
	}

	file, err := (&Javdb{Storage: model.Storage{ID: 81}}).addStar("abp-999", []string{"favorite"})
	if err != nil {
		t.Fatal(err)
	}
	if file.Code != "ABP-999" || file.WorkID == 0 || file.FilmFileID == 0 {
		t.Fatalf("placeholder identity = %+v", file)
	}
	if file.Title != "ABP-999" || file.SourceURL != "" {
		t.Fatalf("placeholder projection = %+v", file)
	}
	stored, err := db.GetFilmWork(file.WorkID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Code != "ABP-999" || stored.SourceURL != "" || stored.SourceRef != "ABP-999" || stored.PrimaryDir != "个人收藏" {
		t.Fatalf("placeholder work = %+v", stored)
	}
	if !reflect.DeepEqual(stored.Tags, model.StringArray{"favorite"}) {
		t.Fatalf("placeholder tags = %#v", stored.Tags)
	}
}

func TestAddStarDoesNotPersistWhenFilmIsNotFound(t *testing.T) {
	resetJavdbMediaWorks(t)
	oldSearch := searchJavdbFilms
	t.Cleanup(func() { searchJavdbFilms = oldSearch })
	searchJavdbFilms = func(*Javdb, string) ([]model.EmbyFileObj, error) {
		return nil, nil
	}

	_, err := (&Javdb{Storage: model.Storage{ID: 81}}).addStar("abp-998", nil)
	if err == nil || !strings.Contains(err.Error(), "未查询到") {
		t.Fatalf("addStar error = %v, want 未查询到", err)
	}
	var count int64
	if err := db.GetDb().Model(&model.FilmWork{}).Where("code = ?", "ABP-998").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("persisted %d works for missing film", count)
	}
}

func TestAddStarResolvesExistingPlaceholderWhenSearchSucceeds(t *testing.T) {
	resetJavdbMediaWorks(t)
	placeholder := model.FilmWork{
		StorageID: 81, Source: DriverName, Code: "ABP-997", SourceRef: "ABP-997", PrimaryDir: "个人收藏",
		Tags: model.StringArray{"favorite"},
	}
	if err := db.GetDb().Create(&placeholder).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := db.EnsureSingleFilmFile(placeholder.ID); err != nil {
		t.Fatal(err)
	}
	oldSearch := searchJavdbFilms
	t.Cleanup(func() { searchJavdbFilms = oldSearch })
	searchJavdbFilms = func(*Javdb, string) ([]model.EmbyFileObj, error) {
		return []model.EmbyFileObj{{
			ObjThumb: model.ObjThumb{
				Object:    model.Object{Name: "ABP-997 Original title"},
				Thumbnail: model.Thumbnail{Thumbnail: "https://jdbstatic.com/covers/abp-997.jpg"},
			},
			Url: "https://javdb.com/v/abp-997",
		}}, nil
	}

	file, err := (&Javdb{Storage: model.Storage{ID: 81}}).addStar("abp-997", nil)
	if err != nil {
		t.Fatal(err)
	}
	if file.WorkID != placeholder.ID || file.SourceURL != "https://javdb.com/v/abp-997" {
		t.Fatalf("resolved projection = %+v", file)
	}
	stored, err := db.GetFilmWork(placeholder.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.SourceURL != "https://javdb.com/v/abp-997" || stored.RawTitle != "Original title" || stored.ImageURL != "https://jdbstatic.com/covers/abp-997.jpg" {
		t.Fatalf("resolved work = %+v", stored)
	}
}

func TestAddFavoriteFilmsStopsAfterUnresolvedSource(t *testing.T) {
	resetJavdbMediaWorks(t)
	oldSearch := searchJavdbFilms
	t.Cleanup(func() { searchJavdbFilms = oldSearch })
	var searched []string
	searchJavdbFilms = func(_ *Javdb, code string) ([]model.EmbyFileObj, error) {
		searched = append(searched, code)
		return nil, errors.New("Forbidden")
	}

	missed, err := (&Javdb{Storage: model.Storage{ID: 83}}).addFavoriteFilms([]string{"ABP-401", "ABP-402"}, []string{"JavDB-TOP250"})
	if !errors.Is(err, errUnresolvedJavdbSource) {
		t.Fatalf("addFavoriteFilms error = %v, want errUnresolvedJavdbSource", err)
	}
	if len(missed) != 0 {
		t.Fatalf("missed = %v", missed)
	}
	if !reflect.DeepEqual(searched, []string{"ABP-401"}) {
		t.Fatalf("searched = %#v, want only the first code", searched)
	}
	var count int64
	if err := db.GetDb().Model(&model.FilmWork{}).Where("storage_id = ?", 83).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("persisted %d works, want 1 placeholder", count)
	}
}

func TestScanUnresolvedSourcesFillsDiscoveryFields(t *testing.T) {
	resetJavdbMediaWorks(t)
	work := model.FilmWork{
		StorageID: 82, Source: DriverName, Code: "ABP-996", SourceRef: "ABP-996", PrimaryDir: "个人收藏",
		Tags: model.StringArray{"favorite"},
	}
	if err := db.GetDb().Create(&work).Error; err != nil {
		t.Fatal(err)
	}
	oldSearch := searchJavdbFilms
	t.Cleanup(func() { searchJavdbFilms = oldSearch })
	searchJavdbFilms = func(*Javdb, string) ([]model.EmbyFileObj, error) {
		return []model.EmbyFileObj{{
			ObjThumb: model.ObjThumb{
				Object:    model.Object{Name: "ABP-996 Original title"},
				Thumbnail: model.Thumbnail{Thumbnail: "https://jdbstatic.com/covers/abp-996.jpg"},
			},
			Url: "https://javdb.com/v/abp-996",
		}}, nil
	}

	(&Javdb{Storage: model.Storage{ID: 82}}).scanUnresolvedSources()

	stored, err := db.GetFilmWork(work.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.SourceURL != "https://javdb.com/v/abp-996" || stored.RawTitle != "Original title" || stored.ImageURL != "https://jdbstatic.com/covers/abp-996.jpg" {
		t.Fatalf("resolved work = %+v", stored)
	}
	if !reflect.DeepEqual(stored.Tags, model.StringArray{"favorite"}) {
		t.Fatalf("resolved tags = %#v", stored.Tags)
	}
}

func TestScanUnresolvedSourcesDefersRetryWhenSearchForbidden(t *testing.T) {
	resetJavdbMediaWorks(t)
	work := model.FilmWork{
		StorageID: 82, Source: DriverName, Code: "ABP-995", SourceRef: "ABP-995", PrimaryDir: "个人收藏",
	}
	if err := db.GetDb().Create(&work).Error; err != nil {
		t.Fatal(err)
	}
	oldSearch := searchJavdbFilms
	t.Cleanup(func() { searchJavdbFilms = oldSearch })
	searchCalls := 0
	searchJavdbFilms = func(*Javdb, string) ([]model.EmbyFileObj, error) {
		searchCalls++
		return nil, errors.New("Forbidden")
	}
	scanStartedAt := time.Now()

	(&Javdb{Storage: model.Storage{ID: 82}}).scanUnresolvedSources()
	(&Javdb{Storage: model.Storage{ID: 82}}).scanUnresolvedSources()

	stored, err := db.GetFilmWork(work.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.SourceScanAt == nil || stored.SourceNextRetryAt == nil || !stored.SourceNextRetryAt.After(scanStartedAt) || stored.SourceLastError != "Forbidden" {
		t.Fatalf("source stage = (scan=%v retry=%v error=%q)", stored.SourceScanAt, stored.SourceNextRetryAt, stored.SourceLastError)
	}
	if stored.SourceURL != "" {
		t.Fatalf("placeholder SourceURL = %q", stored.SourceURL)
	}
	if searchCalls != 1 {
		t.Fatalf("search calls = %d, want 1 across immediate repeated scans", searchCalls)
	}
}

type javdbMetadataTestMagnet struct {
	uri      string
	subtitle bool
	tags     []string
}

func (m javdbMetadataTestMagnet) GetMagnet() string         { return m.uri }
func (m javdbMetadataTestMagnet) GetName() string           { return "test" }
func (m javdbMetadataTestMagnet) GetSize() uint64           { return 0 }
func (m javdbMetadataTestMagnet) IsSubTitle() bool          { return m.subtitle }
func (m javdbMetadataTestMagnet) GetTags() []string         { return m.tags }
func (m javdbMetadataTestMagnet) GetDownloadCount() uint64  { return 0 }
func (m javdbMetadataTestMagnet) GetFiles() []av.File       { return nil }
func (m javdbMetadataTestMagnet) GetReleaseDate() time.Time { return time.Time{} }

func TestMetadataScanAddsSubtitleTagAndMarksNFOStale(t *testing.T) {
	resetJavdbMediaWorks(t)
	work := model.FilmWork{
		StorageID: 85, Source: DriverName, Code: "ABP-850", SourceRef: "https://javdb.test/v/850",
		SourceURL: "https://javdb.test/v/850", PrimaryDir: "actor", MetadataVersion: 1, NfoVersion: 1,
	}
	if err := db.GetDb().Create(&work).Error; err != nil {
		t.Fatal(err)
	}

	oldFetch := getJavdbMeta
	t.Cleanup(func() { getJavdbMeta = oldFetch })
	getJavdbMeta = func(string) (av.Meta, error) {
		return av.Meta{Magnets: []av.Magnet{javdbMetadataTestMagnet{
			uri: "magnet:subtitle", subtitle: true, tags: []string{"HD"},
		}}}, nil
	}
	(&Javdb{Addition: Addition{MatchFilmTagLimit: 1}}).scanMediaMetadataAndMagnets()

	updated, err := db.GetFilmWork(work.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains([]string(updated.Tags), model.TagSubtitle) {
		t.Fatalf("updated tags = %#v", updated.Tags)
	}
	if updated.MetadataVersion <= updated.NfoVersion {
		t.Fatalf("metadata version = %d, NFO version = %d", updated.MetadataVersion, updated.NfoVersion)
	}
}

func TestMergeTagsDeduplicatesCallerTags(t *testing.T) {
	got := mergeTags(model.StringArray{"existing"}, []string{"favorite", "existing", ""})
	want := model.StringArray{"existing", "favorite"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mergeTags() = %#v, want %#v", got, want)
	}
}
