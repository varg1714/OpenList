package db

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"gorm.io/gorm"
)

func setupCloudFileCacheRepositoryTestDB(t *testing.T) {
	t.Helper()

	if err := db.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&model.CloudFileCache{}).Error; err != nil {
		t.Fatalf("reset cloud file caches: %v", err)
	}
	if err := db.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&model.FilmFile{}).Error; err != nil {
		t.Fatalf("reset film files: %v", err)
	}
	if err := db.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&model.FilmWork{}).Error; err != nil {
		t.Fatalf("reset film works: %v", err)
	}
}

func TestGetCloudFileCacheScopesStorageAndFingerprint(t *testing.T) {
	setupCloudFileCacheRepositoryTestDB(t)

	file := createCloudFileCacheTestFile(t, 1, "ABP-101")
	verifiedAt := time.Now().UTC().Truncate(time.Second)
	wantOptions := map[string]string{"pickCode": "pick-exact", "quality": "original"}
	caches := []model.CloudFileCache{
		{
			FilmFileID:        file.ID,
			StorageIdentity:   "storage-a",
			Provider:          "115",
			RemoteFileID:      "remote/exact/id",
			ProviderOptions:   wantOptions,
			MagnetFingerprint: "fingerprint-a",
			VerifiedAt:        &verifiedAt,
		},
		{
			FilmFileID:        file.ID,
			StorageIdentity:   "storage-b",
			Provider:          "pikpak",
			RemoteFileID:      "remote-b",
			ProviderOptions:   map[string]string{"token": "b"},
			MagnetFingerprint: "fingerprint-a",
		},
	}
	if err := db.Create(&caches).Error; err != nil {
		t.Fatalf("create scoped caches: %v", err)
	}

	got, err := GetCloudFileCache("storage-a", file.ID, "fingerprint-a")
	if err != nil {
		t.Fatalf("get exact cache: %v", err)
	}
	if got.RemoteFileID != "remote/exact/id" || got.Provider != "115" || !reflect.DeepEqual(got.ProviderOptions, wantOptions) {
		t.Fatalf("exact cache = %+v, want remote ID and provider options preserved", got)
	}
	if _, err := GetCloudFileCache("storage-c", file.ID, "fingerprint-a"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("different storage lookup error = %v, want gorm.ErrRecordNotFound", err)
	}
	if _, err := GetCloudFileCache("storage-a", file.ID, "fingerprint-b"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("stale fingerprint lookup error = %v, want gorm.ErrRecordNotFound", err)
	}
}

func TestReplaceCloudFileCachesPreservesValues(t *testing.T) {
	setupCloudFileCacheRepositoryTestDB(t)

	file := createCloudFileCacheTestFile(t, 1, "ABP-102")
	options := map[string]string{"pickCode": "00123", "opaque": "a/b:c"}
	caches := []model.CloudFileCache{{
		FilmFileID:        file.ID,
		StorageIdentity:   "storage-a",
		Provider:          "115",
		RemoteFileID:      "000/remote:id",
		ProviderOptions:   options,
		MagnetFingerprint: "fingerprint-new",
	}}
	if err := ReplaceCloudFileCaches("storage-a", "fingerprint-new", caches); err != nil {
		t.Fatalf("replace cloud file caches: %v", err)
	}

	got, err := GetCloudFileCache("storage-a", file.ID, "fingerprint-new")
	if err != nil {
		t.Fatalf("get replaced cache: %v", err)
	}
	if got.RemoteFileID != caches[0].RemoteFileID || !reflect.DeepEqual(got.ProviderOptions, options) {
		t.Fatalf("replaced cache = %+v, want exact remote ID and provider options", got)
	}
}

func TestReplaceCloudFileCachesIsAtomic(t *testing.T) {
	setupCloudFileCacheRepositoryTestDB(t)

	first := createCloudFileCacheTestFile(t, 1, "ABP-103")
	second, err := EnsureSingleFilmFile(first.WorkID)
	if err != nil {
		t.Fatalf("get first film file: %v", err)
	}
	work := createMediaTestWork(t, 1, "javdb", "ABP-104", "actor")
	second, err = EnsureSingleFilmFile(work.ID)
	if err != nil {
		t.Fatalf("create second film file: %v", err)
	}
	original := []model.CloudFileCache{
		{FilmFileID: first.ID, StorageIdentity: "storage-a", Provider: "115", RemoteFileID: "old-first", MagnetFingerprint: "old"},
		{FilmFileID: second.ID, StorageIdentity: "storage-a", Provider: "115", RemoteFileID: "old-second", MagnetFingerprint: "old"},
	}
	if err := db.Create(&original).Error; err != nil {
		t.Fatalf("create original caches: %v", err)
	}

	replacement := []model.CloudFileCache{
		{FilmFileID: first.ID, StorageIdentity: "storage-a", Provider: "115", RemoteFileID: "duplicate", MagnetFingerprint: "new"},
		{FilmFileID: second.ID, StorageIdentity: "storage-a", Provider: "115", RemoteFileID: "duplicate", MagnetFingerprint: "new"},
	}
	if err := ReplaceCloudFileCaches("storage-a", "new", replacement); err == nil {
		t.Fatal("replacement with duplicate remote IDs unexpectedly succeeded")
	}

	var stored []model.CloudFileCache
	if err := db.Where("storage_identity = ?", "storage-a").Order("film_file_id ASC").Find(&stored).Error; err != nil {
		t.Fatalf("list caches after failed replacement: %v", err)
	}
	if len(stored) != 2 || stored[0].MagnetFingerprint != "old" || stored[1].MagnetFingerprint != "old" || stored[0].RemoteFileID != "old-first" || stored[1].RemoteFileID != "old-second" {
		t.Fatalf("failed replacement changed original caches: %+v", stored)
	}
}

