package virtual_file

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCutStringKeepsFourByteUnicodeWithinCoverFilenameLimit(t *testing.T) {
	name := strings.Repeat("😀", 70)

	got := CutString(name)

	if !utf8.ValidString(got) {
		t.Fatalf("CutString returned invalid UTF-8: %q", got)
	}
	if len(AppendImageName(got)) > 255 {
		t.Fatalf("cover filename byte length = %d, want <= 255", len(AppendImageName(got)))
	}
	if got != strings.Repeat("😀", 62) {
		t.Fatalf("CutString returned %d runes, want 62 complete runes", utf8.RuneCountInString(got))
	}
}
