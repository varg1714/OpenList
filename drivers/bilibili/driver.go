package bilibili

import (
	"context"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

const (
	dirFollow       = "我的关注"
	dirFav          = "我的收藏"
	rootName        = "root"
	dirFollowID     = "followings" // 顶层目录 obj.ID（用户不可见，用于 List 分发）
	dirFavID        = "favs"
	upFolderPrefix  = "up_"  // UP 目录 obj.ID 前缀：up_{mid}
	favFolderPrefix = "fav_" // 收藏夹目录 obj.ID 前缀：fav_{media_id}
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

// disambiguate 返回最终显示名：出现频率 >1 的名字追加 _suffix 消歧。
// 全部重名项都追加（而非只加后续者），名字不依赖列表顺序、稳定可预测。
func disambiguate(displays, suffixes []string) []string {
	freq := make(map[string]int, len(displays))
	for _, d := range displays {
		freq[d]++
	}
	names := make([]string, len(displays))
	for i, d := range displays {
		names[i] = d
		if freq[d] > 1 {
			names[i] = d + "_" + suffixes[i]
		}
	}
	return names
}

func folderObj(parent model.Obj, name, id string) model.Obj {
	return &model.Object{
		ID:       id,
		Name:     name,
		Path:     filepath.Join(parent.GetPath(), name),
		IsFolder: true,
		Modified: time.Now(),
	}
}

func newVideoObj(parent model.Obj, v VideoItem, display string) *videoObj {
	vo := &videoObj{bvid: v.Bvid, cid: v.Cid}
	vo.ID = v.Bvid
	vo.Name = display + ".mp4"
	vo.Path = filepath.Join(parent.GetPath(), vo.Name)
	vo.Modified = time.Unix(v.Pubdate, 0)
	if v.Pic != "" {
		vo.Thumbnail = model.Thumbnail{Thumbnail: strings.Replace(v.Pic, "http://", "https://", 1)}
	}
	return vo
}

// List 按目录对象 ID 分发（LocalSort=false，返回顺序即展示顺序）。
// 目录 ID 由本驱动构造时写入 obj.ID（用户不可见）；显示名保持干净，
// 仅重名项追加 _id 后缀消歧。ID 为空（旧缓存 obj）→ 刷新重建前不可用。
func (d *Bilibili) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {
	p := dir.GetPath()
	if p == "" || p == "/" { // 根：RootPath 未填时 Path 可能为空串
		return []model.Obj{
			folderObj(dir, dirFollow, dirFollowID),
			folderObj(dir, dirFav, dirFavID),
		}, nil
	}
	key := dir.GetID()
	if key == "" {
		return nil, errs.ObjectNotFound // 旧缓存 obj 无 ID：刷新（dirCache 穿透）后重建
	}
	// 同目录并发（dirCache 穿透 + 外部定时任务撞车）只放行一次拉取，其余共享结果
	v, err, _ := d.sf.Do(key, func() (interface{}, error) {
		switch key {
		case dirFollowID:
			return d.listFollowings(ctx, dir)
		case dirFavID:
			return d.listFavFolders(ctx, dir)
		default:
			if _, ok := parsePrefixedID(key, upFolderPrefix); ok {
				return d.listUpVideos(ctx, dir)
			}
			if _, ok := parsePrefixedID(key, favFolderPrefix); ok {
				return d.listFavVideos(ctx, dir)
			}
		}
		return nil, errs.ObjectNotFound
	})
	if err != nil {
		return nil, err
	}
	return v.([]model.Obj), nil
}

// parsePrefixedID 解析 "{prefix}{数字}" 形式的目录 ID
func parsePrefixedID(id, prefix string) (int64, bool) {
	rest, ok := strings.CutPrefix(id, prefix)
	if !ok || rest == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(rest, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// ---- 快照门面接线：List 数据经 listWithSnapshot（snapshot.go）——
// 快照 DirKey = 目录 obj.ID；keyOf = 条目稳定 ID；build = 展示层重建（下同）

func (d *Bilibili) listFollowings(ctx context.Context, dir model.Obj) ([]model.Obj, error) {
	return listWithSnapshot(d, ctx, dir, dir.GetPath(),
		fetchFollowingsPage(d, ctx),
		func(f FollowItem) string { return strconv.FormatInt(f.Mid, 10) },
		buildFollowings)
}

func buildFollowings(dir model.Obj, items []FollowItem) ([]model.Obj, error) {
	displays := make([]string, len(items))
	suffixes := make([]string, len(items))
	for i, f := range items {
		displays[i] = sanitizeName(f.Uname, 80)
		suffixes[i] = strconv.FormatInt(f.Mid, 10)
	}
	names := disambiguate(displays, suffixes)
	objs := make([]model.Obj, 0, len(items))
	for i, f := range items {
		objs = append(objs, folderObj(dir, names[i], upFolderPrefix+strconv.FormatInt(f.Mid, 10)))
	}
	return objs, nil
}

func (d *Bilibili) listUpVideos(ctx context.Context, dir model.Obj) ([]model.Obj, error) {
	mid, ok := parsePrefixedID(dir.GetID(), upFolderPrefix)
	if !ok {
		return nil, errs.ObjectNotFound
	}
	return listWithSnapshot(d, ctx, dir, dir.GetPath(),
		fetchUpVideosPage(d, ctx, mid),
		func(v VideoItem) string { return v.Bvid },
		buildUpVideos)
}

func buildUpVideos(dir model.Obj, items []VideoItem) ([]model.Obj, error) {
	displays := make([]string, len(items))
	suffixes := make([]string, len(items))
	for i, v := range items {
		displays[i] = sanitizeName(v.Title, 150)
		suffixes[i] = v.Bvid
	}
	names := disambiguate(displays, suffixes)
	objs := make([]model.Obj, 0, len(items))
	for i := range items {
		objs = append(objs, newVideoObj(dir, items[i], names[i]))
	}
	return objs, nil
}

func (d *Bilibili) listFavFolders(ctx context.Context, dir model.Obj) ([]model.Obj, error) {
	return listWithSnapshot(d, ctx, dir, dir.GetPath(),
		fetchFavFoldersOnce(d, ctx),
		func(f FavFolder) string { return strconv.FormatInt(f.ID, 10) },
		buildFavFolders)
}

func buildFavFolders(dir model.Obj, items []FavFolder) ([]model.Obj, error) {
	displays := make([]string, len(items))
	suffixes := make([]string, len(items))
	for i, f := range items {
		displays[i] = sanitizeName(f.Title, 80)
		suffixes[i] = strconv.FormatInt(f.ID, 10)
	}
	names := disambiguate(displays, suffixes)
	objs := make([]model.Obj, 0, len(items))
	for i, f := range items {
		objs = append(objs, folderObj(dir, names[i], favFolderPrefix+strconv.FormatInt(f.ID, 10)))
	}
	return objs, nil
}

func (d *Bilibili) listFavVideos(ctx context.Context, dir model.Obj) ([]model.Obj, error) {
	mediaID, ok := parsePrefixedID(dir.GetID(), favFolderPrefix)
	if !ok {
		return nil, errs.ObjectNotFound
	}
	return listWithSnapshot(d, ctx, dir, dir.GetPath(),
		fetchFavVideosPage(d, ctx, mediaID),
		func(v VideoItem) string { return v.Bvid },
		buildFavVideos)
}

func buildFavVideos(dir model.Obj, items []VideoItem) ([]model.Obj, error) {
	displays := make([]string, len(items))
	suffixes := make([]string, len(items))
	for i, v := range items {
		displays[i] = sanitizeName(v.Title, 150)
		suffixes[i] = v.Bvid
	}
	names := disambiguate(displays, suffixes)
	objs := make([]model.Obj, 0, len(items))
	for i := range items {
		objs = append(objs, newVideoObj(dir, items[i], names[i]))
	}
	return objs, nil
}

// Get 浅路径直接构造；深路径纯查库定位（快照 key = 展示路径，见 listXxx）。
// 命中返回 obj（带内部 ID，后续 List 分发/Link 照常）；miss → ObjectNotFound。
// 设计决策（用户 A）：Get 永不触发网络/List——快照 = 上次完整拉取结果，
// List 的增量只加新条目不会补旧名字，Get 找不到则 List 也找不到；
// 数据同步只由 List（浏览 / 外部定时任务 refresh）负责。
func (d *Bilibili) Get(ctx context.Context, path string) (model.Obj, error) {
	switch path {
	case "/":
		return &model.Object{Name: rootName, Path: "/", IsFolder: true}, nil
	case "/" + dirFollow:
		return &model.Object{ID: dirFollowID, Name: dirFollow, Path: "/" + dirFollow, IsFolder: true}, nil
	case "/" + dirFav:
		return &model.Object{ID: dirFavID, Name: dirFav, Path: "/" + dirFav, IsFolder: true}, nil
	}
	// 深路径：/我的关注/{UP}[/{视频}] 或 /我的收藏/{收藏夹}[/{视频}]
	segs := strings.Split(strings.Trim(path, "/"), "/")
	if len(segs) < 2 || len(segs) > 3 || (segs[0] != dirFollow && segs[0] != dirFav) {
		return nil, errs.ObjectNotFound // 结构固定 3 层以内，顶层只能是关注/收藏
	}
	parent, err := d.Get(ctx, "/"+segs[0])
	if err != nil {
		return nil, err
	}
	if segs[0] == dirFollow {
		return d.getFollowPath(ctx, parent, segs[1:])
	}
	return d.getFavPath(ctx, parent, segs[1:])
}

// getFollowPath 在关注子树内定位：{UP名}（2 段）或 {UP名}/{视频名}（3 段）
func (d *Bilibili) getFollowPath(ctx context.Context, parent model.Obj, segs []string) (model.Obj, error) {
	items, ok, err := loadSnapshot[FollowItem](d, parent.GetPath())
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errs.ObjectNotFound // 未同步：由 List（浏览/定时任务）负责
	}
	upName := segs[0]
	mid := matchUname(items.Items, upName)
	if mid == 0 {
		// 消歧名 "名_{mid}"：解出 id 并校验前缀（防伪造段误命中）
		if prefix, id, ok := splitFolderName(upName); ok && hasFollowByID(items.Items, id, prefix) {
			mid = id
		}
	}
	if mid == 0 {
		return nil, errs.ObjectNotFound
	}
	upDir := folderObj(parent, upName, upFolderPrefix+strconv.FormatInt(mid, 10))
	if len(segs) == 1 { // segs 已去掉顶层：仅 UP 目录段
		return upDir, nil
	}
	// 视频层：读该 UP 投稿快照按文件名匹配
	vitems, ok, err := loadSnapshot[VideoItem](d, upDir.GetPath())
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errs.ObjectNotFound
	}
	item := matchVideoItem(vitems.Items, segs[1])
	if item == nil {
		return nil, errs.ObjectNotFound
	}
	return newVideoObj(upDir, *item, strings.TrimSuffix(segs[1], ".mp4")), nil
}

// getFavPath 在收藏子树内定位：{收藏夹名}（2 段）或 {收藏夹名}/{视频名}（3 段）
func (d *Bilibili) getFavPath(ctx context.Context, parent model.Obj, segs []string) (model.Obj, error) {
	items, ok, err := loadSnapshot[FavFolder](d, parent.GetPath())
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errs.ObjectNotFound
	}
	favName := segs[0]
	mediaID := matchFavTitle(items.Items, favName)
	if mediaID == 0 {
		if prefix, id, ok := splitFolderName(favName); ok && hasFavByID(items.Items, id, prefix) {
			mediaID = id
		}
	}
	if mediaID == 0 {
		return nil, errs.ObjectNotFound
	}
	favDir := folderObj(parent, favName, favFolderPrefix+strconv.FormatInt(mediaID, 10))
	if len(segs) == 1 { // segs 已去掉顶层：仅收藏夹目录段
		return favDir, nil
	}
	vitems, ok, err := loadSnapshot[VideoItem](d, favDir.GetPath())
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errs.ObjectNotFound
	}
	item := matchVideoItem(vitems.Items, segs[1])
	if item == nil {
		return nil, errs.ObjectNotFound
	}
	return newVideoObj(favDir, *item, strings.TrimSuffix(segs[1], ".mp4")), nil
}

