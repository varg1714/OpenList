package pornhub

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/cmd/flags"
	"github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"
	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestMain(m *testing.M) {
	dataDir, err := os.MkdirTemp("", "pornhub-test-")
	if err != nil {
		panic(err)
	}
	conf.Conf = conf.DefaultConfig(dataDir)
	testDB, err := gorm.Open(sqlite.Open(filepath.Join(dataDir, "pornhub-test.db")), &gorm.Config{})
	if err != nil {
		panic(err)
	}
	db.Init(testDB)

	code := m.Run()
	if sqlDB, sqlErr := testDB.DB(); sqlErr == nil {
		_ = sqlDB.Close()
	}
	_ = os.RemoveAll(dataDir)
	os.Exit(code)
}

type mockFanartMedia struct {
	duration    float64
	frames      map[float64][]byte
	probeErr    error
	extractErrs map[float64]error
}

func (m *mockFanartMedia) ProbeDuration(_ context.Context, _ string) (float64, error) {
	if m.probeErr != nil {
		return 0, m.probeErr
	}
	return m.duration, nil
}

func (m *mockFanartMedia) ExtractFrame(_ context.Context, _ string, positionSec float64) ([]byte, error) {
	if m.extractErrs != nil {
		if err, ok := m.extractErrs[positionSec]; ok {
			return nil, err
		}
	}
	if m.frames != nil {
		if frame, ok := m.frames[positionSec]; ok {
			return frame, nil
		}
	}
	return []byte(fmt.Sprintf("frame at %.1f", positionSec)), nil
}

func setupPornhubFanartTest(t *testing.T) string {
	t.Helper()
	dataDir := t.TempDir()
	previousDataDir := flags.DataDir
	previousConf := conf.Conf
	flags.DataDir = dataDir
	conf.Conf = conf.DefaultConfig(dataDir)
	t.Cleanup(func() {
		flags.DataDir = previousDataDir
		conf.Conf = previousConf
	})
	if err := db.GetDb().Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&model.Film{}).Error; err != nil {
		t.Fatalf("reset films: %v", err)
	}
	return dataDir
}

func createFanartFilm(t *testing.T, name string, url string, count int, scanAt time.Time) model.Film {
	t.Helper()
	film := model.Film{
		Url:               url,
		Name:              name + ".mp4",
		Source:            DriverName,
		Actor:             "actor",
		Date:              time.Now(),
		SampleImageCount:  count,
		SampleImageScanAt: scanAt,
	}
	if err := db.GetDb().Create(&film).Error; err != nil {
		t.Fatalf("create film: %v", err)
	}
	return film
}

func loadFanartFilm(t *testing.T, filmID uint) model.Film {
	t.Helper()
	var film model.Film
	if err := db.GetDb().First(&film, filmID).Error; err != nil {
		t.Fatalf("load film: %v", err)
	}
	return film
}

func newFanartDriver(media fanartMediaOps, getVideo func(context.Context, string) (string, error)) *Pornhub {
	return &Pornhub{
		Addition: Addition{
			FanartCount:     3,
			FanartScanLimit: 10,
		},
		fanartMedia:        media,
		fanartGetVideo:     getVideo,
		removeBackgroundCb: virtual_file.RemoveBackground,
	}
}
