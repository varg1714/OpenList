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

// buildNFOContent 构建与 javdb 格式一致的 nfo XML：title + actor。
func buildNFOContent(title string, setting *model.EmbyDirSetting) ([]byte, error) {
	actors := splitActors(setting.Actors)
	actorInfos := make([]virtual_file.Actor, 0, len(actors))
	for _, a := range actors {
		actorInfos = append(actorInfos, virtual_file.Actor{Name: a})
	}
	return virtual_file.RenderMediaNFO(&virtual_file.Media{
		Title: virtual_file.Inner{Inner: fmt.Sprintf("<![CDATA[%s]]>", title)},
		Actor: actorInfos,
	})
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
		content, err := buildNFOContent(title, setting)
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
