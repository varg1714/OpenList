package db

import (
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

func TestVirtualDirSnapshotCRUD(t *testing.T) {
	// 不存在 → (nil, nil)
	snap, err := GetVirtualDirSnapshot(1, "up_42")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if snap != nil {
		t.Fatalf("want nil for missing snapshot, got %+v", snap)
	}
	// Upsert 新建
	s := &model.VirtualDirSnapshot{StorageID: 1, DirKey: "up_42", Owner: "12345", Data: `{"v":1}`}
	if err := UpsertVirtualDirSnapshot(s); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	got, err := GetVirtualDirSnapshot(1, "up_42")
	if err != nil || got == nil || got.Data != `{"v":1}` || got.Owner != "12345" {
		t.Fatalf("Get after upsert = %+v, %v", got, err)
	}
	// 同 key 覆盖（不产生第二行）
	s2 := &model.VirtualDirSnapshot{StorageID: 1, DirKey: "up_42", Owner: "12345", Data: `{"v":2,"items":["x"]}`}
	if err := UpsertVirtualDirSnapshot(s2); err != nil {
		t.Fatalf("Upsert2: %v", err)
	}
	got, err = GetVirtualDirSnapshot(1, "up_42")
	if err != nil || got == nil || got.Data != `{"v":2,"items":["x"]}` {
		t.Fatalf("Get after overwrite = %+v, %v", got, err)
	}
	// 不同 storage 同 key 互不干扰
	other, err := GetVirtualDirSnapshot(2, "up_42")
	if err != nil || other != nil {
		t.Fatalf("other storage Get = %+v, %v (want nil)", other, err)
	}
}

func TestDeleteVirtualDirSnapshotsNotOwner(t *testing.T) {
	must := func(s *model.VirtualDirSnapshot) {
		t.Helper()
		if err := UpsertVirtualDirSnapshot(s); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
	}
	must(&model.VirtualDirSnapshot{StorageID: 9, DirKey: "a", Owner: "1", Data: "{}"})
	must(&model.VirtualDirSnapshot{StorageID: 9, DirKey: "b", Owner: "2", Data: "{}"})
	must(&model.VirtualDirSnapshot{StorageID: 9, DirKey: "c", Owner: "", Data: "{}"})
	must(&model.VirtualDirSnapshot{StorageID: 10, DirKey: "a", Owner: "1", Data: "{}"})
	if err := DeleteVirtualDirSnapshotsNotOwner(9, "2"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	// storage 9：owner=2 的 b 保留；owner=1 的 a 与无主的 c 删除
	for _, tc := range []struct {
		key  string
		want bool // true = 应仍存在
	}{{"a", false}, {"b", true}, {"c", false}} {
		tc := tc
		snap, err := GetVirtualDirSnapshot(9, tc.key)
		if err != nil {
			t.Fatalf("Get %s: %v", tc.key, err)
		}
		if (snap != nil) != tc.want {
			t.Fatalf("storage 9 key %s: exists=%v, want %v", tc.key, snap != nil, tc.want)
		}
	}
	// storage 10 不受影响
	snap, err := GetVirtualDirSnapshot(10, "a")
	if err != nil || snap == nil {
		t.Fatalf("storage 10 must survive: %+v, %v", snap, err)
	}
}
