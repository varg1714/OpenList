# 设计：Bilibili 年份目录拆分（年份 → emby 季 → 本地迁移）

日期：2026-09-07

## 背景与动机

用户挂载链：`bilibili 驱动 → emby_wrapper 驱动（tv_show 电视剧化）→ strm 驱动（本地落盘 nfo/strm/jpg）→ 本地媒体库`（emby 服务器扫描本地镜像目录）。

现状问题：bilibili 目录下（`/我的关注/{UP}`、`/我的收藏/{收藏夹}`）**所有视频平铺在 UP 文件夹下**。UP 投稿可上千条，浏览与媒体库管理困难。需求：按年份拆分子文件夹；年份目录被 emby_wrapper 天然识别为"季"，本地存量文件一次性迁移到新结构。

约束与决策（与用户逐项确认，2026-09-07）：

| 议题 | 决策 |
|---|---|
| 拆分范围 | `我的关注`（UP 投稿）与 `我的收藏`（收藏夹）两棵子树同构拆分 |
| 年份来源 | 关注 = `VideoItem.Pubdate`（发布时间）；收藏夹 = **FavTime**（收藏时间）——收藏夹快照 `Pubdate` 字段现即存 FavTime（fetchFavVideosPage），**零 schema 改动**；emby episode nfo `<aired>` = obj ModTime = 同一字段，脚本读 aired 对两棵树统一有效 |
| 年份目录时间 | 年份目录 obj **Ctime = 按年份确定性生成**（否则 emby_wrapper 每次刷新按 CreateTime 排序分配季号会漂移，脚本无法复算） |
| 快照/数据模型 | **M1**：UP/收藏夹 master 行（key=目录展示路径）保持唯一事实源，年份目录为**纯内存投影**；增量同步逻辑零改动；无年份的旧访问路径读库兼容，**绝不触发 API 全量拉取** |
| 季号方向 | 年份升序：S01 = 最早年份（emby 按目录 Ctime 升序分配，Ctime=1月1日 → 自然满足）；最早年份内视频编号保持不变 |
| 展示顺序 | 年份目录最新在前（降序）；年份目录内视频保持现有 API 顺序（pubdate 倒序）——与"emby 编号按时间升序"互不冲突（展示顺序 ≠ 编号顺序，emby 内部自行重排） |
| emby_wrapper | **零代码改动**（见 §3） |
| 过渡窗口 | 接受：升级后旧 emby 虚拟路径（如 `S01E131.mp4`）在首次索引重建后 404，直至脚本迁移完成 |
| 迁移脚本 | 临时 Python 工具，**不入 git**；无备份、无 dry-run、无报告文件；stderr 仅打警告/失败；正确性靠测试保证 |
| 工作区 | 实现单开 git worktree |

## 1. 目标结构与展示（drivers/bilibili）

```
/我的关注/{UP}/{年份}/{标题}.mp4           年份 = Pubdate 年
/我的收藏/{收藏夹}/{年份}/{标题}.mp4       年份 = FavTime 年
```

- 年份目录仅当该目录下有该年份视频时生成；目录名 = 四位纯年份字符串。
- 年份目录 obj：
  - `Name` = 年份字符串；`ID` = `up_{mid}_{year}` / `fav_{media_id}_{year}`（前缀体系沿用；年份目录 ID 与父目录 ID 的区别 = 末段 `_{year}`，两段皆纯数字）；
  - **`Ctime = time.Date(year, 1, 1, 0,0,0,0, time.Local)`**（确定性；emby `CreateTime()` 优先 Ctime）；
  - `Modified` = 当前时间（维持现状视觉语义）。
- 年份从 `time.Unix(pubdate, 0)` 本地年取。
- 重名消歧（disambiguate）作用域 = **年份目录内**：跨年份同名视频互不消歧（各自年内唯一），年内重名仍追加 `_BV…` 后缀。此为期望行为——文件名随结构变化是拆分的既定事实；旧 3 段路径靠 bvid 后缀解析（§2）不受影响。

