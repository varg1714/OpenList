package emby_wrapper

import (
	"context"
	"fmt"
	stdpath "path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

// episodePattern 匹配已含 Emby 剧集编号的文件名（SxxExx 及其变体、NxNN）。
// 命中视为已编号，保持原名、跳过时间排序编号。
var episodePattern = regexp.MustCompile(`(?i)(?:^|[._ -])(?:s\d{1,4}[. _-]*e\d{1,3}|\d{1,4}x\d{1,3})(?:[._ -]|$)`)

// isNumberedEpisode 判断文件名是否已含剧集编号。
func isNumberedEpisode(fileName string) bool {
	return episodePattern.MatchString(fileName)
}

// episodeVirtualName 为未编号文件生成虚拟剧集名：S{季号}E{集号}+原扩展名。
// 不带原文件名前缀：Emby 的 EpisodePathParser 会把 SxxExx 前的任意前缀捕获为
// seriesname（剧集名），前缀与剧名不符时会导致剧集归属错乱（观察到 xx1-S01E01.mp4
// 被识别为名为 xx1 的剧）；原文件名经剧集 nfo 的 title 保留（见 buildEpisodeNFO）。
func episodeVirtualName(fileName string, seasonNo, epNo int) string {
	ext := stdpath.Ext(fileName)
	return fmt.Sprintf("S%02dE%02d%s", seasonNo, epNo, ext)
}

// byCreateTimeName 比较两个对象的排序键：创建时间升序（CreateTime 为零时回退
// ModTime，model.Object.CreateTime 已内置该回退），时间相同按名称升序。
// 保证任何输入集都产生确定性顺序。
func byCreateTimeName(a, b model.Obj) bool {
	at, bt := a.CreateTime(), b.CreateTime()
	if !at.Equal(bt) {
		return at.Before(bt)
	}
	return a.GetName() < b.GetName()
}

// tvEntry 索引中的一条文件条目：虚拟展示名 + 真实对象与真实规范路径。
type tvEntry struct {
	real model.Obj // 真实对象（ModTime/Size/GetName 委托）
	name string    // 虚拟展示名（S01E01.mp4；保持原名的文件为原文件名）
	path string    // 真实规范路径（wrapper 命名空间）
}

// realNamed 判断条目是否保持原名（已编号文件/非视频文件；生成的剧集名带 SxxExx 且与原名不同）。
// 真实同名条目在虚拟路径冲突时优先（真实文件优先语义）。
func realNamed(e tvEntry) bool {
	return strings.EqualFold(e.name, e.real.GetName())
}

// tvIndex 一部电视剧的完整索引。展示结构（用户确认 2026-09-02 修订）：
// 根目录直接文件 = 第 1 季（展示于剧集根）；直接子文件夹 = 季——按创建时间+名称排序
// 分配连续季号，虚拟映射为 S{季号} 目录（原文件夹名不再展示），季内全部文件（含嵌套
// 子文件夹，跳过自身标记为 TV 的）提取到该 S 目录下：视频编号为 S{季}E{集}.mp4，
// 非视频保留原名。一次构建，List 展示与 Get 反查共用。
type tvIndex struct {
	root        string             // 剧集根（真实规范路径）
	byVirtual   map[string]tvEntry // 小写虚拟路径（含季别名段）→ 条目
	nfoBases    map[string]string  // 小写虚拟 nfo 路径（去 .nfo 扩展名）→ 虚拟文件名
	byReal      map[string]string  // 真实规范路径 → 虚拟展示名
	seasonNo    map[string]int     // 真实季文件夹路径 → 季号
	seasonAlias map[string]string  // 真实季文件夹路径 → 虚拟季路径（/A/2024年 → /A/S02）
	last        model.Obj          // 最后登记的条目对象（tvshow.nfo 时间戳参考），无条目时为 nil
}

// addEpisode 登记一条文件条目。virtualDir 为展示目录（剧集根或季别名路径）；
// virtualName 为展示文件名（生成的 S{NN}E{MM} 或保持的原名）。
// 虚拟路径冲突时真实同名条目优先（真实文件优先语义），已存在的真实同名条目不被覆盖。
func (idx *tvIndex) addEpisode(real model.Obj, realPath, virtualDir, virtualName string) {
	virtualPath := stdpath.Join(virtualDir, virtualName)
	key := strings.ToLower(virtualPath)
	if existing, ok := idx.byVirtual[key]; ok {
		if realNamed(existing) {
			return // 已有真实同名条目占用该虚拟路径：不再覆盖
		}
		if !realNamed(tvEntry{real: real, name: virtualName}) {
			return // 两个生成名冲突（理论不可能：季号+集号唯一）
		}
	}
	idx.byVirtual[key] = tvEntry{real: real, name: virtualName, path: realPath}
	idx.nfoBases[strings.ToLower(strings.TrimSuffix(virtualPath, stdpath.Ext(virtualName)))] = virtualName
	idx.byReal[realPath] = virtualName
	idx.last = real
}

// entry 按虚拟路径（大小写不敏感）反查条目。
func (idx *tvIndex) entry(virtualPath string) (tvEntry, bool) {
	e, ok := idx.byVirtual[strings.ToLower(virtualPath)]
	return e, ok
}

// episodeName 返回真实对象（规范路径）对应的虚拟展示名。
func (idx *tvIndex) episodeName(realObj model.Obj) (string, bool) {
	name, ok := idx.byReal[realObj.GetPath()]
	return name, ok
}

// seasonRealOf 返回季的展示路径（别名或真实路径）对应的真实季文件夹路径。
func (idx *tvIndex) seasonRealOf(aliasOrReal string) (string, bool) {
	if _, ok := idx.seasonNo[aliasOrReal]; ok {
		return aliasOrReal, true
	}
	for realDir, alias := range idx.seasonAlias {
		if utils.PathEqual(alias, aliasOrReal) {
			return realDir, true
		}
	}
	return "", false
}

// tvFile 收集到的文件：真实对象 + 规范真实路径（wrapper 命名空间）。
type tvFile struct {
	obj  model.Obj
	path string
}

// buildTVIndex 构建整个剧集树的索引：列根目录 → 直接子文件夹（跳过自身标记为
// TV 的，独立成剧）按创建时间+名称排序分配连续季号（根目录存在直接视频时从第 2 季
// 起，否则从第 1 季起）并映射为 S{季号} 别名 → 各季递归收集全部文件 → 季内视频
// 按创建时间+名称编号、非视频保留原名。一次构建，List 展示与 Get 反查共用。
func (d *EmbyWrapper) buildTVIndex(ctx context.Context, rootPath string) (*tvIndex, error) {
	rootPath = utils.FixAndCleanPath(rootPath)
	idx := &tvIndex{
		root:        rootPath,
		byVirtual:   map[string]tvEntry{},
		nfoBases:    map[string]string{},
		byReal:      map[string]string{},
		seasonNo:    map[string]int{},
		seasonAlias: map[string]string{},
	}
	remoteStorage, remoteActualPath, err := d.remote()
	if err != nil {
		return nil, err
	}
	objs, err := op.List(ctx, remoteStorage, stdpath.Join(remoteActualPath, rootPath), model.ListArgs{})
	if err != nil {
		return nil, err
	}
	var rootVideos []model.Obj
	var seasonDirs []model.Obj
	for _, o := range objs {
		if o.IsDir() {
			isTV, err := d.isTVDir(stdpath.Join(rootPath, o.GetName()))
			if err != nil {
				return nil, err
			}
			if !isTV {
				seasonDirs = append(seasonDirs, o)
			}
			continue
		}
		if _, ok := d.supportSuffix[utils.Ext(o.GetName())]; ok {
			rootVideos = append(rootVideos, o)
		}
	}
	// 根目录直接视频 = 第 1 季（展示目录 = 剧集根）
	sort.SliceStable(rootVideos, func(i, j int) bool { return byCreateTimeName(rootVideos[i], rootVideos[j]) })
	ep := 0
	for _, o := range rootVideos {
		canonical := stdpath.Join(rootPath, o.GetName())
		if isNumberedEpisode(o.GetName()) {
			idx.addEpisode(o, canonical, rootPath, o.GetName())
			continue
		}
		ep++
		idx.addEpisode(o, canonical, rootPath, episodeVirtualName(o.GetName(), 1, ep))
	}
	// 直接子文件夹：创建时间+名称排序 → 连续季号 + S{季号} 别名
	sort.SliceStable(seasonDirs, func(i, j int) bool { return byCreateTimeName(seasonDirs[i], seasonDirs[j]) })
	seasonBase := 1
	if len(rootVideos) > 0 {
		seasonBase = 2
	}
	for i, dir := range seasonDirs {
		dirPath := stdpath.Join(rootPath, dir.GetName())
		seasonNo := seasonBase + i
		idx.seasonNo[dirPath] = seasonNo
		alias := stdpath.Join(rootPath, fmt.Sprintf("S%02d", seasonNo))
		idx.seasonAlias[dirPath] = alias
		if err := d.collectSeasonVideos(ctx, idx, remoteStorage, remoteActualPath, dirPath, alias, seasonNo); err != nil {
			return nil, err
		}
	}
	return idx, nil
}

// collectSeasonVideos 递归收集季文件夹下全部文件并登记进索引（展示目录 = 季别名，
// 扁平化：嵌套子文件夹不再展示）：视频按创建时间+名称编号（S{季}E{集}，已编号保持
// 原名），非视频保留原名。
func (d *EmbyWrapper) collectSeasonVideos(ctx context.Context, idx *tvIndex, remoteStorage driver.Driver, remoteActualPath, dirPath, virtualDir string, seasonNo int) error {
	files, err := d.gatherFiles(ctx, remoteStorage, remoteActualPath, dirPath)
	if err != nil {
		return err
	}
	var videos []tvFile
	for _, f := range files {
		if _, ok := d.supportSuffix[utils.Ext(f.obj.GetName())]; ok {
			videos = append(videos, f)
		} else {
			idx.addEpisode(f.obj, f.path, virtualDir, f.obj.GetName())
		}
	}
	sort.SliceStable(videos, func(i, j int) bool { return byCreateTimeName(videos[i].obj, videos[j].obj) })
	ep := 0
	for _, f := range videos {
		if isNumberedEpisode(f.obj.GetName()) {
			idx.addEpisode(f.obj, f.path, virtualDir, f.obj.GetName())
			continue
		}
		ep++
		idx.addEpisode(f.obj, f.path, virtualDir, episodeVirtualName(f.obj.GetName(), seasonNo, ep))
	}
	return nil
}

// gatherFiles 递归收集 dirPath 下全部文件（跳过自身标记为 TV 的子文件夹——独立成剧）。
func (d *EmbyWrapper) gatherFiles(ctx context.Context, remoteStorage driver.Driver, remoteActualPath, dirPath string) ([]tvFile, error) {
	objs, err := op.List(ctx, remoteStorage, stdpath.Join(remoteActualPath, dirPath), model.ListArgs{})
	if err != nil {
		return nil, err
	}
	var out []tvFile
	for _, o := range objs {
		if o.IsDir() {
			child := stdpath.Join(dirPath, o.GetName())
			isTV, err := d.isTVDir(child)
			if err != nil {
				return nil, err
			}
			if isTV {
				continue
			}
			sub, err := d.gatherFiles(ctx, remoteStorage, remoteActualPath, child)
			if err != nil {
				return nil, err
			}
			out = append(out, sub...)
			continue
		}
		out = append(out, tvFile{obj: o, path: stdpath.Join(dirPath, o.GetName())})
	}
	return out, nil
}

// tvPathContext 一次 TV 路径解析的上下文。
type tvPathContext struct {
	root     string // 剧集根（真实规范路径）
	showName string
	realDir  string // 下游真实目录（List 用）
	viewDir  string // 展示目录（剧集根或季别名路径；索引过滤用）
	rootView bool   // 剧集根视图
	isSeason bool   // 季视图（扁平化展示）
}

// tvContext 解析 path 的 TV 上下文：最近的电视剧祖先 + 完整索引。
// 返回 (nil, nil, nil) 表示 path 不在任何电视剧内。
func (d *EmbyWrapper) tvContext(ctx context.Context, path string) (*tvPathContext, *tvIndex, error) {
	path = utils.FixAndCleanPath(path)
	root, showName, ok, err := d.tvShowAncestor(path)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, nil
	}
	idx, err := d.buildTVIndex(ctx, root)
	if err != nil {
		return nil, nil, err
	}
	pc := &tvPathContext{root: root, showName: showName}
	if utils.PathEqual(path, root) {
		pc.realDir, pc.viewDir, pc.rootView = root, root, true
		return pc, idx, nil
	}
	if seasonReal, ok := idx.seasonRealOf(path); ok {
		pc.realDir = seasonReal
		pc.viewDir = idx.seasonAlias[seasonReal]
		pc.isSeason = true
		return pc, idx, nil
	}
	// 其他（季内嵌套真实目录等历史路径）：原样透传
	pc.realDir, pc.viewDir = path, path
	return pc, idx, nil
}

