package fc2

import "testing"

func TestMagnetDisplayTitleRemovesKnownVideoExtension(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "mp4", raw: "FC2 title.mp4", want: "FC2 title"},
		{name: "uppercase mkv", raw: "FC2 title.MKV", want: "FC2 title"},
		{name: "trimmed", raw: "  FC2 title.avi  ", want: "FC2 title"},
		{name: "ordinary dotted title", raw: "FC2 title.part one", want: "FC2 title.part one"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := magnetDisplayTitle(test.raw)
			if got != test.want {
				t.Fatalf("magnetDisplayTitle(%q) = %q, want %q", test.raw, got, test.want)
			}
		})
	}
}
