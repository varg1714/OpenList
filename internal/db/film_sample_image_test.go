package db

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestMain(m *testing.M) {
	dataDir, err := os.MkdirTemp("", "film-sample-image-test-")
	if err != nil {
		panic(err)
	}
	conf.Conf = conf.DefaultConfig(dataDir)
	testDB, err := gorm.Open(sqlite.Open(filepath.Join(dataDir, "test.db")), &gorm.Config{})
	if err != nil {
		panic(err)
	}
	db = testDB
	if err := AutoMigrate(new(model.Film), new(model.MagnetCache)); err != nil {
		panic(err)
	}

	code := m.Run()
	if sqlDB, sqlErr := testDB.DB(); sqlErr == nil {
		_ = sqlDB.Close()
	}
	_ = os.RemoveAll(dataDir)
	os.Exit(code)
}

func setupFilmSampleImageTestDB(t *testing.T) {
	t.Helper()

	previousConf := conf.Conf
	conf.Conf = conf.DefaultConfig("data")
	if err := db.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&model.Film{}).Error; err != nil {
		t.Fatalf("reset films: %v", err)
	}
	if err := db.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&model.MagnetCache{}).Error; err != nil {
		t.Fatalf("reset magnet caches: %v", err)
	}

	t.Cleanup(func() {
		conf.Conf = previousConf
	})
}

func TestQueryDMMPosterFilmsEligibilityAndLimit(t *testing.T) {
	setupFilmSampleImageTestDB(t)

	now := time.Now()
	films := make([]model.Film, 0, 27)
	for index := 0; index < 21; index++ {
		films = append(films, model.Film{
			Name:   fmt.Sprintf("MIDV-%d.mp4", index+1),
			Source: "javdb",
			Date:   now.Add(time.Duration(index) * time.Minute),
		})
	}
	films = append(films,
		model.Film{Name: "pending.mp4", Source: "javdb", Date: now.Add(2 * time.Hour), DMMPosterStatus: model.DMMPosterStatusPending, DMMPosterScanAt: now},
		model.Film{Name: "retry.mp4", Source: "javdb", Date: now.Add(3 * time.Hour), DMMPosterStatus: model.DMMPosterStatusTransientError, DMMPosterScanAt: now.Add(-73 * time.Hour)},
		model.Film{Name: "null-scan-retry.mp4", Source: "javdb", Date: now.Add(4 * time.Hour), DMMPosterStatus: model.DMMPosterStatusTransientError},
		model.Film{Name: "fresh-transient.mp4", Source: "javdb", DMMPosterStatus: model.DMMPosterStatusTransientError, DMMPosterScanAt: now.Add(-71 * time.Hour)},
		model.Film{Name: "success.mp4", Source: "javdb", DMMPosterStatus: model.DMMPosterStatusSuccess, DMMPosterScanAt: now.Add(-100 * time.Hour)},
		model.Film{Name: "not-found.mp4", Source: "javdb", DMMPosterStatus: model.DMMPosterStatusNotFound, DMMPosterScanAt: now.Add(-100 * time.Hour)},
		model.Film{Name: "wrong-source.mp4", Source: "fc2"},
	)
	if err := db.Create(&films).Error; err != nil {
		t.Fatalf("create films: %v", err)
	}
	if err := db.Model(&model.Film{}).Where("name = ?", "null-scan-retry.mp4").Update("dmm_poster_scan_at", nil).Error; err != nil {
		t.Fatalf("set null DMM poster scan time: %v", err)
	}

	got, err := QueryDMMPosterFilms(72*time.Hour, 20)
	if err != nil {
		t.Fatalf("query DMM poster films: %v", err)
	}
	if len(got) != 20 {
		t.Fatalf("got %d films, want bounded batch of 20", len(got))
	}
	if got[0].Name != "null-scan-retry.mp4" || got[1].Name != "retry.mp4" || got[2].Name != "pending.mp4" {
		t.Fatalf("first films = %q, %q, %q, want null-scan retry, stale retry, and pending", got[0].Name, got[1].Name, got[2].Name)
	}
	for _, film := range got {
		if film.Name == "fresh-transient.mp4" || film.Name == "success.mp4" || film.Name == "not-found.mp4" || film.Name == "wrong-source.mp4" {
			t.Fatalf("ineligible film returned: %s", film.Name)
		}
	}
}