// withTVShowNFOs TV 模式展示（2026-09-02 修订版：季目录虚拟映射为 S{季号}，季内扁平化）：
//   - 剧集根视图：季文件夹以 S{季号} 别名展示（真实路径保留），嵌套 TV 文件夹原样；
//     根直接视频映射为虚拟剧集 + 剧集 nfo；tvshow.nfo。
//   - 季视图：季内全部条目（视频虚拟名 + 非视频原名）扁平展示 + season.nfo；
//     嵌套 TV 文件夹原样保留（独立成剧，可访问）。
//   - 真实同名 nfo/文件优先；生成的虚拟名与真实同名条目冲突时真实条目占用。
func (d *EmbyWrapper) withTVShowNFOs(ctx context.Context, dir model.Obj, pc *tvPathContext, idx *tvIndex, objs []model.Obj) []model.Obj {
	if !pc.rootView && !pc.isSeason {
		return objs // 季内嵌套非 TV 真实目录（历史路径）：原样透传
	}
	setting, err := d.resolveSetting(pc.realDir)
	if err != nil {
		utils.Log.Warnf("emby wrapper: resolve setting %s: %+v", pc.realDir, err)
		return objs
	}
	if setting == nil {
		setting = &model.EmbyDirSetting{}
	}
	realNFO := map[string]bool{}
	realFiles := map[string]bool{}
	for _, o := range objs {
		if o.IsDir() {
			continue
		}
		name := o.GetName()
		if strings.EqualFold(utils.Ext(name), "nfo") {
			realNFO[strings.ToLower(nfoBaseName(name)+".nfo")] = true
			continue
		}
		realFiles[strings.ToLower(name)] = true
	}
	if pc.rootView {
		return d.emitTVRootView(pc, idx, setting, objs, realNFO, realFiles)
	}
	return d.emitTVSeasonView(pc, idx, setting, objs, realNFO)
}

