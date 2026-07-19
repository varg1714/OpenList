package media

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"
)

const artifactJournalVersion = 2

type artifactJournal struct {
	Version    int                 `json:"version"`
	PlanID     string              `json:"plan_id"`
	UpdatedAt  time.Time           `json:"updated_at"`
	Operations []artifactOperation `json:"operations"`
}

type artifactProgressEvent struct {
	PlanID      string `json:"plan_id"`
	OperationID string `json:"operation_id"`
	State       string `json:"state"`
}

type artifactProgressLog struct {
	path    string
	dataDir string
	planID  string
}

func loadArtifactJournal(path, dataDir string) (artifactJournal, bool, error) {
	if err := ensurePathWithin(dataDir, path); err != nil {
		return artifactJournal{}, false, err
	}
	if err := rejectSymlinkAncestors(dataDir, path); err != nil {
		return artifactJournal{}, false, err
	}
	content, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return artifactJournal{}, false, nil
	}
	if err != nil {
		return artifactJournal{}, false, &ArtifactMigrationError{Path: path, Reason: err.Error()}
	}
	var version struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(content, &version); err != nil {
		return artifactJournal{}, false, &ArtifactMigrationError{Path: path, Reason: err.Error()}
	}
	if version.Version != artifactJournalVersion {
		return artifactJournal{}, false, fmt.Errorf("%w: journal %s has version %d, want %d", ErrArtifactJournalVersion, path, version.Version, artifactJournalVersion)
	}
	var journal artifactJournal
	if err := json.Unmarshal(content, &journal); err != nil {
		return artifactJournal{}, false, &ArtifactMigrationError{Path: path, Reason: err.Error()}
	}
	if err := applyArtifactProgress(path+".progress", dataDir, &journal); err != nil {
		return artifactJournal{}, false, err
	}
	return journal, true, nil
}

func applyArtifactProgress(path, dataDir string, journal *artifactJournal) error {
	if err := ensurePathWithin(dataDir, path); err != nil {
		return err
	}
	if err := rejectSymlinkAncestors(dataDir, path); err != nil {
		return err
	}
	content, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return &ArtifactMigrationError{Path: path, Reason: err.Error()}
	}
	completeLength := len(content)
	if completeLength > 0 && content[completeLength-1] != '\n' {
		lastNewline := bytes.LastIndexByte(content, '\n')
		if lastNewline < 0 {
			return nil
		}
		completeLength = lastNewline + 1
	}
	operations := make(map[string]*artifactOperation, len(journal.Operations))
	for index := range journal.Operations {
		operations[journal.Operations[index].ID] = &journal.Operations[index]
	}
	lines := bytes.Split(content[:completeLength], []byte{'\n'})
	for index, line := range lines {
		if len(line) == 0 {
			if index == len(lines)-1 {
				continue
			}
			return &ArtifactMigrationError{Path: path, Reason: "progress file contains an empty complete event"}
		}
		var event artifactProgressEvent
		if err := json.Unmarshal(line, &event); err != nil {
			return &ArtifactMigrationError{Path: path, Reason: err.Error()}
		}
		if event.PlanID != journal.PlanID {
			return &ArtifactMigrationError{Path: path, Reason: "progress event plan ID does not match journal"}
		}
		operation := operations[event.OperationID]
		if operation == nil {
			return &ArtifactMigrationError{Path: path, Reason: fmt.Sprintf("progress event references unknown operation %q", event.OperationID)}
		}
		if !validArtifactOperationState(event.State) {
			return &ArtifactMigrationError{Path: path, Reason: fmt.Sprintf("progress event has unknown state %q", event.State)}
		}
		operation.State = event.State
	}
	return nil
}

func validArtifactOperationState(state string) bool {
	switch state {
	case "pending", "placed", "verified", "cleaned", "done":
		return true
	default:
		return false
	}
}

