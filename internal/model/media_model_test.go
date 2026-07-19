package model

import (
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func openMediaModelTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open SQLite database: %v", err)
	}
	if err := db.AutoMigrate(&FilmWork{}, &FilmFile{}, &SourceMagnet{}); err != nil {
		t.Fatalf("migrate media models: %v", err)
	}
	return db
}

func TestMediaModelConstraints(t *testing.T) {
	db := openMediaModelTestDB(t)
	work := FilmWork{
		StorageID:  1,
		Source:     "javdb",
		Code:       "ABP-123",
		SourceRef:  "v/1",
		PrimaryDir: "actor-a",
		Actors:     StringArray{"Actor A"},
		Tags:       StringArray{"Tag A"},
	}
	if err := db.Create(&work).Error; err != nil {
		t.Fatalf("create valid film work: %v", err)
	}

	duplicateWork := work
	duplicateWork.ID = 0
	if err := db.Create(&duplicateWork).Error; err == nil {
		t.Fatal("duplicate (StorageID, Source, Code) accepted")
	}

	file := FilmFile{WorkID: work.ID, PartIndex: 1, PartCount: 1, SourcePath: "/media/ABP-123.mp4"}
	if err := db.Create(&file).Error; err != nil {
		t.Fatalf("create valid film file: %v", err)
	}
	duplicateFile := file
	duplicateFile.ID = 0
	if err := db.Create(&duplicateFile).Error; err == nil {
		t.Fatal("duplicate (WorkID, PartIndex) accepted")
	}

	magnet := SourceMagnet{
		WorkID:      work.ID,
		MagnetURI:   "magnet:?xt=urn:btih:example",
		Fingerprint: "magnet-fingerprint",
		Provider:    "provider-a",
	}
	if err := db.Create(&magnet).Error; err != nil {
		t.Fatalf("create valid source magnet: %v", err)
	}

	var fileWithWork FilmFileWithWork
	if err := db.Table("film_files").Preload("Work").First(&fileWithWork, file.ID).Error; err != nil {
		t.Fatalf("load film file with work: %v", err)
	}
	if fileWithWork.Work.ID != work.ID {
		t.Fatalf("preloaded work ID = %d, want %d", fileWithWork.Work.ID, work.ID)
	}
}
