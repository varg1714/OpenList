package pornhub

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"gorm.io/gorm"
)

func TestReMatchTagsScansPreTaggedWork(t *testing.T) {
	// Given
	if err := db.GetDb().Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&model.FilmWork{}).Error; err != nil {
		t.Fatalf("reset media works: %v", err)
	}
	oldWait := waitPornhubTagScan
	waitPornhubTagScan = func(time.Duration) {}
	t.Cleanup(func() { waitPornhubTagScan = oldWait })
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(`<div class="tagsWrapper"><div class="gtm-event-video-underplayer item"><span>detail-tag</span></div></div>`))
	}))
	t.Cleanup(server.Close)
	work := model.FilmWork{
		StorageID: 92, Source: DriverName, Code: "pre-tagged", SourceRef: "pre-tagged", SourceURL: server.URL,
		PrimaryDir: "playlist", Tags: model.StringArray{"playlist-tag"},
	}
	if err := db.GetDb().Create(&work).Error; err != nil {
		t.Fatalf("seed pre-tagged work: %v", err)
	}
	driver := Pornhub{Addition: Addition{ServerUrl: server.URL, MatchFilmTagLimit: 1}}

	// When
	driver.reMatchTags()

	// Then
	updated, err := db.GetFilmWork(work.ID)
	if err != nil {
		t.Fatalf("get scanned work: %v", err)
	}
	if updated.TagScanAt == nil || !slices.Contains([]string(updated.Tags), "detail-tag") {
		t.Fatalf("tag stage = (scan=%v tags=%v), want scanned detail tags", updated.TagScanAt, updated.Tags)
	}
}