## 2. List / Get 数据流（M1 投影）

### 快照层（internal/db、snapshot.go）：零改动

master 行 key（UP/收藏夹目录展示路径）在新旧结构下**不变**——这正是 M1 可行的根基：无存量数据迁移、无行重写、无 key 变更。

### List 分发（driver.go）

- `List(UP 目录)`（ID = `up_{mid}`）：走现有 `listWithSnapshot` 增量门面（读行 → 需要时翻 1~3 页增量 → 落库）→ **build 改为**：按年份分组 → 输出年份目录列表（年份降序）。仍是唯一触发 API 同步的点。
- `List(年份目录)`（ID = `up_{mid}_{year}`，`fav_{media_id}_{year}` 同构）：**纯内存投影**——父行 key = `dir.GetPath()` 去末段 → `loadSnapshot` → 过滤该年份 → 复用现有 build（sanitize/年内消歧/newVideoObj）。无 API、无写库。父行缺失（快照被清/目录 obj 陈旧）→ `ObjectNotFound`（decision A 语义：dirCache 穿透刷新后自愈）。singleflight key = 目录 ID（沿用）。
- ID 解析：`parsePrefixedID` 对年份目录 ID 会失败（rest 非纯数字）→ 新增年份目录解析（rest 按最后一个 `_` 拆两段，皆纯数字）。

### Get 深路径（纯查库，decision A 保持：绝不触发网络/List）

段数由 2~3 扩为 2~4，两棵子树同构。以关注为例（`segs[0]=我的关注`）：

| 路径 | 行为 |
|---|---|
| `2 段` `/{UP}` | 不变：返回 UP 目录 obj（followings 行按名匹配，含 `_mid` 消歧名解析） |
| `3 段` `/{UP}/{标题}.mp4` | **旧格式兼容**：读 master 行按名匹配（含 `_BV…` 消歧名解析），任意年份，返回 videoObj。存量 URL/书签/脚本无需重拉即恢复 |
| `3 段` `/{UP}/{年份}` | 年份目录：末段以 `.mp4` 结尾 → 按视频处理（年份目录不带扩展名，无歧义）；否则若匹配 `^20\d\d$` 且 master 行中该年份有内容 → 返回年份目录 obj（ID/CTime 与 List 输出一致）；否则回退按旧式无扩展名视频名匹配（老行为兼容）；仍不中 → 404 |
| `4 段` `/{UP}/{年份}/{标题}.mp4` | 读 master 行匹配视频 → **校验 pubdate 年份 == 段中年份**，不符/不在 → 404（防新旧结构错位引用静默放行） |

收藏夹路径同构（`fav_{media_id}_{year}`、FavTime 年）。

### 边界

- `pubdate<=0` 条目：不进入任何年份目录（新结构不可见），但旧 3 段路径仍可解析；防御处理 + 日志。真实 API（arc/search created、fav fav_time）恒有值，实际不可达。
- 空年份目录不生成。
- 视频文件名格式不变（`{标题}.mp4` + 消歧后缀），`Modified`=pubdate 不变。

## 3. emby_wrapper / strm：零代码改动与一致性保证

功能上无需修改：季映射（直接子文件夹→季）、季内编号、tvshow.nfo/season.nfo、poster/fanart 全部由 List 结果动态构建，年份目录天然成为季。

跨组件一致性义务：
1. **bilibili 侧**：年份目录 Ctime 确定性（§1）→ emby `byCreateTimeName` 升序 → S01=最早年份，且跨刷新稳定。
2. **脚本侧**：编号规则复刻（§4.3）→ 脚本产物 ≡ emby 重扫产物 → 之后 strm 增量刷新只增不 rewrite（已存在文件跳过、真实同名 nfo 优先）。
3. poster/fanart 稳定性：候选 = `idx.order` 登记序（pubdate 升序）前 N 个带封面视频；拆分后登记序 = 年份升序 × 年内 pubdate 升序 = 仍是全局最旧 N 个，**内容与现状一致**，无需处理。

