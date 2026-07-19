package tool

import (
	"errors"
	"reflect"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

func TestMatchRemoteMediaFilesExactMatch(t *testing.T) {
	files := []model.FilmFile{
		{ID: 11, SourcePath: "disc-a/movie.mp4", SourceSize: 100, SourceFileFingerprint: "file-a"},
		{ID: 12, SourcePath: "disc-b/movie.mp4", SourceSize: 200, SourceFileFingerprint: "file-b"},
	}
	manifest := model.MagnetFileManifest{
		{Path: "disc-a/movie.mp4", Size: 100, Fingerprint: "file-a"},
		{Path: "disc-b/movie.mp4", Size: 200, Fingerprint: "file-b"},
	}
	remotes := []RemoteFileEvidence{
		{ID: "remote-b", Path: "disc-b/movie.mp4", Size: 200, Fingerprint: "file-b"},
		{ID: "remote-a", Path: "disc-a/movie.mp4", Size: 100, Fingerprint: "file-a", Options: map[string]string{"pickCode": "pick-a"}},
	}

	got, err := MatchRemoteMediaFiles(files, manifest, remotes)
	if err != nil {
		t.Fatalf("match remote media files: %v", err)
	}
	if !reflect.DeepEqual(got, map[uint]RemoteFileEvidence{11: remotes[1], 12: remotes[0]}) {
		t.Fatalf("matches = %#v, want exact path matches", got)
	}
}

func TestMatchRemoteMediaFilesNormalizesPathSeparators(t *testing.T) {
	files := []model.FilmFile{{ID: 21, SourcePath: `.\season\movie.mp4`, SourceSize: 100}}
	manifest := model.MagnetFileManifest{{Path: "season/movie.mp4", Size: 100}}
	remote := RemoteFileEvidence{ID: "remote", Path: `season\movie.mp4`, Size: 100}

	got, err := MatchRemoteMediaFiles(files, manifest, []RemoteFileEvidence{remote})
	if err != nil {
		t.Fatalf("match normalized paths: %v", err)
	}
	if !reflect.DeepEqual(got, map[uint]RemoteFileEvidence{21: remote}) {
		t.Fatalf("matches = %#v, want normalized path match", got)
	}
}

func TestMatchRemoteMediaFilesRejectsSizeMismatch(t *testing.T) {
	files := []model.FilmFile{{ID: 31, SourcePath: "movie.mp4", SourceSize: 100}}
	manifest := model.MagnetFileManifest{{Path: "movie.mp4", Size: 100}}
	remotes := []RemoteFileEvidence{{ID: "wrong-size", Path: "movie.mp4", Size: 101}}

	_, err := MatchRemoteMediaFiles(files, manifest, remotes)
	assertRemoteMatchError(t, err, ErrRemoteFileNotMatched, 31, nil)
}

func TestMatchRemoteMediaFilesRejectsFingerprintMismatchWhenBothExist(t *testing.T) {
	files := []model.FilmFile{{ID: 41, SourcePath: "movie.mp4", SourceSize: 100, SourceFileFingerprint: "expected"}}
	manifest := model.MagnetFileManifest{{Path: "movie.mp4", Size: 100, Fingerprint: "expected"}}
	remotes := []RemoteFileEvidence{{ID: "wrong-fingerprint", Path: "movie.mp4", Size: 100, Fingerprint: "different"}}

	_, err := MatchRemoteMediaFiles(files, manifest, remotes)
	assertRemoteMatchError(t, err, ErrRemoteFileNotMatched, 41, nil)
}

func TestMatchRemoteMediaFilesAllowsMissingRemoteFingerprint(t *testing.T) {
	files := []model.FilmFile{{ID: 42, SourcePath: "movie.mp4", SourceSize: 100, SourceFileFingerprint: "expected"}}
	manifest := model.MagnetFileManifest{{Path: "movie.mp4", Size: 100, Fingerprint: "expected"}}
	remote := RemoteFileEvidence{ID: "without-fingerprint", Path: "movie.mp4", Size: 100}

	got, err := MatchRemoteMediaFiles(files, manifest, []RemoteFileEvidence{remote})
	if err != nil {
		t.Fatalf("match without optional remote fingerprint: %v", err)
	}
	if !reflect.DeepEqual(got, map[uint]RemoteFileEvidence{42: remote}) {
		t.Fatalf("matches = %#v, want optional fingerprint match", got)
	}
}

func TestMatchRemoteMediaFilesRejectsDuplicateCandidatesDeterministically(t *testing.T) {
	files := []model.FilmFile{{ID: 51, SourcePath: "movie.mp4", SourceSize: 100}}
	manifest := model.MagnetFileManifest{{Path: "movie.mp4", Size: 100}}
	remotes := []RemoteFileEvidence{
		{ID: "remote-z", Path: "movie.mp4", Size: 100},
		{ID: "remote-a", Path: "movie.mp4", Size: 100},
	}

	_, err := MatchRemoteMediaFiles(files, manifest, remotes)
	assertRemoteMatchError(t, err, ErrRemoteFileAmbiguous, 51, []string{"remote-a", "remote-z"})
}

func TestMatchRemoteMediaFilesPrefersFullPathOverBasename(t *testing.T) {
	files := []model.FilmFile{{ID: 61, SourcePath: "disc-a/movie.mp4", SourceSize: 100}}
	manifest := model.MagnetFileManifest{{Path: "disc-a/movie.mp4", Size: 100}}
	want := RemoteFileEvidence{ID: "exact", Path: "disc-a/movie.mp4", Size: 100}
	remotes := []RemoteFileEvidence{
		{ID: "same-basename", Path: "other/movie.mp4", Size: 100},
		want,
	}

	got, err := MatchRemoteMediaFiles(files, manifest, remotes)
	if err != nil {
		t.Fatalf("match full path before basename: %v", err)
	}
	if !reflect.DeepEqual(got, map[uint]RemoteFileEvidence{61: want}) {
		t.Fatalf("matches = %#v, want full path candidate", got)
	}
}

func TestMatchRemoteMediaFilesRejectsAmbiguousBasenames(t *testing.T) {
	files := []model.FilmFile{{ID: 71, SourcePath: "source/movie.mp4", SourceSize: 100}}
	manifest := model.MagnetFileManifest{{Path: "source/movie.mp4", Size: 100}}
	remotes := []RemoteFileEvidence{
		{ID: "remote-b", Path: "download-b/movie.mp4", Size: 100},
		{ID: "remote-a", Path: "download-a/movie.mp4", Size: 100},
	}

	_, err := MatchRemoteMediaFiles(files, manifest, remotes)
	assertRemoteMatchError(t, err, ErrRemoteFileAmbiguous, 71, []string{"remote-a", "remote-b"})
}

func TestMatchRemoteMediaFilesRejectsIncompleteMultipart(t *testing.T) {
	files := []model.FilmFile{
		{ID: 81, SourcePath: "part-1.mp4", SourceSize: 100},
		{ID: 82, SourcePath: "part-2.mp4", SourceSize: 200},
	}
	manifest := model.MagnetFileManifest{
		{Path: "part-1.mp4", Size: 100},
		{Path: "part-2.mp4", Size: 200},
	}
	remotes := []RemoteFileEvidence{{ID: "remote-1", Path: "part-1.mp4", Size: 100}}

	_, err := MatchRemoteMediaFiles(files, manifest, remotes)
	assertRemoteMatchError(t, err, ErrRemoteFileNotMatched, 82, nil)
}

func TestMatchRemoteMediaFilesAllowsSingleRemoteWithoutManifest(t *testing.T) {
	files := []model.FilmFile{{ID: 91}}
	remote := RemoteFileEvidence{ID: "only-remote", Path: "download/movie.mp4", Size: 100}

	got, err := MatchRemoteMediaFiles(files, nil, []RemoteFileEvidence{remote})
	if err != nil {
		t.Fatalf("match single remote without manifest: %v", err)
	}
	if !reflect.DeepEqual(got, map[uint]RemoteFileEvidence{91: remote}) {
		t.Fatalf("matches = %#v, want single remote fallback", got)
	}
}

func assertRemoteMatchError(t *testing.T, err, target error, fileID uint, candidateIDs []string) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("error = %v, want errors.Is(_, %v)", err, target)
	}
	var matchErr *RemoteFileMatchError
	if !errors.As(err, &matchErr) {
		t.Fatalf("error type = %T, want *RemoteFileMatchError", err)
	}
	if matchErr.FilmFileID != fileID || !reflect.DeepEqual(matchErr.CandidateIDs, candidateIDs) {
		t.Fatalf("match error = %+v, want file ID %d and candidates %v", matchErr, fileID, candidateIDs)
	}
}
