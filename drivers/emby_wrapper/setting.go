package emby_wrapper

import (
	stdpath "path"
	"strings"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

// resolveSetting 返回 dirPath 生效的目录设置（分维度合并，各字段独立继承）。
// 距离优先：自底向上遇到的第一个有效值即生效（用户确认的语义：近处设置优先于祖先设置）。
// actors 维度：手动 actors 非空，或 use_name_as_actor 开启者（非 origin 自身，合成开启者之下
// 第一段目录名）；plot 维度：最近的非空 Plot；append 维度：最近的显式 AppendFileNameToPlot
// （*bool，nil=未配置，false=显式关闭并阻断上层继承）。
// 任一维度命中即返回合成行（DirPath = 被解析目录，仅作溯源；消费方只应读取 Actors/Plot/
// AppendFileNameToPlot）；全部未命中返回 nil。
func (d *EmbyWrapper) resolveSetting(dirPath string) (*model.EmbyDirSetting, error) {
	dirPath = utils.FixAndCleanPath(dirPath)
	origin := dirPath
	var actorsItem *model.EmbyDirSetting
	plot := ""
	var appendFlag *bool
	for {
		item, err := GetEmbyDirSetting(d.ID, dirPath)
		if err != nil {
			return nil, err
		}
		if item != nil {
			if actorsItem == nil {
				if strings.TrimSpace(item.Actors) != "" {
					actorsItem = item
				} else if item.UseNameAsActor && !utils.PathEqual(dirPath, origin) {
					rel := strings.TrimPrefix(origin, dirPath)
					rel = strings.TrimPrefix(rel, "/")
					if idx := strings.Index(rel, "/"); idx != -1 {
						rel = rel[:idx]
					}
					if rel != "" {
						actorsItem = &model.EmbyDirSetting{Actors: rel}
					}
				}
			}
			if plot == "" {
				plot = strings.TrimSpace(item.Plot)
			}
			if appendFlag == nil && item.AppendFileNameToPlot != nil {
				appendFlag = item.AppendFileNameToPlot
			}
		}
		if actorsItem != nil && plot != "" && appendFlag != nil {
			break
		}
		if utils.PathEqual(dirPath, "/") {
			break
		}
		dirPath = stdpath.Dir(dirPath)
	}
	if actorsItem == nil && plot == "" && appendFlag == nil {
		return nil, nil
	}
	result := &model.EmbyDirSetting{
		StorageID:            d.ID,
		DirPath:              origin,
		Plot:                 plot,
		AppendFileNameToPlot: appendFlag,
	}
	if actorsItem != nil {
		result.Actors = actorsItem.Actors
		result.UseNameAsActor = actorsItem.UseNameAsActor
	}
	return result, nil
}

// tvShowInfo 返回 dirPath 是否为电视剧文件夹及剧名（自定义剧名为空时回退文件夹名）。
// 本地生效，不继承（子文件夹是否为电视剧由自身标记决定）。
func (d *EmbyWrapper) tvShowInfo(dirPath string) (string, bool, error) {
	item, err := GetEmbyDirSetting(d.ID, dirPath)
	if err != nil {
		return "", false, err
	}
	if item == nil || !item.TvShow {
		return "", false, nil
	}
	name := strings.TrimSpace(item.TvShowName)
	if name == "" {
		name = stdpath.Base(dirPath)
	}
	return name, true, nil
}

// isTVDir 判断 dirPath 是否被标记为电视剧（本地标记，不继承）。
func (d *EmbyWrapper) isTVDir(dirPath string) (bool, error) {
	item, err := GetEmbyDirSetting(d.ID, dirPath)
	if err != nil {
		return false, err
	}
	return item != nil && item.TvShow, nil
}

// tvShowAncestor 返回 dirPath 最近的电视剧祖先（含自身）：根路径 + 剧名。
// 任一目录的 List/Get 先经此判断走 TV 分支还是影片分支。
func (d *EmbyWrapper) tvShowAncestor(dirPath string) (string, string, bool, error) {
	dirPath = utils.FixAndCleanPath(dirPath)
	for {
		if name, ok, err := d.tvShowInfo(dirPath); err != nil {
			return "", "", false, err
		} else if ok {
			return dirPath, name, true, nil
		}
		if utils.PathEqual(dirPath, "/") {
			break
		}
		dirPath = stdpath.Dir(dirPath)
	}
	return "", "", false, nil
}
