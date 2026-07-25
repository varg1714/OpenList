package media

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

type artifactOperationKind string

const (
	artifactMove      artifactOperationKind = "move"
	artifactSymlink   artifactOperationKind = "symlink"
	artifactDelete    artifactOperationKind = "delete"
	artifactRemoveDir artifactOperationKind = "remove_directory"
)

type artifactOperation struct {
	ID         string                `json:"id"`
	Kind       artifactOperationKind `json:"kind"`
	SourcePath string                `json:"source_path,omitempty"`
	TargetPath string                `json:"target_path,omitempty"`
	VerifyPath string                `json:"verify_path,omitempty"`
	LinkTarget string                `json:"link_target,omitempty"`
	SHA256     string                `json:"sha256,omitempty"`
	State      string                `json:"state"`
}

type artifactPlan struct {
	ID             string
	Operations     []artifactOperation
	Existing       int
	ReplaceJournal bool
}

type artifactCandidate struct {
	sourcePath   string
	resolvedPath string
	targetPath   string
	sourceRoot   string
	hash         string
	symlink      bool
	exactRoot    bool
}

type cleanupCandidate struct {
	source artifactCandidate
	verify string
	hash   string
}

type artifactPlanBuilder struct {
	dataDir       string
	required      map[string][]artifactCandidate
	cleanup       []cleanupCandidate
	roots         map[string]struct{}
	targetRoots   map[string]struct{}
	recognized    map[string]struct{}
	candidateSeen map[string]struct{}
}

func inspectArtifacts(plan *migrationPlan, options MigrationOptions, report *MigrationReport) (*artifactPlan, error) {
	journal, exists, err := loadArtifactJournal(options.JournalPath, options.DataDir)
	if err != nil {
		return nil, err
	}
	planID := artifactIdentityPlanID(plan)
	var artifacts *artifactPlan
	if exists {
		if journal.PlanID != planID {
			return nil, &ArtifactMigrationError{Path: options.JournalPath, Reason: "journal plan ID does not match the current database plan"}
		}
		if artifactJournalComplete(journal) {
			artifacts, err = collectArtifactPlan(plan, options.DataDir)
			if err != nil {
				return nil, err
			}
			if len(artifacts.Operations) > 0 {
				artifacts.ID = planID
				artifacts.ReplaceJournal = true
				populateArtifactReport(report, artifacts)
				return artifacts, nil
			}
		}
		artifacts = &artifactPlan{ID: journal.PlanID, Operations: append([]artifactOperation(nil), journal.Operations...)}
		if err := preflightJournalOperations(artifacts, options.DataDir); err != nil {
			return nil, err
		}
	} else {
		artifacts, err = collectArtifactPlan(plan, options.DataDir)
		if err != nil {
			return nil, err
		}
		artifacts.ID = planID
	}
	populateArtifactReport(report, artifacts)
	return artifacts, nil
}

func artifactIdentityPlanID(plan *migrationPlan) string {
	values := make([]string, 0, len(plan.works)*2)
	for _, work := range plan.works {
		values = append(values, work.identity.String()+"/"+work.work.PrimaryDir)
		for _, source := range work.artifactSources {
			values = append(values, fmt.Sprintf("%s/%d/%s", work.identity.String(), source.partIndex, source.name))
		}
	}
	slices.Sort(values)
	return stableHash(values...)
}

func populateArtifactReport(report *MigrationReport, plan *artifactPlan) {
	report.ArtifactsExisting = plan.Existing
	for _, operation := range plan.Operations {
		switch operation.Kind {
		case artifactMove, artifactSymlink:
			report.ArtifactMovesPlanned++
		case artifactDelete:
			report.ArtifactDeletesPlanned++
		case artifactRemoveDir:
			report.ArtifactDirectoriesPlanned++
		}
	}
	report.ArtifactsPlanned = report.ArtifactMovesPlanned + report.ArtifactDeletesPlanned + report.ArtifactDirectoriesPlanned
}

func collectArtifactPlan(plan *migrationPlan, dataDir string) (*artifactPlan, error) {
	builder := &artifactPlanBuilder{
		dataDir: dataDir, required: make(map[string][]artifactCandidate), roots: make(map[string]struct{}),
		targetRoots: make(map[string]struct{}), recognized: make(map[string]struct{}), candidateSeen: make(map[string]struct{}),
	}
	for _, work := range plan.works {
		if err := builder.collectWork(work); err != nil {
			return nil, err
		}
	}
	return builder.build()
}

