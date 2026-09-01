package emby_wrapper

import (
	"fmt"
	stdpath "path"
	"regexp"
	"strings"

	"github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

// nfoMultiPartRegexp 匹配多段影片后缀：javdb 风格的 -cd1 与通用媒体库的 .cd1/.CD1。
var nfoMultiPartRegexp = regexp.MustCompile(`(?i)[-.]cd\d+$`)

// nfoBaseName 将影片文件名归一化为 nfo basename：去扩展名 + 去多段 CD 后缀。
func nfoBaseName(fileName string) string {
	base := fileName
	if index := strings.LastIndex(base, "."); index != -1 {
		base = base[:index]
	}
	return nfoMultiPartRegexp.ReplaceAllString(base, "")
}

// virtualNFO 内存构建的 nfo 文件对象，content 为完整 XML 内容。
type virtualNFO struct {
	model.Object
	content []byte
}

// splitActors 按中英文逗号拆分演员列表并去除空白项。
func splitActors(actors string) []string {
	var out []string
	for _, a := range strings.FieldsFunc(actors, func(r rune) bool { return r == ',' || r == '，' }) {
		if a = strings.TrimSpace(a); a != "" {
			out = append(out, a)
		}
	}
	return out
}

// plotFileName 取去扩展名的原始文件名（保留 cd 等原始部分，仅去最后一个扩展名）。
func plotFileName(fileName string) string {
	if index := strings.LastIndex(fileName, "."); index != -1 {
		return fileName[:index]
	}
	return fileName
}

// buildPlot 计算 nfo plot：append 开启时拼接 plot + '-' + 文件名（plot 未设置则直接用文件名）。
func buildPlot(plot string, appendFlag *bool, fileName string) string {
	if appendFlag == nil || !*appendFlag {
		return plot
	}
	name := plotFileName(fileName)
	if plot == "" {
		return name
	}
	return plot + "-" + name
}

// buildNFOWithRoot 构建指定根元素（movie/tvshow/episodedetails）的 nfo XML：title + plot + actor。
func buildNFOWithRoot(root, title, plot string, setting *model.EmbyDirSetting) ([]byte, error) {
	actors := splitActors(setting.Actors)
	actorInfos := make([]virtual_file.Actor, 0, len(actors))
	for _, a := range actors {
		actorInfos = append(actorInfos, virtual_file.Actor{Name: a})
	}
	return virtual_file.RenderNFO(root, &virtual_file.Media{
		Title: virtual_file.Inner{Inner: fmt.Sprintf("<![CDATA[%s]]>", title)},
		Plot:  virtual_file.Inner{Inner: fmt.Sprintf("<![CDATA[%s]]>", plot)},
		Actor: actorInfos,
	})
}

// buildNFOContent 构建与 javdb 格式一致的影片 nfo XML：title + actor + plot。
// plot 配置后 title 与 plot 同值（用户确认：plot 选项同时作用于 title 与 plot）；
// plot 未配置时 title 保持影片文件名（归一化）。
func buildNFOContent(title, fileName string, setting *model.EmbyDirSetting) ([]byte, error) {
	plot := buildPlot(setting.Plot, setting.AppendFileNameToPlot, fileName)
	nfoTitle := title
	if setting.Plot != "" {
		nfoTitle = plot
	}
	return buildNFOWithRoot("movie", nfoTitle, plot, setting)
}

// buildEpisodeNFO 构建剧集 nfo：title（原文件名去扩展名）+ actor，无 plot（plot 属于剧集级 tvshow.nfo）。
func buildEpisodeNFO(title string, setting *model.EmbyDirSetting) ([]byte, error) {
	return buildNFOWithRoot("episodedetails", title, "", setting)
}

// buildTVShowNFO 构建剧集级 nfo：title（自定义剧名）+ plot（剧集介绍）+ actor。
func buildTVShowNFO(showName, plot string, setting *model.EmbyDirSetting) ([]byte, error) {
	return buildNFOWithRoot("tvshow", showName, plot, setting)
}

// buildSeasonNFO 构建季 nfo：<season><seasonnumber>N</seasonnumber><seasonname>名称</seasonname></season>。
// Emby 的 SeasonNfoParser 读取 seasonnumber 设置季号、seasonname 设置显示名，
// 使任意命名的文件夹（如"2024年"）被识别为指定季。
func buildSeasonNFO(seasonNo int, name string) []byte {
	return []byte(fmt.Sprintf(`<?xml version="1.0" encoding="utf-8" standalone="yes"?>
<season>
  <seasonnumber>%d</seasonnumber>
  <seasonname><![CDATA[%s]]></seasonname>
</season>`, seasonNo, name))
}

// withVirtualNFOs 为 dirPath 下每个影片文件追加一个虚拟 nfo；
// 真实同名 nfo 优先（跳过虚拟生成）；同归一化 basename 只生成一个。
func (d *EmbyWrapper) withVirtualNFOs(dirPath string, objs []model.Obj) []model.Obj {
	setting, err := d.resolveSetting(dirPath)
	if err != nil {
		utils.Log.Warnf("emby wrapper: resolve setting %s: %+v", dirPath, err)
		return objs
	}
	if setting == nil {
		return objs
	}

	realNFO := map[string]bool{}
	for _, o := range objs {
		if o.IsDir() {
			continue
		}
		name := o.GetName()
		if strings.EqualFold(utils.Ext(name), "nfo") {
			// key 统一小写：真实 aaa.nfo 应能挡住影片 AAA.mkv 的虚拟 AAA.nfo
			realNFO[strings.ToLower(nfoBaseName(name)+".nfo")] = true
		}
	}

	out := objs
	added := map[string]bool{}
	for _, o := range objs {
		if o.IsDir() {
			continue
		}
		ext := utils.Ext(o.GetName())
		if _, ok := d.supportSuffix[ext]; !ok {
			continue
		}
		nfoName := nfoBaseName(o.GetName()) + ".nfo"
		if realNFO[strings.ToLower(nfoName)] || added[nfoName] {
			continue
		}
		title := strings.TrimSuffix(nfoName, ".nfo")
		content, err := buildNFOContent(title, o.GetName(), setting)
		if err != nil {
			utils.Log.Warnf("emby wrapper: build nfo %s: %+v", nfoName, err)
			continue
		}
		added[nfoName] = true
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
	return out
}