// splitFolderName 解析 "{名字}_{id}" 段名（按最后一个 _ 且其后全数字），
// 返回 (前缀, id, true)；无后缀返回 (_, 0, false)
func splitFolderName(name string) (string, int64, bool) {
	idx := strings.LastIndex(name, "_")
	if idx <= 0 || idx == len(name)-1 {
		return "", 0, false
	}
	id, err := strconv.ParseInt(name[idx+1:], 10, 64)
	if err != nil {
		return "", 0, false
	}
	return name[:idx], id, true
}

// hasFollowByID 校验快照中存在 mid 且其展示名前缀匹配（消歧后缀段防伪造）
func hasFollowByID(items []FollowItem, mid int64, prefix string) bool {
	for _, f := range items {
		if f.Mid == mid && sanitizeName(f.Uname, 80) == prefix {
			return true
		}
	}
	return false
}

// hasFavByID 校验快照中存在 media id 且其展示名前缀匹配
func hasFavByID(items []FavFolder, id int64, prefix string) bool {
	for _, f := range items {
		if f.ID == id && sanitizeName(f.Title, 80) == prefix {
			return true
		}
	}
	return false
}

// matchUname 按展示名（sanitize 后）找 UP。重名时 List 必给消歧后缀，
// 故无后缀段仅在显示名唯一时合法；多个匹配 = 重名未消歧（快照旧/伪路径）→ miss
func matchUname(items []FollowItem, display string) int64 {
	matched := int64(0)
	for _, f := range items {
		if sanitizeName(f.Uname, 80) == display {
			if matched != 0 {
				return 0 // 重名
			}
			matched = f.Mid
		}
	}
	return matched
}

