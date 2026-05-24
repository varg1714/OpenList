package javdb

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/gocolly/colly/v2"
	"github.com/gocolly/colly/v2/extensions"
)

var (
	dmmCodeRegexp    = regexp.MustCompile(`[^a-z0-9]`)
	dmmHtmlTagRegexp = regexp.MustCompile(`<[^>]*>`)
	dmmBrRegexp      = regexp.MustCompile(`<br\s*/?>`)
)

// transformDmmCode 将 javdb code 转为 DMM 格式：小写 + 去除非字母数字字符
// "SSIS-001" → "ssis001"
func transformDmmCode(code string) string {
	code = strings.ToLower(code)
	return dmmCodeRegexp.ReplaceAllString(code, "")
}

// newDmmCollector 创建带 age_check cookie 的 DMM collector
func newDmmCollector() *colly.Collector {
	collector := colly.NewCollector(func(c *colly.Collector) {
		c.SetRequestTimeout(time.Second * 30)
	})
	extensions.RandomUserAgent(collector)
	_ = collector.SetCookies("https://www.dmm.co.jp", []*http.Cookie{
		{Name: "age_check_done", Value: "1"},
	})
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
		synopsis, _ = fetchDmmDetailPage(d.fetchDmmSearchResult(dmmCode))
	}

	return synopsis
}

// fetchDmmSearchResult 通过 DMM 搜索获取第一个匹配的详情页地址
func (d *Javdb) fetchDmmSearchResult(dmmCode string) string {

	searchUrl := fmt.Sprintf("https://www.dmm.co.jp/search/=/searchstr=%s/limit=30/sort=rankprofile/", dmmCode)
	collector := newDmmCollector()

	var detailUrl string
	found := false

	collector.OnHTML(".mx-3.mt-1\\.5.mb-3.h-40", func(element *colly.HTMLElement) {
		if found {
			return
		}
		element.ForEach("a", func(i int, el *colly.HTMLElement) {
			if found {
				return
			}
			href := el.Attr("href")
			if strings.Contains(href, "/detail") {
				detailUrl = href
				found = true
			}
		})
	})

	err := collector.Visit(searchUrl)
	if err != nil {
		utils.Log.Debugf("DMM搜索页访问失败: %s, %v", searchUrl, err)
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

// fetchDmmDetailPage 访问 DMM 详情页并提取简介
// 返回 (synopsis, is404)
func fetchDmmDetailPage(url string) (string, bool) {

	if url == "" {
		return "", false
	}

	collector := newDmmCollector()

	var synopsis string

	collector.OnHTML(".mg-b20.lh4", func(element *colly.HTMLElement) {
		synopsis = extractPlainText(element, "p.mg-b20")
	})

	err := collector.Visit(url)
	if err != nil {
		if strings.Contains(err.Error(), "Not Found") {
			return "", true
		}
		utils.Log.Debugf("DMM详情页访问失败: %s, %v", url, err)
		return "", false
	}

	return synopsis, false
}

// extractPlainText 提取元素的纯文本，将 <br/> 替换为换行符
func extractPlainText(element *colly.HTMLElement, childSelector string) string {

	var html string
	if childSelector != "" {
		var err error
		html, err = element.DOM.Find(childSelector).First().Html()
		if err != nil {
			return ""
		}
	} else {
		var err error
		html, err = element.DOM.Html()
		if err != nil {
			return ""
		}
	}

	if html == "" {
		utils.Log.Debugf("DMM: extractPlainText 未找到子元素 %s", childSelector)
		return ""
	}

	// <br/> → 换行符
	html = dmmBrRegexp.ReplaceAllString(html, "\n")
	// 去除剩余 HTML 标签
	html = dmmHtmlTagRegexp.ReplaceAllString(html, "")
	// 清理首尾空白
	html = strings.TrimSpace(html)

	return html
}
