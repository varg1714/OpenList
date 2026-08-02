package cache

import (
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

// visibleInSyncPaths 判断 relPath 是否可见：与任一白名单条目同链
// （relPath 是条目的祖先/等于，或条目是 relPath 的祖先/等于）。
// 祖先目录作为导航路径展示（如条目 /电影/邻居 的祖先 /电影），
// 但定时扫描范围由 ScheduledSync 驱动经 syncpaths.WithinSyncPaths 限定。
func visibleInSyncPaths(relPath string, entries []string) bool {
	for _, e := range entries {
		if utils.IsSubPath(e, relPath) || utils.IsSubPath(relPath, e) {
			return true
		}
	}
	return false
}