func TestUpdateDMMPosterStatusSetsStatusAndScanTime(t *testing.T) {
	setupFilmSampleImageTestDB(t)

	film := model.Film{Name: "MIDV-169.mp4", Source: "javdb"}
	if err := db.Create(&film).Error; err != nil {
		t.Fatalf("create film: %v", err)
	}
	before := time.Now()
	if err := UpdateDMMPosterStatus(film.ID, model.DMMPosterStatusSuccess); err != nil {
		t.Fatalf("update DMM poster status: %v", err)
	}

	stored := loadFilmForSampleImageTest(t, film.ID)
	if stored.DMMPosterStatus != model.DMMPosterStatusSuccess {
		t.Fatalf("status = %q, want %q", stored.DMMPosterStatus, model.DMMPosterStatusSuccess)
	}
	if stored.DMMPosterScanAt.Before(before) || stored.DMMPosterScanAt.After(time.Now()) {
		t.Fatalf("scan time = %s, want current timestamp", stored.DMMPosterScanAt)
	}
}

func TestQueryFC2SampleImageGroups(t *testing.T) {
	setupFilmSampleImageTestDB(t)

	now := time.Now()
	stale := now.Add(-2 * time.Hour)
	films := []model.Film{
		{Name: "FC2-100-cd1", Source: "fc2", Actor: "alice", Url: "FC2-100", SampleImageCount: 1, SampleImageScanAt: stale},
		{Name: "FC2-100-cd2", Source: "fc2", Actor: "alice", Url: "FC2-100", SampleImageCount: 7, SampleImageScanAt: stale},
		{Name: "FC2-100-cd3", Source: "fc2", Actor: "alice", Url: "FC2-100", SampleImageCount: 4},
		{Name: "FC2-100-cd1", Source: "fc2", Actor: "bob", Url: "FC2-100", SampleImageCount: 3, SampleImageScanAt: stale},
		{Name: "empty-url", Source: "fc2", Actor: "alice", Url: "", SampleImageScanAt: stale},
		{Name: "wrong-source", Source: "javdb", Actor: "alice", Url: "FC2-200", SampleImageScanAt: stale},
		{Name: "fresh-cd1", Source: "fc2", Actor: "fresh", Url: "FC2-300", SampleImageScanAt: stale},
		{Name: "fresh-cd2", Source: "fc2", Actor: "fresh", Url: "FC2-300", SampleImageScanAt: now},
		{Name: "complete-cd1", Source: "fc2", Actor: "complete", Url: "FC2-400", SampleImageScanAt: stale},
		{Name: "complete-cd2", Source: "fc2", Actor: "complete", Url: "FC2-400", SampleImageComplete: true, SampleImageScanAt: stale},
	}
	if err := db.Create(&films).Error; err != nil {
		t.Fatalf("create films: %v", err)
	}
	if err := db.Model(&model.Film{}).Where("name = ?", "FC2-100-cd3").Update("sample_image_scan_at", nil).Error; err != nil {
		t.Fatalf("set null scan time: %v", err)
	}

	groups, err := QueryFC2SampleImageGroups(time.Hour, 20)
	if err != nil {
		t.Fatalf("query FC2 sample-image groups: %v", err)
	}
	want := []FC2SampleImageGroup{
		{Source: "fc2", Actor: "alice", URL: "FC2-100", SampleImageCount: 7},
		{Source: "fc2", Actor: "bob", URL: "FC2-100", SampleImageCount: 3},
	}
	if len(groups) != len(want) {
		t.Fatalf("got %d groups (%+v), want %d", len(groups), groups, len(want))
	}
	for i := range want {
		if groups[i] != want[i] {
			t.Errorf("group %d = %+v, want %+v", i, groups[i], want[i])
		}
	}
}

func TestQueryFC2SampleImageGroupsLimitAppliesAfterGrouping(t *testing.T) {
	setupFilmSampleImageTestDB(t)

	films := make([]model.Film, 0, 63)
	for group := 0; group < 21; group++ {
		for cd := 1; cd <= 3; cd++ {
			films = append(films, model.Film{
				Name:   "sibling",
				Source: "fc2",
				Actor:  "actor",
				Url:    "FC2-" + string(rune('A'+group)),
			})
		}
	}
	if err := db.Create(&films).Error; err != nil {
		t.Fatalf("create films: %v", err)
	}

	groups, err := QueryFC2SampleImageGroups(time.Hour, 20)
	if err != nil {
		t.Fatalf("query FC2 sample-image groups: %v", err)
	}
	if len(groups) != 20 {
		t.Fatalf("got %d groups, want limit of 20 groups", len(groups))
	}
	for i := 1; i < len(groups); i++ {
		if groups[i-1].URL >= groups[i].URL {
			t.Fatalf("groups are not deterministically ordered: %q before %q", groups[i-1].URL, groups[i].URL)
		}
	}
}