// matchFavTitle 按展示名（sanitize 后）找收藏夹；重名（List 会给消歧后缀）时 miss
func matchFavTitle(items []FavFolder, display string) int64 {
	matched := int64(0)
	for _, f := range items {
		if sanitizeName(f.Title, 80) == display {
			if matched != 0 {
				return 0 // 重名
			}
			matched = f.ID
		}
	}
	return matched
}

// matchVideoItem 按文件名段匹配视频：优先解析尾部 _{bvid}（重名消歧后缀）；
// 无后缀按 sanitize 标题匹配（截断规则与 List 一致）
func matchVideoItem(items []VideoItem, fileName string) *VideoItem {
	base := strings.TrimSuffix(fileName, ".mp4")
	if idx := strings.LastIndex(base, "_"); idx > 0 && strings.HasPrefix(base[idx+1:], "BV") {
		bvid := base[idx+1:]
		for i := range items {
			if items[i].Bvid == bvid {
				return &items[i]
			}
		}
		return nil
	}
	// 干净标题段：仅当该标题在列表中唯一（重名标题 List 必带 bvid 后缀）
	matched := -1
	for i := range items {
		if sanitizeName(items[i].Title, 150) == base {
			if matched >= 0 {
				return nil // 重名
			}
			matched = i
		}
	}
	if matched >= 0 {
		return &items[matched]
	}
	return nil
}

