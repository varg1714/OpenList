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
	return listWithSnapshot(d, ctx, dir, dir.GetID(),
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
	return listWithSnapshot(d, ctx, dir, dir.GetID(),
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
	return listWithSnapshot(d, ctx, dir, dir.GetID(),
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
	return listWithSnapshot(d, ctx, dir, dir.GetID(),
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

// Get 浅路径直接构造；深路径 NotSupport 让框架回退父目录 List
func (d *Bilibili) Get(ctx context.Context, path string) (model.Obj, error) {
	switch path {
	case "/":
		return &model.Object{Name: rootName, Path: "/", IsFolder: true}, nil
	case "/" + dirFollow:
		return &model.Object{ID: dirFollowID, Name: dirFollow, Path: "/" + dirFollow, IsFolder: true}, nil
	case "/" + dirFav:
		return &model.Object{ID: dirFavID, Name: dirFav, Path: "/" + dirFav, IsFolder: true}, nil
	}
	return nil, errs.NotSupport
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