func TestFC2SampleImageGroupUpdateHelpers(t *testing.T) {
	setupFilmSampleImageTestDB(t)

	oldScanAt := time.Now().Add(-24 * time.Hour)
	films := []model.Film{
		{Name: "FC2-500-cd1", Source: "fc2", Actor: "alice", Url: "FC2-500", SampleImageCount: 9, SampleImageScanAt: oldScanAt},
		{Name: "FC2-500-cd2", Source: "fc2", Actor: "alice", Url: "FC2-500", SampleImageCount: 2, SampleImageScanAt: oldScanAt},
		{Name: "FC2-500-cd3", Source: "fc2", Actor: "alice", Url: "FC2-500", SampleImageCount: 0, SampleImageScanAt: oldScanAt},
		{Name: "other-actor", Source: "fc2", Actor: "bob", Url: "FC2-500", SampleImageCount: 1, SampleImageScanAt: oldScanAt},
		{Name: "other-source", Source: "javdb", Actor: "alice", Url: "FC2-500", SampleImageCount: 1, SampleImageScanAt: oldScanAt},
		{Name: "other-url", Source: "fc2", Actor: "alice", Url: "FC2-501", SampleImageCount: 1, SampleImageScanAt: oldScanAt},
	}
	if err := db.Create(&films).Error; err != nil {
		t.Fatalf("create films: %v", err)
	}

	if err := UpdateFC2SampleImageGroupProgress("alice", "FC2-500", 8, false); err != nil {
		t.Fatalf("update group progress: %v", err)
	}
	if err := UpdateFC2SampleImageGroupProgress("alice", "FC2-500", 12, true); err != nil {
		t.Fatalf("complete group progress: %v", err)
	}
	assertFC2GroupState(t, "alice", "FC2-500", 12, true, oldScanAt)

	if err := db.Model(&model.Film{}).Where("source = ? AND actor = ? AND url = ?", "fc2", "alice", "FC2-500").Updates(map[string]interface{}{
		"sample_image_complete": false,
		"sample_image_scan_at":  oldScanAt,
	}).Error; err != nil {
		t.Fatalf("reset group state: %v", err)
	}
	beforeComplete := time.Now()
	if err := MarkFC2SampleImageGroupComplete("alice", "FC2-500"); err != nil {
		t.Fatalf("mark group complete: %v", err)
	}
	assertFC2GroupScanState(t, "alice", "FC2-500", true, beforeComplete)

	if err := db.Model(&model.Film{}).Where("source = ? AND actor = ? AND url = ?", "fc2", "alice", "FC2-500").Updates(map[string]interface{}{
		"sample_image_complete": false,
		"sample_image_scan_at":  oldScanAt,
	}).Error; err != nil {
		t.Fatalf("reset group scan state: %v", err)
	}
	beforeScan := time.Now()
	if err := UpdateFC2SampleImageGroupScanAt("alice", "FC2-500"); err != nil {
		t.Fatalf("update group scan time: %v", err)
	}
	assertFC2GroupScanState(t, "alice", "FC2-500", false, beforeScan)

	for _, untouchedID := range []uint{films[3].ID, films[4].ID, films[5].ID} {
		untouched := loadFilmForSampleImageTest(t, untouchedID)
		if untouched.SampleImageCount != 1 || untouched.SampleImageComplete || !untouched.SampleImageScanAt.Equal(oldScanAt) {
			t.Errorf("unrelated film %d changed: %+v", untouchedID, untouched)
		}
	}
}

