package _115

import "testing"

func TestOfflineFileID(t *testing.T) {
	tests := []struct {
		name      string
		fileID    string
		delFileID string
		want      string
	}{
		{
			name:      "ed2k task uses delete file id",
			fileID:    "3137916453630508845",
			delFileID: "3559332724521043416",
			want:      "3559332724521043416",
		},
		{
			name:      "torrent task keeps file id",
			fileID:    "3559332724521043416",
			delFileID: "3559332724521043416",
			want:      "3559332724521043416",
		},
		{
			name:      "empty delete file id falls back",
			fileID:    "3559332724521043416",
			delFileID: "",
			want:      "3559332724521043416",
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
			if got := offlineFileID(tt.fileID, tt.delFileID); got != tt.want {
				t.Fatalf("offlineFileID(%q, %q) = %q, want %q", tt.fileID, tt.delFileID, got, tt.want)
			}
		})
	}
}
