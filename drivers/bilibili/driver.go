package bilibili

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"time"

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

// Link 占位，Task 7 实现
func (d *Bilibili) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
	return nil, errs.NotSupport
}

// Init/Drop 占位，Task 7 实现真实逻辑（initClient/扫码/navInfo 校验；清理登录态）
func (d *Bilibili) Init(ctx context.Context) error {
	return nil
}

func (d *Bilibili) Drop(ctx context.Context) error {
	return nil
}
