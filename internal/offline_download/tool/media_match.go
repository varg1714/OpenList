package tool

import (
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

var (
	ErrManifestFileNotMatched  = errors.New("manifest file not matched")
	ErrManifestFileAmbiguous   = errors.New("manifest file match is ambiguous")
	ErrRemoteFileNotMatched    = errors.New("remote file not matched")
	ErrRemoteFileAmbiguous     = errors.New("remote file match is ambiguous")
	ErrInvalidFileEvidencePath = errors.New("invalid file evidence path")
)

type RemoteFileEvidence struct {
	ID          string
	Path        string
	Size        int64
	Fingerprint string
	Options     map[string]string
}

type RemoteFileMatchError struct {
	FilmFileID   uint
	Path         string
	CandidateIDs []string
	Cause        error
}

func (e *RemoteFileMatchError) Error() string {
	if len(e.CandidateIDs) == 0 {
		return fmt.Sprintf("match film file %d at %q: %v", e.FilmFileID, e.Path, e.Cause)
	}
	return fmt.Sprintf("match film file %d at %q: %v: candidates %v", e.FilmFileID, e.Path, e.Cause, e.CandidateIDs)
}

func (e *RemoteFileMatchError) Unwrap() error {
	return e.Cause
}

type normalizedManifestEntry struct {
	entry model.MagnetFileEntry
	path  string
}

type normalizedRemoteFile struct {
	evidence RemoteFileEvidence
	path     string
	index    int
}

func MatchRemoteMediaFiles(files []model.FilmFile, manifest model.MagnetFileManifest, remotes []RemoteFileEvidence) (map[uint]RemoteFileEvidence, error) {
	if len(manifest) == 0 {
		if len(files) == 1 && len(remotes) == 1 {
			return map[uint]RemoteFileEvidence{files[0].ID: remotes[0]}, nil
		}
		return nil, &RemoteFileMatchError{Cause: ErrManifestFileNotMatched}
	}

	normalizedManifest, err := normalizeManifest(manifest)
	if err != nil {
		return nil, err
	}
	normalizedRemotes, err := normalizeRemoteFiles(remotes)
	if err != nil {
		return nil, err
	}

	orderedFiles := slices.Clone(files)
	slices.SortFunc(orderedFiles, func(a, b model.FilmFile) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return strings.Compare(a.SourcePath, b.SourcePath)
	})

	matches := make(map[uint]RemoteFileEvidence, len(files))
	usedRemotes := make(map[int]struct{}, len(files))
	for _, file := range orderedFiles {
		manifestEntry, err := matchManifestEntry(file, normalizedManifest)
		if err != nil {
			return nil, err
		}

		remote, err := matchRemoteFile(file.ID, manifestEntry, normalizedRemotes, usedRemotes)
		if err != nil {
			return nil, err
		}
		usedRemotes[remote.index] = struct{}{}
		matches[file.ID] = remote.evidence
	}

	return matches, nil
}

func normalizeManifest(manifest model.MagnetFileManifest) ([]normalizedManifestEntry, error) {
	normalized := make([]normalizedManifestEntry, 0, len(manifest))
	for _, entry := range manifest {
		normalizedPath, err := normalizeEvidencePath(entry.Path)
		if err != nil {
			return nil, &RemoteFileMatchError{Path: entry.Path, Cause: err}
		}
		normalized = append(normalized, normalizedManifestEntry{entry: entry, path: normalizedPath})
	}
	return normalized, nil
}

func normalizeRemoteFiles(remotes []RemoteFileEvidence) ([]normalizedRemoteFile, error) {
	normalized := make([]normalizedRemoteFile, 0, len(remotes))
	for i, remote := range remotes {
		normalizedPath, err := normalizeEvidencePath(remote.Path)
		if err != nil {
			return nil, &RemoteFileMatchError{Path: remote.Path, CandidateIDs: []string{remote.ID}, Cause: err}
		}
		normalized = append(normalized, normalizedRemoteFile{evidence: remote, path: normalizedPath, index: i})
	}
	return normalized, nil
}

