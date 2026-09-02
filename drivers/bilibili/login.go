package bilibili

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/OpenListTeam/OpenList/v4/internal/op"
	qrcode "github.com/skip2/go-qrcode"
)

const qrLoginHTMLTemplate = `<body style="text-align:center;font-family:sans-serif;padding-top:24px">
<h3>Bilibili 扫码登录</h3>
<p>%s</p>
<img src="data:image/png;base64,%s" style="width:220px;height:220px"/>
<p>打开手机 Bilibili App 扫码，扫码后<strong>再次点击保存</strong></p>
</body>`

// qrLoginHTML 生成内嵌 base64 二维码的 HTML（189pc 同款交互：前端把错误当 HTML 渲染）
func qrLoginHTML(stateText string, qrURL string) (string, error) {
	png, err := qrcode.Encode(qrURL, qrcode.Medium, 256)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(qrLoginHTMLTemplate, stateText, base64.StdEncoding.EncodeToString(png)), nil
}

type qrGenerateData struct {
	URL       string `json:"url"`
	QrcodeKey string `json:"qrcode_key"`
}

type qrPollData struct {
	Code int    `json:"code"` // 0 成功 / 86038 过期 / 86090 已扫待确认 / 86101 未扫
	URL  string `json:"url"`  // 成功时：带登录 cookie 的回调地址
}

func (d *Bilibili) loginByQRCode(ctx context.Context) error {
	if d.qrcodeKey == "" {
		raw, err := d.doGet(ctx, passportBase+"/x/passport-login/web/qrcode/generate",
			map[string]string{"source": "main-fe-header"}, false)
		if err != nil {
			return err
		}
		var data qrGenerateData
		if err := json.Unmarshal(raw, &data); err != nil {
			return err
		}
		d.qrcodeKey = data.QrcodeKey
		d.qrURL = data.URL
		if d.qrURL == "" {
			return fmt.Errorf("qrcode generate 未返回 url")
		}
	}

	raw, err := d.doGet(ctx, passportBase+"/x/passport-login/web/qrcode/poll",
		map[string]string{"qrcode_key": d.qrcodeKey, "source": "main-fe-header"}, false)
	if err != nil {
		return err
	}
	var data qrPollData
	if err := json.Unmarshal(raw, &data); err != nil {
		return err
	}

	needVerify := func(state string) error {
		html, err := qrLoginHTML(state, d.qrURL)
		if err != nil {
			return err
		}
		return fmt.Errorf("need verify: \n%s", html)
	}

	switch data.Code {
	case 86101:
		return needVerify("二维码已生成，请用手机 Bilibili App 扫码（尚未扫码）")
	case 86090:
		return needVerify("已扫码，请在手机上点击确认登录")
	case 86038:
		d.qrcodeKey = ""
		return needVerify("二维码已过期，请再次点击保存以刷新")
	case 0:
		cookie := cookiesFromLoginURL(data.URL)
		if cookie == "" {
			return fmt.Errorf("扫码成功但回调 URL 未包含 SESSDATA")
		}
		d.Addition.Cookie = cookie
		d.cookieStr = cookie // 立即生效
		d.qrcodeKey = ""
		d.qrURL = ""
		if d.GetStorage().ID != 0 {
			// 真实存储（ID != 0）持久化新 cookie；测试 driver（ID==0）不触发
			op.MustSaveDriverStorage(d)
		}
		return nil
	default:
		d.qrcodeKey = ""
		return fmt.Errorf("扫码登录失败（code=%d），请再次保存重试", data.Code)
	}
}

// cookiesFromLoginURL 从扫码成功回调 URL 提取核心 cookie（SESSDATA/DedeUserID/bili_jct）
func cookiesFromLoginURL(loginURL string) string {
	u, err := url.Parse(loginURL)
	if err != nil {
		return ""
	}
	q := u.Query()
	pairs := []struct{ k, v string }{
		{"SESSDATA", q.Get("SESSDATA")},
		{"DedeUserID", q.Get("DedeUserID")},
		{"DedeUserID__ckMd5", q.Get("DedeUserID__ckMd5")},
		{"bili_jct", q.Get("bili_jct")},
	}
	parts := make([]string, 0, len(pairs))
	for _, p := range pairs {
		if p.v != "" {
			parts = append(parts, p.k+"="+p.v)
		}
	}
	return strings.Join(parts, "; ")
}