## 4. Python 迁移脚本（临时工具，不入 git）

### 4.1 定位

- 运行环境：媒体机（本地镜像目录所在机器），`python3` 标准库 only（pathlib/os/shutil/xml.etree/re/urllib.parse）。
- 用法：`python3 migrate_bilibili_years.py <根路径>`。递归扫描根下目录，对每个"含平铺媒体三件套的目录"（= 一个剧根，UP 或收藏夹目录均可）执行迁移；目录树里 `我的关注`/`我的收藏` 混合、嵌套、已含 `S{NN}/` 结构（幂等续跑）均安全。
- 无备份、无 dry-run、无报告文件；stderr 打印警告与失败；正确性由测试保证（§4.6）。

### 4.2 收集

对每个剧根目录（如 `BiBiPiano/`）：
- 平铺条目 = 根下每个非 `tvshow.nfo`/`season.nfo`/`poster.jpg`/`fanart*.jpg` 的 `.nfo` 文件（剧集 nfo）：读 `<aired>`（wrapper 已写 = 发布/收藏日期）→ 年份。缺 nfo 或 aired 缺失/不可解析 → 警告并跳过该 base（保守，不猜测）。
- base = nfo 去扩展名；关联文件 = 同目录 `{base}.strm`、`{base}.nfo`、`{base}-thumb.jpg` 中存在者（三件套缺件只迁移存在的）。
- 其余根文件（tvshow.nfo、poster.jpg、fanartN.jpg…）**留在根不动**；无对应 nfo 的孤儿 `.strm`/`-thumb.jpg`（正常流程不存在）同样留在根，由后续 strm 增量刷新清理。

### 4.3 目标映射（必须 ≡ emby 重扫编号）

- 该剧根出现年份集合升序 → `{年份}` = S`{NN:02d}`（NN = 年份升序排名；与 emby 按 Ctime 升序分配季号一致）。
- 组内排序：**按旧 E 数字升序**。关键性质：旧编号 E = 全局 (pubdate, 名称) 升序序号（emby `byCreateTimeName` 平铺编号），其年内投影 ≡ 新季内编号（同一排序键、同一 tie-break 规则含消歧后缀名）——**无需复刻排序细节，aired 只用于分年**。
- 新集号 MM = 年内旧 E 排名（`%02d`，与 wrapper `episodeVirtualName` 一致）。
- **保留原名特例**：真实文件名的 nfo `<title>` 命中 emby `isNumberedEpisode` 正则（`(?i)(?:^|[._ -])(?:s\d{1,4}[. _-]*e\d{1,3}|\d{1,4}x\d{1,3})(?:[._ -]|$)`，title 与含扩展名判定等价——尾部边界由 `$` 与 `.` 等价提供）→ emby 新结构下该文件仍保持原名不编号 → 只移入对应季目录、**不重命名**。正则从 emby_wrapper 复刻。

### 4.4 落盘（两阶段）

1. **只读计算阶段**：完整计算全部 (源 → 目标) 映射并校验：aired 缺失、目标冲突（目标已存在且源仍存在）→ **该剧根中止**并打印原因（保持原状）。
2. **执行阶段**：建 `S{NN}/` 目录 → 移动三件套 → 写 season.nfo → 删根 `season.nfo`。执行期 I/O 失败 → 中止并打印已完成/未完成清单（无备份下的保守语义）。

细节：
- **season.nfo**：每新季目录生成，**字节级复刻** emby `buildSeasonNFO`：
  `<?xml version="1.0" encoding="utf-8" standalone="yes"?>\n<season>\n  <seasonnumber>{NN}</seasonnumber>\n  <title><![CDATA[{年份}]]></title>\n</season>`
  （之后 strm 重扫"真实同名 nfo 优先"不覆盖、无差异横跳。）