func TestDeleteCloudFileCacheScopesStorageAndFile(t *testing.T) {
	setupCloudFileCacheRepositoryTestDB(t)

	file := createCloudFileCacheTestFile(t, 1, "ABP-105")
	caches := []model.CloudFileCache{
		{FilmFileID: file.ID, StorageIdentity: "storage-a", Provider: "115", RemoteFileID: "remote-a", MagnetFingerprint: "fingerprint"},
		{FilmFileID: file.ID, StorageIdentity: "storage-b", Provider: "115", RemoteFileID: "remote-b", MagnetFingerprint: "fingerprint"},
	}
	if err := db.Create(&caches).Error; err != nil {
		t.Fatalf("create caches for deletion: %v", err)
	}
	if err := DeleteCloudFileCache("storage-a", file.ID); err != nil {
		t.Fatalf("delete scoped cache: %v", err)
	}
	if _, err := GetCloudFileCache("storage-a", file.ID, "fingerprint"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("deleted cache lookup error = %v, want gorm.ErrRecordNotFound", err)
	}
	if _, err := GetCloudFileCache("storage-b", file.ID, "fingerprint"); err != nil {
		t.Fatalf("cache from other storage was deleted: %v", err)
	}
}

func TestDeleteStaleCloudFileCachesOnlyDeletesSiblingFiles(t *testing.T) {
	setupCloudFileCacheRepositoryTestDB(t)

	targetWork := createMediaTestWork(t, 1, "fc2", "FC2-PPV-201", "actor")
	if err := ReplaceFilmFiles(targetWork.ID, []model.FilmFile{{PartIndex: 1, PartCount: 2}, {PartIndex: 2, PartCount: 2}}); err != nil {
		t.Fatalf("create sibling film files: %v", err)
	}
	targetFiles, err := ListFilmFiles(targetWork.ID)
	if err != nil {
		t.Fatalf("list sibling film files: %v", err)
	}
	otherFile := createCloudFileCacheTestFile(t, 1, "ABP-106")
	caches := []model.CloudFileCache{
		{FilmFileID: targetFiles[0].ID, StorageIdentity: "storage-a", Provider: "115", RemoteFileID: "target-stale-a", MagnetFingerprint: "stale"},
		{FilmFileID: targetFiles[1].ID, StorageIdentity: "storage-b", Provider: "pikpak", RemoteFileID: "target-stale-b", MagnetFingerprint: "stale"},
		{FilmFileID: targetFiles[0].ID, StorageIdentity: "storage-c", Provider: "115", RemoteFileID: "target-current", MagnetFingerprint: "current"},
		{FilmFileID: otherFile.ID, StorageIdentity: "storage-a", Provider: "115", RemoteFileID: "other-stale", MagnetFingerprint: "stale"},
	}
	if err := db.Create(&caches).Error; err != nil {
		t.Fatalf("create stale cache fixtures: %v", err)
	}

	if err := DeleteStaleCloudFileCaches(targetWork.ID, "current"); err != nil {
		t.Fatalf("delete stale sibling caches: %v", err)
	}

	var stored []model.CloudFileCache
	if err := db.Order("remote_file_id ASC").Find(&stored).Error; err != nil {
		t.Fatalf("list caches after stale deletion: %v", err)
	}
	if len(stored) != 2 || stored[0].RemoteFileID != "other-stale" || stored[1].RemoteFileID != "target-current" {
		t.Fatalf("remaining caches = %+v, want current target cache and stale other-work cache", stored)
	}
}

func createCloudFileCacheTestFile(t *testing.T, storageID uint, code string) model.FilmFile {
	t.Helper()

	work := createMediaTestWork(t, storageID, "javdb", code, "actor")
	file, err := EnsureSingleFilmFile(work.ID)
	if err != nil {
		t.Fatalf("create cloud cache film file: %v", err)
	}
	return file
}