// durl URL 约 2h 有效，保守缓存 110min
const durlExpiration = 110 * time.Minute

// Link 生成 durl 直链（方案 C）：仅支持本驱动产出的 videoObj；
// cid 未知（up 空间视频）时先经 view 接口补，再取 playurl durl 首个直链。
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

// Init 初始化 HTTP 客户端并校验登录态；无 cookie 时走扫码流程
// （未扫/待确认/过期返回携带二维码 HTML 的错误，前端渲染即出码）。
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
	// 换账号：清掉非当前 uid 的旧快照（防旧账号数据混入增量基线）
	if err := db.DeleteVirtualDirSnapshotsNotOwner(d.ID, d.snapshotOwner()); err != nil {
		utils.Log.Warnf("bilibili: cleanup foreign snapshots: %v", err)
	}
	return nil
}

// Drop 清理扫码/签名/登录态，下次 Init 重新建立
func (d *Bilibili) Drop(ctx context.Context) error {
	// 注意：不清 qrcodeKey/qrURL —— UpdateStorage（编辑保存）会先 Drop 再 Init，
	// 扫码状态必须跨保存请求存活（189pc 同款：Drop 保留 qrcodeParam），
	// 否则每次保存都重新生成二维码，用户扫的码永远失效（死循环）。
	d.mixinKey = ""
	d.mixinKeyDay = ""
	d.cookieStr = ""
	d.uid = 0
	d.uname = ""
	return nil
}
