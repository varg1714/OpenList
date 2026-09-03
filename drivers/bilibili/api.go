package bilibili

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// ---- 类型 ----

// 快照条目类型：json tag 即持久化字段名（快照 Data 的稳定 schema——字段名改动会
// 静默零值旧数据，只许增不许改；格式迁移靠 snapshotEnvelope.V）
type FollowItem struct {
	Mid   int64  `json:"mid"`
	Uname string `json:"uname"`
}

type VideoItem struct {
	Bvid    string `json:"bvid"`
	Title   string `json:"title"`
	Pic     string `json:"pic"` // http:// 开头，展示时前端可访问；驱动统一转 https 见 driver.go
	Pubdate int64  `json:"pubdate"`
	Cid     int64  `json:"cid"` // 0 = 需调 videoCid 补
}

type FavFolder struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
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

// pageRetryBackoff: 单页拉取失败的重试退避序列（风控/限流通常数秒恢复）；
// 包级变量便于测试覆盖为 0 延迟。
var pageRetryBackoff = []time.Duration{time.Second, 2 * time.Second}

// fetchWithRetry 单页拉取，失败按 pageRetryBackoff 退避重试
func fetchWithRetry[T any](d *Bilibili, ctx context.Context,
	fetch func(pn int) ([]T, int, error), pn int) ([]T, int, error) {
	var lastErr error
	for attempt := 0; attempt <= len(pageRetryBackoff); attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, 0, ctx.Err()
			case <-time.After(pageRetryBackoff[attempt-1]):
			}
		}
		items, total, err := fetch(pn)
		if err == nil {
			return items, total, nil
		}
		lastErr = err
	}
	return nil, 0, lastErr
}

// fetchFollowingsPage 关注列表页拉取器工厂（新关注在前，spec 顺序验证点）。
// 列表数据经 snapshot 门面（snapshot.go）聚合/增量，此处只负责单页拉取解析。
func fetchFollowingsPage(d *Bilibili, ctx context.Context) func(pn int) ([]FollowItem, int, error) {
	return func(pn int) ([]FollowItem, int, error) {
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
	}
}

// fetchUpVideosPage UP 投稿页拉取器工厂（arc/search，wbi 签名，按 pubdate 倒序）
func fetchUpVideosPage(d *Bilibili, ctx context.Context, mid int64) func(pn int) ([]VideoItem, int, error) {
	return func(pn int) ([]VideoItem, int, error) {
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
	}
}

// fetchFavFoldersOnce 收藏夹列表（list-all 单请求接口；pn≥2 给空页令门面停止）
func fetchFavFoldersOnce(d *Bilibili, ctx context.Context) func(pn int) ([]FavFolder, int, error) {
	return func(pn int) ([]FavFolder, int, error) {
		if pn > 1 {
			return nil, 0, nil
		}
		raw, err := d.doGet(ctx, apiBase+"/x/v3/fav/folder/created/list-all", map[string]string{
			"up_mid": strconv.FormatInt(d.uid, 10),
		}, false)
		if err != nil {
			return nil, 0, err
		}
		var resp favFolderResp
		if err := json.Unmarshal(raw, &resp); err != nil {
			return nil, 0, err
		}
		out := make([]FavFolder, 0, len(resp.List))
		for _, f := range resp.List {
			out = append(out, FavFolder{ID: f.ID, Title: f.Title})
		}
		return out, len(out), nil
	}
}

// fetchFavVideosPage 收藏夹视频页拉取器工厂（按收藏时间 mtime 倒序）
func fetchFavVideosPage(d *Bilibili, ctx context.Context, mediaID int64) func(pn int) ([]VideoItem, int, error) {
	return func(pn int) ([]VideoItem, int, error) {
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
	}
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
