package media

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

func TestMigrateLegacyMediaPreservesNormalizedWorkRootAndRemovesOrphan(t *testing.T) {
	// Given
	database := newMigrationTestDB(t)
	dataDir := t.TempDir()
	createStorages(t, database, model.Storage{ID: 15, Driver: "Javdb", MountPath: "/javdb"})
	legacy := model.Film{Source: "javdb", Actor: "miru", Name: "SSNI-772.mp4", Url: "https://javdb.test/v/ssni-772"}
	existing := model.FilmWork{StorageID: 15, Source: "javdb", Code: "SSNI-773", SourceRef: "existing", PrimaryDir: "miru"}
	if err := database.Create(&legacy).Error; err != nil {
		t.Fatalf("seed legacy film: %v", err)
	}
	if err := database.Create(&existing).Error; err != nil {
		t.Fatalf("seed normalized work: %v", err)
	}
	protectedRoot := filepath.Join(dataDir, "emby", "javdb", "miru", "SSNI-773")
	orphanRoot := filepath.Join(dataDir, "emby", "javdb", "miru", "SSNI-774 orphan")
	writeArtifactFixture(t, protectedRoot, map[string]string{"SSNI-773.nfo": "protected"})
	writeArtifactFixture(t, orphanRoot, map[string]string{"SSNI-774 orphan.nfo": "orphan"})

	// When
	report, err := MigrateLegacyMediaWithOptions(context.Background(), database, MigrationOptions{
		Mode: MigrationApply, DataDir: dataDir, JournalPath: filepath.Join(dataDir, "migration-journal.json"),
	})

	// Then
	if err != nil {
		t.Fatalf("migrate with normalized and orphan roots: %v", err)
	}
	if report.ArtifactDirectoriesPlanned != 1 || report.ArtifactDirectoriesRemoved != 1 {
		t.Fatalf("artifact report = %+v", report)
	}
	assertArtifactContent(t, filepath.Join(protectedRoot, "SSNI-773.nfo"), "protected")
	if _, err := os.Stat(orphanRoot); !os.IsNotExist(err) {
		t.Fatalf("orphan root remains: %v", err)
	}
}