func (builder *artifactPlanBuilder) collectWork(work *plannedWork) error {
	targetRoot, err := safeArtifactPath(builder.dataDir, "emby", work.identity.Source, work.work.PrimaryDir, work.identity.Code)
	if err != nil {
		return err
	}
	builder.targetRoots[targetRoot] = struct{}{}
	artifactSources, err := builder.artifactSourcesFor(work)
	if err != nil {
		return err
	}

	exactPart := make(map[int]bool)
	for _, source := range artifactSources {
		if legacyRealNameFor(source.name) == work.identity.Code {
			exactPart[source.partIndex] = true
		}
	}
	for _, source := range artifactSources {
		legacyBase := clearArtifactExtension(source.name)
		legacyRoot, err := safeArtifactPath(builder.dataDir, "emby", work.identity.Source, work.work.PrimaryDir, legacyRealNameFor(source.name))
		if err != nil {
			return err
		}
		builder.roots[legacyRoot] = struct{}{}
		exact := legacyRealNameFor(source.name) == work.identity.Code
		authoritative := source.partIndex == 1 && (!exactPart[source.partIndex] || exact)
		workFiles := [][2]string{{"poster.jpg", "poster.jpg"}, {legacyBase + ".jpg", work.identity.Code + ".jpg"}, {legacyBase + "-background.jpg", work.identity.Code + "-background.jpg"}, {legacyBase + ".nfo", work.identity.Code + ".nfo"}}
		for _, names := range workFiles {
			if err := builder.addLegacyCandidate(legacyRoot, targetRoot, names[0], names[1], authoritative, exact); err != nil {
				return err
			}
		}
		if err := builder.collectLegacyDirectory(legacyRoot, targetRoot, legacyBase, work.identity.Code, source.partIndex, authoritative, exact); err != nil {
			return err
		}
	}

	storageRoot, err := safeArtifactPath(builder.dataDir, "emby", work.identity.Source, strconv.FormatUint(uint64(work.identity.StorageID), 10), work.work.PrimaryDir, work.identity.Code)
	if err != nil {
		return err
	}
	if err := builder.collectStorageScopedRoot(storageRoot, targetRoot, work.identity.Code); err != nil {
		return err
	}
	return nil
}

func (builder *artifactPlanBuilder) addLegacyCandidate(sourceRoot, targetRoot, sourceName, targetName string, required, exact bool) error {
	candidate, exists, err := builder.candidate(sourceRoot, targetRoot, sourceName, targetName, exact)
	if err != nil || !exists {
		return err
	}
	if required {
		builder.required[candidate.targetPath] = append(builder.required[candidate.targetPath], candidate)
	} else {
		builder.cleanup = append(builder.cleanup, cleanupCandidate{source: candidate, verify: candidate.targetPath})
	}
	return nil
}

