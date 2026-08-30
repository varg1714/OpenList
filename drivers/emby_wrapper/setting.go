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
// （*bool，nil=未配置；false 不落库，等于清除并恢复上层继承）。
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
