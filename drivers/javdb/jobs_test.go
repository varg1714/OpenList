package javdb

import (
	"reflect"
	"slices"
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
