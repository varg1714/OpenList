package fc2

import (
	"testing"

	"github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestFC2NFOFlagsPreferForcedRefreshWhenBothEnabled(t *testing.T) {
	oldSync := syncFC2MediaNFOs
	t.Cleanup(func() { syncFC2MediaNFOs = oldSync })
	var forces []bool
	syncFC2MediaNFOs = func(storageID uint, source string, force bool) error {
		require.Equal(t, uint(81), storageID)
		require.Equal(t, "fc2", source)
		forces = append(forces, force)
		return nil
	}

	driver := FC2{Addition: Addition{SyncNfo: true, RefreshNfo: true}}
	driver.ID = 81
	err := driver.syncConfiguredNFOs()

	require.NoError(t, err)
	require.Equal(t, []bool{true}, forces)
}

func TestFC2ScheduledArtifactScanPublishesPersistedImage(t *testing.T) {
	for _, value := range []interface{}{&model.FilmFile{}, &model.FilmWork{}} {
		require.NoError(t, db.GetDb().Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(value).Error)
	}
	work := model.FilmWork{
		StorageID: 84, Source: "fc2", Code: "FC2-PPV-884", SourceRef: "FC2-PPV-884",
		SourceURL: "FC2-PPV-884", PrimaryDir: "actor", RawTitle: "title", ImageURL: "https://image.test/fc2.jpg",
	}
	require.NoError(t, db.GetDb().Create(&work).Error)

	oldCache := cacheFC2MediaImage
	t.Cleanup(func() { cacheFC2MediaImage = oldCache })
	var captured virtual_file.MediaInfo
	cacheFC2MediaImage = func(info virtual_file.MediaInfo) (int, error) {
		captured = info
		return virtual_file.CreatedSuccess, nil
	}

	err := (&FC2{Storage: model.Storage{ID: 84}}).scanMediaArtifacts()

	require.NoError(t, err)
	require.NotNil(t, captured.Identity)
	require.Equal(t, "FC2-PPV-884", captured.Identity.Code)
	require.Equal(t, work.ImageURL, captured.ImgUrl)
	stored, getErr := db.GetFilmWork(work.ID)
	require.NoError(t, getErr)
	require.Equal(t, model.DMMPosterStatusSuccess, stored.DMMPosterStatus)
	require.NotNil(t, stored.DMMPosterScanAt)
}
