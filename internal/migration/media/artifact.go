package media

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

var (
	ErrArtifactCollision = errors.New("media artifact collision")
	ErrArtifactMigration = errors.New("media artifact migration failed")
	ErrArtifactSafety    = errors.New("media artifact path is unsafe")
)

type ArtifactCollisionError struct {
	SourcePath string
	TargetPath string
}

func (e *ArtifactCollisionError) Error() string {
	return fmt.Sprintf("%s: source %s conflicts with target %s", ErrArtifactCollision, e.SourcePath, e.TargetPath)
}

func (e *ArtifactCollisionError) Unwrap() error { return ErrArtifactCollision }

type ArtifactMigrationError struct {
	Path   string
	Reason string
}

func (e *ArtifactMigrationError) Error() string {
	return fmt.Sprintf("%s at %s: %s", ErrArtifactMigration, e.Path, e.Reason)
}

func (e *ArtifactMigrationError) Unwrap() error { return ErrArtifactMigration }

type ArtifactSafetyError struct {
	Path   string
	Reason string
}

func (e *ArtifactSafetyError) Error() string {
	return fmt.Sprintf("%s at %s: %s", ErrArtifactSafety, e.Path, e.Reason)
}

func (e *ArtifactSafetyError) Unwrap() error { return ErrArtifactSafety }

type artifactAction struct {
	SourcePath string
	TargetPath string
}

type artifactJournal struct {
	Version   int                    `json:"version"`
	UpdatedAt time.Time              `json:"updated_at"`
	Entries   []artifactJournalEntry `json:"entries"`
}

type artifactJournalEntry struct {
	SourcePath string    `json:"source_path"`
	TargetPath string    `json:"target_path"`
	SHA256     string    `json:"sha256"`
	State      string    `json:"state"`
	UpdatedAt  time.Time `json:"updated_at"`
}

var (
	legacyRealNamePattern = regexp.MustCompile(`(?i)^(.*?)(?:-cd[0-9]+)?(?:-background)?$`)
	fanartNamePattern     = regexp.MustCompile(`^fanart([1-9][0-9]*)\.jpg$`)
)

func inspectArtifacts(plan *migrationPlan, options MigrationOptions, report *MigrationReport) error {
	actions, err := collectArtifactActions(plan, options.DataDir)
	if err != nil {
		return err
	}
	if err := inspectPlannedTargetCollisions(actions); err != nil {
		return err
	}
	report.ArtifactsPlanned = len(actions)
	for _, action := range actions {
		_, existing, err := processArtifact(action, false)
		if err != nil {
			return err
		}
		if existing {
			report.ArtifactsExisting++
		}
	}
	return nil
}

func inspectPlannedTargetCollisions(actions []artifactAction) error {
	hashesByTarget := make(map[string]string)
	for _, action := range actions {
		info, err := os.Lstat(action.SourcePath)
		if err != nil {
			return &ArtifactMigrationError{Path: action.SourcePath, Reason: err.Error()}
		}
		if !info.Mode().IsRegular() {
			return &ArtifactMigrationError{Path: action.SourcePath, Reason: "source is not a regular file"}
		}
		content, err := os.ReadFile(action.SourcePath)
		if err != nil {
			return &ArtifactMigrationError{Path: action.SourcePath, Reason: err.Error()}
		}
		sum := sha256.Sum256(content)
		hash := hex.EncodeToString(sum[:])
		if previousHash, ok := hashesByTarget[action.TargetPath]; ok && previousHash != hash {
			return &ArtifactCollisionError{SourcePath: action.SourcePath, TargetPath: action.TargetPath}
		}
		hashesByTarget[action.TargetPath] = hash
	}
	return nil
}

func migrateArtifacts(plan *migrationPlan, options MigrationOptions, report *MigrationReport) error {
	actions, err := collectArtifactActions(plan, options.DataDir)
	if err != nil {
		return err
	}
	report.ArtifactsPlanned = len(actions)
	journal, err := loadArtifactJournal(options.JournalPath, options.DataDir)
	if err != nil {
		return err
	}
	for _, action := range actions {
		entry := journalEntry(&journal, action)
		entry.State = "pending"
		entry.UpdatedAt = time.Now().UTC()
		if err := writeArtifactJournal(options.JournalPath, options.DataDir, journal); err != nil {
			return err
		}
		copied, existing, err := processArtifact(action, true)
		if err != nil {
			return err
		}
		if copied {
			report.ArtifactsCopied++
		}
		if existing {
			report.ArtifactsExisting++
		}
		entry.State = "done"
		entry.SHA256 = artifactHash(action.TargetPath)
		entry.UpdatedAt = time.Now().UTC()
		if err := writeArtifactJournal(options.JournalPath, options.DataDir, journal); err != nil {
			return err
		}
	}
	return nil
}

