# Bilibili 驱动实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 新增 `Bilibili` 驱动：扫码登录（189pc 交互模式）后，以虚拟目录树挂载"我的关注"（UP 主 → 其投稿视频）与"我的收藏"（收藏夹 → 视频），视频通过 bilibili durl 接口返回单 mp4/flv 直链播放（上限 1080p，不引入 dash/ffmpeg）。

**Architecture:** 新包 `drivers/bilibili/`，文件：`meta.go`（Addition/Config/注册）、`sign.go`（wbi 签名纯函数）、`http.go`（resty 客户端、手动 cookie 串、限流、doGet/doPost 封装）、`api.go`（各 API 请求函数 + 响应类型）、`login.go`（扫码登录状态机 + HTML 二维码错误）、`driver.go`（Init/Drop/List/Link + 目录树构造 + 文件名清洗）。无第三方 bilibili SDK 依赖；go-qrcode / resty / x/time/rate 均已在 go.mod。

**Tech Stack:** Go 1.25.4（工具链 `/Library/Go/sdk/go1.25.4/bin/go`，gofmt 同路径）、go-resty/resty/v2、skip2/go-qrcode、golang.org/x/time/rate；测试用 resty `SetTransport` 注入自定义 RoundTripper mock（drivers/fc2/client_test.go 同款模式），不启真实网络。

**Spec:** `docs/superpowers/specs/2026-09-02-bilibili-driver-design.md`（计划从 spec 论证；执行者先读 spec 再读本计划）

## Global Constraints

- Go 工具链一律 `/Library/Go/sdk/go1.25.4/bin/go`（gofmt 同理），不用 PATH 上的
- TDD：每任务先写失败测试再实现，跑通后提交；提交信息沿用仓库风格 `feat(bilibili): ...`
- 驱动包名 `bilibili`，目录 `drivers/bilibili/`；驱动注册名 `Bilibili`
- 请求头统一：UA 用 Chrome 桌面 UA（见 Task 3），Referer `https://www.bilibili.com/`；cookie 用**手动全量字符串**（BBDown 验证过的做法），不依赖 cookie jar 域匹配
- 所有 API 响应统一 `{code,message,data}` 包裹：code!=0 报错（-101 未登录给专属提示），成功返回 `data`（json.RawMessage，由各解析函数解）
- 接口鉴权矩阵（来自 spec）：wbi 签名的只有 `x/space/wbi/arc/search` 与 `x/player/wbi/playurl`；`nav`/`followings`/`fav/*`/`view` 免签名仅需 cookie
- 分页接口 ps=50（fav resource 用 ps=20），翻页间隔 `d.pageDelay`（默认 150ms，测试置 0），受 `Addition.MaxListItems`（默认 500，0=不限）截断
- 不做：动态 feed、番剧、多 P 拆 P（一个视频条目=一个文件播首 P）、上传、dash

---

### Task 1: 包骨架 + meta.go + 注册

**Files:**
- Create: `drivers/bilibili/meta.go`
- Modify: `drivers/all.go`（baidu_photo 与 cache 之间插入一行 import）
- Test: 无（编译门：`go build ./drivers/...`）

**Interfaces:**
- Produces: `type Addition struct { Cookie string; MaxListItems int; driver.RootPath }`、`type Bilibili struct`（Task 3-7 逐任务加字段）、`config driver.Config`、`func init()` 注册 —— 后续任务全部依赖本任务的类型与注册

- [ ] **Step 1: 写 `drivers/bilibili/meta.go`**

```go
package bilibili

import (
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
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
}

func (d *Bilibili) Config() driver.Config {
	return config
}

func (d *Bilibili) GetAddition() driver.Additional {
	return &d.Addition
}

func init() {
	op.RegisterDriver(func() driver.Driver {
		return &Bilibili{}
	})
}
```

- [ ] **Step 2: `drivers/all.go` 注册**

在 `_ "github.com/OpenListTeam/OpenList/v4/drivers/baidu_photo"` 之后、`_ "github.com/OpenListTeam/OpenList/v4/drivers/cache"` 之前插入：

```go
	_ "github.com/OpenListTeam/OpenList/v4/drivers/bilibili"
```

- [ ] **Step 3: 编译验证**

Run: `/Library/Go/sdk/go1.25.4/bin/go build ./drivers/...`
Expected: 通过（Task 1 结束时 Bilibili 尚无 List/Link，不满足 driver.Driver 接口，但注册函数返回 `driver.Driver` 是编译期断言失败点——**此时 `op.RegisterDriver(func() driver.Driver {...})` 会编译错误**，因为 Bilibili 还没实现接口。临时处理：Step 3 先不写 `init()` 里的注册（注释掉），Task 6 完成 List/Link 后打开。或者给编译错误预期：直接先注释 `init()` 与 `op` import。）

> 修正 Step 1：`meta.go` 中暂不写 `init()` 注册块（Task 6 末尾打开），仅保留 Addition/config/Bilibili 空壳与 Config/GetAddition 两个方法（这两个方法后面不变）。`op` import 也留到 Task 6。

- [ ] **Step 4: Commit**

```bash
git add drivers/bilibili/meta.go drivers/all.go
git commit -m "feat(bilibili): driver skeleton with Addition and config"
```

---

### Task 2: wbi 签名（sign.go，纯函数）

**Files:**
- Create: `drivers/bilibili/sign.go`
- Test: `drivers/bilibili/sign_test.go`

**Interfaces:**
- Consumes: 无（纯函数）
- Produces:
  - `var mixinKeyEncTab = [32]int{...}`（Task 3 的 ensureMixinKey 用它）
  - `func getMixinKey(orig string) string` —— 输入 img_key+sub_key 拼接串（64 字符），输出 32 字符 mixin key
  - `func encWbi(params map[string]string, mixinKey string) map[string]string` —— 注入 `wts`、按 key 排序、urlencode（空格转 `%20`、滤除 `!'()*`）、拼 mixinKey 后 MD5 得 `w_rid`，返回新 map
  - `func wbiQuery(params map[string]string, mixinKey string) string` —— encWbi 后输出可直接拼 URL 的 query 串（已排序），Task 3 组装请求用

- [ ] **Step 1: 写失败测试 `sign_test.go`**

向量说明：img_key/sub_key 取自 bilibili-API-collect wbi 文档经典示例（2026-09 仍与 PiliPlus/BBDown 源码一致）；期望值已用 python 预计算验证。

```go
package bilibili

import (
	"crypto/md5"
	"encoding/hex"
	"testing"
)

func TestGetMixinKey(t *testing.T) {
	// 文档示例：img_key="7cd084941338484aae1ad9425b84077c" sub_key="4932caff0ff746eab6f01bf08b70ac45"
	orig := "7cd084941338484aae1ad9425b84077c" + "4932caff0ff746eab6f01bf08b70ac45"
	if got := getMixinKey(orig); got != "ea1db124af3c7062474693fa704f4ff8" {
		t.Fatalf("getMixinKey = %q, want ea1db124af3c7062474693fa704f4ff8", got)
	}
}

func TestEncWbi(t *testing.T) {
	const mixinKey = "ea1db124af3c7062474693fa704f4ff8"
	params := encWbi(map[string]string{"foo": "114", "bar": "514", "zab": ""}, mixinKey)
	if params["wts"] == "" {
		t.Fatal("wts not injected")
	}
	// wts 是动态时间戳，期望值现场手算（测试独立实现拼接+md5，不调用被测函数）：
	// 排序 query = bar=514&foo=114&wts={wts}&zab=，md5(query+mixinKey)
	query := "bar=514&foo=114&wts=" + params["wts"] + "&zab="
	sum := md5.Sum([]byte(query + mixinKey))
	want := hex.EncodeToString(sum[:])
	if params["w_rid"] != want {
		t.Fatalf("w_rid = %q, want %q (query=%q)", params["w_rid"], want, query)
	}
	// 固定向量核对（wts=1700000000 时，预计算值）：证明算法与 bilibili 一致
	params2 := encWbi(map[string]string{"foo": "114", "bar": "514", "zab": "", "wts": "1700000000"}, mixinKey)
	if params2["w_rid"] != "5badc9d357d0139c38b633fc665e7f2d" {
		t.Fatalf("fixed-vector w_rid = %q, want 5badc9d357d0139c38b633fc665e7f2d", params2["w_rid"])
	}
}

func TestEncWbiFiltersSpecialChars(t *testing.T) {
	params := encWbi(map[string]string{"title": "a!'()*b", "x": "1"}, "k")
	if got := params["w_rid"]; got == "" {
		t.Fatal("w_rid empty")
	}
	_ = params // 只要不 panic、w_rid 非空即可；过滤字符不进入签名串
}

func TestWbiQuerySorted(t *testing.T) {
	q := wbiQuery(map[string]string{"b": "2", "a": "1"}, "key")
	// 必须以 a=1 开头（排序），且包含 b=2
	if len(q) < 7 || q[:4] != "a=1&" {
		t.Fatalf("wbiQuery not sorted: %q", q)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/bilibili/ -run 'TestGetMixinKey|TestEncWbi|TestWbiQuery' -v`
