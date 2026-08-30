package emby_wrapper

import "testing"

func TestPlotFileNameStripsOnlyExtension(t *testing.T) {
	cases := map[string]string{
		"AAA.mkv":              "AAA",
		"BBB.cd1.mkv":          "BBB.cd1",
		"CCC.CD2.mp4":          "CCC.CD2",
		"noext":                "noext",
		"ASMR   小如快醒醒  R_E_STUDIO  喉咙 .mp4": "ASMR   小如快醒醒  R_E_STUDIO  喉咙 ",
	}
	for in, want := range cases {
		if got := plotFileName(in); got != want {
			t.Errorf("plotFileName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildPlot(t *testing.T) {
	tf := true
	ff := false
	cases := []struct {
		name     string
		plot     string
		append   *bool
		fileName string
		want     string
	}{
		{name: "append off", plot: "P", append: nil, fileName: "AAA.mkv", want: "P"},
		{name: "append on with plot", plot: "P", append: &tf, fileName: "AAA.mkv", want: "P-AAA"},
		{name: "append on without plot", plot: "", append: &tf, fileName: "AAA.mkv", want: "AAA"},
		{name: "append on keeps cd part", plot: "P", append: &tf, fileName: "BBB.cd1.mkv", want: "P-BBB.cd1"},
		{name: "explicit false", plot: "P", append: &ff, fileName: "AAA.mkv", want: "P"},
	}
	for _, c := range cases {
		if got := buildPlot(c.plot, c.append, c.fileName); got != c.want {
			t.Errorf("%s: buildPlot(%q, %v, %q) = %q, want %q", c.name, c.plot, c.append, c.fileName, got, c.want)
		}
	}
}
