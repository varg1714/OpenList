package javdb

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/av"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"gorm.io/gorm"
)

func TestMetadataScanDefersActorStageWhenMetadataFetchFails(t *testing.T) {
	// Given
	resetJavdbMediaWorks(t)
	work := model.FilmWork{
		StorageID: 86, Source: DriverName, Code: "FC2-2473598", SourceRef: "https://javdb.test/v/rate-limited",
		SourceURL: "https://javdb.test/v/rate-limited", PrimaryDir: "actor",
	}
	if err := db.GetDb().Create(&work).Error; err != nil {
		t.Fatalf("seed work: %v", err)
	}
	oldFetch := getJavdbMeta
	t.Cleanup(func() { getJavdbMeta = oldFetch })
	fetchCalls := 0
	getJavdbMeta = func(string) (av.Meta, error) {
		fetchCalls++
		return av.Meta{}, errors.New("Too Many Requests")
	}
	scanStartedAt := time.Now()

	// When
	(&Javdb{Addition: Addition{MatchFilmTagLimit: 1}}).scanMediaMetadataAndMagnets()
	(&Javdb{Addition: Addition{MatchFilmTagLimit: 1}}).scanMediaMetadataAndMagnets()

	// Then
	updated, err := db.GetFilmWork(work.ID)
	if err != nil {
		t.Fatalf("get failed work: %v", err)
	}
	if updated.ActorScanAt == nil || updated.ActorNextRetryAt == nil || !updated.ActorNextRetryAt.After(scanStartedAt) || updated.ActorLastError != "Too Many Requests" {
		t.Fatalf("actor stage = (scan=%v retry=%v error=%q), want deferred rate-limit retry", updated.ActorScanAt, updated.ActorNextRetryAt, updated.ActorLastError)
	}
	if fetchCalls != 1 {
		t.Fatalf("metadata fetch calls = %d, want 1 across immediate repeated scans", fetchCalls)
	}
}

func TestMetadataScanMarksEmptyActorResultComplete(t *testing.T) {
	// Given
	for _, value := range []interface{}{&model.SourceMagnet{}, &model.FilmFile{}, &model.FilmWork{}} {
		if err := db.GetDb().Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(value).Error; err != nil {
			t.Fatalf("reset %T: %v", value, err)
		}
	}
	work := model.FilmWork{
		StorageID: 86, Source: DriverName, Code: "ABP-860", SourceRef: "https://javdb.test/v/860",
		SourceURL: "https://javdb.test/v/860", PrimaryDir: "actor",
	}
	if err := db.GetDb().Create(&work).Error; err != nil {
		t.Fatalf("seed work: %v", err)
	}
	oldFetch := getJavdbMeta
	t.Cleanup(func() { getJavdbMeta = oldFetch })
	getJavdbMeta = func(string) (av.Meta, error) {
		return av.Meta{Magnets: []av.Magnet{javdbMetadataTestMagnet{uri: "magnet:empty-actors"}}}, nil
	}

	// When
	(&Javdb{Addition: Addition{MatchFilmTagLimit: 1}}).scanMediaMetadataAndMagnets()

	// Then
	updated, err := db.GetFilmWork(work.ID)
	if err != nil {
		t.Fatalf("get scanned work: %v", err)
	}
	if updated.ActorScanAt == nil || updated.ActorNextRetryAt != nil || updated.ActorLastError != "" {
		t.Fatalf("actor stage = (scan=%v retry=%v error=%q), want completed empty result", updated.ActorScanAt, updated.ActorNextRetryAt, updated.ActorLastError)
	}
}

func TestMetadataScanRewritesMatchedActorIdToLocalFolderName(t *testing.T) {
	// Given: a pending work whose scraped actor names differ from the local folder name.
	resetJavdbMediaWorks(t)
	if err := db.GetDb().Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&model.Actor{}).Error; err != nil {
		t.Fatalf("reset actors: %v", err)
	}
	work := model.FilmWork{
		StorageID: 86, Source: DriverName, Code: "ABP-861", SourceRef: "https://javdb.test/v/861",
		SourceURL: "https://javdb.test/v/861", PrimaryDir: "MyFolder",
	}
	if err := db.GetDb().Create(&work).Error; err != nil {
		t.Fatalf("seed work: %v", err)
	}
	if err := db.CreateActor("99", "WrongFolder", "abc123"); err != nil {
		t.Fatalf("seed other-storage actor: %v", err)
	}
	if err := db.CreateActor("86", "MyFolder", "abc123"); err != nil {
		t.Fatalf("seed local actor: %v", err)
	}
	oldFetch := getJavdbMeta
	t.Cleanup(func() { getJavdbMeta = oldFetch })
	getJavdbMeta = func(string) (av.Meta, error) {
		return av.Meta{
			Actors: []av.Actor{
				{Id: "abc123", Name: "Mikami Yua"},
				{Id: "other", Name: "Someone Else"},
			},
			Magnets: []av.Magnet{javdbMetadataTestMagnet{uri: "magnet:mapped-actors"}},
		}, nil
	}

	// When: metadata scan runs on a driver whose ID is not the work storage.
	(&Javdb{Addition: Addition{MatchFilmTagLimit: 1}}).scanMediaMetadataAndMagnets()

	// Then: matched actorId uses the local folder name; unmatched keeps the JavDB name.
	updated, err := db.GetFilmWork(work.ID)
	if err != nil {
		t.Fatalf("get scanned work: %v", err)
	}
	want := model.StringArray{"MyFolder", "Someone Else"}
	if !slices.Equal([]string(updated.Actors), []string(want)) {
		t.Fatalf("actors = %#v, want %#v", updated.Actors, want)
	}
}
