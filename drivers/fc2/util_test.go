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

func TestStripFC2CodePrefix(t *testing.T) {
	const code = "FC2-PPV-123456"
	tests := []struct {
		name  string
		code  string
		title string
		want  string
	}{
		{name: "space separated", code: code, title: "FC2-PPV-123456 タイトル", want: "タイトル"},
		{name: "lowercase code", code: code, title: "fc2-ppv-123456 タイトル", want: "タイトル"},
		{name: "no separator", code: code, title: "FC2-PPV-123456タイトル", want: "タイトル"},
		{name: "dash separator", code: code, title: "FC2-PPV-123456-タイトル", want: "タイトル"},
		{name: "underscore separator", code: code, title: "FC2-PPV-123456_タイトル", want: "タイトル"},
		{name: "code only", code: code, title: "FC2-PPV-123456", want: ""},
		{name: "no code prefix", code: code, title: "普通のタイトル", want: "普通のタイトル"},
		{name: "different code", code: code, title: "FC2-PPV-999999 タイトル", want: "FC2-PPV-999999 タイトル"},
		{name: "empty title", code: code, title: "", want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := stripFC2CodePrefix(test.code, test.title)
			if got != test.want {
				t.Fatalf("stripFC2CodePrefix(%q, %q) = %q, want %q", test.code, test.title, got, test.want)
			}
		})
	}
}