func matchManifestEntry(file model.FilmFile, manifest []normalizedManifestEntry) (normalizedManifestEntry, error) {
	filePath, err := normalizeEvidencePath(file.SourcePath)
	if err != nil {
		return normalizedManifestEntry{}, &RemoteFileMatchError{FilmFileID: file.ID, Path: file.SourcePath, Cause: err}
	}

	candidates := manifestCandidates(filePath, file.SourceSize, file.SourceFileFingerprint, manifest, true)
	if len(candidates) == 0 {
		candidates = manifestCandidates(filePath, file.SourceSize, file.SourceFileFingerprint, manifest, false)
	}
	if len(candidates) == 1 {
		return candidates[0], nil
	}

	var candidatePaths []string
	if len(candidates) > 0 {
		candidatePaths = make([]string, 0, len(candidates))
		for _, candidate := range candidates {
			candidatePaths = append(candidatePaths, candidate.path)
		}
		slices.Sort(candidatePaths)
	}
	cause := ErrManifestFileNotMatched
	if len(candidates) > 1 {
		cause = ErrManifestFileAmbiguous
	}
	return normalizedManifestEntry{}, &RemoteFileMatchError{
		FilmFileID:   file.ID,
		Path:         filePath,
		CandidateIDs: candidatePaths,
		Cause:        cause,
	}
}

func manifestCandidates(filePath string, size int64, fingerprint string, manifest []normalizedManifestEntry, fullPath bool) []normalizedManifestEntry {
	candidates := make([]normalizedManifestEntry, 0, 1)
	for _, entry := range manifest {
		pathMatches := entry.path == filePath
		if !fullPath {
			pathMatches = path.Base(entry.path) == path.Base(filePath)
		}
		if pathMatches && entry.entry.Size == size && fingerprintsMatch(fingerprint, entry.entry.Fingerprint) {
			candidates = append(candidates, entry)
		}
	}
	return candidates
}

func matchRemoteFile(fileID uint, manifest normalizedManifestEntry, remotes []normalizedRemoteFile, used map[int]struct{}) (normalizedRemoteFile, error) {
	candidates := remoteCandidates(manifest, remotes, used, true)
	if len(candidates) == 0 {
		candidates = remoteCandidates(manifest, remotes, used, false)
	}
	if len(candidates) == 1 {
		return candidates[0], nil
	}

	var candidateIDs []string
	if len(candidates) > 0 {
		candidateIDs = make([]string, 0, len(candidates))
		for _, candidate := range candidates {
			candidateIDs = append(candidateIDs, candidate.evidence.ID)
		}
		slices.Sort(candidateIDs)
	}
	cause := ErrRemoteFileNotMatched
	if len(candidates) > 1 {
		cause = ErrRemoteFileAmbiguous
	}
	return normalizedRemoteFile{}, &RemoteFileMatchError{
		FilmFileID:   fileID,
		Path:         manifest.path,
		CandidateIDs: candidateIDs,
		Cause:        cause,
	}
}

func remoteCandidates(manifest normalizedManifestEntry, remotes []normalizedRemoteFile, used map[int]struct{}, fullPath bool) []normalizedRemoteFile {
	candidates := make([]normalizedRemoteFile, 0, 1)
	for _, remote := range remotes {
		if _, ok := used[remote.index]; ok {
			continue
		}
		pathMatches := remote.path == manifest.path
		if !fullPath {
			pathMatches = path.Base(remote.path) == path.Base(manifest.path)
		}
		if pathMatches && remote.evidence.Size == manifest.entry.Size && fingerprintsMatch(manifest.entry.Fingerprint, remote.evidence.Fingerprint) {
			candidates = append(candidates, remote)
		}
	}
	return candidates
}

func fingerprintsMatch(a, b string) bool {
	return a == "" || b == "" || a == b
}

func normalizeEvidencePath(value string) (string, error) {
	value = strings.ReplaceAll(strings.TrimSpace(value), `\`, "/")
	normalized := path.Clean(value)
	if value == "" || normalized == "." || normalized == ".." || strings.HasPrefix(normalized, "../") {
		return "", fmt.Errorf("%w: %q", ErrInvalidFileEvidencePath, value)
	}
	return normalized, nil
}