func collectArtifactActions(plan *migrationPlan, dataDir string) ([]artifactAction, error) {
	seen := make(map[string]bool)
	actions := make([]artifactAction, 0)
	for _, work := range plan.works {
		targetRoot, err := safeArtifactPath(dataDir, "emby", work.identity.Source, strconv.FormatUint(uint64(work.identity.StorageID), 10), work.work.PrimaryDir, work.identity.Code)
		if err != nil {
			return nil, err
		}
		for _, source := range work.artifactSources {
			legacyBase := clearArtifactExtension(source.name)
			legacyRealName := legacyRealNameFor(source.name)
			legacyRoot, err := safeArtifactPath(dataDir, "emby", work.identity.Source, work.work.PrimaryDir, legacyRealName)
			if err != nil {
				return nil, err
			}
			add := func(sourceName, targetName string) error {
				return appendArtifactAction(legacyRoot, targetRoot, sourceName, targetName, seen, &actions)
			}
			if err := add("poster.jpg", "poster.jpg"); err != nil {
				return nil, err
			}
			if err := add(legacyBase+".jpg", work.identity.Code+".jpg"); err != nil {
				return nil, err
			}
			if err := add(legacyBase+"-background.jpg", work.identity.Code+"-background.jpg"); err != nil {
				return nil, err
			}
			if err := add(legacyBase+".nfo", work.identity.Code+".nfo"); err != nil {
				return nil, err
			}
			if err := collectDirectoryArtifacts(legacyRoot, targetRoot, legacyBase, work.identity.Code, source.partIndex, seen, &actions); err != nil {
				return nil, err
			}
		}
	}
	slices.SortFunc(actions, func(a, b artifactAction) int {
		if targetCompare := strings.Compare(a.TargetPath, b.TargetPath); targetCompare != 0 {
			return targetCompare
		}
		return strings.Compare(a.SourcePath, b.SourcePath)
	})
	return actions, nil
}

func collectDirectoryArtifacts(legacyRoot, targetRoot, legacyBase, code string, partIndex int, seen map[string]bool, actions *[]artifactAction) error {
	entries, err := os.ReadDir(legacyRoot)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return &ArtifactMigrationError{Path: legacyRoot, Reason: err.Error()}
	}
	for _, entry := range entries {
		name := entry.Name()
		if match := fanartNamePattern.FindStringSubmatch(name); len(match) == 2 {
			if err := appendArtifactAction(legacyRoot, targetRoot, name, name, seen, actions); err != nil {
				return err
			}
			continue
		}
		subtitlePattern := regexp.MustCompile(`^` + regexp.QuoteMeta(legacyBase) + `\.([1-9][0-9]*)\.([^.]+)$`)
		match := subtitlePattern.FindStringSubmatch(name)
		if len(match) != 3 {
			continue
		}
		subtitleIndex, parseErr := strconv.Atoi(match[1])
		if parseErr != nil {
			return &ArtifactMigrationError{Path: filepath.Join(legacyRoot, name), Reason: parseErr.Error()}
		}
		targetBase := code
		if partIndex > 1 {
			targetBase = fmt.Sprintf("%s-cd%d", code, partIndex)
		}
		targetName := fmt.Sprintf("%s.%d.%s", targetBase, subtitleIndex, match[2])
		if err := appendArtifactAction(legacyRoot, targetRoot, name, targetName, seen, actions); err != nil {
			return err
		}
	}
	return nil
}

func appendArtifactAction(legacyRoot, targetRoot, sourceName, targetName string, seen map[string]bool, actions *[]artifactAction) error {
	sourcePath, err := safeArtifactPath(legacyRoot, sourceName)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(sourcePath); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return &ArtifactMigrationError{Path: sourcePath, Reason: err.Error()}
	}
	targetPath, err := safeArtifactPath(targetRoot, targetName)
	if err != nil {
		return err
	}
	key := sourcePath + "\x00" + targetPath
	if !seen[key] {
		seen[key] = true
		*actions = append(*actions, artifactAction{SourcePath: sourcePath, TargetPath: targetPath})
	}
	return nil
}

