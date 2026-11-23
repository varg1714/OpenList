package _115

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	pan115 "github.com/OpenListTeam/OpenList/v4/drivers/115"
	"github.com/OpenListTeam/OpenList/v4/internal/offline_download/tool"
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

func TestEd2kInfoHash(t *testing.T) {
	const hexHash = "8e223b7b3ade9661b4afa46e986d175e"
	tests := []struct {
		name   string
		uri    string
		want   string
		wantOK bool
	}{
		{name: "ed2k", uri: "ed2k://|file|www.98T.la@HMN-915_LTX.mp4|11659372387|8E223B7B3ADE9661B4AFA46E986D175E|/", want: hexHash, wantOK: true},
		{name: "ed2k with aich hash", uri: "ed2k://|file|HMN-915.mp4|11659372387|8E223B7B3ADE9661B4AFA46E986D175E|h=abcdef|/", want: hexHash, wantOK: true},
		{name: "magnet is not ed2k", uri: "magnet:?xt=urn:btih:c9e059ef9a5fdb267f3f0d432a05905747bf7912", wantOK: false},
		{name: "http is not ed2k", uri: "https://example.com/a.torrent", wantOK: false},
		{name: "short hash", uri: "ed2k://|file|a.mp4|1|8E223B7B|/", wantOK: false},
		{name: "missing fields", uri: "ed2k://|file|a.mp4|", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ed2kInfoHash(tt.uri)
			if ok != tt.wantOK || got != tt.want {
				t.Fatalf("ed2kInfoHash(%q) = %q, %v; want %q, %v", tt.uri, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestLinkInfoHash(t *testing.T) {
	const magnetHash = "c9e059ef9a5fdb267f3f0d432a05905747bf7912"
	const ed2kHash = "8e223b7b3ade9661b4afa46e986d175e"

	if got, ok := linkInfoHash("magnet:?xt=urn:btih:" + magnetHash); !ok || got != magnetHash {
		t.Fatalf("linkInfoHash(magnet) = %q, %v; want %q, true", got, ok, magnetHash)
	}
	if got, ok := linkInfoHash("ed2k://|file|a.mp4|1|8E223B7B3ADE9661B4AFA46E986D175E|/"); !ok || got != ed2kHash {
		t.Fatalf("linkInfoHash(ed2k) = %q, %v; want %q, true", got, ok, ed2kHash)
	}
	if _, ok := linkInfoHash("https://example.com/a.torrent"); ok {
		t.Fatal("linkInfoHash accepted a plain HTTP link")
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
		return []*sdk115.OfflineTask{{
			InfoHash: infoHash, Status: 2, Percent: 100, Size: 42, FileId: "111", DelFileId: "222",
		}}, page + 1, false, nil
	}

	task, ok := recoverExistingOfflineTask(context.Background(), nil, "magnet:?xt=urn:btih:"+infoHash+"&dn=example")
	if !ok || task == nil || task.InfoHash != infoHash || !task.IsDone() {
		t.Fatalf("recoverExistingOfflineTask = %+v, %v; want the completed task %q", task, ok, infoHash)
	}
}

func TestRecoverExistingOfflineTaskReusesEd2kTask(t *testing.T) {
	old := listOfflineTasksPage
	t.Cleanup(func() { listOfflineTasksPage = old })

	const infoHash = "8e223b7b3ade9661b4afa46e986d175e"
	listOfflineTasksPage = func(_ context.Context, _ *pan115.Pan115, page int64) ([]*sdk115.OfflineTask, int64, bool, error) {
		return []*sdk115.OfflineTask{{InfoHash: infoHash, Status: 2, Size: 11659372387}}, page + 1, false, nil
	}

	task, ok := recoverExistingOfflineTask(context.Background(), nil, "ed2k://|file|www.98T.la@HMN-915_LTX.mp4|11659372387|8E223B7B3ADE9661B4AFA46E986D175E|/")
	if !ok || task == nil || task.InfoHash != infoHash {
		t.Fatalf("recoverExistingOfflineTask = %+v, %v; want the ed2k task %q", task, ok, infoHash)
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

	if task, ok := recoverExistingOfflineTask(context.Background(), nil, "magnet:?xt=urn:btih:c9e059ef9a5fdb267f3f0d432a05905747bf7912"); ok || task != nil {
		t.Fatalf("recoverExistingOfflineTask = %+v, %v; want no task", task, ok)
	}
}

func TestFindOfflineTaskAcrossPages(t *testing.T) {
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

	task, ok := findOfflineTaskAcrossPages(infoHash, fetch)
	if !ok || task == nil || task.InfoHash != infoHash {
		t.Fatalf("findOfflineTaskAcrossPages = %+v, %v; want the task %q", task, ok, infoHash)
	}
	if want := []int64{0, 1, 2}; !slices.Equal(requested, want) {
		t.Fatalf("requested pages = %v, want %v", requested, want)
	}
}

func TestFindOfflineTaskAcrossPagesStopsWhenNoMorePages(t *testing.T) {
	var requests int
	fetch := func(page int64) ([]*sdk115.OfflineTask, int64, bool, error) {
		requests++
		return []*sdk115.OfflineTask{{InfoHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}, page + 1, false, nil
	}

	if _, ok := findOfflineTaskAcrossPages("c9e059ef9a5fdb267f3f0d432a05905747bf7912", fetch); ok {
		t.Fatal("findOfflineTaskAcrossPages matched a missing task")
	}
	if requests != 1 {
		t.Fatalf("requested %d pages, want 1", requests)
	}
}

func TestFindOfflineTaskAcrossPagesStopsOnPageError(t *testing.T) {
	var requests int
	fetch := func(page int64) ([]*sdk115.OfflineTask, int64, bool, error) {
		requests++
		if page == 1 {
			return nil, 0, false, errors.New("list failed")
		}
		return []*sdk115.OfflineTask{{InfoHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}, page + 1, true, nil
	}

	if _, ok := findOfflineTaskAcrossPages("c9e059ef9a5fdb267f3f0d432a05905747bf7912", fetch); ok {
		t.Fatal("findOfflineTaskAcrossPages matched despite a page error")
	}
	if requests != 2 {
		t.Fatalf("requested %d pages, want 2", requests)
	}
}

func TestFindOfflineTaskAcrossPagesStopsOnEmptyPage(t *testing.T) {
	var requests int
	fetch := func(page int64) ([]*sdk115.OfflineTask, int64, bool, error) {
		requests++
		return nil, page + 1, true, nil
	}

	if _, ok := findOfflineTaskAcrossPages("c9e059ef9a5fdb267f3f0d432a05905747bf7912", fetch); ok {
		t.Fatal("findOfflineTaskAcrossPages matched on an empty page")
	}
	if requests != 1 {
		t.Fatalf("requested %d pages, want 1", requests)
	}
}

func TestFindOfflineTaskAcrossPagesStopsOnStalledCursor(t *testing.T) {
	var requests int
	fetch := func(int64) ([]*sdk115.OfflineTask, int64, bool, error) {
		requests++
		return []*sdk115.OfflineTask{{InfoHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}, 0, true, nil
	}

	if _, ok := findOfflineTaskAcrossPages("c9e059ef9a5fdb267f3f0d432a05905747bf7912", fetch); ok {
		t.Fatal("findOfflineTaskAcrossPages matched on a stalled cursor")
	}
	if requests != 1 {
		t.Fatalf("requested %d pages, want 1", requests)
	}
}

func TestFindOfflineTaskAcrossPagesCapsRequests(t *testing.T) {
	var requests int
	fetch := func(page int64) ([]*sdk115.OfflineTask, int64, bool, error) {
		requests++
		return []*sdk115.OfflineTask{{InfoHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}, page + 1, true, nil
	}

	if _, ok := findOfflineTaskAcrossPages("c9e059ef9a5fdb267f3f0d432a05905747bf7912", fetch); ok {
		t.Fatal("findOfflineTaskAcrossPages matched a missing task")
	}
	if requests != maxOfflineTaskPages {
		t.Fatalf("requested %d pages, want the cap of %d", requests, maxOfflineTaskPages)
	}
}

func TestOfflineTaskStatus(t *testing.T) {
	done := offlineTaskStatus(&sdk115.OfflineTask{
		InfoHash: "abc", Status: 2, Percent: 100, Size: 42, FileId: "111", DelFileId: "222",
	})
	if !done.Completed || done.Progress != 100 || done.TotalBytes != 42 {
		t.Fatalf("completed status = %+v", done)
	}
	if done.FileInfo.FileId != "111" || done.FileInfo.ActualFileId != "222" || done.Err != nil {
		t.Fatalf("completed file info = %+v, err = %v", done.FileInfo, done.Err)
	}

	failed := offlineTaskStatus(&sdk115.OfflineTask{Status: -1})
	if failed.Completed || failed.Err == nil {
		t.Fatalf("failed status = %+v, want an error and not completed", failed)
	}
}

func TestCloud115StatusUsesCompletedRecoveredTask(t *testing.T) {
	p := &Cloud115{}
	p.storeRecoveredTask(&sdk115.OfflineTask{
		InfoHash: "abc", Status: 2, Percent: 100, Size: 42, FileId: "111", DelFileId: "222",
	})

	status, err := p.Status(&tool.DownloadTask{GID: "abc"})
	if err != nil {
		t.Fatalf("Status returned %v", err)
	}
	if status == nil || !status.Completed || status.FileInfo.ActualFileId != "222" {
		t.Fatalf("Status = %+v, want a completed status from the recovered snapshot", status)
	}
}

func TestCloud115StatusIgnoresInProgressRecoveredTask(t *testing.T) {
	p := &Cloud115{}
	p.storeRecoveredTask(&sdk115.OfflineTask{InfoHash: "abc", Status: 1, Percent: 40})

	if _, ok := p.recoveredTask("abc"); ok {
		t.Fatal("an in-progress snapshot must not satisfy a status lookup")
	}
}

func TestCloud115StatusExpiresRecoveredTask(t *testing.T) {
	p := &Cloud115{}
	p.recoveredTasks.Store("abc", recoveredTaskSnapshot{
		task: &sdk115.OfflineTask{InfoHash: "abc", Status: 2},
		at:   time.Now().Add(-time.Hour),
	})

	if _, ok := p.recoveredTask("abc"); ok {
		t.Fatal("an expired snapshot must not satisfy a status lookup")
	}
	if _, ok := p.recoveredTasks.Load("abc"); ok {
		t.Fatal("an expired snapshot must be dropped")
	}
}