func (builder *artifactPlanBuilder) collectLegacyDirectory(root, targetRoot, base, code string, part int, authoritative, exact bool) error {
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return &ArtifactMigrationError{Path: root, Reason: err.Error()}
	}
	subtitlePrefix := base + "."
	for _, entry := range entries {
		name := entry.Name()
		if fanartNamePattern.MatchString(name) {
			if err := builder.addLegacyCandidate(root, targetRoot, name, name, authoritative, exact); err != nil {
				return err
			}
			continue
		}
		if !strings.HasPrefix(name, subtitlePrefix) {
			continue
		}
		remainder := strings.TrimPrefix(name, subtitlePrefix)
		pieces := strings.SplitN(remainder, ".", 2)
		if len(pieces) != 2 {
			continue
		}
		index, parseErr := strconv.Atoi(pieces[0])
		if parseErr != nil || index < 1 || pieces[1] == "" || strings.ContainsAny(pieces[1], `/\`) {
			continue
		}
		targetBase := code
		if part > 1 {
			targetBase = fmt.Sprintf("%s-cd%d", code, part)
		}
		candidate, exists, err := builder.candidate(root, targetRoot, name, fmt.Sprintf("%s.%d.%s", targetBase, index, pieces[1]), exact)
		if err != nil {
			return err
		}
		if exists {
			builder.required[candidate.targetPath] = append(builder.required[candidate.targetPath], candidate)
		}
	}
	return nil
}

func (builder *artifactPlanBuilder) collectStorageScopedRoot(root, targetRoot, code string) error {
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return &ArtifactMigrationError{Path: root, Reason: err.Error()}
	}
	builder.roots[root] = struct{}{}
	subtitlePattern := regexp.MustCompile(`^` + regexp.QuoteMeta(code) + `(?:-cd[1-9][0-9]*)?\.[1-9][0-9]*\.[^.]+$`)
	for _, entry := range entries {
		name := entry.Name()
		isKnown := name == "poster.jpg" || name == code+".jpg" || name == code+"-background.jpg" || name == code+".nfo" || fanartNamePattern.MatchString(name)
		isKnown = isKnown || subtitlePattern.MatchString(name)
		if !isKnown {
			continue
		}
		candidate, exists, err := builder.candidate(root, targetRoot, name, name, true)
		if err != nil {
			return err
		}
		if exists {
			builder.required[candidate.targetPath] = append(builder.required[candidate.targetPath], candidate)
		}
	}
	return nil
}

func (builder *artifactPlanBuilder) candidate(sourceRoot, targetRoot, sourceName, targetName string, exact bool) (artifactCandidate, bool, error) {
	sourcePath := filepath.Join(sourceRoot, sourceName)
	if _, duplicate := builder.candidateSeen[sourcePath+"\x00"+filepath.Join(targetRoot, targetName)]; duplicate {
		return artifactCandidate{}, false, nil
	}
	resolved, err := safeArtifactSourcePath(sourceRoot, sourceName)
	if err != nil {
		return artifactCandidate{}, false, err
	}
	info, err := os.Lstat(sourcePath)
	if os.IsNotExist(err) {
		return artifactCandidate{}, false, nil
	}
	if err != nil {
		return artifactCandidate{}, false, &ArtifactMigrationError{Path: sourcePath, Reason: err.Error()}
	}
	if !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
		return artifactCandidate{}, false, &ArtifactMigrationError{Path: sourcePath, Reason: "source is not a regular file or allowed leaf symlink"}
	}
	if info.Mode().IsRegular() {
		resolved, err = filepath.EvalSymlinks(sourcePath)
		if err != nil {
			return artifactCandidate{}, false, &ArtifactMigrationError{Path: sourcePath, Reason: err.Error()}
		}
	}
	hash, err := fileHash(resolved)
	if err != nil {
		return artifactCandidate{}, false, &ArtifactMigrationError{Path: resolved, Reason: err.Error()}
	}
	targetPath, err := safeArtifactTargetPath(targetRoot, targetName)
	if err != nil {
		return artifactCandidate{}, false, err
	}
	builder.recognized[sourcePath] = struct{}{}
	builder.candidateSeen[sourcePath+"\x00"+targetPath] = struct{}{}
	return artifactCandidate{sourcePath: sourcePath, resolvedPath: resolved, targetPath: targetPath, sourceRoot: sourceRoot, hash: hash, symlink: info.Mode()&os.ModeSymlink != 0, exactRoot: exact}, true, nil
}

func (builder *artifactPlanBuilder) build() (*artifactPlan, error) {
	result := &artifactPlan{}
	targets := make([]string, 0, len(builder.required))
	for target := range builder.required {
		targets = append(targets, target)
	}
	slices.Sort(targets)
	selectedTargets := make(map[string]string)
	selectedRegularTargets := make(map[string]string)
	selectedSources := make(map[string]struct{})
	for _, target := range targets {
		candidates := builder.required[target]
		slices.SortFunc(candidates, func(left, right artifactCandidate) int {
			if left.exactRoot != right.exactRoot {
				if left.exactRoot {
					return -1
				}
				return 1
			}
			return strings.Compare(left.sourcePath, right.sourcePath)
		})
		selected := candidates[0]
		for _, candidate := range candidates[1:] {
			if candidate.hash != selected.hash {
				return nil, &ArtifactCollisionError{SourcePath: candidate.sourcePath, TargetPath: target}
			}
			builder.cleanup = append(builder.cleanup, cleanupCandidate{source: candidate, verify: target, hash: selected.hash})
		}
		selectedTargets[selected.sourcePath] = target
		if !selected.symlink {
			selectedRegularTargets[selected.sourcePath] = target
			selectedRegularTargets[selected.resolvedPath] = target
		}
		selectedSources[selected.sourcePath] = struct{}{}
		targetHash, targetExists, err := regularArtifactHash(target)
		if err != nil {
			return nil, err
		}
		if targetExists {
			if targetHash != selected.hash {
				return nil, &ArtifactCollisionError{SourcePath: selected.sourcePath, TargetPath: target}
			}
			result.Existing++
			if selected.sourcePath != target {
				builder.cleanup = append(builder.cleanup, cleanupCandidate{source: selected, verify: target, hash: selected.hash})
			}
			continue
		}
		if selected.sourcePath == target {
			return nil, &ArtifactMigrationError{Path: target, Reason: "selected target disappeared during preflight"}
		}
		kind := artifactMove
		if selected.symlink {
			kind = artifactSymlink
		}
		result.Operations = append(result.Operations, artifactOperation{Kind: kind, SourcePath: selected.sourcePath, TargetPath: target, SHA256: selected.hash, State: "pending"})
	}

	for index := range result.Operations {
		operation := &result.Operations[index]
		if operation.Kind != artifactSymlink {
			continue
		}
		var resolved string
		for _, candidates := range builder.required {
			for _, candidate := range candidates {
				if candidate.sourcePath == operation.SourcePath {
					resolved = candidate.resolvedPath
				}
			}
		}
		operation.LinkTarget = selectedRegularTargets[resolved]
		if operation.LinkTarget == "" || operation.LinkTarget == operation.TargetPath {
			return nil, &ArtifactMigrationError{Path: operation.SourcePath, Reason: "internal symlink target is not part of the selected artifact plan"}
		}
	}

	cleanupSeen := make(map[string]struct{})
	moveSources := make(map[string]struct{})
	for _, operation := range result.Operations {
		moveSources[operation.SourcePath] = struct{}{}
	}
	for _, cleanup := range builder.cleanup {
		if _, moved := moveSources[cleanup.source.sourcePath]; moved {
			continue
		}
		if _, selected := selectedSources[cleanup.source.sourcePath]; selected && selectedTargets[cleanup.source.sourcePath] != cleanup.verify {
			continue
		}
		if cleanup.source.sourcePath == cleanup.verify {
			continue
		}
		key := cleanup.source.sourcePath + "\x00" + cleanup.verify
		if _, duplicate := cleanupSeen[key]; duplicate {
			continue
		}
		cleanupSeen[key] = struct{}{}
		hash := cleanup.hash
		if hash == "" {
			for _, candidates := range builder.required {
				for _, candidate := range candidates {
					if candidate.targetPath == cleanup.verify {
						hash = candidate.hash
					}
				}
			}
		}
		if hash == "" {
			continue
		}
		result.Operations = append(result.Operations, artifactOperation{Kind: artifactDelete, SourcePath: cleanup.source.sourcePath, VerifyPath: cleanup.verify, SHA256: hash, State: "pending"})
	}
	removalSources := make(map[string]struct{})
	for _, operation := range result.Operations {
		if operation.Kind == artifactMove || operation.Kind == artifactSymlink || operation.Kind == artifactDelete {
			removalSources[operation.SourcePath] = struct{}{}
		}
	}

	for root := range builder.roots {
		if _, target := builder.targetRoots[root]; target {
			continue
		}
		entries, err := os.ReadDir(root)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, &ArtifactMigrationError{Path: root, Reason: err.Error()}
		}
		removable := true
		for _, entry := range entries {
			if removableArtifactMetadata(entry) {
				continue
			}
			if _, removed := removalSources[filepath.Join(root, entry.Name())]; !removed {
				removable = false
				break
			}
		}
		if removable {
			result.Operations = append(result.Operations, artifactOperation{Kind: artifactRemoveDir, SourcePath: root, State: "pending"})
		}
	}

	slices.SortStableFunc(result.Operations, compareArtifactOperations)
	for index := range result.Operations {
		operation := &result.Operations[index]
		operation.ID = stableHash(string(operation.Kind), operation.SourcePath, operation.TargetPath, operation.VerifyPath, operation.LinkTarget, operation.SHA256)
	}
	return result, nil
}

func regularArtifactHash(path string) (string, bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, &ArtifactMigrationError{Path: path, Reason: err.Error()}
	}
	if !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
		return "", false, &ArtifactMigrationError{Path: path, Reason: "target is not a regular file or allowed leaf symlink"}
	}
	hash, err := fileHash(path)
	if err != nil {
		return "", false, &ArtifactMigrationError{Path: path, Reason: err.Error()}
	}
	return hash, true, nil
}

func compareArtifactOperations(left, right artifactOperation) int {
	priority := func(kind artifactOperationKind) int {
		switch kind {
		case artifactMove:
			return 0
		case artifactSymlink:
			return 1
		case artifactDelete:
			return 2
		default:
			return 3
		}
	}
	if difference := priority(left.Kind) - priority(right.Kind); difference != 0 {
		return difference
	}
	if difference := strings.Compare(left.TargetPath, right.TargetPath); difference != 0 {
		return difference
	}
	return strings.Compare(left.SourcePath, right.SourcePath)
}
