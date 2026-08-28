package emby_wrapper

import (
	stdpath "path"
	"strings"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

// resolveSetting 返回 dirPath 自身或其最近祖先的目录设置；都没有则返回 nil。
// 子文件夹继承最近祖先的 actor 设置。
func (d *EmbyWrapper) resolveSetting(dirPath string) (*model.EmbyDirSetting, error) {
	dirPath = utils.FixAndCleanPath(dirPath)
	for {
		item, err := GetEmbyDirSetting(d.ID, dirPath)
		if err != nil {
			return nil, err
		}
		if item != nil && strings.TrimSpace(item.Actors) != "" {
			return item, nil
		}
		if utils.PathEqual(dirPath, "/") {
			return nil, nil
		}
		dirPath = stdpath.Dir(dirPath)
	}
}
