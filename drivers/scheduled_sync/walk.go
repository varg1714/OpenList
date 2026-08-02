package scheduled_sync

import (
	"context"
	stdpath "path"
	"sort"
	"strings"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/internal/syncpaths"
	"github.com/OpenListTeam/OpenList/v4/pkg/generic"
	log "github.com/sirupsen/logrus"
)

// scan 触发一次定时遍历：白名单条目（空白名单为下游根）按深度排序后入
// BFS 队列，每个目录通过下游自己的 List 获取（Refresh 由配置决定）。
// 白名单之外的目录不会出现在下游 List 的返回中（Cache 场景），
// 即使出现（普通驱动场景）也由 WithinSyncPaths 拦截，不会入队。
// 单目录失败仅记日志继续——保留下游已产生的数据，不删除。
func (d *ScheduledSync) scan() {
	remoteStorage, actualPath, err := op.GetStorageAndActualPath(d.RemotePath)
	if err != nil {
		log.Errorf("scheduled_sync: resolve remote %s: %+v", d.RemotePath, err)
		return
	}
	entries, whitelisted := syncpaths.ToRelEntries(actualPath, d.SyncPaths)
	seeds := make([]string, 0)
	if whitelisted {
		seeds = append(seeds, entries...)
	} else {
		seeds = append(seeds, "/")
	}
	if len(seeds) == 0 {
		return
	}
	sort.Slice(seeds, func(i, j int) bool {
		return syncpaths.DirDepth(seeds[i]) < syncpaths.DirDepth(seeds[j])
	})
	queue := generic.NewQueue[string]()
	seen := make(map[string]bool)
	push := func(p string) {
		if seen[p] {
			return
		}
		seen[p] = true
		queue.Push(p)
	}
	for _, s := range seeds {
		push(s)
	}
	ctx := context.Background()
	for !queue.IsEmpty() {
		dirPath := queue.Pop()
		objs, err := op.List(ctx, remoteStorage, stdpath.Join(actualPath, dirPath), model.ListArgs{Refresh: d.Refresh})
		if err != nil {
			log.Errorf("scheduled_sync: list %s: %+v", dirPath, err)
			continue
		}
		for _, o := range objs {
			if !o.IsDir() {
				continue
			}
			name := o.GetName()
			if name == "." || name == ".." || strings.ContainsRune(name, '/') {
				continue
			}
			child := stdpath.Join(dirPath, name)
			if !whitelisted || syncpaths.WithinSyncPaths(child, entries) {
				push(child)
			}
		}
	}
}