func TestQueryFC2MagnetCacheByCodePrefersExactDriver(t *testing.T) {
	setupFilmSampleImageTestDB(t)

	caches := []model.MagnetCache{
		{Name: "javdb", DriverType: "javdb", Code: "ABC-123", Magnet: "magnet:javdb"},
		{Name: "legacy", DriverType: "", Code: "ABC-123", Magnet: "magnet:legacy"},
		{Name: "fc2", DriverType: "fc2", Code: "ABC-123", Magnet: "magnet:fc2"},
	}
	if err := db.Create(&caches).Error; err != nil {
		t.Fatalf("create magnet caches: %v", err)
	}

	got, err := QueryFC2MagnetCacheByCode("ABC-123")
	if err != nil {
		t.Fatalf("query FC2 magnet cache: %v", err)
	}
	if got.ID != caches[2].ID || got.Magnet != caches[2].Magnet {
		t.Fatalf("got cache %+v, want %+v", got, caches[2])
	}
}

func TestQueryFC2MagnetCacheByCodeMigratesLegacyDriverType(t *testing.T) {
	for _, driverType := range []any{"", nil} {
		t.Run(fmt.Sprintf("driver_type_%v", driverType), func(t *testing.T) {
			setupFilmSampleImageTestDB(t)

			legacy := model.MagnetCache{Name: "legacy", Code: "FC2-LEGACY", Magnet: "magnet:legacy"}
			if err := db.Create(&legacy).Error; err != nil {
				t.Fatalf("create legacy magnet cache: %v", err)
			}
			if err := db.Model(&legacy).Update("driver_type", driverType).Error; err != nil {
				t.Fatalf("set legacy driver type: %v", err)
			}
			if err := db.Create(&model.MagnetCache{DriverType: "javdb", Code: legacy.Code, Magnet: "magnet:wrong"}).Error; err != nil {
				t.Fatalf("create other-driver magnet cache: %v", err)
			}

			got, err := QueryFC2MagnetCacheByCode(legacy.Code)
			if err != nil {
				t.Fatalf("query FC2 magnet cache: %v", err)
			}
			if got.ID != legacy.ID || got.Magnet != legacy.Magnet || got.DriverType != "fc2" {
				t.Fatalf("got cache %+v, want migrated legacy cache %+v", got, legacy)
			}

			var persisted model.MagnetCache
			if err := db.First(&persisted, legacy.ID).Error; err != nil {
				t.Fatalf("reload migrated magnet cache: %v", err)
			}
			if persisted.DriverType != "fc2" {
				t.Fatalf("persisted driver type = %q, want fc2", persisted.DriverType)
			}
		})
	}
}

func TestQueryFC2MagnetCacheByCodeNotFound(t *testing.T) {
	setupFilmSampleImageTestDB(t)
	if err := db.Create(&model.MagnetCache{DriverType: "javdb", Code: "FC2-MISSING", Magnet: "magnet:wrong"}).Error; err != nil {
		t.Fatalf("create other-driver magnet cache: %v", err)
	}

	got, err := QueryFC2MagnetCacheByCode("FC2-MISSING")
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("error = %v, want gorm.ErrRecordNotFound", err)
	}
	if got.ID != 0 {
		t.Fatalf("got cache %+v, want zero value", got)
	}
}

func TestQueryFC2MagnetCacheByCodePropagatesDatabaseErrors(t *testing.T) {
	previousDB := db
	brokenDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open broken test database: %v", err)
	}
	sqlDB, err := brokenDB.DB()
	if err != nil {
		t.Fatalf("get SQL database: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close SQL database: %v", err)
	}
	db = brokenDB
	t.Cleanup(func() { db = previousDB })

	_, err = QueryFC2MagnetCacheByCode("FC2-ERROR")
	if err == nil || errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("error = %v, want database error", err)
	}
}