- **strm URL 重写**：`.strm` 内容末路径段整体替换——`…/{显示名}.mp4` → `…/S{NN}/{新显示名}.mp4`；仅操作最后一个 `/` 之后的部分，前缀（host、`/d/`、百分号编码中文段）逐字节保留。校验：URL 末段（解码去扩展名）≠ 本地 base → 警告跳过该文件（保守，防旧生成期错位）。URL 带 `?sign=` → 警告跳过（签名随路径失效，用户样本无 sign，不支持）。
- 根 `season.nfo`（旧平铺季产物）删除；`tvshow.nfo`/poster/fanart 保留。
- 单年份剧根：全部文件搬入 `S01/`，编号不变（全局序=年内序）。

### 4.5 幂等与冲突

- 已迁移 base（平铺源不存在）→ no-op；二次运行收敛。
- 目标已存在且源也在 → 冲突，警告并跳过该源（真实流程不会出现；防止 aired 变更导致的双写错乱）。

### 4.6 测试（stdlib unittest，fixture 以用户真实 nfo/strm 为模板）

覆盖：
1. 多年份多 UP 全量迁移：目录结构、季号升序、E 重编号（与"独立按 emby 规则重算"的断言对照）
2. 最早年份视频编号不变（S01E01..E{k} 原样搬入 S01）
3. 三件套缺件只迁移存在文件
4. aired 缺失 → 警告跳过
5. `isNumberedEpisode` 原名保留：只移动不重命名
6. strm URL 重写保真：含百分号编码中文前缀、含 query 尾缀（无 sign）、非标准 host/port
7. 根文件保留、根 season.nfo 删除、新 season.nfo 字节级精确
8. 幂等：二次运行 no-op；半迁移目录续跑收敛
9. 混合树（我的关注+我的收藏、嵌套、空目录、已迁移结构）安全跳过/正确处理

## 5. 迁移手册与验收

1. 暂停外部定时刷新任务中遍历 `缓存/bilibili` strm 挂载树的部分（防与脚本并发写本地文件）；
2. 部署新驱动代码（bilibili 存量快照行原位可用，无需手工数据操作）；
3. 媒体机跑迁移脚本；
4. Emby 端重扫本地库；
5. 恢复定时任务（此后刷新产物 ≡ 脚本产物，只增不 rewrite）；
6. 验收：关注+收藏夹各 1~2 个真实 UP：OpenList 浏览 UP→年份→视频、播放；升级前抓取的旧 bilibili 3 段直链仍可解析（404 属 bug）；旧 emby 虚拟路径 404 属预期（失效窗口，已接受）。

## 6. Go 测试计划（drivers/bilibili，现有 mock 设施）

- 年份分组：List(UP) 输出年份目录集合/顺序（降序）/Name/ID/Ctime 断言（确定性）
- List(年份目录)：过滤正确、年内顺序、年内重名消歧、跨年同名不消歧；fav 同构
- ID 解析：up_{mid} / up_{mid}_{year} / fav 同构 / 非法
- Get 兼容矩阵：2/3/4 段；旧 3 段（无后缀名、`_BV` 消歧名、无扩展名回退）；年份目录 3 段（含段 3 = 纯数字视频名 vs 年份歧义）；4 段年份校验 404；不存在的年份 404；fav 同构
- 无快照 List(年份目录) → ObjectNotFound
- 增量回归：新视频出现在最新年份目录头部；API 请求次数断言
- 存量兼容回归：**现有测试中平铺结构断言全部升级为新结构语义**，全绿

## 7. 风险与明确不做

- **不做**：年份行落库（M2）、emby_wrapper 旧虚拟名 legacy 别名（失效窗口已接受）、驱动的旧路径永久 404 化、脚本入 git、后台自动迁移 job。
- 风险：脚本与 emby 重扫编号不一致 → 由 §4.3 的纯函数性质 + §4.6 对照断言 + 真实目录验收兜底；本地镜像与 bilibili 快照不同步（缺文件/多残留）→ 脚本只迁移磁盘可见条目，之后的 strm 增量刷新自然补齐/清理。
