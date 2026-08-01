package cache

import (
	"context"
	stdpath "path"
	"sort"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	log "github.com/sirupsen/logrus"
)

func dirDepth(dirPath string) int {
	if dirPath == "/" {
		return 0
	}
	return strings.Count(strings.Trim(dirPath, "/"), "/") + 1
}

// parseSyncPaths 解析白名单字符串（换行/逗号分隔），返回清理后的下游实际路径列表。
// 无有效条目时返回 nil。
func parseSyncPaths(raw string) []string {
	raw = strings.ReplaceAll(raw, ",", "\n")
	seen := make(map[string]bool)
	var res []string
	for _, line := range strings.Split(raw, "\n") {
		p := utils.FixAndCleanPath(strings.TrimSpace(line))
		if p == "/" || seen[p] {
			continue
		}
		seen[p] = true
		res = append(res, p)
	}
	return res
}

// withinSyncPaths 判断 relPath（驱动相对坐标）是否位于任一白名单条目的子树内。
func withinSyncPaths(relPath string, entries []string) bool {
	for _, e := range entries {
		if utils.IsSubPath(e, relPath) {
			return true
		}
	}
	return false
}

// syncPathEntries 解析白名单（下游实际路径坐标）并转换为驱动相对坐标。
// enabled=false 表示未配置白名单（保持全量同步行为）。
func (d *Cache) syncPathEntries(actualPath string) ([]string, bool) {
	if strings.TrimSpace(d.SyncPaths) == "" {
		return nil, false
	}
	var rel []string
	for _, w := range parseSyncPaths(d.SyncPaths) {
		if !utils.IsSubPath(actualPath, w) {
			log.Warnf("cache: sync path %s is not under actual path %s, ignored", w, actualPath)
			continue
		}
		rel = append(rel, utils.FixAndCleanPath(strings.TrimPrefix(w, actualPath)))
	}
	return rel, true
}

func (d *Cache) syncAll() {
	rows, err := ListCacheLists(d.ID)
	if err != nil {
		log.Errorf("cache: list rows: %+v", err)
		return
	}
	if len(rows) == 0 {
		return
	}
	sort.Slice(rows, func(i, j int) bool {
		return dirDepth(rows[i].DirPath) < dirDepth(rows[j].DirPath)
	})
	ttl := time.Duration(d.TTLHours) * time.Hour
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	known := make(map[string]bool, len(rows)*2)
	for i := range rows {
		known[rows[i].DirPath] = true
	}
	queue := make([]string, 0)
	for i := range rows {
		if time.Since(rows[i].UpdatedAt) >= ttl {
			queue = append(queue, rows[i].DirPath)
		}
	}
	ctx := context.Background()
	for len(queue) > 0 {
		dirPath := queue[0]
		queue = queue[1:]
		remoteStorage, remoteActualPath, err := op.GetStorageAndActualPath(d.RemotePath)
		if err != nil {
			log.Errorf("cache: sync resolve remote %s: %+v", d.RemotePath, err)
			continue
		}
		objs, err := op.List(ctx, remoteStorage, stdpath.Join(remoteActualPath, dirPath), model.ListArgs{})
		if err != nil {
			// 保留既有缓存行，不删除：下游错误可能是暂时性故障（超时/5xx），
			// 删行会导致浏览 miss 回源雪崩；待日志足够后按错误细节决定保留/删除策略
			log.Errorf("cache: sync %s: %+v, keep stale row", dirPath, err)
			continue
		}
		snaps := make([]model.CachedObj, 0, len(objs))
		for _, o := range objs {
			snaps = append(snaps, toCachedObj(dirPath, o))
		}
		if err := UpsertCacheList(d.ID, dirPath, snaps); err != nil {
			log.Errorf("cache: sync upsert %s: %+v", dirPath, err)
		}
		for i := range snaps {
			if snaps[i].IsFolder {
				child := stdpath.Join(dirPath, snaps[i].Name)
				if !known[child] {
					known[child] = true
					queue = append(queue, child)
				}
			}
		}
	}
}
