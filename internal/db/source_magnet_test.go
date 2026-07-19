package db

import (
	"errors"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"gorm.io/gorm"
)

func setupSourceMagnetRepositoryTestDB(t *testing.T) {
	t.Helper()
	if err := db.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&model.SourceMagnet{}).Error; err != nil {
		t.Fatalf("reset source magnets: %v", err)
	}
}

func TestUpsertSourceMagnetsDeduplicatesByFingerprint(t *testing.T) {
	setupSourceMagnetRepositoryTestDB(t)

	first := model.SourceMagnet{MagnetURI: "magnet:?xt=urn:btih:first", Fingerprint: "fp-1", Provider: "a", Priority: 4}
	if err := UpsertSourceMagnets(10, []model.SourceMagnet{first}); err != nil {
		t.Fatalf("upsert first magnet: %v", err)
	}
	stored, err := ListSourceMagnets(10)
	if err != nil {
		t.Fatalf("list first magnet: %v", err)
	}
	if len(stored) != 1 || stored[0].ID == 0 {
		t.Fatalf("stored magnets = %+v, want one persisted magnet", stored)
	}
	firstID := stored[0].ID

	first.Provider = "b"
	first.Priority = 1
	first.MagnetURI = "magnet:?xt=urn:btih:updated"
	if err := UpsertSourceMagnets(10, []model.SourceMagnet{first}); err != nil {
		t.Fatalf("upsert duplicate magnet: %v", err)
	}
	stored, err = ListSourceMagnets(10)
	if err != nil {
		t.Fatalf("list duplicate magnet: %v", err)
	}
	if len(stored) != 1 || stored[0].ID != firstID || stored[0].Provider != "b" || stored[0].Priority != 1 || stored[0].MagnetURI != first.MagnetURI {
		t.Fatalf("deduplicated magnet = %+v, want updated row preserving ID %d", stored, firstID)
	}
}

func TestUpsertSourceMagnetsEnsuresOneSelected(t *testing.T) {
	setupSourceMagnetRepositoryTestDB(t)

	magnets := []model.SourceMagnet{
		{MagnetURI: "magnet:one", Fingerprint: "one", Priority: 10, Selected: true},
		{MagnetURI: "magnet:two", Fingerprint: "two", Priority: 1, Selected: true},
	}
	if err := UpsertSourceMagnets(11, magnets); err != nil {
		t.Fatalf("upsert selected magnets: %v", err)
	}
	listed, err := ListSourceMagnets(11)
	if err != nil {
		t.Fatalf("list selected magnets: %v", err)
	}
	selected := selectedSourceMagnets(listed)
	if len(selected) != 1 || selected[0].Fingerprint != "two" {
		t.Fatalf("selected magnets = %+v, want only priority-1 magnet", selected)
	}
}

func TestGetSelectedSourceMagnetFallsBackToPriority(t *testing.T) {
	setupSourceMagnetRepositoryTestDB(t)

	if err := UpsertSourceMagnets(12, []model.SourceMagnet{
		{MagnetURI: "magnet:high", Fingerprint: "high", Priority: 10},
		{MagnetURI: "magnet:low", Fingerprint: "low", Priority: 2},
		{MagnetURI: "magnet:tie", Fingerprint: "tie", Priority: 2},
	}); err != nil {
		t.Fatalf("upsert unselected magnets: %v", err)
	}
	selected, err := GetSelectedSourceMagnet(12)
	if err != nil {
		t.Fatalf("get fallback magnet: %v", err)
	}
	if selected.Fingerprint != "low" || !selected.Selected {
		t.Fatalf("fallback magnet = %+v, want selected priority-2 first row", selected)
	}
}

func TestSelectSourceMagnetRejectsMagnetFromAnotherWork(t *testing.T) {
	setupSourceMagnetRepositoryTestDB(t)

	if err := UpsertSourceMagnets(13, []model.SourceMagnet{{MagnetURI: "magnet:other", Fingerprint: "other", Priority: 1}}); err != nil {
		t.Fatalf("upsert other-work magnet: %v", err)
	}
	other, err := ListSourceMagnets(13)
	if err != nil {
		t.Fatalf("list other-work magnet: %v", err)
	}
	if err := UpsertSourceMagnets(14, []model.SourceMagnet{{MagnetURI: "magnet:mine", Fingerprint: "mine", Priority: 1}}); err != nil {
		t.Fatalf("upsert target-work magnet: %v", err)
	}

	if err := SelectSourceMagnet(14, other[0].ID); err == nil {
		t.Fatal("selecting a magnet from another work unexpectedly succeeded")
	}
	selected, err := GetSelectedSourceMagnet(14)
	if err != nil || selected.Fingerprint != "mine" || !selected.Selected {
		t.Fatalf("target work selection after rejected cross-work select = %+v, %v", selected, err)
	}
}

func TestSourceMagnetEmptyCandidateBehavior(t *testing.T) {
	setupSourceMagnetRepositoryTestDB(t)

	selected, err := GetSelectedSourceMagnet(15)
	if !errors.Is(err, gorm.ErrRecordNotFound) || selected.ID != 0 {
		t.Fatalf("empty selected result = %+v, %v", selected, err)
	}
	if err := SelectSourceMagnet(15, 999999); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("select empty candidate error = %v, want gorm.ErrRecordNotFound", err)
	}
}

func selectedSourceMagnets(magnets []model.SourceMagnet) []model.SourceMagnet {
	selected := make([]model.SourceMagnet, 0, len(magnets))
	for _, magnet := range magnets {
		if magnet.Selected {
			selected = append(selected, magnet)
		}
	}
	return selected
}
