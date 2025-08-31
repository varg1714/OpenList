package quark_share

import (
	"context"
	"errors"
	"fmt"
	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/pkg/cookie"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/Xhofe/go-cache"
	"github.com/go-resty/resty/v2"
	"golang.org/x/time/rate"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// do others that not defined in Driver interface

var shareTokenCache = cache.NewMemCache(cache.WithShards[ShareTokenResp](128))
var fileListRespCache = cache.NewMemCache(cache.WithShards[FileListResp](128))
var limiter = rate.NewLimiter(rate.Every(1000*time.Millisecond), 1)

func (d *QuarkShare) request(pathname string, method string, callback base.ReqCallback, resp interface{}) ([]byte, error) {
	u := d.conf.api + pathname
	req := base.RestyClient.R()
	req.SetHeaders(map[string]string{
		"Cookie":  d.Cookie,
		"Accept":  "application/json, text/plain, */*",
		"Referer": d.conf.referer,
	})
	req.SetQueryParam("fr", "pc")
	if callback != nil {
		callback(req)
	}
	if resp != nil {
		req.SetResult(resp)
	}
	var e Resp
	req.SetError(&e)
	res, err := req.Execute(method, u)
	if err != nil {
		return nil, err
	}
	__puus := cookie.GetCookie(res.Cookies(), "__puus")
	if __puus != nil {
		d.Cookie = cookie.SetStr(d.Cookie, "__puus", __puus.Value)
		op.MustSaveDriverStorage(d)
	}
	if e.Status >= 400 || e.Code != 0 {
		return nil, errors.New(e.Message)
	}
	return res.Body(), nil
}

func (d *QuarkShare) getShareInfo(shareId, pwd string) (string, error) {

	shareToken, exist := shareTokenCache.Get(shareId)
	if exist {
		return shareToken.Data.Stoken, nil
	}

	var shareResp ShareTokenResp
	_, err := d.request("/1/clouddrive/share/sharepage/token", http.MethodPost, func(req *resty.Request) {
		req.SetBody(ShareTokenReq{
			PwdId:    shareId,
			PassCode: pwd,
		})
	}, &shareResp)

	if err != nil {
		utils.Log.Info("获取夸克网盘stToken失败", err)
		return "", err
	}

	if shareResp.Data.Stoken != "" {
		shareTokenCache.Set(shareId, shareResp, cache.WithEx[ShareTokenResp](time.Minute*time.Duration(d.CacheExpiration)))
		return shareResp.Data.Stoken, nil
	} else {
		utils.Log.Infof("获取夸克网盘stToken获取为空:%v", shareResp)
		return "", errors.New("分享链接token获取为空")
	}

}

func (d *QuarkShare) getShareFiles(ctx context.Context, virtualFile model.VirtualFile, dir model.Obj) ([]FileObj, error) {

	stToken, err := d.getShareInfo(virtualFile.ShareID, virtualFile.SharePwd)
	if err != nil {
		return nil, err
	}

	res := make([]FileObj, 0)

	nextPage := true
	page := 1
	pageSize := 50

	buildCacheKeyFunc := func() string {
		return fmt.Sprintf("%s-%s-%s-%d-%d", virtualFile.ShareID, stToken, filepath.Base(dir.GetPath()), page, pageSize)
	}

	getFilesFunc := func() FileListResp {

		var fileResp FileListResp

		cacheKey := buildCacheKeyFunc()
		if cacheResp, exist := fileListRespCache.Get(cacheKey); exist {
			return cacheResp
		}

		err = limiter.WaitN(ctx, 1)
		if err != nil {
			return fileResp
		}

		_, err = d.request("/1/clouddrive/share/sharepage/detail", http.MethodGet, func(req *resty.Request) {
			req.SetQueryParams(
				map[string]string{
					"pr":       "ucpro",
					"force":    "0",
					"pwd_id":   virtualFile.ShareID,
					"stoken":   stToken,
					"pdir_fid": filepath.Base(dir.GetPath()),
					"_page":    strconv.Itoa(page),
					"_size":    strconv.Itoa(pageSize),
					"_sort":    "file_type:asc,file_name:asc,updated_at:desc",
				})
		}, &fileResp)

		fileListRespCache.Set(cacheKey, fileResp, cache.WithEx[FileListResp](time.Minute*time.Duration(d.CacheExpiration)))

		return fileResp
	}

	for nextPage {

		fileResp := getFilesFunc()
		if err != nil && strings.Contains(err.Error(), "分享的stoken过期") {
			utils.Log.Infof("获取夸克分享:%s文件列表失败:%v", dir.GetName(), err)
			err = nil

			shareTokenCache.Del(virtualFile.ShareID)
			fileListRespCache.Del(buildCacheKeyFunc())
			topDir := strings.Split(dir.GetPath(), "/")[0]
			op.ClearCache(d, topDir)
			utils.Log.Infof("由于文件token失效,因此清除:%s目录的文件缓存", topDir)

			stToken, err = d.getShareInfo(virtualFile.ShareID, virtualFile.SharePwd)
			if err != nil {
				utils.Log.Infof("分享的stoken过期后重新获取stoken失败:%s", err.Error())
				return nil, err
			}

			fileResp = getFilesFunc()

		}

		if err != nil {
			utils.Log.Infof("获取夸克分享:%s文件列表失败:%v", dir.GetName(), err)
			return res, err
		}

		for _, item := range fileResp.Data.List {
			res = append(res, FileObj{
				ObjThumb: model.ObjThumb{
					Object: model.Object{
						ID:       item.Fid,
						Name:     item.FileName,
						Size:     item.Size,
						Ctime:    time.UnixMilli(item.CreatedAt),
						Modified: time.UnixMilli(item.UpdateViewAt),
						IsFolder: item.Dir,
					},
					Thumbnail: model.Thumbnail{Thumbnail: item.Thumbnail},
				},
				ShareFidToken: item.ShareFidToken,
			})
		}

		pages := (fileResp.Metadata.Total + pageSize - 1) / pageSize
		nextPage = page <= pages
		page++

	}

	return res, nil
}

func (d *QuarkShare) transformFile(virtualFile model.VirtualFile, obj FileObj) (string, error) {

	stToken, err := d.getShareInfo(virtualFile.ShareID, virtualFile.SharePwd)
	if err != nil {
		return "", err
	}

	utils.Log.Infof("开始转存文件:%s", obj.GetName())

	var transformResult TransformResult
	transferFile := func() {
		_, err = d.request("/1/clouddrive/share/sharepage/save", http.MethodPost, func(req *resty.Request) {
			req.SetQueryParams(
				map[string]string{
					"pr": "ucpro",
				})
			req.SetBody(
				base.Json{
					"fid_list":       []string{obj.GetID()},
					"fid_token_list": []string{obj.ShareFidToken},
					"to_pdir_fid":    d.TransferPath,
					"pwd_id":         virtualFile.ShareID,
					"stoken":         stToken,
					"pdir_fid":       "0",
					"scene":          "link",
				})
		}, &transformResult)
	}

	transferFile()
	if err != nil && (strings.Contains(err.Error(), "token校验异常") || strings.Contains(err.Error(), "分享的stoken过期")) {
		utils.Log.Infof("夸克文件:%s转存失败:%v", obj.GetName(), err)
		err = nil

		shareTokenCache.Del(virtualFile.ShareID)
		topDir := strings.Split(obj.GetPath(), "/")[0]
		op.ClearCache(d, topDir)
		utils.Log.Infof("由于文件token失效,因此清除:%s目录的文件缓存", topDir)

		transferFile()
	}

	if err != nil {
		return "", err
	}

	taskId := transformResult.Data.TaskID
	if taskId == "" {
		utils.Log.Infof("夸克文件:%s转存失败:%v", obj.GetName(), transformResult)
		return "", errors.New("文件转存失败")
	}

	retryCount := 0

	for len(transformResult.Data.SaveAs.SaveAsTopFids) == 0 && retryCount < 10 {
		utils.Log.Infof("转存未完成,第%d次获取文件:%s的转存结果", retryCount, obj.GetName())
		retryCount++
		_, err = d.request("/1/clouddrive/task", http.MethodGet, func(req *resty.Request) {
			req.SetQueryParams(
				map[string]string{
					"pr":          "ucpro",
					"task_id":     taskId,
					"retry_index": strconv.Itoa(retryCount),
				})
		}, &transformResult)
		if err != nil {
			utils.Log.Infof("夸克文件:%s转存失败:%v", obj.GetName(), err)
			return "", err
		}
		if len(transformResult.Data.SaveAs.SaveAsTopFids) == 0 {
			time.Sleep(2 * time.Second)
		}
	}

	if len(transformResult.Data.SaveAs.SaveAsTopFids) == 0 {
		return "", errors.New("夸克文件转存结果获取超时")
	}

	return transformResult.Data.SaveAs.SaveAsTopFids[0], nil
}

var regExpirationTime = regexp.MustCompile(`Expires=(\d+)`)

func GetExpirationTime(url string) (etime time.Duration) {
	exps := regExpirationTime.FindStringSubmatch(url)
	if len(exps) < 2 {
		return
	}
	timestamp, err := strconv.ParseInt(exps[1], 10, 64)
	if err != nil {
		return
	}
	etime = time.Duration(timestamp-time.Now().Unix()) * time.Second
	return
}
