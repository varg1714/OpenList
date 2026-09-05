package emby_wrapper

import (
	"fmt"
	"io"
	"net/http"
	stdpath "path"
	"strconv"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
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

// ---- 剧集根封面（多图，对齐 pornhub fanart 机制）----
// 剧集根目录自动附加 poster.jpg（主海报）+ fanart1..N.jpg（背景轮播），
// 候选 = 按剧集编号顺序（idx 登记序：根视频在前、季按序）前 N 个带缩略图的视频；
// 真实同名文件优先，被真实文件占用的槽位直接跳过（不后移占用其它名字）。

const showPosterName = "poster.jpg"

// showImageName 第 i 个候选的占位文件名：0 → poster.jpg，i>=1 → fanart{i}.jpg。
func showImageName(i int) string {
	if i == 0 {
		return showPosterName
	}
	return fmt.Sprintf("fanart%d.jpg", i)
}

// showImageIndex 解析占位文件名 → 候选序号（showImageName 的逆运算）。
// 仅精确匹配本驱动产出的命名（poster.jpg / fanart{>=1}.jpg，大小写不敏感）；
// fanart0.jpg、fanart.jpg、folder.jpg、backdrop1.jpg 等一律不匹配。
func showImageIndex(name string) (int, bool) {
	l := strings.ToLower(name)
	if l == showPosterName {
		return 0, true
	}
	rest, ok := strings.CutPrefix(l, "fanart")
	if !ok || !strings.HasSuffix(rest, ".jpg") {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSuffix(rest, ".jpg"))
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}

// showImageCandidates 返回剧根封面前 limit 个候选条目：按 idx 登记顺序
// （= 剧集编号顺序）取带缩略图的视频，按真实路径去重；limit<=0 返回空。
func (d *EmbyWrapper) showImageCandidates(idx *tvIndex, limit int) []tvEntry {
	if limit <= 0 {
		return nil
	}
	var out []tvEntry
	seen := map[string]bool{}
	for _, e := range idx.order {
		if _, ok := d.supportSuffix[utils.Ext(e.name)]; !ok {
			continue
		}
		if _, ok := thumbOfEntry(e); !ok {
			continue
		}
		if seen[e.real.GetPath()] {
			continue
		}
		seen[e.real.GetPath()] = true
		out = append(out, e)
		if len(out) >= limit {
			break
		}
	}
	return out
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
