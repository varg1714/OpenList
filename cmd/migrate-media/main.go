package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	appdb "github.com/OpenListTeam/OpenList/v4/internal/db"
	migrationmedia "github.com/OpenListTeam/OpenList/v4/internal/migration/media"
	"github.com/spf13/cobra"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

type migrationReportOutput struct {
	LegacyFilms         int `json:"LegacyFilms"`
	LegacyMagnetCaches  int `json:"LegacyMagnetCaches"`
	SkippedLegacyFilms  int `json:"SkippedLegacyFilms"`
	SkippedMagnetCaches int `json:"SkippedMagnetCaches"`

	WorksCreated            int `json:"WorksCreated"`
	WorksExisting           int `json:"WorksExisting"`
	FilesCreated            int `json:"FilesCreated"`
	FilesExisting           int `json:"FilesExisting"`
	SourceMagnetsCreated    int `json:"SourceMagnetsCreated"`
	SourceMagnetsExisting   int `json:"SourceMagnetsExisting"`
	CloudFileCachesCreated  int `json:"CloudFileCachesCreated"`
	CloudFileCachesExisting int `json:"CloudFileCachesExisting"`
	ArtifactsPlanned        int `json:"ArtifactsPlanned"`
	ArtifactsCopied         int `json:"ArtifactsCopied"`
	ArtifactsExisting       int `json:"ArtifactsExisting"`
}

func compactMigrationReport(report migrationmedia.MigrationReport) migrationReportOutput {
	return migrationReportOutput{
		LegacyFilms:             report.LegacyFilms,
		LegacyMagnetCaches:      report.LegacyMagnetCaches,
		SkippedLegacyFilms:      len(report.SkippedLegacyFilms),
		SkippedMagnetCaches:     len(report.SkippedMagnetCaches),
		WorksCreated:            report.WorksCreated,
		WorksExisting:           report.WorksExisting,
		FilesCreated:            report.FilesCreated,
		FilesExisting:           report.FilesExisting,
		SourceMagnetsCreated:    report.SourceMagnetsCreated,
		SourceMagnetsExisting:   report.SourceMagnetsExisting,
		CloudFileCachesCreated:  report.CloudFileCachesCreated,
		CloudFileCachesExisting: report.CloudFileCachesExisting,
		ArtifactsPlanned:        report.ArtifactsPlanned,
		ArtifactsCopied:         report.ArtifactsCopied,
		ArtifactsExisting:       report.ArtifactsExisting,
	}
}

func main() {
	command := NewCommand(os.Stdout, os.Stderr)
	if err := command.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func NewCommand(stdout, stderr io.Writer) *cobra.Command {
	var dbPath string
	var dataDir string
	var journalPath string
	var tablePrefix string
	var dryRun bool
	var apply bool
	var mappingValues []string

	command := &cobra.Command{
		Use:           "migrate-media",
		Short:         "migrate legacy media identity and local artifacts",
		SilenceUsage:  true,
		SilenceErrors: true,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(dbPath) == "" {
				return fmt.Errorf("--db is required")
			}
			if dryRun == apply {
				return fmt.Errorf("specify exactly one of --dry-run or --apply")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			mapping, err := parseStorageMappings(mappingValues)
			if err != nil {
				return err
			}
			database, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
				NamingStrategy: schema.NamingStrategy{TablePrefix: tablePrefix},
				Logger:         logger.Default.LogMode(logger.Silent),
			})
			if err != nil {
				return fmt.Errorf("open SQLite database: %w", err)
			}
			sqlDB, err := database.DB()
			if err != nil {
				return fmt.Errorf("get SQLite database handle: %w", err)
			}
			defer sqlDB.Close()
			conf.Conf = conf.DefaultConfig(dataDir)
			conf.Conf.Database.DBFile = dbPath
			conf.Conf.Database.TablePrefix = tablePrefix
			if err := appdb.Init(database); err != nil {
				return fmt.Errorf("initialize migration database schema: %w", err)
			}
			mode := migrationmedia.MigrationApply
			if dryRun {
				mode = migrationmedia.MigrationDryRun
			}
			report, migrationErr := migrationmedia.MigrateLegacyMediaWithOptions(cmd.Context(), database, migrationmedia.MigrationOptions{
				Mode: mode, DataDir: dataDir, JournalPath: journalPath, StorageMapping: mapping,
			})
			if err := json.NewEncoder(cmd.OutOrStdout()).Encode(compactMigrationReport(report)); err != nil {
				return fmt.Errorf("write migration report: %w", err)
			}
			return migrationErr
		},
	}
	command.SetOut(stdout)
	command.SetErr(stderr)
	command.Flags().StringVar(&dbPath, "db", "", "SQLite database path")
	command.Flags().StringVar(&dataDir, "data-dir", "data", "OpenList data directory containing emby artifacts")
	command.Flags().StringVar(&journalPath, "journal", "", "migration artifact journal path")
	command.Flags().StringVar(&tablePrefix, "table-prefix", "", "SQLite table prefix")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "inspect migration without database, artifact, or journal writes")
	command.Flags().BoolVar(&apply, "apply", false, "apply normalized database and artifact migration")
	command.Flags().StringArrayVar(&mappingValues, "storage-map", nil, "legacy storage mapping source:primaryDir=storageID; repeatable")
	return command
}

func parseStorageMappings(values []string) (map[string]uint, error) {
	mapping := make(map[string]uint, len(values))
	for _, value := range values {
		parts := strings.SplitN(value, "=", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" {
			return nil, fmt.Errorf("invalid --storage-map %q, want source:primaryDir=storageID", value)
		}
		storageID, err := strconv.ParseUint(strings.TrimSpace(parts[1]), 10, 0)
		if err != nil || storageID == 0 {
			return nil, fmt.Errorf("invalid storage ID in --storage-map %q", value)
		}
		key := strings.TrimSpace(parts[0])
		if _, exists := mapping[key]; exists {
			return nil, fmt.Errorf("duplicate --storage-map key %q", key)
		}
		mapping[key] = uint(storageID)
	}
	return mapping, nil
}