func processArtifact(action artifactAction, write bool) (copied, existing bool, err error) {
	sourceInfo, err := os.Lstat(action.SourcePath)
	if err != nil {
		return false, false, &ArtifactMigrationError{Path: action.SourcePath, Reason: err.Error()}
	}
	if !sourceInfo.Mode().IsRegular() {
		return false, false, &ArtifactMigrationError{Path: action.SourcePath, Reason: "source is not a regular file"}
	}
	content, err := os.ReadFile(action.SourcePath)
	if err != nil {
		return false, false, &ArtifactMigrationError{Path: action.SourcePath, Reason: err.Error()}
	}
	if targetInfo, statErr := os.Lstat(action.TargetPath); statErr == nil {
		if !targetInfo.Mode().IsRegular() {
			return false, false, &ArtifactMigrationError{Path: action.TargetPath, Reason: "target is not a regular file"}
		}
		targetContent, readErr := os.ReadFile(action.TargetPath)
		if readErr != nil {
			return false, false, &ArtifactMigrationError{Path: action.TargetPath, Reason: readErr.Error()}
		}
		if !equalArtifactBytes(content, targetContent) {
			return false, false, &ArtifactCollisionError{SourcePath: action.SourcePath, TargetPath: action.TargetPath}
		}
		return false, true, nil
	} else if !os.IsNotExist(statErr) {
		return false, false, &ArtifactMigrationError{Path: action.TargetPath, Reason: statErr.Error()}
	}
	if !write {
		return false, false, nil
	}
	if err := os.MkdirAll(filepath.Dir(action.TargetPath), 0o755); err != nil {
		return false, false, &ArtifactMigrationError{Path: filepath.Dir(action.TargetPath), Reason: err.Error()}
	}
	temporary, err := os.CreateTemp(filepath.Dir(action.TargetPath), ".media-migration-artifact-*")
	if err != nil {
		return false, false, &ArtifactMigrationError{Path: action.TargetPath, Reason: err.Error()}
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return false, false, &ArtifactMigrationError{Path: action.TargetPath, Reason: err.Error()}
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return false, false, &ArtifactMigrationError{Path: action.TargetPath, Reason: err.Error()}
	}
	if err := temporary.Close(); err != nil {
		return false, false, &ArtifactMigrationError{Path: action.TargetPath, Reason: err.Error()}
	}
	if err := os.Rename(temporaryPath, action.TargetPath); err != nil {
		return false, false, &ArtifactMigrationError{Path: action.TargetPath, Reason: err.Error()}
	}
	verified, err := os.ReadFile(action.TargetPath)
	if err != nil || !equalArtifactBytes(content, verified) {
		if err == nil {
			err = errors.New("post-copy bytes differ from source")
		}
		return false, false, &ArtifactMigrationError{Path: action.TargetPath, Reason: err.Error()}
	}
	return true, false, nil
}

func loadArtifactJournal(path, dataDir string) (artifactJournal, error) {
	if err := ensurePathWithin(dataDir, path); err != nil {
		return artifactJournal{}, err
	}
	if err := rejectSymlinkAncestors(dataDir, path); err != nil {
		return artifactJournal{}, err
	}
	content, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return artifactJournal{Version: 1}, nil
	}
	if err != nil {
		return artifactJournal{}, &ArtifactMigrationError{Path: path, Reason: err.Error()}
	}
	var journal artifactJournal
	if err := json.Unmarshal(content, &journal); err != nil {
		return artifactJournal{}, &ArtifactMigrationError{Path: path, Reason: err.Error()}
	}
	return journal, nil
}

func writeArtifactJournal(path, dataDir string, journal artifactJournal) error {
	if err := ensurePathWithin(dataDir, path); err != nil {
		return err
	}
	if err := rejectSymlinkAncestors(dataDir, path); err != nil {
		return err
	}
	journal.Version = 1
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

func journalEntry(journal *artifactJournal, action artifactAction) *artifactJournalEntry {
	for index := range journal.Entries {
		entry := &journal.Entries[index]
		if entry.SourcePath == action.SourcePath && entry.TargetPath == action.TargetPath {
			return entry
		}
	}
	journal.Entries = append(journal.Entries, artifactJournalEntry{SourcePath: action.SourcePath, TargetPath: action.TargetPath})
	return &journal.Entries[len(journal.Entries)-1]
}

func safeArtifactPath(root string, components ...string) (string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", &ArtifactMigrationError{Path: root, Reason: err.Error()}
	}
	current := root
	for _, component := range components {
		if component == "" || component == "." || component == ".." || filepath.IsAbs(component) || strings.ContainsAny(component, `/\`) || strings.ContainsRune(component, 0) {
			return "", &ArtifactSafetyError{Path: filepath.Join(current, component), Reason: ErrArtifactSafety.Error()}
		}
		current = filepath.Join(current, component)
	}
	if err := ensurePathWithin(root, current); err != nil {
		return "", err
	}
	if err := rejectSymlinkAncestors(root, current); err != nil {
		return "", err
	}
	return current, nil
}

func ensurePathWithin(root, path string) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return &ArtifactMigrationError{Path: root, Reason: err.Error()}
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return &ArtifactMigrationError{Path: path, Reason: err.Error()}
	}
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return &ArtifactSafetyError{Path: path, Reason: ErrArtifactSafety.Error()}
	}
	return nil
}

func rejectSymlinkAncestors(root, path string) error {
	root, _ = filepath.Abs(root)
	path, _ = filepath.Abs(path)
	relative, _ := filepath.Rel(root, path)
	current := root
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		if component == "." || component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return &ArtifactMigrationError{Path: current, Reason: err.Error()}
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return &ArtifactSafetyError{Path: current, Reason: ErrArtifactSafety.Error()}
		}
	}
	return nil
}

func legacyRealNameFor(name string) string {
	base := clearArtifactExtension(name)
	match := legacyRealNamePattern.FindStringSubmatch(base)
	if len(match) == 2 {
		return match[1]
	}
	return base
}

func clearArtifactExtension(name string) string {
	ext := filepath.Ext(name)
	if ext == "" {
		return name
	}
	return strings.TrimSuffix(name, ext)
}

func equalArtifactBytes(left, right []byte) bool {
	leftHash := sha256.Sum256(left)
	rightHash := sha256.Sum256(right)
	return leftHash == rightHash
}

func artifactHash(path string) string {
	content, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}