func (progress artifactProgressLog) record(operation *artifactOperation, state string) error {
	if !validArtifactOperationState(state) {
		return &ArtifactMigrationError{Path: progress.path, Reason: fmt.Sprintf("unknown progress state %q", state)}
	}
	if err := ensurePathWithin(progress.dataDir, progress.path); err != nil {
		return err
	}
	if err := rejectSymlinkAncestors(progress.dataDir, progress.path); err != nil {
		return err
	}
	content, err := json.Marshal(artifactProgressEvent{PlanID: progress.planID, OperationID: operation.ID, State: state})
	if err != nil {
		return &ArtifactMigrationError{Path: progress.path, Reason: err.Error()}
	}
	content = append(content, '\n')
	if err := repairArtifactProgressTail(progress.path); err != nil {
		return &ArtifactMigrationError{Path: progress.path, Reason: err.Error()}
	}
	file, err := os.OpenFile(progress.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return &ArtifactMigrationError{Path: progress.path, Reason: err.Error()}
	}
	written, writeErr := file.Write(content)
	if writeErr == nil && written != len(content) {
		writeErr = io.ErrShortWrite
	}
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		return &ArtifactMigrationError{Path: progress.path, Reason: writeErr.Error()}
	}
	if closeErr != nil {
		return &ArtifactMigrationError{Path: progress.path, Reason: closeErr.Error()}
	}
	operation.State = state
	return nil
}

func repairArtifactProgressTail(path string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	size := info.Size()
	if size == 0 {
		return nil
	}
	var last [1]byte
	if _, err := file.ReadAt(last[:], size-1); err != nil {
		return err
	}
	if last[0] == '\n' {
		return nil
	}
	const chunkSize int64 = 4096
	buffer := make([]byte, chunkSize)
	for end := size; end > 0; {
		start := max(int64(0), end-chunkSize)
		length := end - start
		if _, err := file.ReadAt(buffer[:length], start); err != nil {
			return err
		}
		if index := bytes.LastIndexByte(buffer[:length], '\n'); index >= 0 {
			return truncateAndSync(file, start+int64(index)+1)
		}
		end = start
	}
	return truncateAndSync(file, 0)
}

func truncateAndSync(file *os.File, size int64) error {
	if err := file.Truncate(size); err != nil {
		return err
	}
	return file.Sync()
}

func writeArtifactJournal(path, dataDir string, journal artifactJournal) error {
	if err := ensurePathWithin(dataDir, path); err != nil {
		return err
	}
	if err := rejectSymlinkAncestors(dataDir, path); err != nil {
		return err
	}
	journal.Version = artifactJournalVersion
	journal.UpdatedAt = time.Now().UTC()
	content, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return &ArtifactMigrationError{Path: path, Reason: err.Error()}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return &ArtifactMigrationError{Path: path, Reason: err.Error()}
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".media-migration-journal-*")
	if err != nil {
		return &ArtifactMigrationError{Path: path, Reason: err.Error()}
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return &ArtifactMigrationError{Path: path, Reason: err.Error()}
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return &ArtifactMigrationError{Path: path, Reason: err.Error()}
	}
	if err := temporary.Close(); err != nil {
		return &ArtifactMigrationError{Path: path, Reason: err.Error()}
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return &ArtifactMigrationError{Path: path, Reason: err.Error()}
	}
	return nil
}

