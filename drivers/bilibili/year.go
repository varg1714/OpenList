package bilibili

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

// ---- 年份拆分（spec 2026-09-07：M1 内存投影，快照层零改动）----

// videoYearOf 条目年份（关注 = 发布时间；收藏夹 = FavTime——Pubdate 字段即收藏时间）。
// Pubdate<=0（防御，真实数据不存在）→ 0（不进年份目录，旧 3 段路径仍可解析）。
func videoYearOf(v VideoItem) int {
	if v.Pubdate <= 0 {
		return 0
	}
	return time.Unix(v.Pubdate, 0).Year()
}

// yearSegRe 年份段："20xx" 四位
var yearSegRe = regexp.MustCompile(`^20\d\d$`)

// parseYearSeg 段解析为年份（段必须形如 "20xx"）
func parseYearSeg(seg string) (int, bool) {
	if !yearSegRe.MatchString(seg) {
		return 0, false
	}
	year, err := strconv.Atoi(seg)
	if err != nil {
		return 0, false
	}
	return year, true
}

// parseYearDirID 解析 "{prefix}{数字}_{年份}" 目录 ID（up_{mid}_{year} / fav_{id}_{year}）；
// 纯 "{prefix}{数字}"（父目录 ID）与无关前缀不匹配。
func parseYearDirID(id, prefix string) (int64, int, bool) {
	rest, ok := strings.CutPrefix(id, prefix)
	if !ok || rest == "" {
		return 0, 0, false
	}
	i := strings.LastIndex(rest, "_")
	if i <= 0 || i == len(rest)-1 {
		return 0, 0, false
	}
	base, err := strconv.ParseInt(rest[:i], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	year, err := strconv.Atoi(rest[i+1:])
	if err != nil {
		return 0, 0, false
	}
	return base, year, true
}

// videoYearGroup 单年份分组
type videoYearGroup struct {
	Year  int
	Items []VideoItem
}

// groupVideosByYear 按年份分组（年份降序输出；year<=0 排除；组内保持入序 = API pubdate 倒序）
func groupVideosByYear(items []VideoItem) []videoYearGroup {
	byYear := make(map[int][]VideoItem)
	for _, it := range items {
		if yy := videoYearOf(it); yy > 0 {
			byYear[yy] = append(byYear[yy], it)
		}
	}
	years := make([]int, 0, len(byYear))
	for yy := range byYear {
		years = append(years, yy)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(years)))
	groups := make([]videoYearGroup, 0, len(years))
	for _, yy := range years {
		groups = append(groups, videoYearGroup{Year: yy, Items: byYear[yy]})
	}
	return groups
}

// hasVideoYear 是否存在某年份的视频（年份目录存在性判定）
func hasVideoYear(items []VideoItem, year int) bool {
	for _, it := range items {
		if videoYearOf(it) == year {
			return true
		}
	}
	return false
}

// yearFolderObj 年份目录对象：Ctime = 当年 1 月 1 日（确定性——emby_wrapper 按 Ctime
// 升序分配季号，S01 = 最早年份；跨刷新稳定，迁移脚本可复算）
func yearFolderObj(parent model.Obj, name, id string, year int) model.Obj {
	o := folderObj(parent, name, id).(*model.Object)
	o.Ctime = time.Date(year, 1, 1, 0, 0, 0, 0, time.Local)
	return o
}

// buildYearDirs 父目录（UP/收藏夹）展示层：条目按年份分组 → 年份目录列表（年份降序）。
// idPrefix = "up_{mid}" / "fav_{media_id}"；年份目录 ID = idPrefix + "_{year}"
func buildYearDirs(parent model.Obj, idPrefix string, items []VideoItem) ([]model.Obj, error) {
	groups := groupVideosByYear(items)
	objs := make([]model.Obj, 0, len(groups))
	for _, g := range groups {
		ys := strconv.Itoa(g.Year)
		objs = append(objs, yearFolderObj(parent, ys, idPrefix+"_"+ys, g.Year))
	}
	return objs, nil
}

// buildVideoObjs 目录内视频展示层：sanitize + 目录内重名消歧 + videoObj，保持 items 顺序。
// 拆分后消歧作用域 = 年份目录内（跨年同名不再互消歧，spec 2026-09-07）。
func buildVideoObjs(dir model.Obj, items []VideoItem) ([]model.Obj, error) {
	displays := make([]string, len(items))
	suffixes := make([]string, len(items))
	for i, v := range items {
		displays[i] = sanitizeName(v.Title, 150)
		suffixes[i] = v.Bvid
	}
	names := disambiguate(displays, suffixes)
	objs := make([]model.Obj, 0, len(items))
	for i := range items {
		objs = append(objs, newVideoObj(dir, items[i], names[i]))
	}
	return objs, nil
}

// resolveVideoLeaf 深路径叶段（{UP}/{收藏夹} 之后仅 1 段）：年份目录（该年有内容）优先；
// 否则按视频名匹配（带或不带 .mp4，旧格式兼容）。dir = UP/收藏夹目录 obj。
func resolveVideoLeaf(dir model.Obj, idPrefix, seg string, items []VideoItem) (model.Obj, error) {
	if year, ok := parseYearSeg(seg); ok && hasVideoYear(items, year) {
		return yearFolderObj(dir, seg, idPrefix+"_"+seg, year), nil
	}
	item := matchVideoItem(items, seg)
	if item == nil {
		return nil, errs.ObjectNotFound
	}
	return newVideoObj(dir, *item, strings.TrimSuffix(seg, ".mp4")), nil
}

// resolveYearVideo 4 段路径 {年份}/{视频}：年份段必须存在且与条目 pubdate 年份一致；
// 视频名在该年份范围内匹配（跨年同名互不干扰，spec §2 年份目录语义）。
func resolveYearVideo(dir model.Obj, idPrefix, yearSeg, videoSeg string, items []VideoItem) (model.Obj, error) {
	year, ok := parseYearSeg(yearSeg)
	if !ok {
		return nil, errs.ObjectNotFound
	}
	scoped := make([]VideoItem, 0, len(items))
	for _, it := range items {
		if videoYearOf(it) == year {
			scoped = append(scoped, it)
		}
	}
	item := matchVideoItem(scoped, videoSeg)
	if item == nil {
		return nil, errs.ObjectNotFound
	}
	yearDir := yearFolderObj(dir, yearSeg, idPrefix+"_"+yearSeg, year)
	return newVideoObj(yearDir, *item, strings.TrimSuffix(videoSeg, ".mp4")), nil
}
