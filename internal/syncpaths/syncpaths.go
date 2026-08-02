package syncpaths

import (
	"strings"

	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	log "github.com/sirupsen/logrus"
)

// DirDepth returns the depth of a driver-relative path; "/" has depth 0.
func DirDepth(dirPath string) int {
	if dirPath == "/" {
		return 0
	}
	return strings.Count(strings.Trim(dirPath, "/"), "/") + 1
}

// ParseSyncPaths parses a whitelist string (newline/comma separated) into a
// cleaned, de-duplicated path list. Returns nil when there are no valid entries.
func ParseSyncPaths(raw string) []string {
	raw = strings.ReplaceAll(raw, ",", "\n")
	seen := make(map[string]bool)
	var res []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || seen[utils.FixAndCleanPath(line)] {
			continue
		}
		p := utils.FixAndCleanPath(line)
		seen[p] = true
		res = append(res, p)
	}
	return res
}

// WithinSyncPaths reports whether relPath (driver-relative) lies inside any
// whitelist entry's subtree.
func WithinSyncPaths(relPath string, entries []string) bool {
	for _, e := range entries {
		if utils.IsSubPath(e, relPath) {
			return true
		}
	}
	return false
}

// ToRelEntries parses the whitelist (downstream actual-path coordinates) and
// converts it to driver-relative coordinates under actualPath. Entries not
// under actualPath are logged and dropped. Returns relEntries and whether the
// whitelist is enabled (raw contains any non-blank content).
func ToRelEntries(actualPath, raw string) ([]string, bool) {
	if strings.TrimSpace(raw) == "" {
		return nil, false
	}
	var rel []string
	for _, w := range ParseSyncPaths(raw) {
		if !utils.IsSubPath(actualPath, w) {
			log.Warnf("syncpaths: sync path %s is not under actual path %s, ignored", w, actualPath)
			continue
		}
		rel = append(rel, utils.FixAndCleanPath(strings.TrimPrefix(w, actualPath)))
	}
	return rel, true
}
