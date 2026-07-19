package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestMigrateLegacyMediaMapsSourcesFilesAndCaches(t *testing.T) {
	database := newMigrationTestDB(t)
	fixture := seedCompleteLegacyFixture(t, database)

	report, err := MigrateLegacyMedia(context.Background(), database)
	if err != nil {
		t.Fatalf("migrate legacy media: %v", err)
	}
	if report.LegacyFilms != 5 || report.LegacyMagnetCaches != 7 {
		t.Fatalf("legacy counts = films %d, caches %d", report.LegacyFilms, report.LegacyMagnetCaches)
	}
	if report.WorksCreated != 3 || report.FilesCreated != 5 || report.SourceMagnetsCreated != 2 || report.CloudFileCachesCreated != 4 {
		t.Fatalf("created counts = %+v", report)
	}

	javdb := getWork(t, database, 1, "javdb", "ABP-123")
	if javdb.PrimaryDir != "Actor A" || javdb.SourceRef != fixture.javdbURL || javdb.SourceURL != fixture.javdbURL {
		t.Fatalf("JavDB identity = %+v", javdb)
	}
	if javdb.RawTitle != "Original title" || javdb.TranslatedTitle != "Translated title" || javdb.Synopsis != "Synopsis" || javdb.ImageURL != "https://img.test/abp.jpg" {
		t.Fatalf("JavDB text metadata = %+v", javdb)
	}
	if !reflect.DeepEqual(javdb.Actors, model.StringArray{"Actor A", "Actor B"}) || !reflect.DeepEqual(javdb.Tags, model.StringArray{"tag-a", model.TagSubtitle}) {
		t.Fatalf("JavDB arrays = actors %#v, tags %#v", javdb.Actors, javdb.Tags)
	}
	if !javdb.ReleaseDate.Equal(fixture.releaseDate) || !javdb.SynopsisExcluded || !javdb.SampleImageComplete || javdb.SampleImageCount != 4 || javdb.SampleImageScanAt == nil || javdb.DMMPosterScanAt == nil {
		t.Fatalf("JavDB stage state = %+v", javdb)
	}

	fc2 := getWork(t, database, 2, "fc2", "FC2-PPV-100")
	if fc2.SourceRef != "FC2-PPV-100" || fc2.SourceURL != fixture.fc2URL || fc2.PrimaryDir != "Seller A" || fc2.TranslatedTitle != "FC2 translated" {
		t.Fatalf("FC2 work = %+v", fc2)
	}
	fc2Files := listFiles(t, database, fc2.ID)
	if len(fc2Files) != 3 {
		t.Fatalf("FC2 files = %+v", fc2Files)
	}
	for index, file := range fc2Files {
		if file.PartIndex != index+1 || file.PartCount != 3 || file.SourcePath != fmt.Sprintf("FC2-100-cd%d.mp4", index+1) {
			t.Fatalf("FC2 file %d = %+v", index, file)
		}
	}

	pornhub := getWork(t, database, 3, "pornhub", "view-key-9")
	if pornhub.SourceRef != "view-key-9" || pornhub.SourceURL != fixture.pornhubURL || pornhub.RawTitle != "" || pornhub.TranslatedTitle != "Porn title" {
		t.Fatalf("Pornhub work = %+v", pornhub)
	}

	javdbMagnets := listMagnets(t, database, javdb.ID)
	if len(javdbMagnets) != 1 || !javdbMagnets[0].Selected || !javdbMagnets[0].Subtitle {
		t.Fatalf("JavDB magnets = %+v", javdbMagnets)
	}
	wantJavFingerprint := fingerprint(fixture.javdbMagnet)
	if javdbMagnets[0].Fingerprint != wantJavFingerprint || len(javdbMagnets[0].FileManifest) != 1 || javdbMagnets[0].FileManifest[0].Path != "abp-123 Original title.mp4" {
		t.Fatalf("JavDB magnet manifest = %+v", javdbMagnets[0])
	}
	fc2Magnets := listMagnets(t, database, fc2.ID)
	if len(fc2Magnets) != 1 || len(fc2Magnets[0].FileManifest) != 3 {
		t.Fatalf("FC2 magnet manifest = %+v", fc2Magnets)
	}

	var cloudCaches []model.CloudFileCache
	if err := database.Order("remote_file_id ASC").Find(&cloudCaches).Error; err != nil {
		t.Fatalf("list cloud caches: %v", err)
	}
	if len(cloudCaches) != 4 {
		t.Fatalf("cloud caches = %+v", cloudCaches)
	}
	if cloudCaches[0].StorageIdentity != "11" || cloudCaches[0].Provider != "115 Cloud" || cloudCaches[0].MagnetFingerprint != fingerprint(fixture.fc2Magnet) {
		t.Fatalf("115 cache = %+v", cloudCaches[0])
	}
	if cloudCaches[0].VerifiedAt == nil || !cloudCaches[0].VerifiedAt.Equal(fixture.scanAt) {
		t.Fatalf("115 cache VerifiedAt = %v, want %v", cloudCaches[0].VerifiedAt, fixture.scanAt)
	}
	if cloudCaches[3].StorageIdentity != "10" || cloudCaches[3].Provider != "PikPak" || cloudCaches[3].RemoteFileID != "remote-javdb" || cloudCaches[3].ProviderOptions["opaque"] != "keep" {
		t.Fatalf("PikPak cache = %+v", cloudCaches[3])
	}
	if cloudCaches[3].VerifiedAt == nil || !cloudCaches[3].VerifiedAt.Equal(fixture.scanAt) {
		t.Fatalf("PikPak cache VerifiedAt = %v, want %v", cloudCaches[3].VerifiedAt, fixture.scanAt)
	}

	if _, err := ValidateLegacyMediaMigration(context.Background(), database); err != nil {
		t.Fatalf("validate migrated identity: %v", err)
	}
	assertCount(t, database, &model.Film{}, 5)
	assertCount(t, database, &model.MagnetCache{}, 7)
}

