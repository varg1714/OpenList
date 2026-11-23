package _115

import (
	"testing"

	driver115 "github.com/SheltonZhu/115driver/pkg/driver"
)

func TestOfflinePageCursor(t *testing.T) {
	tests := []struct {
		name     string
		resp     driver115.OfflineTaskResp
		wantNext int64
		wantMore bool
	}{
		{
			name:     "full first page continues",
			resp:     driver115.OfflineTaskResp{Page: 0, PageRow: 3, Tasks: make([]*driver115.OfflineTask, 3)},
			wantNext: 1,
			wantMore: true,
		},
		{
			name:     "short page is the last page",
			resp:     driver115.OfflineTaskResp{Page: 0, PageRow: 3, Tasks: make([]*driver115.OfflineTask, 2)},
			wantNext: 1,
			wantMore: false,
		},
		{
			name:     "empty page is the last page",
			resp:     driver115.OfflineTaskResp{Page: 0, PageRow: 0},
			wantNext: 1,
			wantMore: false,
		},
		{
			name:     "provider echoed page drives the next cursor",
			resp:     driver115.OfflineTaskResp{Page: 5, PageRow: 2, Tasks: make([]*driver115.OfflineTask, 2)},
			wantNext: 6,
			wantMore: true,
		},
		{
			name:     "page row without tasks stops",
			resp:     driver115.OfflineTaskResp{Page: 2, PageRow: 30},
			wantNext: 3,
			wantMore: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			next, more := offlinePageCursor(tt.resp)
			if next != tt.wantNext || more != tt.wantMore {
				t.Fatalf("offlinePageCursor() = %d, %v; want %d, %v", next, more, tt.wantNext, tt.wantMore)
			}
		})
	}
}