func TestQuerySampleImageFilms(t *testing.T) {
	setupFilmSampleImageTestDB(t)

	now := time.Now()
	films := []model.Film{
		{Name: "older", Source: "target", Image: "older.jpg", Date: now.Add(-time.Hour), SampleImageScanAt: now.Add(-2 * time.Hour)},
		{Name: "newer-first", Source: "target", Image: "first.jpg", Date: now, SampleImageScanAt: now.Add(-2 * time.Hour)},
		{Name: "newer-second", Source: "target", Image: "second.jpg", Date: now},
		{Name: "wrong-source", Source: "other", Image: "other.jpg", Date: now.Add(time.Hour), SampleImageScanAt: now.Add(-2 * time.Hour)},
		{Name: "empty-image", Source: "target", Date: now.Add(time.Hour), SampleImageScanAt: now.Add(-2 * time.Hour)},
		{Name: "complete", Source: "target", Image: "complete.jpg", Date: now.Add(time.Hour), SampleImageComplete: true, SampleImageScanAt: now.Add(-2 * time.Hour)},
		{Name: "fresh", Source: "target", Image: "fresh.jpg", Date: now.Add(time.Hour), SampleImageScanAt: now},
	}
	if err := db.Create(&films).Error; err != nil {
		t.Fatalf("create films: %v", err)
	}
	if err := db.Model(&model.Film{}).Where("id = ?", films[2].ID).Update("sample_image_scan_at", nil).Error; err != nil {
		t.Fatalf("set null scan time: %v", err)
	}

	got, err := QuerySampleImageFilms("target", time.Hour, 2)
	if err != nil {
		t.Fatalf("query sample-image films: %v", err)
	}
	wantIDs := []uint{films[2].ID, films[1].ID}
	if len(got) != len(wantIDs) {
		t.Fatalf("got %d films, want %d", len(got), len(wantIDs))
	}
	for i, wantID := range wantIDs {
		if got[i].ID != wantID {
			t.Errorf("film %d ID = %d, want %d", i, got[i].ID, wantID)
		}
	}
}

func TestQueryFanartFilmsUsesURLAndConfiguredCount(t *testing.T) {
	setupFilmSampleImageTestDB(t)

	now := time.Now()
	films := []model.Film{
		{Name: "no-cover", Source: "target", Url: "view-1", Date: now, SampleImageScanAt: now.Add(-2 * time.Hour)},
		{Name: "count-increased", Source: "target", Url: "view-2", Date: now.Add(-time.Minute), SampleImageCount: 2, SampleImageComplete: true, SampleImageScanAt: now.Add(-2 * time.Hour)},
		{Name: "at-target", Source: "target", Url: "view-3", Date: now.Add(time.Hour), SampleImageCount: 3, SampleImageComplete: true, SampleImageScanAt: now.Add(-2 * time.Hour)},
		{Name: "wrong-source", Source: "other", Url: "view-4", Date: now.Add(time.Hour), SampleImageScanAt: now.Add(-2 * time.Hour)},
		{Name: "empty-url", Source: "target", Date: now.Add(time.Hour), SampleImageScanAt: now.Add(-2 * time.Hour)},
		{Name: "fresh", Source: "target", Url: "view-5", Date: now.Add(time.Hour), SampleImageScanAt: now},
	}
	if err := db.Create(&films).Error; err != nil {
		t.Fatalf("create films: %v", err)
	}

	got, err := QueryFanartFilms("target", time.Hour, 10, 3)
	if err != nil {
		t.Fatalf("query fanart films: %v", err)
	}
	wantIDs := []uint{films[2].ID, films[0].ID, films[1].ID}
	if len(got) != len(wantIDs) {
		t.Fatalf("got %d films, want %d", len(got), len(wantIDs))
	}
	for index, wantID := range wantIDs {
		if got[index].ID != wantID {
			t.Errorf("film %d ID = %d, want %d", index, got[index].ID, wantID)
		}
	}
}

