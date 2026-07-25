package media

import (
	"os"
	"strings"

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
	known := make(map[string]struct{}, len(sources))
	for _, source := range sources {
		known[legacyRealNameFor(source.name)] = struct{}{}
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || name == work.identity.Code {
			continue
		}
		fields := strings.Fields(name)
		if len(fields) < 2 {
			continue
		}
		code, normalizeErr := model.NormalizeMediaCode("javdb", fields[0])
		if normalizeErr != nil || code != work.identity.Code {
			continue
		}
		if _, exists := known[name]; exists {
			continue
		}
		sources = append(sources, plannedArtifactSource{name: name, partIndex: 1})
		known[name] = struct{}{}
	}
	return sources, nil
}

func removableArtifactMetadata(entry os.DirEntry) bool {
	return entry.Name() == ".DS_Store" && entry.Type().IsRegular()
}