func preflightJournalOperations(plan *artifactPlan, dataDir string) error {
	moveTargets := make(map[string]string)
	regularMoveTargets := make(map[string]string)
	for _, operation := range plan.Operations {
		if !validArtifactOperationState(operation.State) {
			return &ArtifactMigrationError{Path: operation.SourcePath, Reason: fmt.Sprintf("unknown journal operation state %q", operation.State)}
		}
		wantID := stableHash(string(operation.Kind), operation.SourcePath, operation.TargetPath, operation.VerifyPath, operation.LinkTarget, operation.SHA256)
		if operation.ID != wantID {
			return &ArtifactMigrationError{Path: operation.SourcePath, Reason: "journal operation ID is invalid"}
		}
		for _, path := range []string{operation.SourcePath, operation.TargetPath, operation.VerifyPath, operation.LinkTarget} {
			if path != "" {
				if err := ensurePathWithin(dataDir, path); err != nil {
					return err
				}
			}
		}
		if operation.Kind == artifactMove || operation.Kind == artifactSymlink {
			moveTargets[operation.TargetPath] = operation.SHA256
		}
		if operation.Kind == artifactMove {
			regularMoveTargets[operation.TargetPath] = operation.SHA256
		}
	}
	for _, operation := range plan.Operations {
		if operation.Kind == artifactSymlink {
			if operation.LinkTarget == "" || operation.LinkTarget == operation.TargetPath {
				return &ArtifactMigrationError{Path: operation.SourcePath, Reason: "symlink dependency is empty or self-referential"}
			}
			if expectedHash, planned := regularMoveTargets[operation.LinkTarget]; planned {
				if expectedHash != operation.SHA256 {
					return &ArtifactMigrationError{Path: operation.SourcePath, Reason: "symlink dependency hash differs from regular move target"}
				}
			} else {
				info, err := os.Lstat(operation.LinkTarget)
				if err != nil {
					return &ArtifactMigrationError{Path: operation.LinkTarget, Reason: "symlink dependency is unresolved"}
				}
				if !info.Mode().IsRegular() {
					return &ArtifactMigrationError{Path: operation.LinkTarget, Reason: "symlink dependency is not a regular file destination"}
				}
				hash, err := fileHash(operation.LinkTarget)
				if err != nil {
					return &ArtifactMigrationError{Path: operation.LinkTarget, Reason: err.Error()}
				}
				if hash != operation.SHA256 {
					return &ArtifactMigrationError{Path: operation.LinkTarget, Reason: "symlink dependency hash differs from expected content"}
				}
			}
		}
		switch operation.Kind {
		case artifactMove, artifactSymlink:
			targetHash, targetExists, err := regularArtifactHash(operation.TargetPath)
			if err != nil {
				return err
			}
			if targetExists {
				if targetHash != operation.SHA256 {
					return &ArtifactCollisionError{SourcePath: operation.SourcePath, TargetPath: operation.TargetPath}
				}
				continue
			}
			sourceHash, sourceExists, err := regularArtifactHash(operation.SourcePath)
			if err != nil {
				return err
			}
			if !sourceExists || sourceHash != operation.SHA256 {
				return &ArtifactMigrationError{Path: operation.SourcePath, Reason: "neither source nor verified target matches the journal"}
			}
		case artifactDelete:
			verifyHash, exists, err := regularArtifactHash(operation.VerifyPath)
			if err != nil {
				return err
			}
			if exists && verifyHash != operation.SHA256 {
				return &ArtifactCollisionError{SourcePath: operation.SourcePath, TargetPath: operation.VerifyPath}
			}
			if !exists && moveTargets[operation.VerifyPath] != operation.SHA256 {
				return &ArtifactMigrationError{Path: operation.VerifyPath, Reason: "cleanup verification target is unavailable"}
			}
		case artifactRemoveDir:
		default:
			return &ArtifactMigrationError{Path: operation.SourcePath, Reason: fmt.Sprintf("unknown journal operation kind %q", operation.Kind)}
		}
	}
	return nil
}