func TestMigrateLegacyMediaRerunIsIdempotent(t *testing.T) {
	database := newMigrationTestDB(t)
	seedCompleteLegacyFixture(t, database)

	first, err := MigrateLegacyMedia(context.Background(), database)
	if err != nil {
		t.Fatalf("first migration: %v", err)
	}
	preservedVerifiedAt := time.Date(2025, time.January, 3, 4, 5, 6, 0, time.UTC)
	if err := database.Model(&model.CloudFileCache{}).
		Where("remote_file_id = ?", "remote-javdb").
		Update("verified_at", preservedVerifiedAt).Error; err != nil {
		t.Fatalf("advance normalized verification time: %v", err)
	}
	second, err := MigrateLegacyMedia(context.Background(), database)
	if err != nil {
		t.Fatalf("second migration: %v", err)
	}
	if first.WorksCreated == 0 || second.WorksCreated != 0 || second.FilesCreated != 0 || second.SourceMagnetsCreated != 0 || second.CloudFileCachesCreated != 0 {
		t.Fatalf("rerun reports = first %+v, second %+v", first, second)
	}
	if second.WorksExisting != 3 || second.FilesExisting != 5 || second.SourceMagnetsExisting != 2 || second.CloudFileCachesExisting != 4 {
		t.Fatalf("rerun existing counts = %+v", second)
	}
	assertCount(t, database, &model.FilmWork{}, 3)
	assertCount(t, database, &model.FilmFile{}, 5)
	assertCount(t, database, &model.SourceMagnet{}, 2)
	assertCount(t, database, &model.CloudFileCache{}, 4)
	var preserved model.CloudFileCache
	if err := database.Where("remote_file_id = ?", "remote-javdb").First(&preserved).Error; err != nil {
		t.Fatalf("load rerun cloud cache: %v", err)
	}
	if preserved.VerifiedAt == nil || !preserved.VerifiedAt.Equal(preservedVerifiedAt) {
		t.Fatalf("rerun VerifiedAt = %v, want preserved %v", preserved.VerifiedAt, preservedVerifiedAt)
	}
}

