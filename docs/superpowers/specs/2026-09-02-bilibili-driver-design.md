# 设计：Bilibili 驱动（关注 UP 投稿 + 收藏夹浏览播放）

日期：2026-09-02

## 背景与动机

用户需求：在 OpenList 中挂载自己的 bilibili 账号，实现——

1. 浏览**我的关注**（UP 主列表），进入每个 UP 主可看他**最近的投稿**（按发布时间倒序，第一页即最新更新）
2. 浏览**我的收藏**（收藏夹列表 → 各收藏夹内视频）
3. 视频可**直接播放**

### 生态调研结论（2026-09 gh API 实测）

- **Go SDK 生态不可用**：iyear/biligo 停更于 2023-05；WhiteBlue/bilibili-sdk-go 停更于 2017；其余均 <40 stars 或停更。`lux`（Go）bilibili extractor 较旧。
- **风险信号**：bilibili 官方 2026 年起法律清理逆向项目——`Nemo2011/bilibili-api`（Python SDK ⭐4.2k）2026-07 收律师函关停；`SocialSisterYi/bilibili-API-collect`（API 文档 ⭐20k）归档且内容清空。客户端类项目（BBDown ⭐13.9k / PiliPlus ⭐18k / BiliPai ⭐4.4k）截至 2026-09 仍活跃更新。
- 因此**自研实现**：直接参考活跃客户端（PiliPlus / BBDown 源码）的请求构造，不引入任何第三方 SDK 依赖，代码中不引用被下架文档。

### 方案选择

| 议题 | 选项 | 决策 |
|---|---|---|
| 登录 | cookie 直填 vs **交互式扫码** | 扫码（189pc 同款"HTML 二维码错误"机制），cookie 自动回填，也支持手填 |
| 播放格式 | A: dash 双 URL（前端不支持）<br>B: dash + 服务端 ffmpeg 混流（依赖部署环境 ffmpeg）<br>**C: durl 单 mp4 直链** | **C**：`fnval` 不带 dash 位 → bilibili 返回单文件 mp4/flv URL，`Link` 直接返回；上限 1080p（登录账号）。4K/杜比留待后续升级 |
| 目录结构 | 含"关注动态"feed 等 | 用户确认**去掉** feed 流；保留 `我的关注`（UP→投稿）+ `我的收藏`（收藏夹→视频）两级 |
| 依赖 | 第三方 bilibili SDK | 无；resty + go-qrcode（均在现有 go.mod） |

## 新驱动 Bilibili（drivers/bilibili/）

### 文件布局

```
drivers/bilibili/
├── meta.go      # Addition / config / init 注册
├── driver.go    # Driver 骨架：Init/Drop/List/Link
├── login.go     # 扫码登录流程（generate/poll + HTML 二维码）
├── util.go      # resty client、cookie 管理、wbi 签名、限流
├── api.go       # 各 API 调用与响应结构（followings/arc-search/fav/playurl）
└── *_test.go    # 单测
```

注册：`drivers/all.go` 增加 blank import；`cmd/root.go` 已聚合 `drivers` 包，无需改动。驱动名 **"Bilibili"**。

### Addition 与 Config

```go
type Addition struct {
	Cookie       string `json:"cookie" type:"text" help:"扫码登录成功后自动回填；也可手动粘贴浏览器 cookie（需含 SESSDATA）"`
	MaxListItems int    `json:"max_list_items" type:"number" default:"500" help:"每个分页列表（关注/投稿/收藏）最多拉取条数，0=不限；防超大 UP 同步翻页过慢"`
	driver.RootPath
}

var config = driver.Config{
	Name:        "Bilibili",
	LocalSort:   false, // 驱动返回顺序即展示顺序（投稿 pubdate 倒序 = 最新在前）
	NoUpload:    true,
	DefaultRoot: "/",
	Alert:       "仅供个人账号使用；cookie 失效后请重新保存触发扫码",
}
```

- `LocalSort: false`：op 层不做重排（`internal/op/fs.go` 仅在 LocalSort=true 时 SortFiles），驱动返回顺序即目录展示顺序，保证"最新投稿在前"开箱即用，与 jable_tv 一致。
- 存储级 `order_by/order_direction` 选项因 LocalSort=false 不会出现（`internal/op/driver.go` 条件追加）。

