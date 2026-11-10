package _115

import "testing"

func TestOfflineActualFileID(t *testing.T) {
	tests := []struct {
		name      string
		fileID    string
		delFileID string
		want      string
	}{
		{
			name:      "ed2k task exposes the concrete file",
			fileID:    "3137916453630508845",
			delFileID: "3559332724521043416",
			want:      "3559332724521043416",
		},
		{
			name:      "torrent task has no separate file",
			fileID:    "3559332724521043416",
			delFileID: "3559332724521043416",
			want:      "",
		},
		{
			name:      "empty delete file id",
			fileID:    "3559332724521043416",
			delFileID: "",
			want:      "",
		},
		{
			name:      "both empty",
			fileID:    "",
			delFileID: "",
			want:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := offlineActualFileID(tt.fileID, tt.delFileID); got != tt.want {
				t.Fatalf("offlineActualFileID(%q, %q) = %q, want %q", tt.fileID, tt.delFileID, got, tt.want)
			}
		})
	}
}
