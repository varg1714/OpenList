package emby_wrapper

import (
	stdpath "path"
	"strings"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

// resolveSetting 返回 dirPath 生效的目录设置（自身或最近祖先）。
// 优先级：手动 actors > use_name_as_actor（最近的开启者）> 无设置。
// use_name_as_actor 开启者自身不获得 actor（配置只作用于其直接子文件夹及后代）；
// 命中开启者时返回合成 setting，Actors = 开启者之下第一段目录名。
func (d *EmbyWrapper) resolveSetting(dirPath string) (*model.EmbyDirSetting, error) {
	dirPath = utils.FixAndCleanPath(dirPath)
	origin := dirPath
	for {
		item, err := GetEmbyDirSetting(d.ID, dirPath)
		if err != nil {
			return nil, err
		}
		if item != nil {
			if strings.TrimSpace(item.Actors) != "" {
				return item, nil
			}
			if item.UseNameAsActor && !utils.PathEqual(dirPath, origin) {
				rel := strings.TrimPrefix(origin, dirPath)
				rel = strings.TrimPrefix(rel, "/")
				if idx := strings.Index(rel, "/"); idx != -1 {
					rel = rel[:idx]
				}
				if rel != "" {
					return &model.EmbyDirSetting{
						StorageID:      d.ID,
						DirPath:        dirPath,
						Actors:         rel,
						UseNameAsActor: false,
					}, nil
				}
			}
		}
		if utils.PathEqual(dirPath, "/") {
			return nil, nil
		}
		dirPath = stdpath.Dir(dirPath)
	}
}
