package fc2

import (
	"reflect"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"gorm.io/gorm"
)

func TestSyncMissAvFilmsUsesFilmWorkIdentityWithoutLegacyMagnetCache(t *testing.T) {
	resetFC2MediaWorks(t)
	if err := db.GetDb().Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&model.MagnetCache{}).Error; err != nil {
		t.Fatal(err)
	}
	work := model.FilmWork{
		StorageID: 71, Source: "fc2", Code: "FC2-PPV-123", SourceRef: "FC2-PPV-123",
		PrimaryDir: "Ranked", Tags: model.StringArray{"existing"},
	}
	if err := db.GetDb().Create(&work).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.GetDb().Create(&model.FilmFile{WorkID: work.ID, PartIndex: 1, PartCount: 1}).Error; err != nil {
		t.Fatal(err)
	}
	queried := []model.EmbyFileObj{{
		ObjThumb: model.ObjThumb{Object: model.Object{Name: "FC2-PPV-123"}},
		Tags:     []string{"Ranked-Top30", "Ranked"},
	}}
	if err := (&FC2{Storage: model.Storage{ID: 71}}).syncMissAvFilms(queried); err != nil {
		t.Fatal(err)
	}
	stored, err := db.GetFilmWorkByIdentity(71, "fc2", "FC2-PPV-123")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stored.Tags, model.StringArray{"existing", "Ranked-Top30", "Ranked"}) {
		t.Fatalf("synced tags = %#v", stored.Tags)
	}
	var legacyCount int64
	if err := db.GetDb().Model(&model.MagnetCache{}).Count(&legacyCount).Error; err != nil {
		t.Fatal(err)
	}
	if legacyCount != 0 {
		t.Fatalf("MissAV sync wrote %d legacy magnet caches", legacyCount)
	}
}
