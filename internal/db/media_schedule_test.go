package db

import (
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

func TestQueryGeneratedSampleImageMediaWorksIncludesImageLessWork(t *testing.T) {
	// Given
	setupMediaRepositoryTestDB(t)
	work := model.FilmWork{
		StorageID: 93, Source: "fc2", Code: "FC2-PPV-930", SourceRef: "930", PrimaryDir: "actor",
	}
	if err := db.Create(&work).Error; err != nil {
		t.Fatalf("seed image-less work: %v", err)
	}

	// When
	selected, err := QueryGeneratedSampleImageMediaWorks("fc2", 72*time.Hour, 20)

	// Then
	if err != nil {
		t.Fatalf("query generated sample works: %v", err)
	}
	if len(selected) != 1 || selected[0].ID != work.ID {
		t.Fatalf("selected works = %v, want image-less work %d", selected, work.ID)
	}
}
