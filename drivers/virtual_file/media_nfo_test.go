package virtual_file

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/cmd/flags"
)

func TestMediaNFOWritesProjectedTitleToCodeOnlyFileName(t *testing.T) {
	previous := flags.DataDir
	flags.DataDir = t.TempDir()
	t.Cleanup(func() { flags.DataDir = previous })

	identity := MediaIdentity{StorageID: 12, Source: "javdb", PrimaryDir: "Actor A", Code: "ABP-123"}
	if err := UpdateMediaNfo(MediaInfo{Identity: &identity, Title: "ABP-123 translated title"}); err != nil {
		t.Fatalf("update media NFO: %v", err)
	}

	path := filepath.Join(flags.DataDir, "emby", "javdb", "12", "Actor A", "ABP-123", "ABP-123.nfo")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read NFO: %v", err)
	}
	var media Media
	if err := xml.Unmarshal(content, &media); err != nil {
		t.Fatalf("parse NFO: %v", err)
	}
	if media.Title.Inner != "<![CDATA[ABP-123 translated title]]>" {
		t.Fatalf("NFO title = %q", media.Title.Inner)
	}
}
