package _115_share

import (
	"context"

	"github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	driver115 "github.com/SheltonZhu/115driver/pkg/driver"
	"golang.org/x/time/rate"
)

type Pan115Share struct {
	model.Storage
	Addition
	client  *driver115.Pan115Client
	limiter *rate.Limiter
}

func (d *Pan115Share) Config() driver.Config {
	return config
}

func (d *Pan115Share) GetAddition() driver.Additional {
	return &d.Addition
}

func (d *Pan115Share) Init(ctx context.Context) error {
	if d.LimitRate > 0 {
		d.limiter = rate.NewLimiter(rate.Limit(d.LimitRate), 1)
	}

	return d.login()
}

func (d *Pan115Share) WaitLimit(ctx context.Context) error {
	if d.limiter != nil {
		return d.limiter.Wait(ctx)
	}
	return nil
}

func (d *Pan115Share) Drop(ctx context.Context) error {
	return nil
}

func (d *Pan115Share) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {

	return virtual_file.List(d.ID, dir, func(virtualFile model.VirtualFile, dir model.Obj) ([]model.Obj, error) {

		if err := d.WaitLimit(ctx); err != nil {
			return nil, err
		}

		parentId := ""
		if virDir, ok := dir.(*model.ObjVirtualDir); ok {
			parentId = virDir.VirtualFile.ParentDir
		} else {
			parentId = dir.GetID()
		}

		files := make([]driver115.ShareFile, 0)
		fileResp, err := d.client.GetShareSnap(virtualFile.ShareID, virtualFile.SharePwd, parentId, driver115.QueryLimit(int(d.PageSize)))
		if err != nil {
			return nil, err
		}
		files = append(files, fileResp.Data.List...)
		total := fileResp.Data.Count
		count := len(fileResp.Data.List)
		for total > count {
			fileResp, err := d.client.GetShareSnap(
				virtualFile.ShareID, virtualFile.SharePwd, dir.GetID(),
				driver115.QueryLimit(int(d.PageSize)), driver115.QueryOffset(count),
			)
			if err != nil {
				return nil, err
			}
			files = append(files, fileResp.Data.List...)
			count += len(fileResp.Data.List)
		}

		return utils.SliceConvert(files, func(src driver115.ShareFile) (model.Obj, error) {
			return transFunc(dir, src)
		})
	})

}

func (d *Pan115Share) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
	if err := d.WaitLimit(ctx); err != nil {
		return nil, err
	}

	virtualFile := virtual_file.GetSubscription(d.ID, file.GetPath())
	downloadInfo, err := d.client.DownloadByShareCode(virtualFile.ShareID, virtualFile.SharePwd, file.GetID())
	if err != nil {
		return nil, err
	}

	return &model.Link{URL: downloadInfo.URL.URL}, nil
}

func (d *Pan115Share) MakeDir(ctx context.Context, parentDir model.Obj, dirName string) error {
	return virtual_file.MakeDir(d.ID, parentDir, dirName)
}

func (d *Pan115Share) Move(ctx context.Context, srcObj, dstDir model.Obj) error {
	return virtual_file.Move(d.ID, srcObj, dstDir)
}

func (d *Pan115Share) Rename(ctx context.Context, srcObj model.Obj, newName string) error {
	return virtual_file.Rename(d.ID, srcObj, newName)
}

func (d *Pan115Share) Copy(ctx context.Context, srcObj, dstDir model.Obj) error {
	return errs.NotSupport
}

func (d *Pan115Share) Remove(ctx context.Context, obj model.Obj) error {
	return virtual_file.DeleteVirtualFile(d.ID, obj)
}

func (d *Pan115Share) Put(ctx context.Context, dstDir model.Obj, stream model.FileStreamer, up driver.UpdateProgress) error {
	return errs.NotSupport
}

func (d *Pan115Share) MkdirConfig() []driver.Item {
	return virtual_file.GetMkdirConfig()
}

var _ driver.Driver = (*Pan115Share)(nil)
