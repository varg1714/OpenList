package bilibili

import (
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/go-resty/resty/v2"
	"golang.org/x/time/rate"
	// TODO(Task 6): import op and re-enable init() below once
	// Bilibili implements the full driver.Driver interface (List/Link).
	// "github.com/OpenListTeam/OpenList/v4/internal/op"
)

type Addition struct {
	Cookie       string `json:"cookie" type:"text" help:"auto-filled after QR code login; or paste browser cookie manually (must contain SESSDATA)"`
	MaxListItems int    `json:"max_list_items" type:"number" default:"500" help:"max items per paged list (followings/videos/fav), 0 = unlimited"`
	driver.RootPath
}

var config = driver.Config{
	Name:        "Bilibili",
	LocalSort:   false, // driver returns pubdate-desc order; keep as-is
	NoUpload:    true,
	DefaultRoot: "/",
}

type Bilibili struct {
	model.Storage
	Addition

	// http client 层（Task 3+）：resty 复用池 + 全局限流 + 手动 cookie 串
	client    *resty.Client
	limiter   *rate.Limiter
	pageDelay time.Duration // 分页间延迟（翻页过快会被风控）

	cookieStr   string // 手动维护的 cookie 串（种子来自 Addition.Cookie，随 Set-Cookie 合并）
	mixinKey    string // wbi 签名 key（按日缓存）
	mixinKeyDay string // mixinKey 的抓取日期 YYYYMMDD

	uid   int64  // nav 登录态缓存（navInfo 写入）
	uname string // nav 登录态缓存（navInfo 写入）

	qrcodeKey string // 扫码登录 key（loginByQRCode 写入，过期/成功后清除）
	qrURL     string // 扫码登录二维码内容 URL（loginByQRCode 写入）
}

func (d *Bilibili) Config() driver.Config {
	return config
}

func (d *Bilibili) GetAddition() driver.Additional {
	return &d.Addition
}

// TODO(Task 6): enable registration once Bilibili implements driver.Driver.
//
// func init() {
// 	op.RegisterDriver(func() driver.Driver {
// 		return &Bilibili{}
// 	})
// }