### 客户端与 cookie 管理（util.go）

- `resty.New()`，统一 header：浏览器 UA（Chrome 系）、`Referer: https://www.bilibili.com/`。
- **cookie 双通道**：
  - `Addition.Cookie` 为持久化种子（SESSDATA / DedeUserID / bili_jct 等核心 cookie，扫码成功后回填并 `op.MustSaveDriverStorage`）；
  - resty **cookie jar**（`SetCookieJar`）自动吸收每次响应的 `Set-Cookie`（buvid3 / b_nut / b_lsid 等风控临时 cookie，进程内续期）；
  - 请求前将 `Addition.Cookie` 解析播种进 jar（`jar.SetCookies`），重启后由种子重建。
- **风控节流**：进程级 `limiter`（`golang.org/x/time/rate`，5r/s 兜底）+ 列表翻页循环内 150ms 间隔。

### wbi 签名（util.go）

按活跃客户端一致算法自研（单测锁定）：

1. `GET /x/web-interface/nav` → `data.wbi_img.img_url/sub_url` 文件名（去扩展名）拼接
2. 64 位打乱表取前 32 位 → **mixin key**
3. 签名：params 注入 `wts`（秒）→ key 升序 → `url.QueryEscape` 后过滤 `!'()*` → 拼 mixin key → MD5 → `w_rid`
4. mixin key 进程内**按日缓存**（普通变量 + 时间戳，过期即重新通过 nav 获取）

### 扫码登录（login.go，189pc 交互模式）

OpenList 无专用二维码 UI；189pc 先例（drivers/189pc/utils.go `genQRCode`）：驱动返回的**错误信息为内嵌 base64 二维码的 HTML**，前端保存失败时直接渲染该 HTML → 用户手机扫码 → **再次点击保存**触发下一次 Init 轮询。Bilibili 驱动沿用：

1. `Init` 且 `Cookie` 为空 → 进扫码流程：
   - 无进程内 qrcode_key：`POST passport.bilibili.com/x/passport-login/web/qrcode/generate` → 拿 `url`（二维码内容）+ `qrcode_key`，暂存进程内；
   - `GET x/passport-login/web/qrcode/poll?qrcode_key=` 轮询：
     - `86101` 未扫 / `86090` 已扫待确认 → go-qrcode 生成 PNG → base64 → 返回 HTML 错误（提示语区分两种状态）
     - `0` 成功 → 回调 URL query 中提取 `SESSDATA`、`DedeUserID`、`bili_jct` 等 → 组 cookie 串回填 `Addition.Cookie` → `op.MustSaveDriverStorage` → 继续初始化
     - `86038` 过期 → 清 qrcode_key → 返回 HTML 错误提示"二维码已过期，请再次保存刷新"
2. 校验：`GET /x/web-interface/nav`，`code != 0`（-101 未登录）→ 报错提示重新登录；成功则缓存 uid/uname。
3. `Drop`：清 qrcode_key 与进程内 mixin key 缓存。

### 虚拟目录与 API 映射（driver.go List）

```
/ 根（静态两目录）
├── 📁 我的关注          → GET /x/relation/followings?vmid={uid}&pn=&ps=50&order=desc（分页全量）
│    └── 📁 {uname}_{mid} → GET /x/space/wbi/arc/search?mid={mid}&order=pubdate&pn=&ps=50（wbi 签名，分页）
│         └── 🎬 {title}.mp4
└── 📁 我的收藏          → GET /x/v3/fav/folder/created/list-all?up_mid={uid}（一次返回）
     └── 📁 {收藏夹名}    → GET /x/v3/fav/resource/list?media_id={id}&pn=&ps=20&order=mtime&type=2（分页）
          └── 🎬 {title}.mp4
```

