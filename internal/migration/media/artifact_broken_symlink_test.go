package media

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

func TestMigrateLegacyMediaDeletesDanglingArtifactSymlink(t *testing.T) {
	// Given
	database := newMigrationTestDB(t)
	dataDir := t.TempDir()
	createStorages(t, database, model.Storage{ID: 18, Driver: "FC2", MountPath: "/fc2"})
	films := []model.Film{
		{Source: "fc2", Actor: "个人收藏", Name: "FC2-PPV-1042868-cd1.mp4", Url: "https://adult.contents.fc2.com/article/1042868/"},
		{Source: "fc2", Actor: "个人收藏", Name: "FC2-PPV-1042868-cd2.mp4", Url: "https://adult.contents.fc2.com/article/1042868/"},
	}
	if err := database.Create(&films).Error; err != nil {
		t.Fatalf("seed multipart films: %v", err)
	}
	artifactRoot := filepath.Join(dataDir, "emby", "fc2", "个人收藏", "FC2-PPV-1042868")
	if err := os.MkdirAll(artifactRoot, 0o755); err != nil {
		t.Fatalf("create artifact root: %v", err)
	}
	danglingLink := filepath.Join(artifactRoot, "FC2-PPV-1042868-cd2-background.jpg")
	if err := os.Symlink("FC2-PPV-1042868-cd2.jpg", danglingLink); err != nil {
		t.Fatalf("create dangling artifact symlink: %v", err)
	}

	// When
	report, err := MigrateLegacyMediaWithOptions(context.Background(), database, MigrationOptions{
		Mode: MigrationApply, DataDir: dataDir, JournalPath: filepath.Join(dataDir, "journal.json"),
	})

	// Then
	if err != nil {
		t.Fatalf("migrate artifacts with dangling symlink: %v", err)
	}
	if report.ArtifactDeletesPlanned != 1 || report.ArtifactsDeleted != 1 {
		t.Fatalf("dangling symlink report = %+v", report)
	}
	if _, err := os.Lstat(danglingLink); !os.IsNotExist(err) {
		t.Fatalf("dangling artifact symlink remains: %v", err)
	}
}
