package media

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

func (builder *artifactPlanBuilder) artifactSourcesFor(work *plannedWork) ([]plannedArtifactSource, error) {
	sources := append([]plannedArtifactSource(nil), work.artifactSources...)
	if work.identity.Source != "javdb" {
		return sources, nil
	}
	parent, err := safeArtifactPath(builder.dataDir, "emby", work.identity.Source, work.work.PrimaryDir)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(parent)
	if os.IsNotExist(err) {
		return sources, nil
	}
	if err != nil {
		return nil, &ArtifactMigrationError{Path: parent, Reason: err.Error()}
	}
	matches := make([]plannedArtifactSource, 0)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		fields := strings.Fields(name)
		if len(fields) == 0 {
			continue
		}
		code, normalizeErr := model.NormalizeMediaCode("javdb", fields[0])
		if normalizeErr == nil && code == work.identity.Code {
			matches = append(matches, plannedArtifactSource{name: name, directoryName: name, partIndex: 1})
		}
	}

	resolved := make([]plannedArtifactSource, 0, len(sources)+len(matches))
	replacedLongName := false
	known := make(map[string]struct{}, len(sources)+len(matches))
	for _, source := range sources {
		rootName := source.legacyRootName()
		root := filepath.Join(parent, rootName)
		if _, statErr := os.Lstat(root); errors.Is(statErr, syscall.ENAMETOOLONG) {
			if len(matches) == 0 {
				return nil, &ArtifactMigrationError{Path: root, Reason: statErr.Error()}
			}
			replacedLongName = true
			continue
		}
		resolved = append(resolved, source)
		known[rootName] = struct{}{}
	}
	for _, match := range matches {
		if match.name == work.identity.Code && !replacedLongName {
			continue
		}
		if _, exists := known[match.name]; exists {
			continue
		}
		resolved = append(resolved, match)
		known[match.name] = struct{}{}
	}
	return resolved, nil
}

func (source plannedArtifactSource) legacyRootName() string {
	if source.directoryName != "" {
		return source.directoryName
	}
	return legacyRealNameFor(source.name)
}

func (source plannedArtifactSource) legacyBaseName() string {
	if source.directoryName != "" {
		return source.directoryName
	}
	return clearArtifactExtension(source.name)
}

func removableArtifactMetadata(entry os.DirEntry) bool {
	return entry.Name() == ".DS_Store" && entry.Type().IsRegular()
}
