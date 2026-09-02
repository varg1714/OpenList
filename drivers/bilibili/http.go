package bilibili

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/go-resty/resty/v2"
	"golang.org/x/time/rate"
)

const (
	apiBase      = "https://api.bilibili.com"
	passportBase = "https://passport.bilibili.com"
	browserUA    = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"
)

// biliResp 是所有 bilibili API 的统一包裹结构
type biliResp struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// initClient 幂等初始化 HTTP 客户端与限流器
func (d *Bilibili) initClient() {
	if d.client != nil {
		return
	}
	d.client = resty.New().
		SetHeader("User-Agent", browserUA).
		SetHeader("Referer", "https://www.bilibili.com/")
	d.limiter = rate.NewLimiter(5, 5) // 5 r/s 兜底
	d.pageDelay = 150 * time.Millisecond
	if d.cookieStr == "" {
		d.cookieStr = d.Addition.Cookie
	}
}

// setCookieFromResp 把响应的 Set-Cookie 按 name 覆盖合并进 cookieStr
func (d *Bilibili) setCookieFromResp(resp *resty.Response) {
	if resp == nil {
		return
	}
	cur := parseCookies(d.cookieStr)
	for _, sc := range resp.Header().Values("Set-Cookie") {
		kv, _, _ := strings.Cut(sc, ";")
		name, value, ok := strings.Cut(strings.TrimSpace(kv), "=")
		if !ok || name == "" {
			continue
		}
		cur[name] = value
	}
	d.cookieStr = joinCookies(cur)
}

func parseCookies(s string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(s, ";") {
		name, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok && name != "" {
			out[name] = value
		}
	}
	return out
}

func joinCookies(m map[string]string) string {
	parts := make([]string, 0, len(m))
	for k, v := range m {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, "; ")
}

// fetchMixinKey 从 nav 接口取 wbi_img 并计算 mixin key；按日缓存
func (d *Bilibili) fetchMixinKey(ctx context.Context) (string, error) {
	today := time.Now().Format("20060102")
	// mixinKeyDay 为空视为外部注入的 key（测试 seam），同样命中缓存；
	// 自身拉取时两字段同时写入，跨日后自动重取。
	if d.mixinKey != "" && (d.mixinKeyDay == "" || d.mixinKeyDay == today) {
		return d.mixinKey, nil
	}
	raw, err := d.doGet(ctx, apiBase+"/x/web-interface/nav", nil, false)
	if err != nil {
		return "", err
	}
	var data struct {
		WbiImg struct {
			ImgURL string `json:"img_url"`
			SubURL string `json:"sub_url"`
		} `json:"wbi_img"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return "", err
	}
	imgKey := strings.TrimSuffix(path.Base(data.WbiImg.ImgURL), path.Ext(data.WbiImg.ImgURL))
	subKey := strings.TrimSuffix(path.Base(data.WbiImg.SubURL), path.Ext(data.WbiImg.SubURL))
	if imgKey == "" || subKey == "" {
		return "", errors.New("wbi_img keys empty from nav")
	}
	d.mixinKey = getMixinKey(imgKey + subKey)
	d.mixinKeyDay = today
	return d.mixinKey, nil
}

// doGet GET 请求；signed=true 时追加 wbi 签名
func (d *Bilibili) doGet(ctx context.Context, url string, params map[string]string, signed bool) (json.RawMessage, error) {
	if err := d.limiter.Wait(ctx); err != nil {
		return nil, err
	}
	if signed {
		key, err := d.fetchMixinKey(ctx)
		if err != nil {
			return nil, fmt.Errorf("fetch wbi key: %w", err)
		}
		params = encWbi(params, key)
	}
	req := d.client.R().SetContext(ctx).
		SetHeader("Cookie", d.cookieStr).
		SetQueryParams(params)
	resp, err := req.Get(url)
	if err != nil {
		return nil, err
	}
	d.setCookieFromResp(resp)
	return d.decodeResp(resp)
}

// doPostForm POST form 请求（扫码登录用，无需签名）
func (d *Bilibili) doPostForm(ctx context.Context, url string, form map[string]string) (json.RawMessage, error) {
	if err := d.limiter.Wait(ctx); err != nil {
		return nil, err
	}
	req := d.client.R().SetContext(ctx).
		SetHeader("Cookie", d.cookieStr).
		SetFormData(form)
	resp, err := req.Post(url)
	if err != nil {
		return nil, err
	}
	d.setCookieFromResp(resp)
	return d.decodeResp(resp)
}

func (d *Bilibili) decodeResp(resp *resty.Response) (json.RawMessage, error) {
	var br biliResp
	if err := json.Unmarshal(resp.Body(), &br); err != nil {
		return nil, fmt.Errorf("bad json from %s: %w", resp.Request.URL, err)
	}
	switch br.Code {
	case 0:
		return br.Data, nil
	case -101:
		return nil, errors.New("bilibili cookie 失效或未登录（-101），请重新保存存储触发扫码登录")
	default:
		return nil, fmt.Errorf("bilibili api error %d: %s", br.Code, br.Message)
	}
}