func TestMigrateLegacyMediaRejectsIdentityCollisionAndRollsBack(t *testing.T) {
	database := newMigrationTestDB(t)
	createStorages(t, database, model.Storage{ID: 1, Driver: "Javdb", MountPath: "/javdb"})
	films := []model.Film{
		{Source: "javdb", Actor: "Actor A", Name: "ABP-123 first.mp4", Url: "https://javdb.test/v/one"},
		{Source: "JAVDB", Actor: "Actor B", Name: "abp-123 second.mp4", Url: "https://javdb.test/v/two"},
	}
	if err := database.Create(&films).Error; err != nil {
		t.Fatalf("seed colliding films: %v", err)
	}

	_, err := MigrateLegacyMedia(context.Background(), database)
	if !errors.Is(err, ErrIdentityCollision) {
		t.Fatalf("collision error = %v, want ErrIdentityCollision", err)
	}
	var collision *IdentityCollisionError
	if !errors.As(err, &collision) || collision.Identity.Code != "ABP-123" || !reflect.DeepEqual(collision.LegacyFilmIDs, []uint{films[0].ID, films[1].ID}) {
		t.Fatalf("typed collision = %#v", collision)
	}
	assertCount(t, database, &model.FilmWork{}, 0)
	assertCount(t, database, &model.FilmFile{}, 0)
	assertCount(t, database, &model.SourceMagnet{}, 0)
	assertCount(t, database, &model.CloudFileCache{}, 0)
	assertCount(t, database, &model.Film{}, 2)
}

type completeFixture struct {
	javdbURL, fc2URL, pornhubURL string
	javdbMagnet, fc2Magnet       string
	releaseDate, scanAt          time.Time
}

func seedCompleteLegacyFixture(t *testing.T, database *gorm.DB) completeFixture {
	t.Helper()
	fixture := completeFixture{
		javdbURL:    "https://javdb.test/v/abp-123",
		fc2URL:      "https://adult.contents.fc2.com/article/100/",
		pornhubURL:  "https://www.pornhub.test/view_video.php?viewkey=view-key-9",
		javdbMagnet: "magnet:?xt=urn:btih:javdb",
		fc2Magnet:   "magnet:?xt=urn:btih:fc2",
		releaseDate: time.Date(2024, time.June, 2, 0, 0, 0, 0, time.UTC),
	}
	fixture.scanAt = fixture.releaseDate.Add(48 * time.Hour)
	createStorages(t, database,
		model.Storage{ID: 1, Driver: "Javdb", MountPath: "/javdb"},
		model.Storage{ID: 2, Driver: "FC2", MountPath: "/fc2"},
		model.Storage{ID: 3, Driver: "Pornhub", MountPath: "/pornhub"},
		model.Storage{ID: 10, Driver: "PikPak", MountPath: "/pikpak"},
		model.Storage{ID: 11, Driver: "115 Cloud", MountPath: "/115"},
	)
	scanAt := fixture.scanAt
	films := []model.Film{
		{
			Source: "JavDB", Actor: "Actor A", Name: "abp-123 Original title.mp4", Url: fixture.javdbURL,
			Image: "https://img.test/abp.jpg", Title: "Translated title", Synopsis: "Synopsis",
			Actors: model.StringArray{"Actor A", "Actor B"}, Tags: model.StringArray{"tag-a", model.TagSubtitle}, Date: fixture.releaseDate,
			SynopsisScanAt: scanAt, SynopsisExcluded: true, SampleImageCount: 4, SampleImageComplete: true,
			SampleImageScanAt: scanAt, DMMPosterStatus: model.DMMPosterStatusSuccess, DMMPosterScanAt: scanAt,
		},
		{Source: "FC2", Actor: "Seller A", Name: "FC2-100-cd1.mp4", Url: fixture.fc2URL, Title: "FC2 translated"},
		{Source: "fc2", Actor: "Seller A", Name: "FC2-100-cd2.mp4", Url: fixture.fc2URL, Title: "FC2 translated"},
		{Source: "Fc2", Actor: "Seller A", Name: "FC2-100-cd3.mp4", Url: fixture.fc2URL, Title: "FC2 translated"},
		{Source: "PornHub", Actor: "Channel A", Name: "view-key-9", Url: fixture.pornhubURL, Title: "Porn title"},
	}
	if err := database.Create(&films).Error; err != nil {
		t.Fatalf("seed films: %v", err)
	}
	caches := []model.MagnetCache{
		{DriverType: "JavDB", Magnet: fixture.javdbMagnet, Name: films[0].Name, Code: "abp-123", Subtitle: true, ScanAt: scanAt},
		{DriverType: "javdb", Magnet: fixture.javdbMagnet, Name: films[0].Name, Code: "ABP-123", Subtitle: true, ScanAt: scanAt},
		{DriverType: "PikPak", Magnet: fixture.javdbMagnet, FileId: "remote-javdb", Name: films[0].Name, Code: "ABP-123", Option: map[string]string{"opaque": "keep"}, ScanAt: scanAt},
		{DriverType: "FC2", Magnet: fixture.fc2Magnet, Name: films[1].Name, Code: "FC2-100", ScanAt: scanAt},
		{DriverType: "115 Cloud", Magnet: fixture.fc2Magnet, FileId: "remote-fc2-1", Name: films[1].Name, Code: "FC2-100", Option: map[string]string{"pickCode": "pick-1"}, ScanAt: scanAt},
		{DriverType: "115 Cloud", Magnet: fixture.fc2Magnet, FileId: "remote-fc2-2", Name: films[2].Name, Code: "FC2-100", Option: map[string]string{"pickCode": "pick-2"}, ScanAt: scanAt},
		{DriverType: "115 Cloud", Magnet: fixture.fc2Magnet, FileId: "remote-fc2-3", Name: films[3].Name, Code: "FC2-100", Option: map[string]string{"pickCode": "pick-3"}, ScanAt: scanAt},
	}
	if err := database.Create(&caches).Error; err != nil {
		t.Fatalf("seed magnet caches: %v", err)
	}
	return fixture
}

func newMigrationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:media-migration-%s?mode=memory&cache=shared", t.Name())
	database, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open migration database: %v", err)
	}
	if err := database.AutoMigrate(&model.Storage{}, &model.Film{}, &model.MagnetCache{}, &model.FilmWork{}, &model.FilmFile{}, &model.SourceMagnet{}, &model.CloudFileCache{}); err != nil {
		t.Fatalf("migrate test schema: %v", err)
	}
	return database
}

func createStorages(t *testing.T, database *gorm.DB, storages ...model.Storage) {
	t.Helper()
	if err := database.Create(&storages).Error; err != nil {
		t.Fatalf("seed storages: %v", err)
	}
}

func getWork(t *testing.T, database *gorm.DB, storageID uint, source, code string) model.FilmWork {
	t.Helper()
	var work model.FilmWork
	if err := database.Where("storage_id = ? AND source = ? AND code = ?", storageID, source, code).First(&work).Error; err != nil {
		t.Fatalf("get work %d/%s/%s: %v", storageID, source, code, err)
	}
	return work
}

func listFiles(t *testing.T, database *gorm.DB, workID uint) []model.FilmFile {
	t.Helper()
	var files []model.FilmFile
	if err := database.Where("work_id = ?", workID).Order("part_index ASC").Find(&files).Error; err != nil {
		t.Fatalf("list files for work %d: %v", workID, err)
	}
	return files
}

func listMagnets(t *testing.T, database *gorm.DB, workID uint) []model.SourceMagnet {
	t.Helper()
	var magnets []model.SourceMagnet
	if err := database.Where("work_id = ?", workID).Order("priority ASC, id ASC").Find(&magnets).Error; err != nil {
		t.Fatalf("list magnets for work %d: %v", workID, err)
	}
	return magnets
}

func assertCount(t *testing.T, database *gorm.DB, value interface{}, want int64) {
	t.Helper()
	var got int64
	if err := database.Model(value).Count(&got).Error; err != nil {
		t.Fatalf("count %T: %v", value, err)
	}
	if got != want {
		t.Fatalf("count %T = %d, want %d", value, got, want)
	}
}

func fingerprint(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
