package tool

import (
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/stretchr/testify/require"
)

func TestEd2kFileSize(t *testing.T) {
	tests := []struct {
		name   string
		magnet string
		want   int64
	}{
		{
			name:   "ed2k uri",
			magnet: "ed2k://|file|www.98T.la@SNOS-426_restored_wm.mp4|7636645320|C053C4F10CC545C651AD8FA69AE8EDF6|/",
			want:   7636645320,
		},
		{
			name:   "ed2k uri with spaces in name",
			magnet: "ed2k://|file|SNOS-426 restored.mp4|7636645320|C053C4F10CC545C651AD8FA69AE8EDF6|/",
			want:   7636645320,
		},
		{
			name:   "magnet uri",
			magnet: "magnet:?xt=urn:btih:0123456789abcdef",
			want:   0,
		},
		{
			name:   "malformed ed2k uri",
			magnet: "ed2k://|file|broken.mp4|not-a-number|HASH|/",
			want:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, ed2kFileSize(tt.magnet))
		})
	}
}

func TestFilterEd2kFileMatchesBySize(t *testing.T) {
	files := []model.Obj{
		&model.Object{ID: "first", Name: "169bbs.com@IPZZ-708_[4K].mkv", Size: 23805351936},
		&model.Object{ID: "second", Name: "www.98T.la@SNOS-426_restored_wm.mp4", Size: 7636645320},
	}
	magnet := "ed2k://|file|www.98T.la@SNOS-426_restored_wm.mp4|7636645320|C053C4F10CC545C651AD8FA69AE8EDF6|/"

	got := filterEd2kFile(magnet, files)

	require.Len(t, got, 1)
	require.Equal(t, "second", got[0].GetID())
}

func TestFilterEd2kFileKeepsAllWhenSizeDoesNotMatch(t *testing.T) {
	files := []model.Obj{
		&model.Object{ID: "first", Name: "first.mp4", Size: 100},
		&model.Object{ID: "second", Name: "second.mp4", Size: 200},
	}
	magnet := "ed2k://|file|missing.mp4|300|HASH|/"

	got := filterEd2kFile(magnet, files)

	require.Len(t, got, 2)
}

func TestFilterEd2kFileKeepsAllForNonEd2kMagnet(t *testing.T) {
	files := []model.Obj{
		&model.Object{ID: "first", Name: "first.mp4", Size: 100},
		&model.Object{ID: "second", Name: "second.mp4", Size: 200},
	}

	got := filterEd2kFile("magnet:?xt=urn:btih:0123456789abcdef", files)

	require.Len(t, got, 2)
}

func TestFilterEd2kFileKeepsAllWhenSizeIsAmbiguous(t *testing.T) {
	files := []model.Obj{
		&model.Object{ID: "first", Name: "first.mp4", Size: 200},
		&model.Object{ID: "second", Name: "second.mp4", Size: 200},
	}
	magnet := "ed2k://|file|first.mp4|200|HASH|/"

	got := filterEd2kFile(magnet, files)

	require.Len(t, got, 2)
}

func TestFilterActualFileMatchesByID(t *testing.T) {
	files := []model.Obj{
		&model.Object{ID: "first", Name: "first.mp4", Size: 100},
		&model.Object{ID: "second", Name: "second.mp4", Size: 200},
	}

	got := filterActualFile("second", files)

	require.Len(t, got, 1)
	require.Equal(t, "second", got[0].GetID())
}

func TestFilterActualFileKeepsAllWhenIDMissing(t *testing.T) {
	files := []model.Obj{
		&model.Object{ID: "first", Name: "first.mp4", Size: 100},
		&model.Object{ID: "second", Name: "second.mp4", Size: 200},
	}

	got := filterActualFile("missing", files)

	require.Len(t, got, 2)
}

func TestFilterActualFileKeepsAllWhenEmpty(t *testing.T) {
	files := []model.Obj{
		&model.Object{ID: "first", Name: "first.mp4", Size: 100},
		&model.Object{ID: "second", Name: "second.mp4", Size: 200},
	}

	got := filterActualFile("", files)

	require.Len(t, got, 2)
}
