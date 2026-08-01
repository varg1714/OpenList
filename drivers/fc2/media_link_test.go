package fc2

import (
	"context"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/av"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type fc2TestMagnet struct {
	uri      string
	name     string
	files    []av.File
	subtitle bool
}

func (m fc2TestMagnet) GetMagnet() string         { return m.uri }
func (m fc2TestMagnet) GetName() string           { return m.name }
func (m fc2TestMagnet) GetSize() uint64           { return 0 }
func (m fc2TestMagnet) IsSubTitle() bool          { return m.subtitle }
func (m fc2TestMagnet) GetTags() []string         { return nil }
func (m fc2TestMagnet) GetDownloadCount() uint64  { return 0 }
func (m fc2TestMagnet) GetFiles() []av.File       { return m.files }
func (m fc2TestMagnet) GetReleaseDate() time.Time { return time.Time{} }

func TestFC2PlaybackMagnetsUsesPersistedRowsBeforeSuke(t *testing.T) {
	resetFC2PlaybackTables(t)
	work := createFC2PlaybackWork(t, "FC2-PPV-501")
	require.NoError(t, db.UpsertSourceMagnets(work.ID, []model.SourceMagnet{{
		MagnetURI: "magnet:persisted", Fingerprint: "persisted", Provider: "fc2", Priority: 1,
	}}))

	oldFetch := getFC2SukeMeta
	t.Cleanup(func() { getFC2SukeMeta = oldFetch })
	calls := 0
	getFC2SukeMeta = func(string) (av.Meta, error) {
		calls++
		return av.Meta{}, nil
	}

	magnets, err := (&FC2{}).playbackMagnets(context.Background(), work)

	require.NoError(t, err)
	require.Len(t, magnets, 1)
	require.Equal(t, "persisted", magnets[0].Fingerprint)
	require.Zero(t, calls)
}

func TestFC2PlaybackMagnetsPersistsSukeRowsAndTopologyBeforeReturning(t *testing.T) {
	resetFC2PlaybackTables(t)
	work := createFC2PlaybackWork(t, "FC2-PPV-502")

	oldFetch := getFC2SukeMeta
	t.Cleanup(func() { getFC2SukeMeta = oldFetch })
	getFC2SukeMeta = func(string) (av.Meta, error) {
		return av.Meta{Magnets: []av.Magnet{fc2TestMagnet{
			uri: "magnet:remote",
			files: []av.File{
				{Name: "z-video.mp4", Size: 300 * 1024 * 1024},
				{Name: "a-video.mp4", Size: 200 * 1024 * 1024},
			},
		}}}, nil
	}

	magnets, err := (&FC2{}).playbackMagnets(context.Background(), work)

	require.NoError(t, err)
	require.Len(t, magnets, 1)
	require.NotZero(t, magnets[0].ID)
	files, err := db.ListFilmFiles(work.ID)
	require.NoError(t, err)
	require.Len(t, files, 2)
	require.Equal(t, "a-video.mp4", files[0].SourcePath)
	require.Equal(t, int64(200*1024*1024), files[0].SourceSize)
	require.Equal(t, "z-video.mp4", files[1].SourcePath)
}

func resetFC2PlaybackTables(t *testing.T) {
	t.Helper()
	for _, value := range []interface{}{&model.SourceMagnet{}, &model.FilmFile{}, &model.FilmWork{}} {
		require.NoError(t, db.GetDb().Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(value).Error)
	}
}

func createFC2PlaybackWork(t *testing.T, code string) model.FilmWork {
	t.Helper()
	work := model.FilmWork{StorageID: 501, Source: "fc2", Code: code, SourceRef: code, SourceURL: code, PrimaryDir: "actor"}
	require.NoError(t, db.GetDb().Create(&work).Error)
	_, err := db.EnsureSingleFilmFile(work.ID)
	require.NoError(t, err)
	return work
}