func migrateArtifacts(plan *artifactPlan, options MigrationOptions, report *MigrationReport) error {
	if len(plan.Operations) == 0 {
		return nil
	}
	journal, exists, err := loadArtifactJournal(options.JournalPath, options.DataDir)
	if err != nil {
		return err
	}
	if !exists {
		if _, err := os.Lstat(options.JournalPath + ".progress"); err == nil {
			return &ArtifactMigrationError{Path: options.JournalPath + ".progress", Reason: "progress file exists without its immutable journal"}
		} else if !os.IsNotExist(err) {
			return &ArtifactMigrationError{Path: options.JournalPath + ".progress", Reason: err.Error()}
		}
		journal = artifactJournal{Version: artifactJournalVersion, PlanID: plan.ID, Operations: append([]artifactOperation(nil), plan.Operations...)}
		if err := writeArtifactJournal(options.JournalPath, options.DataDir, journal); err != nil {
			return err
		}
	}
	if journal.PlanID != plan.ID {
		return &ArtifactMigrationError{Path: options.JournalPath, Reason: "journal plan ID changed after preflight"}
	}
	progress := &artifactProgressLog{path: options.JournalPath + ".progress", dataDir: options.DataDir, planID: journal.PlanID}
	slices.SortStableFunc(journal.Operations, compareArtifactOperations)
	for index := range journal.Operations {
		operation := &journal.Operations[index]
		changed, err := executeArtifactOperation(operation, progress)
		if err != nil {
			return err
		}
		switch operation.Kind {
		case artifactMove, artifactSymlink:
			if changed {
				report.ArtifactsMoved++
			} else {
				report.ArtifactsExisting++
			}
		case artifactDelete:
			if changed {
				report.ArtifactsDeleted++
			}
		case artifactRemoveDir:
			if changed {
				report.ArtifactDirectoriesRemoved++
			}
		}
	}
	return nil
}

func executeArtifactOperation(operation *artifactOperation, progress *artifactProgressLog) (bool, error) {
	switch operation.Kind {
	case artifactMove:
		return executeRegularMove(operation, progress)
	case artifactSymlink:
		return executeSymlinkMove(operation, progress)
	case artifactDelete:
		return executeArtifactDelete(operation, progress)
	case artifactRemoveDir:
		return executeDirectoryRemoval(operation, progress)
	default:
		return false, &ArtifactMigrationError{Path: operation.SourcePath, Reason: fmt.Sprintf("unknown operation kind %q", operation.Kind)}
	}
}

func executeRegularMove(operation *artifactOperation, progress *artifactProgressLog) (bool, error) {
	targetHash, targetExists, err := regularArtifactHash(operation.TargetPath)
	if err != nil {
		return false, err
	}
	changed := false
	if !targetExists {
		if err := os.MkdirAll(filepath.Dir(operation.TargetPath), 0o755); err != nil {
			return false, &ArtifactMigrationError{Path: filepath.Dir(operation.TargetPath), Reason: err.Error()}
		}
		if err := os.Rename(operation.SourcePath, operation.TargetPath); err != nil {
			return false, &ArtifactMigrationError{Path: operation.SourcePath, Reason: err.Error()}
		}
		changed = true
		if err := progress.record(operation, "placed"); err != nil {
			return false, err
		}
		targetHash, targetExists, err = regularArtifactHash(operation.TargetPath)
		if err != nil {
			return false, err
		}
	}
	if !targetExists || targetHash != operation.SHA256 {
		return false, &ArtifactCollisionError{SourcePath: operation.SourcePath, TargetPath: operation.TargetPath}
	}
	if err := progress.record(operation, "verified"); err != nil {
		return false, err
	}
	if err := progress.record(operation, "done"); err != nil {
		return false, err
	}
	return changed, nil
}