// emitTVRootView 剧集根视图：季文件夹 → S{季号} 别名；嵌套 TV 文件夹原样；
// 根直接视频 → 虚拟剧集 + 剧集 nfo；tvshow.nfo。
func (d *EmbyWrapper) emitTVRootView(pc *tvPathContext, idx *tvIndex, setting *model.EmbyDirSetting, objs []model.Obj, realNFO, realFiles map[string]bool) []model.Obj {
	out := make([]model.Obj, 0, len(objs)+2)
	addedNFO := map[string]bool{}
	for _, o := range objs {
		if o.IsDir() {
			if alias, ok := idx.seasonAlias[o.GetPath()]; ok {
				out = append(out, newVirtualObj(o, stdpath.Base(alias), o.GetPath()))
			} else {
				out = append(out, o) // 嵌套 TV（独立成剧）等
			}
			continue
		}
		name := o.GetName()
		if strings.EqualFold(utils.Ext(name), "nfo") {
			out = append(out, o)
			continue
		}
		if _, ok := d.supportSuffix[utils.Ext(name)]; !ok {
			out = append(out, o)
			continue
		}
		epName, ok := idx.episodeName(o)
		if !ok {
			out = append(out, o)
			continue
		}
		if epName != name && realFiles[strings.ToLower(epName)] {
			// 虚拟名已被真实文件占用：原样展示，不生成虚拟剧集
			out = append(out, o)
			continue
		}
		out = append(out, newVirtualObj(o, epName, o.GetPath()))
		nfoName := strings.TrimSuffix(epName, stdpath.Ext(epName)) + ".nfo"
		if realNFO[strings.ToLower(nfoName)] || addedNFO[nfoName] {
			continue
		}
		content, err := buildEpisodeNFO(strings.TrimSuffix(name, stdpath.Ext(name)), setting)
		if err != nil {
			utils.Log.Warnf("emby wrapper: build episode nfo %s: %+v", nfoName, err)
			continue
		}
		addedNFO[nfoName] = true
		out = append(out, &virtualNFO{
			Object: model.Object{
				Name:     nfoName,
				Size:     int64(len(content)),
				Modified: o.ModTime(),
				Path:     stdpath.Join(pc.viewDir, nfoName),
				ID:       "vnfo-" + nfoName,
			},
			content: content,
		})
	}
	// tvshow.nfo（真实同名 nfo 优先）
	if !realNFO["tvshow.nfo"] {
		content, err := buildTVShowNFO(pc.showName, setting.Plot, setting)
		if err == nil {
			modified := time.Time{}
			if idx.last != nil {
				modified = idx.last.ModTime()
			}
			out = append(out, &virtualNFO{
				Object: model.Object{
					Name:     "tvshow.nfo",
					Size:     int64(len(content)),
					Modified: modified,
					Path:     stdpath.Join(pc.viewDir, "tvshow.nfo"),
					ID:       "vnfo-tvshow.nfo",
				},
				content: content,
			})
		}
	}
	return out
}

