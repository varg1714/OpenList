package fc2

import (
	"path/filepath"
	"strings"
)

func magnetDisplayTitle(raw string) string {
	title := strings.TrimSpace(raw)
	extension := strings.ToLower(filepath.Ext(title))
	switch extension {
	case ".avi", ".flv", ".m4v", ".mkv", ".mov", ".mp4", ".ts", ".webm", ".wmv":
		return strings.TrimSuffix(title, filepath.Ext(title))
	default:
		return title
	}
}
