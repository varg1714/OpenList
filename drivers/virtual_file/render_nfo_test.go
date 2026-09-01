package virtual_file

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestRenderMediaNFO(t *testing.T) {
	out, err := RenderMediaNFO(&Media{
		Title: Inner{Inner: "<![CDATA[测试标题]]>"},
		Actor: []Actor{{Name: "三上悠亚"}},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	got := string(out)
	if !strings.HasPrefix(got, xml.Header) {
		t.Errorf("missing xml header, got %s", got)
	}
	if !strings.Contains(got, "<movie>") || !strings.Contains(got, "</movie>") {
		t.Errorf("missing movie root, got %s", got)
	}
	if !strings.Contains(got, "<![CDATA[测试标题]]>") {
		t.Errorf("missing title, got %s", got)
	}
	if !strings.Contains(got, "<name>三上悠亚</name>") {
		t.Errorf("missing actor, got %s", got)
	}
}

// TestRenderNFOWithRoot：根元素参数化，且不突变入参 Media。
func TestRenderNFOWithRoot(t *testing.T) {
	m := &Media{
		XMLName: xml.Name{Local: "movie"},
		Title:   Inner{Inner: "<![CDATA[测试标题]]>"},
		Actor:   []Actor{{Name: "三上悠亚"}},
	}
	out, err := RenderNFO("tvshow", m)
	if err != nil {
		t.Fatalf("render tvshow: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "<tvshow>") || !strings.Contains(got, "</tvshow>") {
		t.Errorf("missing tvshow root, got %s", got)
	}
	if strings.Contains(got, "<movie>") {
		t.Errorf("tvshow must not use movie root, got %s", got)
	}
	out2, err := RenderNFO("episodedetails", m)
	if err != nil {
		t.Fatalf("render episodedetails: %v", err)
	}
	if !strings.Contains(string(out2), "<episodedetails>") {
		t.Errorf("missing episodedetails root, got %s", string(out2))
	}
	// 入参不被突变：仍保持 movie 根
	if m.XMLName.Local != "movie" {
		t.Errorf("RenderNFO must not mutate input Media, XMLName=%s", m.XMLName.Local)
	}
	// RenderMediaNFO 行为不变
	out3, err := RenderMediaNFO(m)
	if err != nil {
		t.Fatalf("render movie: %v", err)
	}
	if !strings.Contains(string(out3), "<movie>") {
		t.Errorf("RenderMediaNFO must keep movie root, got %s", string(out3))
	}
}
