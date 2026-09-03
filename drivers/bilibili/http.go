package bilibili

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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

	// defaultRateLimit: Addition.LimitRate ≤0（老存储未设置）时回落的安全默认值
	defaultRateLimit = 2
)

// biliResp 是所有 bilibili API 的统一包裹结构
type biliResp struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// effectiveRate Addition.LimitRate ≤0 时回落默认值（0 无法表达"不限流"：
// 老存储没有 limit_rate 字段会得到 0，而 bilibili 必须限流防风控）
func (d *Bilibili) effectiveRate() float64 {
	if d.LimitRate > 0 {
		return d.LimitRate
	}
	return defaultRateLimit
}

// initClient 幂等初始化 HTTP 客户端与限流器
func (d *Bilibili) initClient() {
	if d.client != nil {
		return
	}
	d.client = resty.New().
		SetHeader("User-Agent", browserUA).
		SetHeader("Referer", "https://www.bilibili.com/")
	// burst 1：请求严格均匀间隔（115 同款），翻页节奏不会被风控误判为突发
	d.limiter = rate.NewLimiter(rate.Limit(d.effectiveRate()), 1)
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
	// path.Base("") 返回 "."，空 wbi_img 会漏过 == "" 检查并在 getMixinKey 中
	// 越界 panic（table 索引 46 越出 2 字符串）；key 恒为 32 位 hex，长度即哨兵。
	if len(imgKey) < 32 || len(subKey) < 32 {
		return "", errors.New("wbi_img keys malformed from nav")
	}
	d.mixinKey = getMixinKey(imgKey + subKey)
	d.mixinKeyDay = today
	return d.mixinKey, nil
}

// doGet GET 请求；signed=true 时追加 wbi 签名。
// wbi 的 img_key/sub_key 会被 bilibili 不定期轮换（数小时~数天），过期后签名
// 请求返回 HTTP 412——此时丢弃日缓存、重取 nav key 重签重试一次；仍 412 才报错
// （BBDown/PiliPlus 同款处理）。注意限流器等待发生在重试外层，重发不额外计速。
func (d *Bilibili) doGet(ctx context.Context, url string, params map[string]string, signed bool) (json.RawMessage, error) {
	if d.limiter != nil {
		if err := d.limiter.Wait(ctx); err != nil {
			return nil, err
		}
	}
	orig := params // encWbi 为纯函数不改入参，重签时用原参数
	if signed {
		key, err := d.fetchMixinKey(ctx)
		if err != nil {
			return nil, fmt.Errorf("fetch wbi key: %w", err)
		}
		params = encWbi(params, key)
	}
	resp, err := d.get(ctx, url, params)
	if err != nil {
		return nil, err
	}
	// 412 且为签名请求：key 可能已被轮换 → 重取重签重试一次
	if signed && resp.StatusCode() == http.StatusPreconditionFailed {
		d.mixinKey = ""
		d.mixinKeyDay = ""
		key, err := d.fetchMixinKey(ctx)
		if err != nil {
			return nil, fmt.Errorf("fetch wbi key after 412: %w", err)
		}
		resp, err = d.get(ctx, url, encWbi(orig, key))
		if err != nil {
			return nil, err
		}
	}
	d.setCookieFromResp(resp)
	return d.decodeResp(resp)
}

// get 发送 GET 并返回原始响应（不解析 body）
func (d *Bilibili) get(ctx context.Context, url string, params map[string]string) (*resty.Response, error) {
	req := d.client.R().SetContext(ctx).
		SetHeader("Cookie", d.cookieStr).
		SetQueryParams(params)
	return req.Get(url)
}

func (d *Bilibili) decodeResp(resp *resty.Response) (json.RawMessage, error) {
	if resp.StatusCode() != http.StatusOK {
		return nil, fmt.Errorf("bilibili http %d from %s (rate-limited or blocked)", resp.StatusCode(), resp.Request.URL)
	}
	var br biliResp
	if err := json.Unmarshal(resp.Body(), &br); err != nil {
		body := bytes.TrimSpace(resp.Body())
		if len(body) > 0 && body[0] == '<' {
			// HTML 响应 = 风控/验证页（-412/滑块），非 JSON；截前缀便于诊断
			prefix := string(body)
			if len(prefix) > 160 {
				prefix = prefix[:160]
			}
			return nil, fmt.Errorf("bilibili returned HTML instead of JSON from %s (risk-control/verify page), prefix: %q", resp.Request.URL, prefix)
		}
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
