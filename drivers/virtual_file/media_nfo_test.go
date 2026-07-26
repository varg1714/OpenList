package virtual_file

import (
	"encoding/xml"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/cmd/flags"
	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestMediaNFOWritesProjectedTitleToCodeOnlyFileName(t *testing.T) {
	previous := flags.DataDir
	flags.DataDir = t.TempDir()
	t.Cleanup(func() { flags.DataDir = previous })

	identity := MediaIdentity{StorageID: 12, Source: "javdb", PrimaryDir: "Actor A", Code: "ABP-123"}
	if err := UpdateMediaNfo(MediaInfo{Identity: &identity, Title: "ABP-123 translated title"}); err != nil {
		t.Fatalf("update media NFO: %v", err)
	}

	path := filepath.Join(flags.DataDir, "emby", "javdb", "Actor A", "ABP-123", "ABP-123.nfo")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read NFO: %v", err)
	}
	var media Media
	if err := xml.Unmarshal(content, &media); err != nil {
		t.Fatalf("parse NFO: %v", err)
	}
	if media.Title.Inner != "<![CDATA[ABP-123 translated title]]>" {
		t.Fatalf("NFO title = %q", media.Title.Inner)
	}
}

func TestSyncMediaNFOsWritesOnlyStaleWorks(t *testing.T) {
	setupMediaNFOSyncTestDB(t)
	fresh := createMediaNFOSyncWork(t, 71, "javdb", "FRESH", 2, 2)
	stale := createMediaNFOSyncWork(t, 71, "javdb", "STALE", 3, 1)
	createMediaNFOSyncWork(t, 72, "javdb", "OTHER-STORAGE", 3, 1)

	oldWrite := writeNormalizedMediaNFO
	t.Cleanup(func() { writeNormalizedMediaNFO = oldWrite })
	var written []string
	writeNormalizedMediaNFO = func(info MediaInfo) error {
		written = append(written, info.Identity.Code)
		return nil
	}

	err := SyncMediaNFOs(71, "javdb", MediaNFOSyncOptions{IncludeCode: true})

	require.NoError(t, err)
	require.Equal(t, []string{"STALE"}, written)
	freshAfter, err := db.GetFilmWork(fresh.ID)
	require.NoError(t, err)
	require.Equal(t, uint(2), freshAfter.NfoVersion)
	staleAfter, err := db.GetFilmWork(stale.ID)
	require.NoError(t, err)
	require.Equal(t, uint(3), staleAfter.NfoVersion)
	require.Empty(t, staleAfter.NfoLastError)
}

func TestRefreshMediaNFOsRewritesFreshAndStaleWorks(t *testing.T) {
	setupMediaNFOSyncTestDB(t)
	createMediaNFOSyncWork(t, 73, "fc2", "FRESH", 2, 2)
	createMediaNFOSyncWork(t, 73, "fc2", "STALE", 3, 1)

	oldWrite := writeNormalizedMediaNFO
	t.Cleanup(func() { writeNormalizedMediaNFO = oldWrite })
	var written []string
	writeNormalizedMediaNFO = func(info MediaInfo) error {
		written = append(written, info.Identity.Code)
		return nil
	}

	err := SyncMediaNFOs(73, "fc2", MediaNFOSyncOptions{Force: true, IncludeCode: true})

	require.NoError(t, err)
	require.Equal(t, []string{"FRESH", "STALE"}, written)
}

func TestSyncMediaNFOsRecordsErrorAndContinues(t *testing.T) {
	setupMediaNFOSyncTestDB(t)
	failed := createMediaNFOSyncWork(t, 74, "pornhub", "FAIL", 2, 0)
	succeeded := createMediaNFOSyncWork(t, 74, "pornhub", "SUCCEED", 4, 0)

	oldWrite := writeNormalizedMediaNFO
	t.Cleanup(func() { writeNormalizedMediaNFO = oldWrite })
	var written []string
	writeNormalizedMediaNFO = func(info MediaInfo) error {
		written = append(written, info.Identity.Code)
		if info.Identity.Code == "FAIL" {
			return errors.New("disk full")
		}
		return nil
	}

	err := SyncMediaNFOs(74, "pornhub", MediaNFOSyncOptions{})

	require.ErrorContains(t, err, "disk full")
	require.Equal(t, []string{"FAIL", "SUCCEED"}, written)
	failedAfter, err := db.GetFilmWork(failed.ID)
	require.NoError(t, err)
	require.Equal(t, uint(0), failedAfter.NfoVersion)
	require.Equal(t, "disk full", failedAfter.NfoLastError)
	succeededAfter, err := db.GetFilmWork(succeeded.ID)
	require.NoError(t, err)
	require.Equal(t, uint(4), succeededAfter.NfoVersion)
}

func TestSyncMediaNFOsProjectsConfiguredTitle(t *testing.T) {
	tests := []struct {
		name        string
		includeCode bool
		wantTitle   string
	}{
		{name: "includes code", includeCode: true, wantTitle: "TITLE title"},
		{name: "omits code", includeCode: false, wantTitle: "title"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setupMediaNFOSyncTestDB(t)
			createMediaNFOSyncWork(t, 75, "pornhub", "TITLE", 2, 0)

			oldWrite := writeNormalizedMediaNFO
			t.Cleanup(func() { writeNormalizedMediaNFO = oldWrite })
			var title string
			writeNormalizedMediaNFO = func(info MediaInfo) error {
				title = info.Title
				return nil
			}

			err := SyncMediaNFOs(75, "pornhub", MediaNFOSyncOptions{IncludeCode: test.includeCode})

			require.NoError(t, err)
			require.Equal(t, test.wantTitle, title)
		})
	}
}

func setupMediaNFOSyncTestDB(t *testing.T) {
	t.Helper()
	dataDir := t.TempDir()
	previousConfig := conf.Conf
	conf.Conf = conf.DefaultConfig(dataDir)
	t.Cleanup(func() { conf.Conf = previousConfig })
	testDB, err := gorm.Open(sqlite.Open(filepath.Join(dataDir, "nfo-sync.db")), &gorm.Config{})
	require.NoError(t, err)
	db.Init(testDB)
	require.NoError(t, db.AutoMigrate(new(model.FilmWork)))
}

func createMediaNFOSyncWork(t *testing.T, storageID uint, source, code string, metadataVersion, nfoVersion uint) model.FilmWork {
	t.Helper()
	work := model.FilmWork{
		StorageID: storageID, Source: source, Code: code, SourceRef: code, SourceURL: code,
		PrimaryDir: "actor", RawTitle: "title", MetadataVersion: metadataVersion, NfoVersion: nfoVersion,
	}
	require.NoError(t, db.GetDb().Create(&work).Error)
	return work
}
