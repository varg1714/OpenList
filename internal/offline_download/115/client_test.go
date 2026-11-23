package _115

import (
	"context"
	"errors"
	"slices"
	"testing"

	pan115 "github.com/OpenListTeam/OpenList/v4/drivers/115"
	sdk115 "github.com/SheltonZhu/115driver/pkg/driver"
)

func TestOfflineActualFileID(t *testing.T) {
	tests := []struct {
		name      string
		fileID    string
		delFileID string
		want      string
	}{
		{
			name:      "ed2k task exposes the concrete file",
			fileID:    "3137916453630508845",
			delFileID: "3559332724521043416",
			want:      "3559332724521043416",
		},
		{
			name:      "torrent task has no separate file",
			fileID:    "3559332724521043416",
			delFileID: "3559332724521043416",
			want:      "",
		},
		{
			name:      "empty delete file id",
			fileID:    "3559332724521043416",
			delFileID: "",
			want:      "",
		},
		{
			name:      "both empty",
			fileID:    "",
			delFileID: "",
			want:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := offlineActualFileID(tt.fileID, tt.delFileID); got != tt.want {
				t.Fatalf("offlineActualFileID(%q, %q) = %q, want %q", tt.fileID, tt.delFileID, got, tt.want)
			}
		})
	}
}