func TestSampleImageUpdateHelpers(t *testing.T) {
	setupFilmSampleImageTestDB(t)

	oldScanAt := time.Now().Add(-24 * time.Hour)
	film := model.Film{
		Name:                "sample",
		Source:              "target",
		Image:               "sample.jpg",
		SampleImageCount:    9,
		SampleImageComplete: true,
		SampleImageScanAt:   oldScanAt,
	}
	if err := db.Create(&film).Error; err != nil {
		t.Fatalf("create film: %v", err)
	}

	if err := UpdateSampleImageProgress(film.ID, 0, false); err != nil {
		t.Fatalf("update progress: %v", err)
	}
	progress := loadFilmForSampleImageTest(t, film.ID)
	if progress.SampleImageCount != 9 {
		t.Errorf("sample image count = %d, want 9", progress.SampleImageCount)
	}
	if !progress.SampleImageComplete {
		t.Error("sample image complete = false, want true")
	}
	if !progress.SampleImageScanAt.Equal(oldScanAt) {
		t.Errorf("progress scan time = %s, want unchanged %s", progress.SampleImageScanAt, oldScanAt)
	}

	if err := db.Model(&model.Film{}).Where("id = ?", film.ID).Updates(map[string]interface{}{
		"sample_image_complete": false,
		"sample_image_scan_at":  oldScanAt,
	}).Error; err != nil {
		t.Fatalf("reset progress state: %v", err)
	}
	if err := UpdateSampleImageProgress(film.ID, 12, false); err != nil {
		t.Fatalf("advance progress: %v", err)
	}
	if err := UpdateSampleImageProgress(film.ID, 10, true); err != nil {
		t.Fatalf("complete progress: %v", err)
	}
	progress = loadFilmForSampleImageTest(t, film.ID)
	if progress.SampleImageCount != 12 || !progress.SampleImageComplete {
		t.Errorf("monotonic progress = (%d, %t), want (12, true)", progress.SampleImageCount, progress.SampleImageComplete)
	}
	if !progress.SampleImageScanAt.Equal(oldScanAt) {
		t.Errorf("advanced progress scan time = %s, want unchanged %s", progress.SampleImageScanAt, oldScanAt)
	}

	if err := db.Model(&model.Film{}).Where("id = ?", film.ID).Update("sample_image_scan_at", oldScanAt).Error; err != nil {
		t.Fatalf("reset scan time: %v", err)
	}
	beforeComplete := time.Now()
	if err := MarkSampleImageComplete(film.ID); err != nil {
		t.Fatalf("mark complete: %v", err)
	}
	complete := loadFilmForSampleImageTest(t, film.ID)
	if !complete.SampleImageComplete {
		t.Error("sample image complete = false, want true")
	}
	assertScanTimeUpdated(t, complete.SampleImageScanAt, beforeComplete)

	if err := db.Model(&model.Film{}).Where("id = ?", film.ID).Update("sample_image_scan_at", oldScanAt).Error; err != nil {
		t.Fatalf("reset scan time: %v", err)
	}
	beforeScan := time.Now()
	if err := UpdateSampleImageScanAt(film.ID); err != nil {
		t.Fatalf("update scan time: %v", err)
	}
	scanned := loadFilmForSampleImageTest(t, film.ID)
	if scanned.SampleImageCount != 12 || !scanned.SampleImageComplete {
		t.Errorf("scan-only update changed progress: count=%d complete=%t", scanned.SampleImageCount, scanned.SampleImageComplete)
	}
	assertScanTimeUpdated(t, scanned.SampleImageScanAt, beforeScan)
}

func loadFilmForSampleImageTest(t *testing.T, filmID uint) model.Film {
	t.Helper()

	var film model.Film
	if err := db.First(&film, filmID).Error; err != nil {
		t.Fatalf("load film: %v", err)
	}
	return film
}

func assertFC2GroupState(t *testing.T, actor, url string, count int, complete bool, scanAt time.Time) {
	t.Helper()

	var films []model.Film
	if err := db.Where("source = ? AND actor = ? AND url = ?", "fc2", actor, url).Order("id").Find(&films).Error; err != nil {
		t.Fatalf("load FC2 group: %v", err)
	}
	if len(films) != 3 {
		t.Fatalf("got %d FC2 siblings, want 3", len(films))
	}
	for _, film := range films {
		if film.SampleImageCount != count || film.SampleImageComplete != complete {
			t.Errorf("film %d progress = (%d, %t), want (%d, %t)", film.ID, film.SampleImageCount, film.SampleImageComplete, count, complete)
		}
		if !film.SampleImageScanAt.Equal(scanAt) {
			t.Errorf("film %d scan time = %s, want unchanged %s", film.ID, film.SampleImageScanAt, scanAt)
		}
	}
}

func assertFC2GroupScanState(t *testing.T, actor, url string, complete bool, earliest time.Time) {
	t.Helper()

	var films []model.Film
	if err := db.Where("source = ? AND actor = ? AND url = ?", "fc2", actor, url).Order("id").Find(&films).Error; err != nil {
		t.Fatalf("load FC2 group: %v", err)
	}
	if len(films) != 3 {
		t.Fatalf("got %d FC2 siblings, want 3", len(films))
	}
	for _, film := range films {
		if film.SampleImageCount != 12 || film.SampleImageComplete != complete {
			t.Errorf("film %d state = (%d, %t), want (12, %t)", film.ID, film.SampleImageCount, film.SampleImageComplete, complete)
		}
		assertScanTimeUpdated(t, film.SampleImageScanAt, earliest)
	}
}

func assertScanTimeUpdated(t *testing.T, scanAt, earliest time.Time) {
	t.Helper()

	if scanAt.Before(earliest) {
		t.Errorf("scan time %s is before update started at %s", scanAt, earliest)
	}
}
