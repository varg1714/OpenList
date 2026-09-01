package emby_wrapper

import (
	"context"
	"fmt"
	stdpath "path"
	"regexp"
	"sort"
	"strings"

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

// episodeVirtualName 为未编号文件生成虚拟剧集名：原基础名-S{季号}E{集号}+原扩展名。
func episodeVirtualName(fileName string, seasonNo, epNo int) string {
	ext := stdpath.Ext(fileName)
	return fmt.Sprintf("%s-S%02dE%02d%s", strings.TrimSuffix(fileName, ext), seasonNo, epNo, ext)
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

// tvIndex 一部电视剧的完整索引：根目录直接文件 = 第 1 季；直接子文件夹 = 季
// （按创建时间+名称排序分配连续季号，保留原名）。一次构建，List 展示与 Get 反查共用。
type tvIndex struct {
	root     string               // 剧集根目录规范路径
	byPath   map[string]model.Obj // 小写规范虚拟路径 → 真实对象（按目录限定，防跨季同名碰撞）
	titles   map[string]string    // 小写虚拟名 → 集名（原文件名去扩展名）
	nfoBases map[string]string    // 小写虚拟名去扩展名 → 虚拟名
	byReal   map[string]string    // 规范真实路径 → 虚拟名
	seasonNo map[string]int       // 直接子文件夹规范路径 → 季号
	last     model.Obj            // 最后登记的剧集对象（tvshow.nfo 时间戳参考），无视频时为 nil
}

// addEpisode 将真实对象登记为一个剧集条目（虚拟名 → 真实对象及各派生映射）。
// canonicalPath 为 wrapper 命名空间下的真实路径。
func (idx *tvIndex) addEpisode(real model.Obj, canonicalPath, virtualName string) {
	key := strings.ToLower(virtualName)
	idx.byPath[strings.ToLower(stdpath.Join(stdpath.Dir(canonicalPath), virtualName))] = real
	ext := stdpath.Ext(real.GetName())
	idx.titles[key] = strings.TrimSuffix(real.GetName(), ext)
	idx.nfoBases[strings.ToLower(strings.TrimSuffix(virtualName, stdpath.Ext(virtualName)))] = virtualName
	idx.byReal[canonicalPath] = virtualName
	idx.last = real
}

// episodeName 返回真实对象（规范路径）对应的虚拟名。
func (idx *tvIndex) episodeName(realObj model.Obj) (string, bool) {
	name, ok := idx.byReal[realObj.GetPath()]
	return name, ok
}

// tvFile 收集到的视频：真实对象 + 规范路径（wrapper 命名空间）。
type tvFile struct {
	obj  model.Obj
	path string
}

// buildTVIndex 构建整个剧集树的索引：列根目录 → 直接子文件夹（跳过自身标记为
// TV 的，独立成剧）按创建时间+名称排序分配连续季号（根目录存在直接视频时从第 2 季
// 起，否则从第 1 季起）→ 各季递归收集视频 → 季内按创建时间+名称编号。
// 一次构建，List 展示与 Get 反查共用。
func (d *EmbyWrapper) buildTVIndex(ctx context.Context, rootPath string) (*tvIndex, error) {
	rootPath = utils.FixAndCleanPath(rootPath)
	idx := &tvIndex{
		root:     rootPath,
		byPath:   map[string]model.Obj{},
		titles:   map[string]string{},
		nfoBases: map[string]string{},
		byReal:   map[string]string{},
		seasonNo: map[string]int{},
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
	// 根目录直接视频 = 第 1 季
	sort.SliceStable(rootVideos, func(i, j int) bool { return byCreateTimeName(rootVideos[i], rootVideos[j]) })
	ep := 0
	for _, o := range rootVideos {
		canonical := stdpath.Join(rootPath, o.GetName())
		if isNumberedEpisode(o.GetName()) {
			idx.addEpisode(o, canonical, o.GetName())
			continue
		}
		ep++
		idx.addEpisode(o, canonical, episodeVirtualName(o.GetName(), 1, ep))
	}
	// 直接子文件夹：创建时间+名称排序 → 连续季号
	sort.SliceStable(seasonDirs, func(i, j int) bool { return byCreateTimeName(seasonDirs[i], seasonDirs[j]) })
	seasonBase := 1
	if len(rootVideos) > 0 {
		seasonBase = 2
	}
	for i, dir := range seasonDirs {
		dirPath := stdpath.Join(rootPath, dir.GetName())
		idx.seasonNo[dirPath] = seasonBase + i
		if err := d.collectSeasonVideos(ctx, idx, remoteStorage, remoteActualPath, dirPath, seasonBase+i); err != nil {
			return nil, err
		}
	}
	return idx, nil
}

// collectSeasonVideos 递归收集季文件夹下所有视频并登记进索引，季内按创建时间+名称编号。
func (d *EmbyWrapper) collectSeasonVideos(ctx context.Context, idx *tvIndex, remoteStorage driver.Driver, remoteActualPath, dirPath string, seasonNo int) error {
	files, err := d.gatherVideos(ctx, remoteStorage, remoteActualPath, dirPath)
	if err != nil {
		return err
	}
	sort.SliceStable(files, func(i, j int) bool { return byCreateTimeName(files[i].obj, files[j].obj) })
	ep := 0
	for _, f := range files {
		if isNumberedEpisode(f.obj.GetName()) {
			idx.addEpisode(f.obj, f.path, f.obj.GetName())
			continue
		}
		ep++
		idx.addEpisode(f.obj, f.path, episodeVirtualName(f.obj.GetName(), seasonNo, ep))
	}
	return nil
}

// gatherVideos 递归收集 dirPath 下所有视频（跳过自身标记为 TV 的子文件夹）。
func (d *EmbyWrapper) gatherVideos(ctx context.Context, remoteStorage driver.Driver, remoteActualPath, dirPath string) ([]tvFile, error) {
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
			sub, err := d.gatherVideos(ctx, remoteStorage, remoteActualPath, child)
			if err != nil {
				return nil, err
			}
			out = append(out, sub...)
			continue
		}
		if _, ok := d.supportSuffix[utils.Ext(o.GetName())]; ok {
			out = append(out, tvFile{obj: o, path: stdpath.Join(dirPath, o.GetName())})
		}
	}
	return out, nil
}

// withTVShowNFOs TV 模式展示：当前目录的直接视频按索引映射为虚拟剧集（原地），
// 生成剧集 nfo（episodedetails：title=原文件名、actors、无 plot）；
// 剧集根目录追加 tvshow.nfo；直接子文件夹（季）追加 season.nfo。
// 真实同名 nfo/文件优先：同目录已存在同名文件或 nfo 时跳过虚拟生成。
func (d *EmbyWrapper) withTVShowNFOs(ctx context.Context, dir model.Obj, rootPath, showName string, objs []model.Obj) []model.Obj {
	dirPath := dir.GetPath()
	idx, err := d.buildTVIndex(ctx, rootPath)
	if err != nil {
		utils.Log.Warnf("emby wrapper: build tv index %s: %+v", rootPath, err)
		return objs
	}
	setting, err := d.resolveSetting(dirPath)
	if err != nil {
		utils.Log.Warnf("emby wrapper: resolve setting %s: %+v", dirPath, err)
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
	out := make([]model.Obj, 0, len(objs)+2)
	addedNFO := map[string]bool{}
	for _, o := range objs {
		if o.IsDir() {
			out = append(out, o)
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
		out = append(out, newVirtualEpisode(o, epName, o.GetPath()))
		nfoName := strings.TrimSuffix(epName, stdpath.Ext(epName)) + ".nfo"
		if realNFO[strings.ToLower(nfoName)] || addedNFO[nfoName] {
			continue
		}
		content, err := buildEpisodeNFO(idx.titles[strings.ToLower(epName)], setting)
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
				Path:     stdpath.Join(dirPath, nfoName),
				ID:       "vnfo-" + nfoName,
			},
			content: content,
		})
	}
	// tvshow.nfo：仅剧集根目录（真实同名 nfo 优先）
	if utils.PathEqual(dirPath, rootPath) && !realNFO["tvshow.nfo"] {
		content, err := buildTVShowNFO(showName, setting.Plot, setting)
		if err == nil {
			modified := dir.ModTime()
			if idx.last != nil {
				modified = idx.last.ModTime()
			}
			out = append(out, &virtualNFO{
				Object: model.Object{
					Name:     "tvshow.nfo",
					Size:     int64(len(content)),
					Modified: modified,
					Path:     stdpath.Join(dirPath, "tvshow.nfo"),
					ID:       "vnfo-tvshow.nfo",
				},
				content: content,
			})
		}
	}
	// season.nfo：直接子文件夹（季），真实同名 nfo 优先
	if !utils.PathEqual(dirPath, rootPath) && utils.PathEqual(stdpath.Dir(dirPath), rootPath) {
		if seasonNo, ok := idx.seasonNo[dirPath]; ok && !realNFO["season.nfo"] {
			content := buildSeasonNFO(seasonNo, stdpath.Base(dirPath))
			modified := dir.ModTime()
			if idx.last != nil {
				modified = idx.last.ModTime()
			}
			out = append(out, &virtualNFO{
				Object: model.Object{
					Name:     "season.nfo",
					Size:     int64(len(content)),
					Modified: modified,
					Path:     stdpath.Join(dirPath, "season.nfo"),
					ID:       "vnfo-season.nfo",
				},
				content: content,
			})
		}
	}
	return out
}
