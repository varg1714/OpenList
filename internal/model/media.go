package model

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

const CurrentTranslationVersion uint = 1

var (
	javCodePattern = regexp.MustCompile(`^[A-Z0-9]+(?:[-_.][A-Z0-9]+)*$`)
	fc2CodePattern = regexp.MustCompile(`^FC2-PPV-[A-Z0-9]+(?:[-_][A-Z0-9]+)*$`)
)

func NormalizeMediaCode(source, value string) (string, error) {
	value = strings.TrimSpace(value)
	switch source {
	case "javdb":
		value = strings.ToUpper(value)
		if !javCodePattern.MatchString(value) {
			return "", fmt.Errorf("invalid javdb code: %q", value)
		}
	case "fc2":
		value = strings.ToUpper(value)
		if !strings.HasPrefix(value, "FC2-PPV-") {
			value = "FC2-PPV-" + value
		}
		if !fc2CodePattern.MatchString(value) {
			return "", fmt.Errorf("invalid fc2 code: %q", value)
		}
	case "pornhub":
		if value == "" || strings.ContainsAny(value, `/\`) {
			return "", fmt.Errorf("invalid pornhub view key: %q", value)
		}
	default:
		return "", fmt.Errorf("unsupported media source: %q", source)
	}
	return value, nil
}

func BuildMediaFileName(code string, partIndex, partCount int) (string, error) {
	if code == "" || strings.ContainsAny(code, `/\`) {
		return "", fmt.Errorf("unsafe media code: %q", code)
	}
	if partCount < 1 || partIndex < 1 || partIndex > partCount {
		return "", fmt.Errorf("invalid part %d/%d", partIndex, partCount)
	}
	if partCount == 1 {
		return code + ".mp4", nil
	}
	return fmt.Sprintf("%s-cd%d.mp4", code, partIndex), nil
}

func BuildMediaTitle(code, rawTitle, translatedTitle string) string {
	title := strings.TrimSpace(translatedTitle)
	if title == "" {
		title = strings.TrimSpace(rawTitle)
	}
	if title == "" {
		return code
	}
	return code + " " + title
}

type FilmWork struct {
	ID        uint   `gorm:"primaryKey"`
	StorageID uint   `gorm:"not null;uniqueIndex:idx_media_work_identity"`
	Source    string `gorm:"not null;uniqueIndex:idx_media_work_identity"`
	Code      string `gorm:"not null;uniqueIndex:idx_media_work_identity"`

	SourceRef  string `gorm:"not null"`
	SourceURL  string `gorm:"index"`
	PrimaryDir string `gorm:"not null;index"`

	RawTitle        string
	TranslatedTitle string
	Synopsis        string
	ImageURL        string
	ReleaseDate     time.Time
	Actors          StringArray `gorm:"type:json;serializer:json"`
	Tags            StringArray `gorm:"type:json;serializer:json"`

	TranslationStatus      string `gorm:"index"`
	TranslationAttempts    uint
	TranslationNextRetryAt *time.Time `gorm:"index"`
	TranslationLastError   string
	TranslationVersion     uint

	SynopsisScanAt      *time.Time
	SynopsisNextRetryAt *time.Time `gorm:"index"`
	SynopsisLastError   string
	SynopsisExcluded    bool
	ReleaseScanAt       *time.Time
	ReleaseNextRetryAt  *time.Time `gorm:"index"`
	ReleaseLastError    string
	ActorScanAt         *time.Time
	ActorNextRetryAt    *time.Time `gorm:"index"`
	ActorLastError      string
	TagScanAt           *time.Time
	TagNextRetryAt      *time.Time `gorm:"index"`
	TagLastError        string
	TagVersion          uint
	MagnetScanAt        *time.Time
	MagnetNextRetryAt   *time.Time
	MagnetLastError     string

	SampleImageCount    int
	SampleImageComplete bool
	SampleImageScanAt   *time.Time
	DMMPosterStatus     string `gorm:"index"`
	DMMPosterScanAt     *time.Time
	SubtitleScanAt      *time.Time
	SubtitleNextRetryAt *time.Time
	SubtitleLastError   string

	MetadataVersion uint `gorm:"not null;default:1"`
	NfoVersion      uint `gorm:"not null;default:0"`
	NfoLastError    string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type FilmFile struct {
	ID        uint `gorm:"primaryKey"`
	WorkID    uint `gorm:"not null;uniqueIndex:idx_media_file_part"`
	PartIndex int  `gorm:"not null;uniqueIndex:idx_media_file_part;check:part_index >= 1"`
	PartCount int  `gorm:"not null;check:part_count >= 1"`

	SourcePath string
	SourceSize int64
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type SourceMagnet struct {
	ID          uint   `gorm:"primaryKey"`
	WorkID      uint   `gorm:"not null;uniqueIndex:idx_source_magnet_fingerprint"`
	MagnetURI   string `gorm:"not null"`
	Fingerprint string `gorm:"not null;uniqueIndex:idx_source_magnet_fingerprint"`

	Provider  string
	Priority  int
	Selected  bool `gorm:"index"`
	Subtitle  bool
	ScanAt    *time.Time
	LastError string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type FilmFileWithWork struct {
	FilmFile
	Work FilmWork `gorm:"foreignKey:WorkID"`
}
