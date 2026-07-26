package pornhub

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

func TestRunFanartProcessesNormalizedWorkWithoutLegacyFilm(t *testing.T) {
	// Given
	setupPornhubFanartTest(t)
	work := model.FilmWork{
		StorageID:   1,
		Source:      DriverName,
		Code:        "normalized-view-key",
		SourceRef:   "normalized-view-key",
		SourceURL:   "https://www.pornhub.com/view_video.php?viewkey=normalized-view-key",
		ImageURL:    "https://example.test/cover.jpg",
		PrimaryDir:  "normalized-actor",
		ReleaseDate: time.Now(),
	}
	if err := db.GetDb().Create(&work).Error; err != nil {
		t.Fatal(err)
	}
	var resolvedSourceRef string
	driver := newFanartDriver(&mockFanartMedia{duration: 60}, func(_ context.Context, sourceRef string) (string, error) {
		resolvedSourceRef = sourceRef
		return "https://example.test/video.mp4", nil
	})
	driver.FanartCount = 1
	driver.fanartCtx = context.Background()

	// When
	driver.runFanart()

	// Then
	stored, err := db.GetFilmWork(work.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.SampleImageCount != 1 || !stored.SampleImageComplete {
		t.Fatalf("normalized fanart progress = (%d, %t), want (1, true)", stored.SampleImageCount, stored.SampleImageComplete)
	}
	if resolvedSourceRef != work.SourceRef {
		t.Fatalf("resolved source ref = %q, want %q", resolvedSourceRef, work.SourceRef)
	}
	path, err := virtual_file.FanartPath(DriverName, work.PrimaryDir, work.Code, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("normalized fanart path was not published: %v", err)
	}
}