- **层级识别**：List 收到 `dir` 后按 `dir.GetName()`/路径段分发（jable_tv 模式）：根 → 两个固定文件夹；`我的关注` → followings；`{uname}_{mid}` → 该 UP 投稿（解析 mid）；`我的收藏` → 收藏夹列表；收藏夹名 → 收藏内容。
- **UP 文件夹命名**：`{uname}_{mid}`（uname 允许重名，mid 保证唯一、可直接解析）；名称做文件名清洗（替换 `/\:*?"<>|` 与控制字符）。解析 mid 时按**最后一个 `_`** 分割（uname 本身可能含 `_`）。
- **叶子文件**：`model.ObjThumb`，`Name = {清理后的标题}.mp4`，`Modified = pubdate`（时间列/排序依据），`Thumbnail` 填封面 URL（`http://` 归一为 `https://`）；**Obj 内部用私有字段携带 `bvid` + `cid`**（自定义 struct 包装 `model.ObjThumb`，List 时从 vlist/medias 提取 cid，Link 免二次查询）。
- **分页全量**：followings/arc-search/fav-resource 循环至 `page.count` 尽；每页 150ms 节流。
- **列表缓存：不设驱动内缓存**。框架层已有目录缓存（`internal/op/fs.go` dirCache）：非 Refresh 的 List 命中缓存直接返回（存储级 `cache_expiration` 配置，默认 30min，用户可调），手动刷新（`Refresh=true`）穿透缓存直达驱动。驱动内再自建一层属冗余（多占内存、多一层一致性维护），故 `Config.NoCache=false` 交给框架缓存即可；目录浏览/播放器 Get 回退的父目录 List 都由框架缓存兜底，翻页成本仅发生在缓存过期或手动刷新时。
- **上限保护**：`Addition.MaxListItems`（number，默认 `500`，0=不限），作用于上述三个分页循环，防超大 UP（数千投稿）同步翻页拖垮 List。超限时记日志说明截断。
- **根目录**：`/` 直接返回两个静态文件夹 obj，不发 API。

### Link（播放，方案 C：durl 直链）

```go
func (d *Bilibili) Link(ctx, file, args) (*model.Link, error)
```

1. 从自定义 Obj 取 `bvid`/`cid`（缺失时回退 `GET /x/web-interface/view?bvid=` 补 cid）
2. `GET /x/player/wbi/playurl`（wbi 签名），参数：`bvid`、`cid`、`qn=64`、`fnval=1`（**不带 dash 位**，强制 durl）、`fnver=0`、`fourk=0`、`otype=json`
3. 取 `data.durl[0].url`（flv 情况存在 `durl` 同构返回）：
   - 成功 → `&model.Link{URL: url, Header: {Referer: https://www.bilibili.com/, UA}, Expiration: 110min}`（URL 实际 ~2h 有效，110min 保守缓存；quark_share 的 LinkCacheTime 同思路）
   - `durl` 为空（接口只回 dash）→ 明确报错 `"该视频无 durl 直链（dash only），当前版本不支持"`；后续可在此升级 dash+混流
4. 风控参数（`dm_img_str` 等）暂不携带，先以真实账号实测；若 playurl 返回 -412/-352 再按 PiliPlus 请求补全。

### 错误处理

- API 响应统一 `{code, message}`：`code!=0` → 包装 `message` 返回；`-101`（未登录）提示重新保存触发扫码。
- List 内单 UP 投稿失败不拖垮整目录：记录日志并跳过该页（保序）。
- 关注/收藏夹目录为空 → 返回空列表（不报错）。

### 明确不做（v1 范围外）

- 关注动态 feed 流（用户已砍）、番剧/电影/直播、充电/课程、上传、4K/杜比（dash）、收藏夹增删改、稍后再看
- 不做多实例共享扫码态：qrcode_key 存进程内，重启后重新生成（cookie 已持久化则无需再扫）

## 测试

- **单测**（mock HTTP，`httptest` + resty 自定义 Transport）：
  - wbi 签名：固定 mixin key 与参数向量断言 `w_rid`
  - mixin key 计算：已知 img_key/sub_key 断言 32 位结果
  - 目录构造：mock followings/arc-search/fav 响应 → List 各层返回正确 obj（名称/时间/是否文件夹）
  - playurl 响应 → Link 返回 URL + Referer header；durl 空 → 报错文案
  - 文件名清洗、http→https 封面归一、mid 解析
  - 分页聚合与 MaxListItems 截断
- **真实环境手测**（需用户 bilibili 账号）：扫码登录全流程 → 关注列表 → 某 UP 投稿列表 → 播放器播放（web 前端）→ 收藏夹浏览播放
