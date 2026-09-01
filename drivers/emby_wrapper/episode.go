package emby_wrapper

import (
	"fmt"
	stdpath "path"
	"regexp"
	"strings"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
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
	root      string               // 剧集根目录规范路径
	byVirtual map[string]model.Obj // 小写虚拟名（含扩展名）→ 真实对象
	titles    map[string]string    // 小写虚拟名 → 集名（原文件名去扩展名）
	names     map[string]string    // 小写虚拟名 → 虚拟名（保留原样）
	nfoBases  map[string]string    // 小写虚拟名去扩展名 → 虚拟名
	byReal    map[string]string    // 规范真实路径 → 虚拟名
	seasonNo  map[string]int       // 直接子文件夹规范路径 → 季号
	last      model.Obj            // 排序后最新的视频对象（tvshow.nfo 时间戳用，无视频时为 nil）
}

// addEpisode 将真实对象登记为一个剧集条目（虚拟名 → 真实对象及各派生映射）。
// canonicalPath 为 wrapper 命名空间下的真实路径。
func (idx *tvIndex) addEpisode(real model.Obj, canonicalPath, virtualName string) {
	key := strings.ToLower(virtualName)
	idx.byVirtual[key] = real
	idx.names[key] = virtualName
	ext := stdpath.Ext(real.GetName())
	idx.titles[key] = strings.TrimSuffix(real.GetName(), ext)
	idx.nfoBases[strings.ToLower(strings.TrimSuffix(virtualName, stdpath.Ext(virtualName)))] = virtualName
	idx.byReal[canonicalPath] = virtualName
	idx.last = real
}

// resolve 按虚拟名（含扩展名，大小写不敏感）反查真实对象；未命中返回 nil。
func (idx *tvIndex) resolve(virtualName string) model.Obj {
	return idx.byVirtual[strings.ToLower(virtualName)]
}

// episodeName 返回真实对象（规范路径）对应的虚拟名。
func (idx *tvIndex) episodeName(realObj model.Obj) (string, bool) {
	name, ok := idx.byReal[realObj.GetPath()]
	return name, ok
}
