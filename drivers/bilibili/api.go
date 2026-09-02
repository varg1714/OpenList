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
	List []struct {
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
		if len(*out) >= total || len(items) == 0 {
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
