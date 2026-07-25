package media

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	ErrArtifactCollision      = errors.New("media artifact collision")
	ErrArtifactMigration      = errors.New("media artifact migration failed")
	ErrArtifactSafety         = errors.New("media artifact path is unsafe")
	ErrArtifactJournalVersion = errors.New("unsupported media artifact journal version")
	errDanglingArtifactLink   = errors.New("media artifact symlink target does not exist")
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

var (
	legacyRealNamePattern = regexp.MustCompile(`(?i)^(.*?)(?:-cd[0-9]+)?(?:-background)?$`)
	fanartNamePattern     = regexp.MustCompile(`^fanart([1-9][0-9]*)\.jpg$`)
)

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

func safeArtifactSourcePath(root, name string) (string, error) {
	if name == "" || name == "." || name == ".." || filepath.IsAbs(name) || strings.ContainsAny(name, `/\`) || strings.ContainsRune(name, 0) {
		return "", &ArtifactSafetyError{Path: filepath.Join(root, name), Reason: ErrArtifactSafety.Error()}
	}
	sourcePath := filepath.Join(root, name)
	if err := ensurePathWithin(root, sourcePath); err != nil {
		return "", err
	}
	if err := rejectSymlinkAncestors(root, filepath.Dir(sourcePath)); err != nil {
		return "", err
	}
	info, err := os.Lstat(sourcePath)
	if err != nil {
		return sourcePath, nil
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return sourcePath, nil
	}
	resolved, err := filepath.EvalSymlinks(sourcePath)
	if os.IsNotExist(err) {
		return sourcePath, errDanglingArtifactLink
	}
	if err != nil {
		return "", &ArtifactMigrationError{Path: sourcePath, Reason: err.Error()}
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", &ArtifactMigrationError{Path: root, Reason: err.Error()}
	}
	if err := ensurePathWithin(canonicalRoot, resolved); err != nil {
		return "", &ArtifactSafetyError{Path: sourcePath, Reason: ErrArtifactSafety.Error()}
	}
	if err := rejectSymlinkAncestors(canonicalRoot, resolved); err != nil {
		return "", err
	}
	resolvedInfo, err := os.Lstat(resolved)
	if err != nil {
		return "", &ArtifactMigrationError{Path: resolved, Reason: err.Error()}
	}
	if !resolvedInfo.Mode().IsRegular() {
		return "", &ArtifactMigrationError{Path: resolved, Reason: "symlink target is not a regular file"}
	}
	return resolved, nil
}

func danglingArtifactLinkExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, &ArtifactMigrationError{Path: path, Reason: err.Error()}
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return false, &ArtifactMigrationError{Path: path, Reason: "expected a dangling artifact symlink"}
	}
	if _, err := filepath.EvalSymlinks(path); err == nil {
		return false, &ArtifactMigrationError{Path: path, Reason: "artifact symlink target now exists"}
	} else if !os.IsNotExist(err) {
		return false, &ArtifactMigrationError{Path: path, Reason: err.Error()}
	}
	return true, nil
}

func safeArtifactTargetPath(root, name string) (string, error) {
	if name == "" || name == "." || name == ".." || filepath.IsAbs(name) || strings.ContainsAny(name, `/\`) || strings.ContainsRune(name, 0) {
		return "", &ArtifactSafetyError{Path: filepath.Join(root, name), Reason: ErrArtifactSafety.Error()}
	}
	targetPath := filepath.Join(root, name)
	if err := ensurePathWithin(root, targetPath); err != nil {
		return "", err
	}
	if err := rejectSymlinkAncestors(root, filepath.Dir(targetPath)); err != nil {
		return "", err
	}
	info, err := os.Lstat(targetPath)
	if os.IsNotExist(err) {
		return targetPath, nil
	}
	if err != nil {
		return "", &ArtifactMigrationError{Path: targetPath, Reason: err.Error()}
	}
	if info.Mode().IsRegular() {
		return targetPath, nil
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return "", &ArtifactMigrationError{Path: targetPath, Reason: "target is not a regular file or allowed leaf symlink"}
	}
	resolved, err := filepath.EvalSymlinks(targetPath)
	if err != nil {
		return "", &ArtifactMigrationError{Path: targetPath, Reason: err.Error()}
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", &ArtifactMigrationError{Path: root, Reason: err.Error()}
	}
	if err := ensurePathWithin(canonicalRoot, resolved); err != nil {
		return "", &ArtifactSafetyError{Path: targetPath, Reason: ErrArtifactSafety.Error()}
	}
	resolvedInfo, err := os.Lstat(resolved)
	if err != nil {
		return "", &ArtifactMigrationError{Path: resolved, Reason: err.Error()}
	}
	if !resolvedInfo.Mode().IsRegular() {
		return "", &ArtifactMigrationError{Path: resolved, Reason: "symlink target is not a regular file"}
	}
	return targetPath, nil
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

func fileHash(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:]), nil
}

func stableHash(values ...string) string {
	hash := sha256.New()
	for _, value := range values {
		_, _ = hash.Write([]byte(value))
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}
