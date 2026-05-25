package javdb

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/gocolly/colly/v2"
	"github.com/gocolly/colly/v2/extensions"
)

// transformDmmCode 小写 + 去除 -
// "SSIS-001" → "ssis001"，用于详情页 cid 参数
func transformDmmCode(code string) string {
	return strings.ReplaceAll(strings.ToLower(code), "-", "")
}

// transformDmmSearchCode 小写 + -替换为空格
// "SSIS-001" → "ssis 001"，用于搜索页 searchstr 参数
func transformDmmSearchCode(code string) string {
	return strings.ReplaceAll(strings.ToLower(code), "-", " ")
}

// codeMatchesCode 检查 code（原始格式 xxx-001）的 - 前后部分是否都是 target 的子串
func codeMatchesCode(code, target string) bool {
	code = strings.ToLower(code)
	target = strings.ToLower(target)
	parts := strings.SplitN(code, "-", 2)
	if len(parts) != 2 {
		return false
	}
	return strings.Contains(target, parts[0]) && strings.Contains(target, parts[1])
}

// newDmmCollector 创建带 age_check cookie 的 DMM collector
func newDmmCollector(pageUrl string) *colly.Collector {
	collector := colly.NewCollector(func(c *colly.Collector) {
		c.SetRequestTimeout(time.Second * 30)
	})
	extensions.RandomUserAgent(collector)
	if u, err := url.Parse(pageUrl); err == nil {
		_ = collector.SetCookies(fmt.Sprintf("%s://%s", u.Scheme, u.Host), []*http.Cookie{
			{Name: "age_check_done", Value: "1"},
		})
	}
	return collector
}

// fetchDmmSynopsis 从 DMM 站点爬取影片简介，返回纯文本（<br/>保留为换行符）
func (d *Javdb) fetchDmmSynopsis(code string) string {

	dmmCode := transformDmmCode(code)
	if dmmCode == "" {
		return ""
	}

	detailUrl := fmt.Sprintf("https://www.dmm.co.jp/mono/dvd/-/detail/=/cid=%s/", dmmCode)
	synopsis, is404 := fetchDmmDetailPage(detailUrl)

	if is404 || synopsis == "" {
		time.Sleep(1 * time.Second)
		synopsis, _ = fetchDmmDetailPage(d.fetchDmmSearchResult(code))
	}

	return synopsis
}

// fetchDmmSearchResult 通过 DMM 搜索获取第一个匹配的详情页地址
func (d *Javdb) fetchDmmSearchResult(code string) string {

	searchUrl := fmt.Sprintf("https://www.dmm.co.jp/search/=/searchstr=%s/limit=30/sort=rankprofile/", transformDmmSearchCode(code))
	collector := newDmmCollector(searchUrl)

	var detailUrl string

	collector.OnHTML(".mx-3.mt-1\\.5.mb-3.h-40", func(element *colly.HTMLElement) {
		if detailUrl != "" {
			return
		}
		element.ForEach("a", func(i int, el *colly.HTMLElement) {
			if detailUrl != "" {
				return
			}
			href := el.Attr("href")
			if !strings.Contains(href, "/detail") || !strings.Contains(href, "www.dmm.co.jp") {
				return
			}
			cid := parseCidFromPath(href)
			if cid != "" && codeMatchesCode(code, cid) {
				detailUrl = href
			}
		})
	})

	err := collector.Visit(searchUrl)
	if err != nil {
		utils.Log.Warnf("DMM搜索页访问失败: %s, %v", searchUrl, err)
		return ""
	}

	if detailUrl == "" {
		return ""
	}

	if !strings.HasPrefix(detailUrl, "http") {
		detailUrl = fmt.Sprintf("https://www.dmm.co.jp%s", detailUrl)
	}

	return detailUrl
}

// parseCidFromPath 从路径格式中提取 cid，如 /detail/=/cid=mvg155/
func parseCidFromPath(href string) string {
	idx := strings.Index(href, "cid=")
	if idx == -1 {
		return ""
	}
	cid := href[idx+4:]
	if end := strings.IndexAny(cid, "/?&"); end != -1 {
		cid = cid[:end]
	}
	return cid
}

// fetchDmmDetailPage 访问 DMM 详情页并提取简介
// 返回 (synopsis, is404)
func fetchDmmDetailPage(url string) (string, bool) {

	if url == "" {
		return "", false
	}

	collector := newDmmCollector(url)

	var synopsis string

	// 规则一：.mg-b20.lh4 p.mg-b20
	collector.OnHTML(".mg-b20.lh4", func(element *colly.HTMLElement) {
		synopsis = strings.TrimSpace(element.DOM.Find("p.mg-b20").First().Text())
	})

	// 规则二：.product-description-block .ignore-new-line
	collector.OnHTML(".product-description-block", func(element *colly.HTMLElement) {
		if synopsis != "" {
			return
		}
		synopsis = strings.TrimSpace(element.DOM.Find(".ignore-new-line").First().Text())
	})

	err := collector.Visit(url)
	if err != nil {
		if strings.Contains(err.Error(), "Not Found") {
			return "", true
		}
		utils.Log.Warnf("DMM详情页访问失败: %s, %v", url, err)
		return "", false
	}

	return synopsis, false
}
