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
