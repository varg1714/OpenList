package emby_wrapper

import (
	"fmt"
	"io"
	"net/http"
	stdpath "path"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

// thumbCacheLimit 内存 thumb 缓存上限：超过后整体清空（简单策略，防长会话膨胀）。
const thumbCacheLimit = 256 << 20

// virtualThumb 内存构建的剧集封面图对象（{剧集名}-thumb.jpg，Emby episode 图片规则）。
// List 只附加占位（Size=0），图片字节在 Link 时按需从上游驱动的 thumb URL
// 惰性下载并按 URL 键控缓存（方案 B）。
type virtualThumb struct {
	model.Object
	thumbURL string
}

// thumbNameOf 剧集封面文件名：{虚拟名去扩展名}-thumb.jpg。
func thumbNameOf(virtualName string) string {
	return strings.TrimSuffix(virtualName, stdpath.Ext(virtualName)) + "-thumb.jpg"
}

// thumbOfEntry 返回条目对应上游对象的缩略图 URL（未提供或为空则返回 false）。
func thumbOfEntry(e tvEntry) (string, bool) {
	url, ok := model.GetThumb(e.real)
	return url, ok && url != ""
}

// newVirtualThumb 构造占位封面对象。
func newVirtualThumb(path, thumbURL string, modified time.Time) *virtualThumb {
	return &virtualThumb{
		Object: model.Object{
			Name:     stdpath.Base(path),
			Path:     path,
			Modified: modified,
			ID:       "vthumb-" + stdpath.Base(path),
		},
		thumbURL: thumbURL,
	}
}

// thumbContent 获取 thumb URL 的图片字节：命中缓存直接返回，未命中下载并缓存。
// 下载失败返回错误且不缓存（下次重试）。
func (d *EmbyWrapper) thumbContent(url string) ([]byte, error) {
	d.thumbMu.Lock()
	if d.thumbCache == nil {
		d.thumbCache = map[string][]byte{}
	}
	if b, ok := d.thumbCache[url]; ok {
		d.thumbMu.Unlock()
		return b, nil
	}
	d.thumbMu.Unlock()
	b, err := downloadThumb(url)
	if err != nil {
		return nil, err
	}
	d.thumbMu.Lock()
	defer d.thumbMu.Unlock()
	if d.thumbBytes+int64(len(b)) > thumbCacheLimit {
		d.thumbCache = map[string][]byte{}
		d.thumbBytes = 0
	}
	d.thumbCache[url] = b
	d.thumbBytes += int64(len(b))
	return b, nil
}

// downloadThumb 下载缩略图字节（超时 30s，上限 8MB）。
func downloadThumb(url string) ([]byte, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download thumb %s: status %d", url, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}