func TestMagnetInfoHash(t *testing.T) {
	const hexHash = "c9e059ef9a5fdb267f3f0d432a05905747bf7912"
	tests := []struct {
		name   string
		uri    string
		want   string
		wantOK bool
	}{
		{name: "hex", uri: "magnet:?xt=urn:btih:" + hexHash + "&dn=example", want: hexHash, wantOK: true},
		{name: "upper hex is normalized", uri: "magnet:?xt=urn:btih:C9E059EF9A5FDB267F3F0D432A05905747BF7912", want: hexHash, wantOK: true},
		{name: "base32", uri: "magnet:?xt=urn:btih:ZHQFT342L7NSM7Z7BVBSUBMQK5D366IS", want: hexHash, wantOK: true},
		{name: "not a btih magnet", uri: "magnet:?xt=urn:sha1:abc", wantOK: false},
		{name: "not a magnet", uri: "https://example.com/a.torrent", wantOK: false},
		{name: "empty", uri: "", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := magnetInfoHash(tt.uri)
			if ok != tt.wantOK || got != tt.want {
				t.Fatalf("magnetInfoHash(%q) = %q, %v; want %q, %v", tt.uri, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestFindOfflineTaskHash(t *testing.T) {
	const infoHash = "c9e059ef9a5fdb267f3f0d432a05905747bf7912"
	tasks := []*sdk115.OfflineTask{
		{InfoHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		{InfoHash: infoHash},
	}

	got, ok := findOfflineTaskHash(infoHash, tasks)
	if !ok || got != infoHash {
		t.Fatalf("findOfflineTaskHash(%q) = %q, %v; want %q, true", infoHash, got, ok, infoHash)
	}
	if _, ok := findOfflineTaskHash("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", tasks); ok {
		t.Fatal("findOfflineTaskHash matched an unrelated task")
	}
	if _, ok := findOfflineTaskHash("", tasks); ok {
		t.Fatal("findOfflineTaskHash matched an empty info hash")
	}
}

func TestFindOfflineTaskHashMatchesCaseInsensitively(t *testing.T) {
	tasks := []*sdk115.OfflineTask{{InfoHash: "C9E059EF9A5FDB267F3F0D432A05905747BF7912"}}

	got, ok := findOfflineTaskHash("c9e059ef9a5fdb267f3f0d432a05905747bf7912", tasks)
	if !ok || got != tasks[0].InfoHash {
		t.Fatalf("findOfflineTaskHash returned %q, %v; want the stored task hash %q", got, ok, tasks[0].InfoHash)
	}
}

func TestRecoverExistingOfflineTaskReusesTaskByInfoHash(t *testing.T) {
	old := listOfflineTasksPage
	t.Cleanup(func() { listOfflineTasksPage = old })

	const infoHash = "c9e059ef9a5fdb267f3f0d432a05905747bf7912"
	listOfflineTasksPage = func(_ context.Context, _ *pan115.Pan115, page int64) ([]*sdk115.OfflineTask, int64, bool, error) {
		return []*sdk115.OfflineTask{{InfoHash: infoHash}}, page + 1, false, nil
	}

	got, ok := recoverExistingOfflineTask(context.Background(), nil, "magnet:?xt=urn:btih:"+infoHash+"&dn=example")
	if !ok || got != infoHash {
		t.Fatalf("recoverExistingOfflineTask = %q, %v; want %q, true", got, ok, infoHash)
	}
}

func TestRecoverExistingOfflineTaskSkipsUnparseableMagnet(t *testing.T) {
	old := listOfflineTasksPage
	t.Cleanup(func() { listOfflineTasksPage = old })
	listOfflineTasksPage = func(context.Context, *pan115.Pan115, int64) ([]*sdk115.OfflineTask, int64, bool, error) {
		t.Fatal("offline task list must not be queried for a link without a btih")
		return nil, 0, false, nil
	}

	if _, ok := recoverExistingOfflineTask(context.Background(), nil, "https://example.com/a.torrent"); ok {
		t.Fatal("recoverExistingOfflineTask accepted a non-magnet link")
	}
}

func TestRecoverExistingOfflineTaskReportsMiss(t *testing.T) {
	old := listOfflineTasksPage
	t.Cleanup(func() { listOfflineTasksPage = old })
	listOfflineTasksPage = func(_ context.Context, _ *pan115.Pan115, page int64) ([]*sdk115.OfflineTask, int64, bool, error) {
		return []*sdk115.OfflineTask{{InfoHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}, page + 1, false, nil
	}

	if _, ok := recoverExistingOfflineTask(context.Background(), nil, "magnet:?xt=urn:btih:c9e059ef9a5fdb267f3f0d432a05905747bf7912"); ok {
		t.Fatal("recoverExistingOfflineTask matched a task with a different info hash")
	}
}

func TestFindOfflineTaskHashAcrossPages(t *testing.T) {
	const infoHash = "c9e059ef9a5fdb267f3f0d432a05905747bf7912"
	pages := map[int64][]*sdk115.OfflineTask{
		0: {{InfoHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}},
		1: {{InfoHash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}},
		2: {{InfoHash: infoHash}},
	}
	var requested []int64
	fetch := func(page int64) ([]*sdk115.OfflineTask, int64, bool, error) {
		requested = append(requested, page)
		return pages[page], page + 1, page < 2, nil
	}

	got, ok := findOfflineTaskHashAcrossPages(infoHash, fetch)
	if !ok || got != infoHash {
		t.Fatalf("findOfflineTaskHashAcrossPages = %q, %v; want %q, true", got, ok, infoHash)
	}
	if want := []int64{0, 1, 2}; !slices.Equal(requested, want) {
		t.Fatalf("requested pages = %v, want %v", requested, want)
	}
}

func TestFindOfflineTaskHashAcrossPagesStopsWhenNoMorePages(t *testing.T) {
	var requests int
	fetch := func(page int64) ([]*sdk115.OfflineTask, int64, bool, error) {
		requests++
		return []*sdk115.OfflineTask{{InfoHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}, page + 1, false, nil
	}

	if _, ok := findOfflineTaskHashAcrossPages("c9e059ef9a5fdb267f3f0d432a05905747bf7912", fetch); ok {
		t.Fatal("findOfflineTaskHashAcrossPages matched a missing task")
	}
	if requests != 1 {
		t.Fatalf("requested %d pages, want 1", requests)
	}
}

func TestFindOfflineTaskHashAcrossPagesStopsOnPageError(t *testing.T) {
	var requests int
	fetch := func(page int64) ([]*sdk115.OfflineTask, int64, bool, error) {
		requests++
		if page == 1 {
			return nil, 0, false, errors.New("list failed")
		}
		return []*sdk115.OfflineTask{{InfoHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}, page + 1, true, nil
	}

	if _, ok := findOfflineTaskHashAcrossPages("c9e059ef9a5fdb267f3f0d432a05905747bf7912", fetch); ok {
		t.Fatal("findOfflineTaskHashAcrossPages matched despite a page error")
	}
	if requests != 2 {
		t.Fatalf("requested %d pages, want 2", requests)
	}
}

func TestFindOfflineTaskHashAcrossPagesStopsOnEmptyPage(t *testing.T) {
	var requests int
	fetch := func(page int64) ([]*sdk115.OfflineTask, int64, bool, error) {
		requests++
		return nil, page + 1, true, nil
	}

	if _, ok := findOfflineTaskHashAcrossPages("c9e059ef9a5fdb267f3f0d432a05905747bf7912", fetch); ok {
		t.Fatal("findOfflineTaskHashAcrossPages matched on an empty page")
	}
	if requests != 1 {
		t.Fatalf("requested %d pages, want 1", requests)
	}
}

func TestFindOfflineTaskHashAcrossPagesStopsOnStalledCursor(t *testing.T) {
	var requests int
	fetch := func(int64) ([]*sdk115.OfflineTask, int64, bool, error) {
		requests++
		return []*sdk115.OfflineTask{{InfoHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}, 0, true, nil
	}

	if _, ok := findOfflineTaskHashAcrossPages("c9e059ef9a5fdb267f3f0d432a05905747bf7912", fetch); ok {
		t.Fatal("findOfflineTaskHashAcrossPages matched on a stalled cursor")
	}
	if requests != 1 {
		t.Fatalf("requested %d pages, want 1", requests)
	}
}

func TestFindOfflineTaskHashAcrossPagesCapsRequests(t *testing.T) {
	var requests int
	fetch := func(page int64) ([]*sdk115.OfflineTask, int64, bool, error) {
		requests++
		return []*sdk115.OfflineTask{{InfoHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}, page + 1, true, nil
	}

	if _, ok := findOfflineTaskHashAcrossPages("c9e059ef9a5fdb267f3f0d432a05905747bf7912", fetch); ok {
		t.Fatal("findOfflineTaskHashAcrossPages matched a missing task")
	}
	if requests != maxOfflineTaskPages {
		t.Fatalf("requested %d pages, want the cap of %d", requests, maxOfflineTaskPages)
	}
}