Expected: 编译失败（sign.go 不存在 / 函数未定义）

- [ ] **Step 3: 实现 `sign.go`**

```go
package bilibili

import (
	"crypto/md5"
	"encoding/hex"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// 与 PiliPlus/BBDown 一致的前 32 位打乱索引（bilibili wbi 签名）
var mixinKeyEncTab = [32]int{
	46, 47, 18, 2, 53, 8, 23, 32, 15, 50, 10, 31, 58, 3, 45, 35,
	27, 43, 5, 49, 33, 9, 42, 19, 29, 28, 14, 39, 12, 38, 41, 13,
}

// getMixinKey 将 img_key+sub_key（64 字符）按打乱表映射为 32 字符 mixin key
func getMixinKey(orig string) string {
	b := make([]byte, 32)
	for i, idx := range mixinKeyEncTab {
		b[i] = orig[idx]
	}
	return string(b)
}

var wbiChrFilter = strings.NewReplacer("!", "", "'", "", "(", "", ")", "", "*", "")

// wbiEscape 同 url.QueryEscape，但空格编码为 %20（bilibili 要求）
func wbiEscape(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

// encWbi 注入 wts 并计算 w_rid，返回含 wts/w_rid 的新参数表
func encWbi(params map[string]string, mixinKey string) map[string]string {
	out := make(map[string]string, len(params)+2)
	for k, v := range params {
		out[k] = v
	}
	// 调用方不传 wts；测试可注入固定 wts 做向量验证
	if _, ok := out["wts"]; !ok {
		out["wts"] = strconv.FormatInt(time.Now().Unix(), 10)
	}
	keys := make([]string, 0, len(out))
	for k := range out {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte('&')
		}
		sb.WriteString(wbiEscape(k))
		sb.WriteByte('=')
		sb.WriteString(wbiEscape(wbiChrFilter.Replace(out[k])))
	}
	sum := md5.Sum([]byte(sb.String() + mixinKey))
	out["w_rid"] = hex.EncodeToString(sum[:])
	return out
}

// wbiQuery 返回签名后可拼入 URL 的完整 query 串（已排序）
func wbiQuery(params map[string]string, mixinKey string) string {
	signed := encWbi(params, mixinKey)
	keys := make([]string, 0, len(signed))
	for k := range signed {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte('&')
		}
		sb.WriteString(wbiEscape(k))
		sb.WriteByte('=')
		sb.WriteString(wbiEscape(signed[k]))
	}
	return sb.String()
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/bilibili/ -v`
Expected: 4 个测试全 PASS

- [ ] **Step 5: Commit**

```bash
git add drivers/bilibili/sign.go drivers/bilibili/sign_test.go
git commit -m "feat(bilibili): wbi signing (mixin key + w_rid)"
```

---

### Task 3: HTTP 客户端（http.go）：client、cookie、限流、请求封装

**Files:**
- Create: `drivers/bilibili/http.go`
- Test: `drivers/bilibili/http_test.go`

**Interfaces:**
- Consumes: Task 2 的 `wbiQuery`；Task 1 的 `Bilibili`/`Addition`
- Produces（Task 4/5/6/7 全部依赖）:
  - 常量 `const apiBase = "https://api.bilibili.com"`、`const passportBase = "https://passport.bilibili.com"`、`const browserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"`
  - `func (d *Bilibili) initClient()` —— 幂等建 resty client：全局 UA/Referer header、限流器（5r/s，容量 5）、pageDelay=150ms
  - `func (d *Bilibili) doGet(ctx context.Context, url string, params map[string]string, signed bool) (json.RawMessage, error)` —— 限流 → 合并签名 → GET → 合并 Set-Cookie → 解析 `{code,message,data}`：code==0 返回 data，`-101` 返回"cookie 失效"专属错误，其他包装 message
  - `func (d *Bilibili) doPostForm(ctx context.Context, url string, form map[string]string) (json.RawMessage, error)` —— 同上但 POST form
  - `func (d *Bilibili) setCookieFromResp(resp *resty.Response)` —— 把响应的 Set-Cookie 合并进 d.cookieStr（同名覆盖）
  - `func (d *Bilibili) cookieHeader() string` —— 返回 d.cookieStr（初始为 Addition.Cookie 的非空值）
  - 内部 `type biliResp struct { Code int; Message string; Data json.RawMessage }`
  - 测试缝：`d.client` 可被测试替换（`resty.New().SetTransport(rt)`）；`d.limiter` 测试置 `rate.NewLimiter(rate.Inf, 0)`

- [ ] **Step 1: 写失败测试 `http_test.go`**

```go
package bilibili

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-resty/resty/v2"
	"golang.org/x/time/rate"
)

// roundTripFunc 是 fc2 同款 transport mock 缝
type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newMockServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(handler)
	t.Cleanup(s.Close)
	return s
}

func newTestDriver() *Bilibili {
	d := &Bilibili{}
	d.Addition.Cookie = "SESSDATA=abc;DedeUserID=1"
	d.cookieStr = d.Addition.Cookie
	d.limiter = rate.NewLimiter(rate.Inf, 0) // 测试不限速
	d.pageDelay = 0
	return d
}

func TestDoGetCodeZeroReturnsData(t *testing.T) {
	srv := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Set-Cookie", "buvid3=xyz; path=/")
		w.Write([]byte(`{"code":0,"message":"0","data":{"mid":42}}`))
	})
	d := newTestDriver()
	d.client = resty.New().SetTransport(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		// cookie 手动头已带上种子
		if c := req.Header.Get("Cookie"); !strings.Contains(c, "SESSDATA=abc") {
			t.Errorf("cookie header = %q, want SESSDATA=abc", c)
		}
		return srv.Client().Transport.RoundTrip(req)
	}))
	data, err := d.doGet(context.Background(), srv.URL+"/x/web-interface/nav", nil, false)
	if err != nil {
		t.Fatalf("doGet: %v", err)
	}
	var mid struct {
		Mid int64 `json:"mid"`
	}
	if err := json.Unmarshal(data, &mid); err != nil || mid.Mid != 42 {
		t.Fatalf("data = %s, err = %v", data, err)
	}
	// Set-Cookie 被合并
	if !strings.Contains(d.cookieStr, "buvid3=xyz") {
		t.Fatalf("cookieStr not merged: %q", d.cookieStr)
	}
}

func TestDoGetNonZeroCode(t *testing.T) {
	srv := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":-101,"message":"账号未登录"}`))
	})
	d := newTestDriver()
	d.client = resty.New().SetTransport(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return srv.Client().Transport.RoundTrip(req)
	}))
	_, err := d.doGet(context.Background(), srv.URL+"/x", nil, false)
	if err == nil || !strings.Contains(err.Error(), "未登录") {
		t.Fatalf("err = %v, want 未登录 hint", err)
	}
}

func TestDoGetSignedAddsWRid(t *testing.T) {
	var gotQuery string
	srv := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Write([]byte(`{"code":0,"data":{}}`))
	})
	d := newTestDriver()
	d.mixinKey = "ea1db124af3c7062474693fa704f4ff8" // 直接注入，跳过 nav
	d.client = resty.New().SetTransport(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return srv.Client().Transport.RoundTrip(req)
	}))
	if _, err := d.doGet(context.Background(), srv.URL+"/x/space/wbi/arc/search",
		map[string]string{"mid": "2", "pn": "1"}, true); err != nil {
		t.Fatalf("doGet: %v", err)
	}
	if !strings.Contains(gotQuery, "w_rid=") || !strings.Contains(gotQuery, "wts=") {
		t.Fatalf("signed query missing w_rid/wts: %q", gotQuery)
	}
}