func executeSymlinkMove(operation *artifactOperation, progress *artifactProgressLog) (bool, error) {
	targetHash, targetExists, err := regularArtifactHash(operation.TargetPath)
	if err != nil {
		return false, err
	}
	changed := false
	if !targetExists {
		linkHash, linkExists, err := regularArtifactHash(operation.LinkTarget)
		if err != nil {
			return false, err
		}
		if !linkExists || linkHash != operation.SHA256 {
			return false, &ArtifactMigrationError{Path: operation.LinkTarget, Reason: "symlink destination target is not verified"}
		}
		if err := os.MkdirAll(filepath.Dir(operation.TargetPath), 0o755); err != nil {
			return false, &ArtifactMigrationError{Path: filepath.Dir(operation.TargetPath), Reason: err.Error()}
		}
		relativeTarget, err := filepath.Rel(filepath.Dir(operation.TargetPath), operation.LinkTarget)
		if err != nil {
			return false, &ArtifactMigrationError{Path: operation.TargetPath, Reason: err.Error()}
		}
		temporary := operation.TargetPath + ".media-migration-link"
		if err := removeExpectedTemporarySymlink(temporary, relativeTarget); err != nil {
			return false, err
		}
		if err := os.Symlink(relativeTarget, temporary); err != nil {
			return false, &ArtifactMigrationError{Path: temporary, Reason: err.Error()}
		}
		if err := os.Rename(temporary, operation.TargetPath); err != nil {
			_ = os.Remove(temporary)
			return false, &ArtifactMigrationError{Path: operation.TargetPath, Reason: err.Error()}
		}
		changed = true
		if err := progress.record(operation, "placed"); err != nil {
			return false, err
		}
		targetHash, targetExists, err = regularArtifactHash(operation.TargetPath)
		if err != nil {
			return false, err
		}
	}
	if !targetExists || targetHash != operation.SHA256 {
		return false, &ArtifactCollisionError{SourcePath: operation.SourcePath, TargetPath: operation.TargetPath}
	}
	if err := progress.record(operation, "verified"); err != nil {
		return false, err
	}
	if err := os.Remove(operation.SourcePath); err != nil && !os.IsNotExist(err) {
		return false, &ArtifactMigrationError{Path: operation.SourcePath, Reason: err.Error()}
	}
	if err := progress.record(operation, "cleaned"); err != nil {
		return false, err
	}
	if err := progress.record(operation, "done"); err != nil {
		return false, err
	}
	return changed, nil
}

func removeExpectedTemporarySymlink(path, expectedTarget string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return &ArtifactMigrationError{Path: path, Reason: err.Error()}
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return &ArtifactMigrationError{Path: path, Reason: "unexpected non-symlink at deterministic temporary path"}
	}
	target, err := os.Readlink(path)
	if err != nil {
		return &ArtifactMigrationError{Path: path, Reason: err.Error()}
	}
	if target != expectedTarget {
		return &ArtifactMigrationError{Path: path, Reason: fmt.Sprintf("temporary symlink target %q differs from expected %q", target, expectedTarget)}
	}
	if err := os.Remove(path); err != nil {
		return &ArtifactMigrationError{Path: path, Reason: err.Error()}
	}
	return nil
}

func executeArtifactDelete(operation *artifactOperation, progress *artifactProgressLog) (bool, error) {
	hash, exists, err := regularArtifactHash(operation.VerifyPath)
	if err != nil {
		return false, err
	}
	if !exists || hash != operation.SHA256 {
		return false, &ArtifactCollisionError{SourcePath: operation.SourcePath, TargetPath: operation.VerifyPath}
	}
	if err := progress.record(operation, "verified"); err != nil {
		return false, err
	}
	_, statErr := os.Lstat(operation.SourcePath)
	changed := statErr == nil
	if statErr != nil && !os.IsNotExist(statErr) {
		return false, &ArtifactMigrationError{Path: operation.SourcePath, Reason: statErr.Error()}
	}
	if changed {
		if err := os.Remove(operation.SourcePath); err != nil {
			return false, &ArtifactMigrationError{Path: operation.SourcePath, Reason: err.Error()}
		}
	}
	if err := progress.record(operation, "cleaned"); err != nil {
		return false, err
	}
	if err := progress.record(operation, "done"); err != nil {
		return false, err
	}
	return changed, nil
}

func executeDirectoryRemoval(operation *artifactOperation, progress *artifactProgressLog) (bool, error) {
	entries, err := os.ReadDir(operation.SourcePath)
	if os.IsNotExist(err) {
		return false, progress.record(operation, "done")
	}
	if err != nil {
		return false, &ArtifactMigrationError{Path: operation.SourcePath, Reason: err.Error()}
	}
	changed := false
	if len(entries) == 0 {
		if err := os.Remove(operation.SourcePath); err != nil {
			return false, &ArtifactMigrationError{Path: operation.SourcePath, Reason: err.Error()}
		}
		changed = true
	}
	if err := progress.record(operation, "done"); err != nil {
		return false, err
	}
	return changed, nil
}