// emitTVSeasonView 季视图（扁平化）：季内全部条目展示于季别名目录下——
// 视频以虚拟名 + 剧集 nfo，非视频保留原名；嵌套 TV 文件夹原样保留；season.nfo。
func (d *EmbyWrapper) emitTVSeasonView(pc *tvPathContext, idx *tvIndex, setting *model.EmbyDirSetting, objs []model.Obj, realNFO map[string]bool) []model.Obj {
	out := make([]model.Obj, 0, len(objs)+4)
	// 嵌套 TV 文件夹原样保留（独立成剧，保持可访问）
	for _, o := range objs {
		if !o.IsDir() {
			continue
		}
		if isTV, err := d.isTVDir(stdpath.Join(pc.realDir, o.GetName())); err == nil && isTV {
			out = append(out, o)
		}
	}
	// 收集本季条目；真实同名 nfo 条目补充 realNFO（优先级）；生成的虚拟名被
	// 真实同名条目占用时跳过。
	var entries []tvEntry
	claimed := map[string]bool{}
	for key, e := range idx.byVirtual {
		if stdpath.Dir(key) != strings.ToLower(pc.viewDir) {
			continue
		}
		entries = append(entries, e)
		if realNamed(e) {
			claimed[key] = true
			if strings.EqualFold(utils.Ext(e.name), "nfo") {
				realNFO[strings.ToLower(e.name)] = true
			}
		}
	}
	addedNFO := map[string]bool{}
	for _, e := range entries {
		key := strings.ToLower(stdpath.Join(pc.viewDir, e.name))
		if claimed[key] && !realNamed(e) {
			continue // 生成的虚拟名被真实同名条目占用：真实条目展示
		}
		out = append(out, newVirtualObj(e.real, e.name, e.path))
		// 保持原名的条目：已编号视频保留同名剧集 nfo（title=原名）；非视频无 nfo
		if realNamed(e) {
			if _, ok := d.supportSuffix[utils.Ext(e.name)]; !ok {
				continue
			}
			nfoName := strings.TrimSuffix(e.name, stdpath.Ext(e.name)) + ".nfo"
			if realNFO[strings.ToLower(nfoName)] || addedNFO[nfoName] {
				continue
			}
			content, err := buildEpisodeNFO(strings.TrimSuffix(e.real.GetName(), stdpath.Ext(e.real.GetName())), setting)
			if err != nil {
				utils.Log.Warnf("emby wrapper: build episode nfo %s: %+v", nfoName, err)
				continue
			}
			addedNFO[nfoName] = true
			out = append(out, &virtualNFO{
				Object: model.Object{
					Name:     nfoName,
					Size:     int64(len(content)),
					Modified: e.real.ModTime(),
					Path:     stdpath.Join(pc.viewDir, nfoName),
					ID:       "vnfo-" + nfoName,
				},
				content: content,
			})
			continue
		}
		nfoName := strings.TrimSuffix(e.name, stdpath.Ext(e.name)) + ".nfo"
		if realNFO[strings.ToLower(nfoName)] || addedNFO[nfoName] {
			continue
		}
		content, err := buildEpisodeNFO(strings.TrimSuffix(e.real.GetName(), stdpath.Ext(e.real.GetName())), setting)
		if err != nil {
			utils.Log.Warnf("emby wrapper: build episode nfo %s: %+v", nfoName, err)
			continue
		}
		addedNFO[nfoName] = true
		out = append(out, &virtualNFO{
			Object: model.Object{
				Name:     nfoName,
				Size:     int64(len(content)),
				Modified: e.real.ModTime(),
				Path:     stdpath.Join(pc.viewDir, nfoName),
				ID:       "vnfo-" + nfoName,
			},
			content: content,
		})
	}
	// season.nfo（真实同名 nfo 优先；季号已由 S{季号} 目录名承载，nfo 仅作双保险）
	if !realNFO["season.nfo"] {
		content := buildSeasonNFO(idx.seasonNo[pc.realDir], "")
		modified := time.Time{}
		if idx.last != nil {
			modified = idx.last.ModTime()
		}
		out = append(out, &virtualNFO{
			Object: model.Object{
				Name:     "season.nfo",
				Size:     int64(len(content)),
				Modified: modified,
				Path:     stdpath.Join(pc.viewDir, "season.nfo"),
				ID:       "vnfo-season.nfo",
			},
			content: content,
		})
	}
	return out
}