func TestDoGetSignedFetchesMixinKeyFromNav(t *testing.T) {
	// signed=true 且 mixinKey 为空 → 先请求 /x/web-interface/nav 拿 wbi_img
	var navHits int
	srv := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/x/web-interface/nav":
			navHits++
			w.Write([]byte(`{"code":0,"data":{"wbi_img":{
				"img_url":"https://i0.hdslb.com/bfs/wbi/7cd084941338484aae1ad9425b84077c.png",
				"sub_url":"https://i0.hdslb.com/bfs/wbi/4932caff0ff746eab6f01bf08b70ac45.png"}}}`))
		default:
			w.Write([]byte(`{"code":0,"data":{}}`))
		}
	})
	d := newTestDriver()
	d.client = resty.New().SetTransport(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return srv.Client().Transport.RoundTrip(req)
	}))
	if _, err := d.doGet(context.Background(), srv.URL+"/x/space/wbi/arc/search",
		map[string]string{"mid": "2"}, true); err != nil {
		t.Fatalf("doGet: %v", err)
	}
	if navHits != 1 {
		t.Fatalf("nav hits = %d, want 1", navHits)
	}
	if d.mixinKey != "ea1db124af3c7062474693fa704f4ff8" {
		t.Fatalf("mixinKey = %q", d.mixinKey)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/bilibili/ -run TestDoGet -v`
Expected: 编译失败（http.go 不存在）

- [ ] **Step 3: 实现 `http.go`**

```go
package bilibili

import (
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
	if d.mixinKey != "" && d.mixinKeyDay == today {
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
```

> 注意：`fetchMixinKey` 里 `d.doGet(nav)` 是 signed=false，不会递归。`d.mixinKeyDay`、`d.mixinKey` 字段加到 Task 1 的 `Bilibili` struct（本文件顶部以注释提示：在 meta.go 的 struct 中补 `client *resty.Client; limiter *rate.Limiter; pageDelay time.Duration; cookieStr, mixinKey, mixinKeyDay string`）。日志依赖先不引入（doGet 不记日志），utils import 可去掉。

- [ ] **Step 4: 同步扩展 `meta.go` 的 struct 字段，跑测试**

在 Task 1 的 `Bilibili` struct 中补字段（client/limiter/pageDelay/cookieStr/mixinKey/mixinKeyDay），然后：

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/bilibili/ -run TestDoGet -v`
Expected: 4 个测试全 PASS

- [ ] **Step 5: Commit**

```bash
git add drivers/bilibili/http.go drivers/bilibili/http_test.go drivers/bilibili/meta.go
git commit -m "feat(bilibili): http client with cookie merge, rate limit, wbi signing"
```

---

### Task 4: API 层（api.go）：nav/followings/arc-search/fav/view/playurl

**Files:**
- Create: `drivers/bilibili/api.go`
- Modify: `drivers/bilibili/http_test.go`（在 helper 区追加 `mockRoundTrip`——Task 3 的测试直接内联转发，但 apiBase 硬编码 URL 会泄漏到真实网络；httptest client transport 不重写 host，必须手动改 scheme+host。本任务起所有测试 helper 统一用它）
- Test: `drivers/bilibili/api_test.go`

**Interfaces:**
- Consumes: Task 3 的 `doGet`/`apiBase`；Task 1 的 struct
- Produces（Task 6/7 依赖；字段名与 2025-07 bilibili API 文档及 BBDown/PiliPlus 源码一致）:
  - `func (d *Bilibili) navInfo(ctx) (uid int64, uname string, err error)` —— 校验登录并缓存 `d.uid`/`d.uname`
  - `func (d *Bilibili) followings(ctx) ([]FollowItem, error)` —— 全量分页聚合（ps=50）
  - `type FollowItem struct { Mid int64; Uname string }`
  - `func (d *Bilibili) upVideos(ctx, mid int64) ([]VideoItem, error)` —— space/wbi/arc/search 全量分页（ps=50，wbi）
  - `type VideoItem struct { Bvid string; Title string; Pic string; Pubdate int64; Cid int64 }`（Cid=0 表示需 view 补）
  - `func (d *Bilibili) favFolders(ctx) ([]FavFolder, error)` —— created/list-all（无分页）
  - `type FavFolder struct { ID int64; Title string }`
  - `func (d *Bilibili) favVideos(ctx, mediaID int64) ([]VideoItem, error)` —— fav/resource/list 分页（ps=20）
  - `func (d *Bilibili) videoCid(ctx, bvid string) (int64, error)` —— view 接口补 cid
  - `func (d *Bilibili) playURLDurl(ctx, bvid string, cid int64) (url string, size int64, err error)` —— player/wbi/playurl（wbi），取 `data.durl[0]`
  - 包级泛型函数 `func collectPages[T any](d *Bilibili, ctx context.Context, pageSize int, fetch func(pn int) ([]T, int, error), out *[]T) error` —— 分页聚合（pageDelay 间隔与 MaxListItems 截断集中在这里）。注意：**Go 方法不能声明类型参数，必须是包级函数**

- [ ] **Step 0: 先往 `drivers/bilibili/http_test.go` 的 helper 区追加 `mockRoundTrip`**

（原因见 Files 段：httptest client transport 不重写 host，硬编码 apiBase/passportBase 的请求会泄漏到真实网络。Task 4 起所有测试统一用此 helper 转发。）

```go
// mockRoundTrip 把任意请求（含硬编码的 apiBase/passportBase URL）转发到 srv：
// httptest server 的 client transport 是标准 transport，会 dial req.URL.Host，
// 所以必须先改写 scheme+host
func mockRoundTrip(srv *httptest.Server) roundTripFunc {
	return roundTripFunc(func(req *http.Request) (*http.Response, error) {
		req2 := req.Clone(req.Context())
		req2.URL.Scheme = "http"
		req2.URL.Host = strings.TrimPrefix(srv.URL, "http://")
		return srv.Client().Transport.RoundTrip(req2)
	})
}
```

> 此函数需要 `net/http/httptest` 与 `strings` import——http_test.go 均已具备。

- [ ] **Step 1: 写失败测试 `api_test.go`**

mock 数据与真实接口字段一致（vlist 无 cid！收藏 medias 在 `ugc.first_cid`）：

```go
package bilibili

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/go-resty/resty/v2"
)

// apiDriver：单 handler mock（按 path/query 自行分发）
func apiDriver(t *testing.T, handler http.HandlerFunc) *Bilibili {
	t.Helper()
	d := newTestDriver()
	srv := newMockServer(t, handler)
	d.client = resty.New().SetTransport(mockRoundTrip(srv))
	return d
}

func jsonResp(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(body))
}

func TestNavInfo(t *testing.T) {
	d := apiDriver(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/x/web-interface/nav" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		jsonResp(w, `{"code":0,"data":{"isLogin":true,"mid":12345,"uname":"测试君"}}`)
	})
	uid, uname, err := d.navInfo(context.Background())
	if err != nil || uid != 12345 || uname != "测试君" {
		t.Fatalf("navInfo = %d %q %v", uid, uname, err)
	}
}

func TestFollowingsPagination(t *testing.T) {
	// 2 页 × 每页 2 条 = 4 人
	page1 := `{"code":0,"data":{"list":[{"mid":1,"uname":"A"},{"mid":2,"uname":"B"}],"total":4}}`
	page2 := `{"code":0,"data":{"list":[{"mid":3,"uname":"C"},{"mid":4,"uname":"D"}],"total":4}}`
	d := apiDriver(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/x/relation/followings" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if r.URL.Query().Get("pn") == "1" {
			jsonResp(w, page1)
		} else {
			jsonResp(w, page2)
		}
	})
	items, err := d.followings(context.Background())
	if err != nil {
		t.Fatalf("followings: %v", err)
	}
	if len(items) != 4 || items[0].Uname != "A" || items[3].Mid != 4 {
		t.Fatalf("followings = %+v", items)
	}
}

func TestUpVideosSignedAndParsed(t *testing.T) {
	var query string
	d := apiDriver(t, func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		// vlist 字段与真实接口一致：无 cid
		jsonResp(w, `{"code":0,"data":{"list":{"vlist":[
			{"bvid":"BV1xx","title":"测试视频","pic":"http://i0.hdslb.com/1.jpg","created":1700000000,"length":"10:00"}
		]},"page":{"count":1}}}`)
	})
	d.mixinKey = "ea1db124af3c7062474693fa704f4ff8" // 注入，避免走 nav
	items, err := d.upVideos(context.Background(), 2)
	if err != nil {
		t.Fatalf("upVideos: %v", err)
	}
	if !strings.Contains(query, "w_rid=") {
		t.Fatalf("arc/search not signed: %q", query)
	}
	if len(items) != 1 || items[0].Bvid != "BV1xx" || items[0].Cid != 0 {
		t.Fatalf("upVideos = %+v", items)
	}
}

func TestFavVideosFirstCid(t *testing.T) {
	d := apiDriver(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/x/v3/fav/resource/list" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		jsonResp(w, `{"code":0,"data":{"info":{"media_count":1},"medias":[
			{"id":100,"bvid":"BV1yy","title":"收藏视频","cover":"http://i0.hdslb.com/2.jpg",
			 "fav_time":1700000001,"ugc":{"first_cid":888}}]}}`)
	})
	items, err := d.favVideos(context.Background(), 777)
	if err != nil {
		t.Fatalf("favVideos: %v", err)
	}
	if len(items) != 1 || items[0].Bvid != "BV1yy" || items[0].Cid != 888 {
		t.Fatalf("favVideos = %+v", items)
	}
}

func TestVideoCidViaView(t *testing.T) {
	d := apiDriver(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/x/web-interface/view" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		jsonResp(w, `{"code":0,"data":{"bvid":"BV1xx","cid":555,"title":"测试"}}`)
	})
	cid, err := d.videoCid(context.Background(), "BV1xx")
	if err != nil || cid != 555 {
		t.Fatalf("videoCid = %d, %v", cid, err)
	}
}

func TestPlayURLDurl(t *testing.T) {
	d := apiDriver(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/x/player/wbi/playurl" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		jsonResp(w, `{"code":0,"data":{"durl":[{"url":"https://upos.example/video.mp4?t=1","size":1024000}]}}`)
	})
	u, size, err := d.playURLDurl(context.Background(), "BV1xx", 555)
	if err != nil || u == "" || size != 1024000 {
		t.Fatalf("playURLDurl = %q %d %v", u, size, err)
	}
}

func TestPlayURLDurlEmpty(t *testing.T) {
	d := apiDriver(t, func(w http.ResponseWriter, r *http.Request) {
		jsonResp(w, `{"code":0,"data":{"dash":{"video":[]},"durl":null}}`)
	})
	_, _, err := d.playURLDurl(context.Background(), "BV1xx", 555)
	if err == nil {
		t.Fatal("want error when durl empty (dash-only video)")
	}
}

func TestCollectPagesMaxLimit(t *testing.T) {
	d := newTestDriver()
	d.Addition.MaxListItems = 3
	var got []int
	err := collectPages(d, context.Background(), 50, func(pn int) ([]int, int, error) {
		start := (pn - 1) * 2
		return []int{start + 1, start + 2}, 100, nil
	}, &got)
	if err != nil {
		t.Fatalf("collectPages: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d items, want 3 (MaxListItems cut)", len(got))
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/bilibili/ -run 'TestNavInfo|TestFollowings|TestUpVideos|TestFavVideos|TestVideoCid|TestPlayURL|TestCollectPages' -v`
Expected: 编译失败（api.go 不存在）

- [ ] **Step 3: 实现 `api.go`**

```go
package bilibili

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// ---- 类型 ----

type FollowItem struct {
	Mid   int64
	Uname string
}

type VideoItem struct {
	Bvid    string
	Title   string
	Pic     string // http:// 开头，展示时前端可访问；驱动统一转 https 见 driver.go
	Pubdate int64
	Cid     int64 // 0 = 需调 videoCid 补
}

type FavFolder struct {
	ID    int64
	Title string
}

// ---- 内部响应结构（与真实接口字段一致） ----

type navData struct {
	IsLogin bool   `json:"isLogin"`
	Mid     int64  `json:"mid"`
	Uname   string `json:"uname"`
	WbiImg  struct {
		ImgURL string `json:"img_url"`
		SubURL string `json:"sub_url"`
	} `json:"wbi_img"`
}

type followingPage struct {
	List  []struct {
		Mid   int64  `json:"mid"`
		Uname string `json:"uname"`
	} `json:"list"`
	Total int `json:"total"`
}

type arcPage struct {
	List struct {
		Vlist []struct {
			Bvid    string `json:"bvid"`
			Title   string `json:"title"`
			Pic     string `json:"pic"`
			Created int64  `json:"created"`
		} `json:"vlist"`
	} `json:"list"`
	Page struct {
		Count int `json:"count"`
	} `json:"page"`
}

type favFolderResp struct {
	List []struct {
		ID    int64  `json:"id"`
		Title string `json:"title"`
	} `json:"list"`
}

type favResourcePage struct {
	Info struct {
		MediaCount int `json:"media_count"`
	} `json:"info"`
	Medias []struct {
		Bvid    string `json:"bvid"`
		Title   string `json:"title"`
		Cover   string `json:"cover"`
		FavTime int64  `json:"fav_time"`
		Ugc     struct {
			FirstCid int64 `json:"first_cid"`
		} `json:"ugc"`
	} `json:"medias"`
}

type viewData struct {
	Cid int64 `json:"cid"`
}

type playurlData struct {
	Durl []struct {
		URL  string `json:"url"`
		Size int64  `json:"size"`
	} `json:"durl"`
}

// ---- 请求函数 ----

func (d *Bilibili) navInfo(ctx context.Context) (int64, string, error) {
	raw, err := d.doGet(ctx, apiBase+"/x/web-interface/nav", nil, false)
	if err != nil {
		return 0, "", err
	}
	var data navData
	if err := json.Unmarshal(raw, &data); err != nil {
		return 0, "", err
	}
	if !data.IsLogin || data.Mid == 0 {
		return 0, "", fmt.Errorf("bilibili 未登录（nav isLogin=false），请重新保存存储触发扫码登录")
	}
	d.uid, d.uname = data.Mid, data.Uname
	return data.Mid, data.Uname, nil
}

// collectPages 通用分页聚合：fetch 返回一页 items 与 total；受 MaxListItems 与 pageDelay 约束
// 包级函数（Go 方法不允许类型参数）
func collectPages[T any](d *Bilibili, ctx context.Context, pageSize int,
	fetch func(pn int) ([]T, int, error), out *[]T) error {
	maxItems := d.MaxListItems
	if maxItems == 0 {
		maxItems = int(^uint(0) >> 1) // 不限
	}
	pn := 1
	for {
		items, total, err := fetch(pn)
		if err != nil {
			return err
		}
		for _, it := range items {
			*out = append(*out, it)
			if len(*out) >= maxItems {
				return nil
			}
		}
		if len(*out) >= total || len(items) == 0 || pn*pageSize >= total {
			return nil
		}
		pn++
		if d.pageDelay > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(d.pageDelay):
			}
		}
	}
}

func (d *Bilibili) followings(ctx context.Context) ([]FollowItem, error) {
	out := make([]FollowItem, 0, 64)
	err := collectPages(d, ctx, 50, func(pn int) ([]FollowItem, int, error) {
		raw, err := d.doGet(ctx, apiBase+"/x/relation/followings", map[string]string{
			"vmid": strconv.FormatInt(d.uid, 10), "pn": strconv.Itoa(pn), "ps": "50",
			"order": "desc", "order_type": "",
		}, false)
		if err != nil {
			return nil, 0, err
		}
		var page followingPage
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, 0, err
		}
		items := make([]FollowItem, 0, len(page.List))
		for _, f := range page.List {
			items = append(items, FollowItem{Mid: f.Mid, Uname: f.Uname})
		}
		return items, page.Total, nil
	}, &out)
	return out, err
}

func (d *Bilibili) upVideos(ctx context.Context, mid int64) ([]VideoItem, error) {
	out := make([]VideoItem, 0, 64)
	err := collectPages(d, ctx, 50, func(pn int) ([]VideoItem, int, error) {
		raw, err := d.doGet(ctx, apiBase+"/x/space/wbi/arc/search", map[string]string{
			"mid": strconv.FormatInt(mid, 10), "pn": strconv.Itoa(pn), "ps": "50",
			"order": "pubdate", "tid": "0",
		}, true) // wbi 签名
		if err != nil {
			return nil, 0, err
		}
		var page arcPage
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, 0, err
		}
		items := make([]VideoItem, 0, len(page.List.Vlist))
		for _, v := range page.List.Vlist {
			items = append(items, VideoItem{Bvid: v.Bvid, Title: v.Title, Pic: v.Pic, Pubdate: v.Created})
		}
		return items, page.Page.Count, nil
	}, &out)
	return out, err
}

func (d *Bilibili) favFolders(ctx context.Context) ([]FavFolder, error) {
	raw, err := d.doGet(ctx, apiBase+"/x/v3/fav/folder/created/list-all", map[string]string{
		"up_mid": strconv.FormatInt(d.uid, 10),
	}, false)
	if err != nil {
		return nil, err
	}
	var resp favFolderResp
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	out := make([]FavFolder, 0, len(resp.List))
	for _, f := range resp.List {
		out = append(out, FavFolder{ID: f.ID, Title: f.Title})
	}
	return out, nil
}

func (d *Bilibili) favVideos(ctx context.Context, mediaID int64) ([]VideoItem, error) {
	out := make([]VideoItem, 0, 64)
	err := collectPages(d, ctx, 20, func(pn int) ([]VideoItem, int, error) {
		raw, err := d.doGet(ctx, apiBase+"/x/v3/fav/resource/list", map[string]string{
			"media_id": strconv.FormatInt(mediaID, 10), "pn": strconv.Itoa(pn), "ps": "20",
			"order": "mtime", "type": "2", "tid": "0", "platform": "web",
		}, false)
		if err != nil {
			return nil, 0, err
		}
		var page favResourcePage
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, 0, err
		}
		items := make([]VideoItem, 0, len(page.Medias))
		for _, m := range page.Medias {
			items = append(items, VideoItem{Bvid: m.Bvid, Title: m.Title, Pic: m.Cover, Pubdate: m.FavTime, Cid: m.Ugc.FirstCid})
		}
		return items, page.Info.MediaCount, nil
	}, &out)
	return out, err
}

func (d *Bilibili) videoCid(ctx context.Context, bvid string) (int64, error) {
	raw, err := d.doGet(ctx, apiBase+"/x/web-interface/view", map[string]string{"bvid": bvid}, false)
	if err != nil {
		return 0, err
	}
	var data viewData
	if err := json.Unmarshal(raw, &data); err != nil {
		return 0, err
	}
	if data.Cid == 0 {
		return 0, fmt.Errorf("view 接口未返回 cid: %s", bvid)
	}
	return data.Cid, nil
}

func (d *Bilibili) playURLDurl(ctx context.Context, bvid string, cid int64) (string, int64, error) {
	raw, err := d.doGet(ctx, apiBase+"/x/player/wbi/playurl", map[string]string{
		"bvid": bvid, "cid": strconv.FormatInt(cid, 10),
		"qn": "64", "fnval": "1", "fnver": "0", "fourk": "0", "otype": "json",
	}, true) // wbi 签名
	if err != nil {
		return "", 0, err
	}
	var data playurlData
	if err := json.Unmarshal(raw, &data); err != nil {
		return "", 0, err
	}
	if len(data.Durl) == 0 || data.Durl[0].URL == "" {
		return "", 0, fmt.Errorf("该视频无 durl 直链（dash-only），当前版本仅支持 durl 播放")
	}
	return data.Durl[0].URL, data.Durl[0].Size, nil
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/bilibili/ -v`
Expected: 全部 PASS（含 Task 2/3 测试）

> 若 `TestCollectPagesMaxLimit` 因 `d.pageDelay` 未初始化（nil driver）失败，在测试里显式 `d.pageDelay = 0`（newTestDriver 已有）。`newTestDriver` 需补 `d.MaxListItems = 500` 默认一致性（collectPages 用 0=unlimited，测试显式设 3 即可，不强制）。

- [ ] **Step 5: Commit**

```bash
git add drivers/bilibili/api.go drivers/bilibili/api_test.go
git commit -m "feat(bilibili): api layer (nav/followings/arc-search/fav/view/playurl)"
```

---

### Task 5: 扫码登录（login.go）

**Files:**
- Create: `drivers/bilibili/login.go`
- Test: `drivers/bilibili/login_test.go`

**Interfaces:**
- Consumes: Task 3 的 `doGet`/`doPostForm`/`passportBase`；`op.MustSaveDriverStorage`（internal/op）
- Produces:
  - `func (d *Bilibili) loginByQRCode(ctx context.Context) error` —— Init 中 cookie 为空时调用；"未扫/待确认/过期"时返回含 HTML 二维码的错误（`fmt.Errorf("need verify: \n%s", html)`，189pc 同款前缀）；成功时回填 `Addition.Cookie` + `MustSaveDriverStorage` 并返回 nil
  - `func qrLoginHTML(stateText string, qrURL string) (string, error)` —— base64 PNG 二维码 HTML（可单测的纯函数）

**状态机**（BBDown 一致）：
- `d.qrcodeKey == ""` → POST `passportBase/x/passport-login/web/qrcode/generate?source=main-fe-header`（BBDown 用 GET，PiliPlus 用 GET；实测两可——用 GET 走 doGet 无需 form）→ 存 `d.qrcodeKey`、`d.qrURL`
- GET `passportBase/x/passport-login/web/qrcode/poll?qrcode_key={key}&source=main-fe-header` → `data.code`：
  - `86101` 未扫 / `86090` 已扫待确认 → 返回 HTML 错误（文案区分）
  - `86038` 过期 → 清 `d.qrcodeKey` → 返回 HTML 错误"已过期，请再次保存刷新"
  - `0` 成功 → 解析 `data.url` query 中 `SESSDATA`/`DedeUserID`/`bili_jct` → 组 cookie 串（`SESSDATA=..;DedeUserID=..;bili_jct=..`）→ `d.Addition.Cookie = 串` → `op.MustSaveDriverStorage(d)` → 清 qrcodeKey → nil

- [ ] **Step 1: 写失败测试 `login_test.go`**

```go
package bilibili

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/go-resty/resty/v2"
)

func TestQRLoginHTMLContainsImage(t *testing.T) {
	html, err := qrLoginHTML("等待扫码", "https://passport.bilibili.com/x/passport-login/web/qrcode/generate?foo=1")
	if err != nil {
		t.Fatalf("qrLoginHTML: %v", err)
	}
	if !strings.Contains(html, "data:image/png;base64,") {
		t.Fatal("html missing base64 png")
	}
	if !strings.Contains(html, "等待扫码") {
		t.Fatal("html missing state text")
	}
}

func TestLoginByQRCodeGenerateThenPollPending(t *testing.T) {
	// 第一次：无 key → generate；poll 返回 86101 → HTML 错误
	var hits int
	srv := newMockServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		switch r.URL.Path {
		case "/x/passport-login/web/qrcode/generate":
			w.Write([]byte(`{"code":0,"data":{"url":"https://passport.bilibili.com/x/passport-login/web/qrcode/generate?qrcode_key=k123","qrcode_key":"k123"}}`))
		case "/x/passport-login/web/qrcode/poll":
			w.Write([]byte(`{"code":0,"data":{"code":86101,"message":"未扫码"}}`))
		}
	}))
	d := newTestDriver()
	d.client = resty.New().SetTransport(mockRoundTrip(srv))
	err := d.loginByQRCode(context.Background())
	if err == nil {
		t.Fatal("want HTML error when not scanned")
	}
	if !strings.Contains(err.Error(), "need verify:") || !strings.Contains(err.Error(), "base64") {
		t.Fatalf("err not HTML qr: %v", err)
	}
	if d.qrcodeKey != "k123" {
		t.Fatalf("qrcodeKey = %q, want k123", d.qrcodeKey)
	}
}

func TestLoginByQRCodeSuccessFillsCookie(t *testing.T) {
	srv := newMockServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/x/passport-login/web/qrcode/generate":
			w.Write([]byte(`{"code":0,"data":{"url":"https://passport.bilibili.com/x/passport-login/web/qrcode/generate?qrcode_key=k1","qrcode_key":"k1"}}`))
		case "/x/passport-login/web/qrcode/poll":
			w.Write([]byte(`{"code":0,"data":{"code":0,"url":"https://passport.bilibili.com/login?SESSDATA=abc123&DedeUserID=42&bili_jct=csrf"}}`))
		}
	}))
	d := newTestDriver()
	d.client = resty.New().SetTransport(mockRoundTrip(srv))
	err := d.loginByQRCode(context.Background())
	if err != nil {
		t.Fatalf("loginByQRCode: %v", err)
	}
	if !strings.Contains(d.Addition.Cookie, "SESSDATA=abc123") {
		t.Fatalf("cookie not filled: %q", d.Addition.Cookie)
	}
	if d.qrcodeKey != "" {
		t.Fatalf("qrcodeKey should be cleared after success")
	}
}

func TestLoginByQRCodeExpired(t *testing.T) {
	srv := newMockServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/x/passport-login/web/qrcode/generate":
			w.Write([]byte(`{"code":0,"data":{"url":"https://passport.bilibili.com/x/passport-login/web/qrcode/generate?qrcode_key=k2","qrcode_key":"k2"}}`))
		case "/x/passport-login/web/qrcode/poll":
			w.Write([]byte(`{"code":0,"data":{"code":86038,"message":"二维码已失效"}}`))
		}
	}))
	d := newTestDriver()
	d.client = resty.New().SetTransport(mockRoundTrip(srv))
	err := d.loginByQRCode(context.Background())
	if err == nil || !strings.Contains(err.Error(), "过期") {
		t.Fatalf("err = %v, want expired hint", err)
	}
	if d.qrcodeKey != "" {
		t.Fatal("qrcodeKey should reset on expire")
	}
}

func TestQRLoginStateDistinct(t *testing.T) {
	// 86101 与 86090 文案不同（用于确认）
	srv := newMockServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/x/passport-login/web/qrcode/generate":
			w.Write([]byte(`{"code":0,"data":{"url":"https://p/x?qrcode_key=k","qrcode_key":"k"}}`))
		case "/x/passport-login/web/qrcode/poll":
			w.Write([]byte(`{"code":0,"data":{"code":86090}}`))
		}
	}))
	d := newTestDriver()
	d.client = resty.New().SetTransport(mockRoundTrip(srv))
	err := d.loginByQRCode(context.Background())
	if err == nil || !strings.Contains(err.Error(), "确认") {
		t.Fatalf("err = %v, want confirm hint for 86090", err)
	}
}
```

> 注：`TestLoginByQRCodeSuccessFillsCookie` 中 `op.MustSaveDriverStorage(d)` 会真实触发——测试 driver 非持久化 storage 会怎样？`MustSaveDriverStorage` 内部调 `SaveDriverStorage`，对无 ID 的 storage 可能报错。**实现时把保存调用包在"仅当 d.GetStorage().ID 非空"的条件里**（测试 driver ID 为空跳过保存；真实驱动 ID 存在正常保存）。在 login.go 中实现为：

```go
	if d.GetStorage().ID != 0 {
		op.MustSaveDriverStorage(d)
	}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/bilibili/ -run TestLogin -v`
Expected: 编译失败（login.go 不存在）

- [ ] **Step 3: 实现 `login.go`**

```go
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
```

- [ ] **Step 4: 跑测试确认通过**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/bilibili/ -run TestLogin -v`
Expected: 全 PASS

- [ ] **Step 5: Commit**

```bash
git add drivers/bilibili/login.go drivers/bilibili/login_test.go
git commit -m "feat(bilibili): qr code login (generate/poll + html verify)"
```

---

### Task 6: List 目录树（driver.go 上半）

**Files:**
- Create: `drivers/bilibili/driver.go`
- Modify: `drivers/bilibili/meta.go`（打开 Task 1 注释掉的 `init()` 注册）
- Test: `drivers/bilibili/driver_test.go`

**Interfaces:**
- Consumes: Task 1 Addition/config、Task 4 API 层全部函数与类型、Task 3 struct 字段
- Produces:
  - `func (d *Bilibili) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error)`
  - `func (d *Bilibili) Get(ctx context.Context, path string) (model.Obj, error)` —— 实现 `driver.Getter`：对"/"、"/我的关注"、"/我的收藏"直接构造；其余路径返回 `errs.NotSupport` 让框架回退 List（internal/op/fs.go 对 NotSupport 错误回退父目录 List）
  - `type videoObj struct { model.ObjThumb; bvid string; cid int64 }`（Link 断言用）
  - `func sanitizeName(s string, maxLen int) string`（文件名清洗，纯函数）
  - `func splitFolderName(name string) (display string, id int64, ok bool)`（`名字_123` → 按最后 `_` 解析）
  - 常量：`dirFollow = "我的关注"`、`dirFav = "我的收藏"`

**目录结构**（spec 决策）：
```
/ 根：我的关注、我的收藏 两个文件夹
我的关注：FollowItem 每项 → 文件夹 "{sanitize(uname)}_ {mid}"…（注意：格式 "{uname}_{mid}"）
UP 文件夹：upVideos(mid) → videoObj（Name=sanitize(title)+".mp4"，无 cid → Link 时补）
我的收藏：favFolders → 文件夹 "{sanitize(title)}_{id}"
收藏夹文件夹：favVideos(id) → videoObj
```
文件 Modified = time.Unix(Pubdate,0)；文件夹 Modified = time.Now()。封面 Pic http→https（strings.Replace(pic, "http://", "https://", 1)）填 Thumbnail。Path 用 filepath.Join(dir.GetPath(), name)。

- [ ] **Step 1: 写失败测试 `driver_test.go`**

```go
package bilibili

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/go-resty/resty/v2"
)

const rootObjPath = "/" // List 的 dir 由框架传入

func listDriver(t *testing.T, handler http.HandlerFunc) *Bilibili {
	t.Helper()
	d := newTestDriver()
	d.uid = 12345
	srv := newMockServer(t, handler)
	d.client = resty.New().SetTransport(mockRoundTrip(srv))
	return d
}

func dirObj(t *testing.T, path string) model.Obj {
	t.Helper()
	return &model.Object{Name: pathName(path), Path: path, IsFolder: true}
}

func pathName(p string) string {
	if p == "/" {
		return "root"
	}
	parts := strings.Split(strings.Trim(p, "/"), "/")
	return parts[len(parts)-1]
}

func TestListRoot(t *testing.T) {
	d := listDriver(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("root list must not call api")
	})
	objs, err := d.List(context.Background(), dirObj(t, "/"), model.ListArgs{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(objs) != 2 || !objs[0].IsDir() || objs[0].GetName() != "我的关注" || objs[1].GetName() != "我的收藏" {
		t.Fatalf("root objs = %+v", objs)
	}
}

func TestListFollowings(t *testing.T) {
	d := listDriver(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/x/relation/followings" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		w.Write([]byte(`{"code":0,"data":{"list":[{"mid":42,"uname":"测试UP"},{"mid":7,"uname":"带_下划线"}],"total":2}}`))
	})
	objs, err := d.List(context.Background(), dirObj(t, "/我的关注"), model.ListArgs{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(objs) != 2 {
		t.Fatalf("objs = %d", len(objs))
	}
	f0 := objs[0]
	if !f0.IsDir() || f0.GetName() != "测试UP_42" {
		t.Fatalf("folder0 = %q dir=%v", f0.GetName(), f0.IsDir())
	}
	if objs[1].GetName() != "带_下划线_7" {
		t.Fatalf("folder1 = %q", objs[1].GetName())
	}
}

func TestListUpperVideos(t *testing.T) {
	d := listDriver(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/x/space/wbi/arc/search" {
			w.Write([]byte(`{"code":0,"data":{"list":{"vlist":[
				{"bvid":"BV1a","title":"最新视频","pic":"http://i0.hdslb.com/p.jpg","created":1700000100}
			]},"page":{"count":1}}}`))
			return
		}
		t.Fatalf("unexpected path %s", r.URL.Path)
	})
	objs, err := d.List(context.Background(), dirObj(t, "/我的关注/测试UP_42"), model.ListArgs{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("objs = %d", len(objs))
	}
	vo, ok := objs[0].(*videoObj)
	if !ok {
		t.Fatalf("obj type = %T, want *videoObj", objs[0])
	}
	if vo.GetName() != "最新视频.mp4" || vo.bvid != "BV1a" || vo.cid != 0 {
		t.Fatalf("video = %q bvid=%s cid=%d", vo.GetName(), vo.bvid, vo.cid)
	}
	if vo.Thumbnail.Thumbnail != "https://i0.hdslb.com/p.jpg" {
		t.Fatalf("thumb = %q", vo.Thumbnail.Thumbnail)
	}
}

func TestListFavFolders(t *testing.T) {
	d := listDriver(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":0,"data":{"list":[{"id":999,"title":"我的收藏夹"}]}}`))
	})
	objs, err := d.List(context.Background(), dirObj(t, "/我的收藏"), model.ListArgs{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(objs) != 1 || objs[0].GetName() != "我的收藏夹_999" {
		t.Fatalf("objs = %+v", objs)
	}
}

func TestListFavVideos(t *testing.T) {
	d := listDriver(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":0,"data":{"info":{"media_count":1},"medias":[
			{"bvid":"BV1b","title":"收藏的教程","cover":"http://i0.hdslb.com/c.jpg","fav_time":1700000200,"ugc":{"first_cid":555}}]}}`))
	})
	objs, err := d.List(context.Background(), dirObj(t, "/我的收藏/我的收藏夹_999"), model.ListArgs{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	vo, ok := objs[0].(*videoObj)
	if !ok {
		t.Fatalf("obj type = %T", objs[0])
	}
	if vo.cid != 555 || vo.bvid != "BV1b" {
		t.Fatalf("video bvid=%s cid=%d", vo.bvid, vo.cid)
	}
}

func TestSanitizeName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"正常标题", "正常标题"},
		{`a/b\c:d*e?f"g<h>i|j`, "a_b_c_d_e_f_g_h_i_j"},
		{"  前后空格  ", "前后空格"},
		// 150 rune 截断："很长标题" 4 runes + 146 x
		{"很长标题" + strings.Repeat("x", 200), "很长标题" + strings.Repeat("x", 146)},
	}
	for _, c := range cases {
		if got := sanitizeName(c.in, 150); got != c.want {
			t.Errorf("sanitizeName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := sanitizeName("///", 10); got == "" {
		t.Fatal("sanitize of all-illegal should not be empty")
	}
}

func TestSplitFolderName(t *testing.T) {
	display, id, ok := splitFolderName("测试UP_42")
	if !ok || display != "测试UP" || id != 42 {
		t.Fatalf("split = %q %d %v", display, id, ok)
	}
	display, id, ok = splitFolderName("带_下划线_7")
	if !ok || display != "带_下划线" || id != 7 {
		t.Fatalf("split underscore = %q %d %v", display, id, ok)
	}
	if _, _, ok := splitFolderName("没有数字"); ok {
		t.Fatal("should not parse without trailing id")
	}
}

func TestGetShallowPaths(t *testing.T) {
	d := newTestDriver()
	obj, err := d.Get(context.Background(), "/我的关注")
	if err != nil || !obj.IsDir() {
		t.Fatalf("Get /我的关注 = %v %v", obj, err)
	}
	if _, err := d.Get(context.Background(), "/我的关注/某某_1/BV1xx.mp4"); !errs.IsNotSupportError(err) {
		t.Fatalf("deep Get err = %v, want NotSupport", err)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/bilibili/ -run 'TestList|TestSanitize|TestSplit|TestGet' -v`
Expected: 编译失败（driver.go 不存在）

- [ ] **Step 3: 实现 `driver.go`**

```go
package bilibili

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

const (
	dirFollow = "我的关注"
	dirFav    = "我的收藏"
	rootName  = "root"
)

// videoObj 视频文件对象；私有字段供 Link 使用
type videoObj struct {
	model.ObjThumb
	bvid string
	cid  int64 // 0 = Link 时经 view 补
}

// sanitizeName 清洗文件名非法字符并截断；结果非空
func sanitizeName(s string, maxLen int) string {
	repl := strings.NewReplacer(
		"/", "_", "\\", "_", ":", "_", "*", "_", "?", "_",
		"\"", "_", "<", "_", ">", "_", "|", "_",
		"\r", "", "\n", "", "\t", "",
	)
	s = strings.TrimSpace(repl.Replace(s))
	s = strings.Trim(s, " .")
	if s == "" {
		s = "未命名"
	}
	if len([]rune(s)) > maxLen {
		s = string([]rune(s)[:maxLen])
	}
	return s
}

// splitFolderName 解析 "{名字}_{id}" 目录名（按最后一个 _ 且其后全数字）
func splitFolderName(name string) (string, int64, bool) {
	idx := -1
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '_' {
			idx = i
			break
		}
	}
	if idx <= 0 || idx == len(name)-1 {
		return "", 0, false
	}
	id, err := strconv.ParseInt(name[idx+1:], 10, 64)
	if err != nil {
		return "", 0, false
	}
	return name[:idx], id, true
}

func folderObj(parent model.Obj, name string) model.Obj {
	return &model.Object{
		Name:     name,
		Path:     filepath.Join(parent.GetPath(), name),
		IsFolder: true,
		Modified: time.Now(),
	}
}

func newVideoObj(parent model.Obj, v VideoItem) *videoObj {
	vo := &videoObj{bvid: v.Bvid, cid: v.Cid}
	vo.Name = sanitizeName(v.Title, 150) + ".mp4"
	vo.Path = filepath.Join(parent.GetPath(), vo.Name)
	vo.Modified = time.Unix(v.Pubdate, 0)
	if v.Pic != "" {
		vo.Thumbnail = model.Thumbnail{Thumbnail: strings.Replace(v.Pic, "http://", "https://", 1)}
	}
	return vo
}

// List 按目录路径分发（LocalSort=false，返回顺序即展示顺序）
func (d *Bilibili) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {
	p := dir.GetPath()
	switch {
	case p == "" || p == "/": // 根：RootPath 未填时 Path 可能为空串
		return []model.Obj{
			folderObj(dir, dirFollow),
			folderObj(dir, dirFav),
		}, nil
	case p == "/"+dirFollow:
		return d.listFollowings(ctx, dir)
	case strings.HasPrefix(p, "/"+dirFollow+"/"):
		_, mid, ok := splitFolderName(filepath.Base(p))
		if !ok {
			return nil, errs.ObjectNotFound
		}
		return d.listUpVideos(ctx, dir, mid)
	case p == "/"+dirFav:
		return d.listFavFolders(ctx, dir)
	case strings.HasPrefix(p, "/"+dirFav+"/"):
		_, mediaID, ok := splitFolderName(filepath.Base(p))
		if !ok {
			return nil, errs.ObjectNotFound
		}
		return d.listFavVideos(ctx, dir, mediaID)
	}
	return nil, errs.ObjectNotFound
}

func (d *Bilibili) listFollowings(ctx context.Context, dir model.Obj) ([]model.Obj, error) {
	items, err := d.followings(ctx)
	if err != nil {
		return nil, err
	}
	objs := make([]model.Obj, 0, len(items))
	for _, f := range items {
		objs = append(objs, folderObj(dir, sanitizeName(f.Uname, 80)+"_"+strconv.FormatInt(f.Mid, 10)))
	}
	return objs, nil
}

func (d *Bilibili) listUpVideos(ctx context.Context, dir model.Obj, mid int64) ([]model.Obj, error) {
	items, err := d.upVideos(ctx, mid)
	if err != nil {
		return nil, err
	}
	objs := make([]model.Obj, 0, len(items))
	for i := range items {
		objs = append(objs, newVideoObj(dir, items[i]))
	}
	return objs, nil
}

func (d *Bilibili) listFavFolders(ctx context.Context, dir model.Obj) ([]model.Obj, error) {
	items, err := d.favFolders(ctx)
	if err != nil {
		return nil, err
	}
	objs := make([]model.Obj, 0, len(items))
	for _, f := range items {
		objs = append(objs, folderObj(dir, sanitizeName(f.Title, 80)+"_"+strconv.FormatInt(f.ID, 10)))
	}
	return objs, nil
}

func (d *Bilibili) listFavVideos(ctx context.Context, dir model.Obj, mediaID int64) ([]model.Obj, error) {
	items, err := d.favVideos(ctx, mediaID)
	if err != nil {
		return nil, err
	}
	objs := make([]model.Obj, 0, len(items))
	for i := range items {
		objs = append(objs, newVideoObj(dir, items[i]))
	}
	return objs, nil
}

// Get 浅路径直接构造；深路径 NotSupport 让框架回退父目录 List
func (d *Bilibili) Get(ctx context.Context, path string) (model.Obj, error) {
	switch path {
	case "/":
		return &model.Object{Name: rootName, Path: "/", IsFolder: true}, nil
	case "/" + dirFollow:
		return &model.Object{Name: dirFollow, Path: "/" + dirFollow, IsFolder: true}, nil
	case "/" + dirFav:
		return &model.Object{Name: dirFav, Path: "/" + dirFav, IsFolder: true}, nil
	}
	return nil, errs.NotSupport
}
```

- [ ] **Step 4: 打开 meta.go 的注册并编译**

把 Task 1 中注释掉的 `init()` 恢复（加回 `op` import）：

```go
func init() {
	op.RegisterDriver(func() driver.Driver {
		return &Bilibili{}
	})
}
```

Run: `/Library/Go/sdk/go1.25.4/bin/go build ./drivers/... && /Library/Go/sdk/go1.25.4/bin/go vet ./drivers/bilibili/`
Expected: 通过（此时 List/Get 已实现；Link 尚未实现，driver.Driver 接口还缺 Reader.Link —— 编译会报 Bilibili 缺 Link 方法。**Task 7 补 Link 后注册才完整**；本任务结尾可用 `go build ./drivers/bilibili/`（只编包不触发接口断言，注册处的断言在编译 drivers 包时触发——drivers/all.go import bilibili → init 注册 func() driver.Driver 返回 &Bilibili{}，类型断言在运行时才发生（RegisterDriver 存 factory），编译期不检查返回值是否实现接口？`func() driver.Driver` 返回类型是 driver.Driver 接口，&Bilibili{} 转接口在编译期检查 → 会报错！因此 Task 6 结束时 Link 未实现无法通过编译 —— **把 Link 的桩先补上**（Step 4 内加临时桩）：

```go
// Link 占位，Task 7 实现
func (d *Bilibili) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
	return nil, errs.NotSupport
}
```

Task 7 替换为真实实现。测试仍全跑。

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/bilibili/ -v`
Expected: 全 PASS

- [ ] **Step 5: Commit**

```bash
git add drivers/bilibili/driver.go drivers/bilibili/driver_test.go drivers/bilibili/meta.go
git commit -m "feat(bilibili): virtual directory tree (followings/up videos/fav)"
```

---

### Task 7: Link（durl 播放）+ Init/Drop 整合

**Files:**
- Modify: `drivers/bilibili/driver.go`（替换 Task 6 的 Link 桩；新增 Init/Drop）
- Test: `drivers/bilibili/link_test.go`

**Interfaces:**
- Consumes: Task 4 的 `playURLDurl`/`videoCid`；Task 6 的 `videoObj`；Task 5 的 `loginByQRCode`；Task 3 的 `initClient`
- Produces:
  - `func (d *Bilibili) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error)` —— 见下
  - `func (d *Bilibili) Init(ctx context.Context) error` —— initClient → cookie 空则 loginByQRCode（可能返回 HTML 错误）→ navInfo 校验（失败给"重新保存"提示）→ 幂等
  - `func (d *Bilibili) Drop(ctx context.Context) error` —— 清 qrcodeKey/qrURL/mixinKey/cookieStr

**Link 行为**（spec 决策：方案 C durl）：
1. `file.(*videoObj)` 断言失败 → 报错"文件格式错误"
2. `cid == 0` → `videoCid(ctx, bvid)` 补
3. `playURLDurl(ctx, bvid, cid)` → URL + size
4. 返回 `&model.Link{URL: u, Header: http.Header{"Referer": {ref}, "User-Agent": {browserUA}}, ContentLength: size, Expiration: 110min}`

- [ ] **Step 1: 写失败测试 `link_test.go`**

```go
package bilibili

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/go-resty/resty/v2"
)

func playDriver(t *testing.T, handler http.HandlerFunc) *Bilibili {
	t.Helper()
	d := newTestDriver()
	srv := newMockServer(t, handler)
	d.client = resty.New().SetTransport(mockRoundTrip(srv))
	return d
}

func TestLinkWithCid(t *testing.T) {
	d := playDriver(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/x/player/wbi/playurl" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		w.Write([]byte(`{"code":0,"data":{"durl":[{"url":"https://upos.example/v.mp4?sig=1","size":2048}]}}`))
	})
	file := &videoObj{}
	file.Name = "测试.mp4"
	file.bvid = "BV1xx"
	file.cid = 555
	link, err := d.Link(context.Background(), file, model.LinkArgs{})
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if link.URL != "https://upos.example/v.mp4?sig=1" {
		t.Fatalf("url = %q", link.URL)
	}
	if link.Header.Get("Referer") != "https://www.bilibili.com/" {
		t.Fatalf("referer = %q", link.Header.Get("Referer"))
	}
	if link.ContentLength != 2048 {
		t.Fatalf("content length = %d", link.ContentLength)
	}
	if link.Expiration == nil {
		t.Fatal("expiration should be set")
	}
}

func TestLinkFetchesCidViaView(t *testing.T) {
	d := playDriver(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/x/web-interface/view":
			w.Write([]byte(`{"code":0,"data":{"cid":555}}`))
		case "/x/player/wbi/playurl":
			w.Write([]byte(`{"code":0,"data":{"durl":[{"url":"https://upos.example/v.mp4"}]}}`))
		}
	})
	file := &videoObj{}
	file.Name = "测试.mp4"
	file.bvid = "BV1xx" // cid=0 → 触发 view
	link, err := d.Link(context.Background(), file, model.LinkArgs{})
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if !strings.Contains(link.URL, "upos.example") {
		t.Fatalf("url = %q", link.URL)
	}
}

func TestLinkWrongType(t *testing.T) {
	d := newTestDriver()
	_, err := d.Link(context.Background(), &model.Object{Name: "x"}, model.LinkArgs{})
	if err == nil {
		t.Fatal("want error for non-videoObj")
	}
}

func TestLinkDurlEmptyError(t *testing.T) {
	d := playDriver(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":0,"data":{"dash":{"video":[]}}}`))
	})
	file := &videoObj{}
	file.Name = "x.mp4"
	file.bvid = "BV1xx"
	file.cid = 1
	if _, err := d.Link(context.Background(), file, model.LinkArgs{}); err == nil {
		t.Fatal("want error when durl empty")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/bilibili/ -run TestLink -v`
Expected: 失败（Link 桩返回 NotSupport / 未实现行为）

- [ ] **Step 3: 实现 Link + Init/Drop（driver.go 追加）**

```go
// ---- Link ----

const durlExpiration = 110 * time.Minute // durl URL 约 2h 有效，保守缓存

func (d *Bilibili) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
	vo, ok := file.(*videoObj)
	if !ok {
		return nil, errs.NotSupport
	}
	cid := vo.cid
	if cid == 0 {
		var err error
		cid, err = d.videoCid(ctx, vo.bvid)
		if err != nil {
			return nil, err
		}
	}
	u, size, err := d.playURLDurl(ctx, vo.bvid, cid)
	if err != nil {
		return nil, err
	}
	exp := durlExpiration
	return &model.Link{
		URL:           u,
		ContentLength: size,
		Expiration:    &exp,
		Header: http.Header{
			"Referer":    {"https://www.bilibili.com/"},
			"User-Agent": {browserUA},
		},
	}, nil
}

