package emby_wrapper_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/fs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/http_range"
)

func TestEndToEndThroughFS(t *testing.T) {
	_ = setup(t)
	// 通过 fs 重命名设置 actor（等价于 UI 操作）
	if err := fs.Rename(context.Background(), "/ew/Movies", `{"actors":"三上悠亚"}`); err != nil {
		t.Fatalf("rename via fs: %+v", err)
	}
	// fs 列表应包含虚拟 nfo
	objs, err := fs.List(context.Background(), "/ew/Movies", &fs.ListArgs{})
	if err != nil {
		t.Fatalf("fs list: %+v", err)
	}
	var found bool
	for _, o := range objs {
		if o.GetName() == "AAA.nfo" {
			found = true
		}
	}
	if !found {
		t.Fatal("virtual nfo must appear in fs list")
	}
	// fs 链接（strm generateStrm 同款调用链：Link -> 读取内容）
	link, _, err := fs.Link(context.Background(), "/ew/Movies/AAA.nfo", model.LinkArgs{})
	if err != nil {
		t.Fatalf("fs link: %+v", err)
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
	if !strings.Contains(got, "三上悠亚") || !strings.Contains(got, "AAA") {
		t.Errorf("nfo content mismatch: %s", got)
	}
}
