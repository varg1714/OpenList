package media

import (
	"fmt"
	"path/filepath"

	"github.com/OpenListTeam/OpenList/v4/cmd/flags"
)

type MigrationMode string

const (
	MigrationApply  MigrationMode = "apply"
	MigrationDryRun MigrationMode = "dry-run"
)

type MigrationOptions struct {
	Mode           MigrationMode
	DataDir        string
	JournalPath    string
	StorageMapping map[string]uint
}

func defaultMigrationOptions() MigrationOptions {
	return MigrationOptions{Mode: MigrationApply, DataDir: flags.DataDir}
}

func (options MigrationOptions) normalized() (MigrationOptions, error) {
	if options.Mode == "" {
		options.Mode = MigrationApply
	}
	if options.Mode != MigrationApply && options.Mode != MigrationDryRun {
		return MigrationOptions{}, fmt.Errorf("unsupported migration mode %q", options.Mode)
	}
	if options.DataDir == "" {
		options.DataDir = flags.DataDir
	}
	if options.DataDir == "" {
		options.DataDir = "data"
	}
	dataDir, err := filepath.Abs(options.DataDir)
	if err != nil {
		return MigrationOptions{}, err
	}
	options.DataDir = dataDir
	if options.JournalPath == "" {
		options.JournalPath = filepath.Join(dataDir, "emby", ".media-migration-journal.json")
	} else if options.JournalPath, err = filepath.Abs(options.JournalPath); err != nil {
		return MigrationOptions{}, err
	}
	return options, nil
}