// ---- Init / Drop ----

func (d *Bilibili) Init(ctx context.Context) error {
	d.initClient()
	if d.cookieStr == "" {
		d.cookieStr = d.Addition.Cookie
	}
	// 无 cookie → 扫码流程（未扫/待确认/过期会返回 HTML 错误）
	if d.cookieStr == "" {
		if err := d.loginByQRCode(ctx); err != nil {
			return err
		}
	}
	// 校验登录态并缓存 uid/uname
	if _, _, err := d.navInfo(ctx); err != nil {
		return err
	}
	return nil
}

func (d *Bilibili) Drop(ctx context.Context) error {
	d.qrcodeKey = ""
	d.qrURL = ""
	d.mixinKey = ""
	d.mixinKeyDay = ""
	d.cookieStr = ""
	d.uid = 0
	d.uname = ""
	return nil
}
```

> driver.go 需补 imports：`net/http`、`time`。`errs.NotSupport` 用于非 videoObj 文件（与 Get 一致，让框架能对文件对象正确处理——文件若不是本驱动产出，走 NotSupport 合适；正常路径 file 必是 videoObj）。

- [ ] **Step 4: 全量测试 + vet**

Run: `/Library/Go/sdk/go1.25.4/bin/go test ./drivers/bilibili/ -v && /Library/Go/sdk/go1.25.4/bin/go vet ./drivers/bilibili/ && /Library/Go/sdk/go1.25.4/bin/go build ./drivers/...`
Expected: 全 PASS、vet 干净、drivers 全量编译通过（注册接口断言此刻满足）

- [ ] **Step 5: Commit**

```bash
git add drivers/bilibili/driver.go drivers/bilibili/link_test.go
git commit -m "feat(bilibili): durl link + init/drop lifecycle"
```

---

### Task 8: 收尾：全量测试、gofmt、README 登记与手测清单

**Files:**
- Modify: 无新增（验证 + 文档性 commit；若 README_cn.md 有驱动清单则登记）
- Test: 全量

- [ ] **Step 1: gofmt + 全量单测**

```bash
/Library/Go/sdk/go1.25.4/bin/gofmt -l -w drivers/bilibili/
/Library/Go/sdk/go1.25.4/bin/go test ./drivers/bilibili/ -v
/Library/Go/sdk/go1.25.4/bin/go build ./...
```

Expected: gofmt 无输出（或已格式化）；测试全 PASS；全仓编译通过

- [ ] **Step 2: 真实环境手测清单（写给用户执行）**

在 commit message 中附注或终端输出以下手测步骤（供用户验证，不进代码）：

```
1. 启动 openlist，管理面板 → 存储 → 添加：驱动选 "Bilibili"，留空 cookie 保存
   → 预期：保存失败，页面显示二维码（HTML 错误渲染）
