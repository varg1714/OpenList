package javdb

import (
	"context"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/av"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestJavdbPlaybackMagnetsUsesPersistedRowsBeforeRemoteDiscovery(t *testing.T) {
	for _, value := range []interface{}{&model.SourceMagnet{}, &model.FilmFile{}, &model.FilmWork{}} {
		require.NoError(t, db.GetDb().Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(value).Error)
	}
	work := model.FilmWork{
		StorageID: 601, Source: DriverName, Code: "ABC-601", SourceRef: "https://javdb.test/v/601",
		SourceURL: "https://javdb.test/v/601", PrimaryDir: "actor",
	}
	require.NoError(t, db.GetDb().Create(&work).Error)
	require.NoError(t, db.UpsertSourceMagnets(work.ID, []model.SourceMagnet{{
		MagnetURI: "magnet:persisted", Fingerprint: "persisted", Provider: DriverName, Priority: 1,
	}}))

	oldJavdb := getJavdbMeta
	oldSuke := getJavdbSukeMeta
	t.Cleanup(func() {
		getJavdbMeta = oldJavdb
		getJavdbSukeMeta = oldSuke
	})
	calls := 0
	getJavdbMeta = func(string) (av.Meta, error) {
		calls++
		return av.Meta{}, nil
	}
	getJavdbSukeMeta = func(string) (av.Meta, error) {
		calls++
		return av.Meta{}, nil
	}

	magnets, err := (&Javdb{}).playbackMagnets(context.Background(), work)

	require.NoError(t, err)
	require.Len(t, magnets, 1)
	require.Equal(t, "persisted", magnets[0].Fingerprint)
	require.Zero(t, calls)
}
