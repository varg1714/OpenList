package emby_wrapper_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/http_range"
)

func TestLinkServesVirtualNFOContent(t *testing.T) {
	d := setup(t)
	if err := d.Rename(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, `{"actors":"三上悠亚,深田咏美"}`); err != nil {
		t.Fatalf("set actors: %+v", err)
	}
	obj, err := d.Get(context.Background(), "/Movies/AAA.nfo")
	if err != nil {
		t.Fatalf("get nfo: %+v", err)
	}
	link, err := d.Link(context.Background(), obj, model.LinkArgs{})
	if err != nil {
		t.Fatalf("link nfo: %+v", err)
	}
	if link.ContentLength != obj.GetSize() {
		t.Errorf("content length mismatch: %d vs %d", link.ContentLength, obj.GetSize())
	}
	if link.RangeReader == nil {
		t.Fatal("nfo link must have a range reader")
	}
	rc, err := link.RangeReader.RangeRead(context.Background(), http_range.Range{Length: -1})
	if err != nil {
		t.Fatalf("range read: %+v", err)
	}
	defer rc.Close()
	body, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read: %+v", err)
	}
	got := string(body)
	if !strings.Contains(got, "AAA") {
		t.Errorf("nfo must contain movie title, got %s", got)
	}
	if !strings.Contains(got, "三上悠亚") || !strings.Contains(got, "深田咏美") {
		t.Errorf("nfo must contain actors, got %s", got)
	}
}

func TestLinkForwardsDownstream(t *testing.T) {
	d := setup(t)
	obj, err := d.Get(context.Background(), "/Movies/AAA.mkv")
	if err != nil {
		t.Fatalf("get movie: %+v", err)
	}
	link, err := d.Link(context.Background(), obj, model.LinkArgs{})
	if err != nil {
		t.Fatalf("link movie: %+v", err)
	}
	if link.URL == "" && link.RangeReader == nil {
		t.Error("downstream link must provide url or range reader")
	}
	if link.URL != "" && !strings.Contains(link.URL, "AAA.mkv") {
		t.Errorf("unexpected link url %s", link.URL)
	}
}