2. 手机 Bilibili App 扫码 → 再次保存 → 再保存一次（确认后）
   → 预期：保存成功，cookie 字段已回填 SESSDATA=...
3. 打开 /我的关注 → 预期：UP 主文件夹列表（{名字}_{mid}）
4. 点进一个 UP → 预期：投稿视频列表，最新在前，带封面缩略图与发布时间
5. 点一个视频 → 预期：web 播放器直接播放（durl 1080p）
6. /我的收藏 → 收藏夹 → 视频 → 播放
7. 回到存储编辑页把 Cookie 清空保存 → 预期：重新走扫码
```

- [ ] **Step 3: Commit（若 README 有驱动清单则一并登记）**

```bash
git add -A drivers/bilibili/
git commit -m "feat(bilibili): bilibili driver (qr login, followings/up videos/fav, durl play)"
```

---

## Self-Review 记录

- **Spec 覆盖**：Addition（Cookie/MaxListItems/RootPath）→ T1；wbi 签名 → T2；cookie 双通道（种子+Set-Cookie 合并）与限流 → T3；nav/followings/arc-search/fav folder/fav resource/view/playurl 鉴权矩阵 → T4；扫码登录状态机 + HTML 二维码（189pc need verify 模式）→ T5；虚拟目录树/命名（`{名字}_{id}` 尾部分割）/封面 https 归一/无 cid 补 view → T6；Link durl + Expiration 110min + Referer → T7；Init 校验与 Drop → T7；LocalSort=false、MaxListItems、pageDelay、多 P 仅首 P（fav 取 ugc.first_cid / up 投稿取 view cid）→ T4/T6。spec "不做"清单全部未实现（正确）。
- **占位符扫描**：无 TBD；T6 Step4 的 Link 桩是显式标注的临时过渡代码（T7 替换），非占位。
- **修正记录**：collectPages 因"Go 方法不允许类型参数"改为包级泛型函数（调用点全部带 d 参数）；videoObj 封面赋值用 `model.Thumbnail{Thumbnail: url}` 包装（wopan/javdb 同款）；Task 4/6 测试 helper 统一为单 handler mock（apiDriver/listDriver），根目录判断放宽为 `p=="" || p=="/"`（RootPath 未填时 Path 为空串）。
- **类型一致性**：`videoObj{bvid,cid}` 在 T6 定义 T7 消费；`FollowItem/VideoItem/FavFolder` T4 定义 T6 消费；`doGet(ctx,url,params,signed)` T3 定义 T4/T5 消费；`sanitizeName(s,maxLen)`/`splitFolderName` T6 定义同任务消费；`errs.NotSupport` 用于 Get 深路径与 Link 类型不符，与 internal/op/fs.go 的回退语义一致。`mixinKeyDay` 字段在 T3 引入、T7 Drop 清理，一致。
- **测试可运行性**：所有 mock 响应字段与真实接口一致（vlist 无 cid；fav medias 用 `bvid`/`cover`/`ugc.first_cid`/`fav_time`；followings 用 `list[].mid/uname` + `total`；playurl 用 `durl[0].url/size`；qrcode poll 用 `data.code` 86101/86090/86038/0 + 成功 `url`）。测试驱动 `newTestDriver` 统一 `limiter=Inf`、`pageDelay=0`。
- **风险标注**：真实账号可能遇 -352/v_voucher 二次风控（spec 已记 v1.1 按 PiliPlus 补 dm_img_* 参数）；扫码回调 URL 域名 passport.bilibili.com 与 api 域不同但手动 cookie 全量串不受域限制（BBDown 同款）。
