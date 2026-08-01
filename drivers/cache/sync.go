package cache

import (
	"context"
	stdpath "path"
	"sort"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	log "github.com/sirupsen/logrus"
)

func dirDepth(dirPath string) int {
	if dirPath == "/" {
		return 0
	}
	return strings.Count(strings.Trim(dirPath, "/"), "/") + 1
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
			continue
		}
		objs, err := op.List(ctx, remoteStorage, stdpath.Join(remoteActualPath, dirPath), model.ListArgs{})
		if err != nil {
			log.Errorf("cache: sync %s: %+v, drop row", dirPath, err)
			_ = DeleteCacheList(d.ID, dirPath)
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
