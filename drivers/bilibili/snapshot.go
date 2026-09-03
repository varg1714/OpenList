package bilibili

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

// snapshotEnvelope 快照 Data 的负载结构（v 为格式版本，未来迁移用）
type snapshotEnvelope[T any] struct {
	V         int    `json:"v"`
	FetchedAt string `json:"fetched_at"`
	Items     []T    `json:"items"`
}

// snapshotOwner 当前登录账号的快照 owner 标识
func (d *Bilibili) snapshotOwner() string {
	return strconv.FormatInt(d.uid, 10)
}

// loadSnapshot 读快照并解码；无快照返回 (nil, false, nil)
func loadSnapshot[T any](d *Bilibili, dirKey string) (*snapshotEnvelope[T], bool, error) {
	row, err := db.GetVirtualDirSnapshot(d.ID, dirKey)
	if err != nil {
		return nil, false, err
	}
	if row == nil {
		return nil, false, nil
	}
	var env snapshotEnvelope[T]
	if err := json.Unmarshal([]byte(row.Data), &env); err != nil {
		// 数据损坏：按无快照处理（下次全量重建覆盖）
		return nil, false, nil
	}
	return &env, true, nil
}

// saveSnapshot 整体覆盖写快照（原子：仅在完整成功后调用）
func saveSnapshot[T any](d *Bilibili, dirKey string, env *snapshotEnvelope[T]) error {
	env.V = 1
	env.FetchedAt = time.Now().Format(time.RFC3339)
	data, err := json.Marshal(env)
	if err != nil {
		return err
	}
	return db.UpsertVirtualDirSnapshot(&model.VirtualDirSnapshot{
		StorageID: d.ID,
		DirKey:    dirKey,
		Owner:     d.snapshotOwner(),
		Data:      string(data),
	})
}

// listWithSnapshot 目录列表统一门面：读快照 → 翻页增量 → 原子落库 → build 展示。
//
// 语义（spec 2026-09-03）：
//   - 无快照 = 首次：known 为空集，翻到空页或达 total 即全量；成功即落库（空目录也落）
//   - 有快照 = 增量：从第 1 页起，页内出现任一已知条目即停（顺序假设：新条目连续在头部，
//     接上页之后必然全已知）；全未知页继续翻——新内容只可能在头部；
//     新增条目插到旧数据头部（保持 API 新→旧顺序）
//   - 任意页失败（重试耗尽）：有快照 → 返回旧快照数据且不写库；无快照 → 返回错误。
//     绝不返回部分分页结果（原子性，用户 Z 决策）
//
// 包级泛型函数（Go 方法不允许类型参数），fetchWithRetry 页级退避在 api.go。
func listWithSnapshot[T any](d *Bilibili, ctx context.Context, dir model.Obj, dirKey string,
	fetchPage func(pn int) ([]T, int, error), keyOf func(T) string,
	build func(dir model.Obj, items []T) ([]model.Obj, error)) ([]model.Obj, error) {

	env, hasSnap, err := loadSnapshot[T](d, dirKey)
	if err != nil {
		return nil, fmt.Errorf("load snapshot %s: %w", dirKey, err)
	}

	known := make(map[string]bool)
	if hasSnap {
		for _, it := range env.Items {
			known[keyOf(it)] = true
		}
	}

	var merged []T
	if hasSnap {
		merged = make([]T, len(env.Items))
		copy(merged, env.Items)
	}
	var newHead []T // 增量新增（保持页序 = API 新→旧）
	changed := false

	for pn := 1; ; pn++ {
		items, total, err := fetchWithRetry(d, ctx, fetchPage, pn)
		if err != nil {
			if hasSnap {
				return build(dir, env.Items) // 降级：旧快照，不写库
			}
			return nil, err
		}
		if len(items) == 0 {
			break // API 拉完
		}
		var fresh []T
		for _, it := range items {
			if !known[keyOf(it)] {
				known[keyOf(it)] = true
				fresh = append(fresh, it)
			}
		}
		if !hasSnap {
			merged = append(merged, fresh...)
			if len(merged) >= total {
				break // 首次全量拉完
			}
		} else {
			if len(fresh) > 0 {
				newHead = append(newHead, fresh...)
			}
			if len(fresh) < len(items) {
				// 页内出现已知条目 = 已接上（顺序假设：新条目连续在头部，
				// 接上页之后必然全已知）；全未知页则继续翻
				break
			}
		}
	}

	if !hasSnap {
		changed = true // 首次：空目录也要落库（防每次重拉）
	} else if len(newHead) > 0 {
		merged = append(newHead, merged...)
		changed = true
	}
	if changed {
		if err := saveSnapshot(d, dirKey, &snapshotEnvelope[T]{Items: merged}); err != nil {
			return nil, fmt.Errorf("save snapshot %s: %w", dirKey, err)
		}
	}
	return build(dir, merged)
}
