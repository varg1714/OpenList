package fc2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"
	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/emby"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/pkg/cron"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/go-resty/resty/v2"
)

type FC2 struct {
	model.Storage
	Addition
	AccessToken string
	ShareToken  string
	DriveId     string
	cron        *cron.Cron
	client      *resty.Client
}

func (d *FC2) Config() driver.Config {
	return config
}

func (d *FC2) GetAddition() driver.Additional {
	return &d.Addition
}

func (d *FC2) Init(ctx context.Context) error {
	if d.client == nil {
		d.client = newFC2HTTPClient()
	}

	duration := time.Minute * time.Duration(d.ReleaseScanTime)
	if duration <= 0 {
		duration = time.Minute * 60
	}

	d.cron = cron.NewCron(duration)
	d.cron.Do(func() {
		d.rematchMediaReleaseTime()
		if err := d.scanMediaArtifacts(); err != nil {
			utils.Log.Warnf("failed to synchronize FC2 artifacts: %s", err)
		}
		if err := d.syncConfiguredNFOs(); err != nil {
			utils.Log.Warnf("failed to synchronize FC2 NFOs: %s", err)
		}
		d.scanMediaSampleImages()
	})

	return nil
}

func newFC2HTTPClient() *resty.Client {
	return base.NewRestyClient().
		SetRetryCount(0).
		SetRedirectPolicy(resty.NoRedirectPolicy())
}

func (d *FC2) Drop(ctx context.Context) error {
	if d.cron != nil {
		d.cron.Stop()
	}
	return nil
}

func (d *FC2) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {

	categories := make(map[string]model.Actor)
	results := make([]model.Obj, 0)

	dirName := dir.GetName()

	actors := db.QueryActor(strconv.Itoa(int(d.ID)))
	for _, actor := range actors {
		categories[actor.Name] = actor
	}

	if d.RootID.GetRootId() == dirName {
		results = append(results, &model.ObjThumb{
			Object: model.Object{
				Name:     "关注演员",
				IsFolder: true,
				ID:       "关注演员",
				Size:     622857143,
				Modified: time.Now(),
			},
		}, &model.ObjThumb{
			Object: model.Object{
				Name:     "个人收藏",
				IsFolder: true,
				ID:       "个人收藏",
				Size:     622857143,
				Modified: time.Now(),
			},
		})
		return results, nil
	} else if dirName == "关注演员" {
		// 1. 顶级目录
		for category := range categories {
			results = append(results, &model.ObjThumb{
				Object: model.Object{
					Name:     category,
					IsFolder: true,
					ID:       category,
					Size:     622857143,
					Modified: categories[category].UpdatedAt,
				},
			})
		}
		return results, nil
	} else if dirName == "个人收藏" {
		films, err := virtual_file.ListMediaFiles(d.ID, "fc2", "个人收藏")
		if err != nil {
			return nil, err
		}
		return utils.SliceConvert(virtual_file.WrapMediaFiles(films), func(src model.EmbyFileDirWrapper) (model.Obj, error) {
			return &src, nil
		})
	} else if categories[dirName].Url != "" {
		// 自定义目录
		var films []model.EmbyFileObj
		var err error
		if strings.Contains(categories[dirName].Url, "missav.ai/dm99") {
			films, err = d.getMissAvFilms(dirName, func(index int) string {
				return d.ScraperApi + fmt.Sprintf(categories[dirName].Url, index)
			})
		} else {
			films, err = d.getFilms(dirName, func(index int) string {
				return fmt.Sprintf(categories[dirName].Url, index)
			})
		}
		if err != nil {
			return nil, err
		}
		return utils.SliceConvert(virtual_file.WrapMediaFiles(films), func(src model.EmbyFileDirWrapper) (model.Obj, error) {
			return &src, nil
		})

	} else if dirWrapper, ok := dir.(*model.EmbyFileDirWrapper); ok {
		return utils.SliceConvert(dirWrapper.EmbyFiles, func(src model.EmbyFileObj) (model.Obj, error) {
			return &src, nil
		})
	} else {
		return results, nil
	}

}

func (d *FC2) Get(ctx context.Context, path string) (model.Obj, error) {
	return virtual_file.ResolveMediaActorTreeObj(d.ID, "fc2", path, d.RootID.GetRootId(), d.Storage.Modified)
}

func (d *FC2) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {

	mockedLink := &model.Link{
		URL: d.MockedLink,
	}

	if d.MockedByMatchUa != "" && !virtual_file.AllowUA(args.Header.Get("User-Agent"), d.MockedByMatchUa) && d.MockedLink != "" {
		return mockedLink, nil
	}

	if d.Mocked && d.MockedLink != "" {
		return mockedLink, nil
	}

	mediaFile, err := mediaFileFromObj(file)
	if err != nil {
		return nil, err
	}
	return d.cloudPlayMedia(ctx, args, mediaFile)

}

func (d *FC2) Remove(ctx context.Context, obj model.Obj) error {
	if group, ok := obj.(*model.EmbyFileDirWrapper); ok && len(group.EmbyFiles) > 0 {
		return d.removeMediaWork(group.EmbyFiles[0].WorkID)
	}
	if mediaFile, ok := obj.(*model.EmbyFileObj); ok && mediaFile.WorkID != 0 {
		return d.removeMediaWork(mediaFile.WorkID)
	}
	works, err := db.ListFilmWorks(d.ID, "fc2", obj.GetName())
	if err != nil {
		return err
	}
	for _, work := range works {
		if err := d.removeMediaWork(work.ID); err != nil {
			return err
		}
	}
	return db.DeleteActor(strconv.Itoa(int(d.ID)), obj.GetName())
}

func (d *FC2) removeMediaWork(workID uint) error {
	work, err := db.GetFilmWork(workID)
	if err != nil {
		return err
	}
	if err := db.CreateMissedFilms([]string{work.Code}); err != nil {
		return fmt.Errorf("persist FC2 tombstone for %s: %w", work.Code, err)
	}
	return virtual_file.DeleteMediaWork(workID)
}

func (d *FC2) MakeDir(ctx context.Context, parentDir model.Obj, dirName string) error {

	var param MakeDirParam
	err := json.Unmarshal([]byte(dirName), &param)
	if err != nil {
		return err
	}

	var url string
	if param.Type == 0 {
		// 0 演员
		url = fmt.Sprintf("https://paipancon.com/fc2daily/actor/%s/", param.Url) + "page-%d.html"
	} else if param.Type == 1 {
		// 贩卖者
		url = fmt.Sprintf("https://paipancon.com/fc2daily/search/%s/", param.Url) + "page-%d.html"
	} else if param.Type == 2 {
		// missAv fc2收藏榜
		url = "https://missav.ai/dm99/cn/fc2?sort=saved&page=%d"
	} else {
		return errors.New("illegal actorType")
	}

	return db.CreateActor(strconv.Itoa(int(d.ID)), param.DirName, url)

}

func (d *FC2) Put(ctx context.Context, dstDir model.Obj, stream model.FileStreamer, up driver.UpdateProgress) (model.Obj, error) {
	star, err := d.addStar(stream.GetName(), []string{})
	if err != nil {
		return nil, err
	}
	op.Cache.DeleteDirectory(d, "个人收藏")
	if d.EmbyServers != "" {
		emby.Refresh(d.EmbyServers)
	}

	dirWrapper, err := wrapAddedStar(star)
	return &dirWrapper, err

}

func wrapAddedStar(star model.EmbyFileObj) (model.EmbyFileDirWrapper, error) {
	wrapped := virtual_file.WrapMediaFiles([]model.EmbyFileObj{star})
	if len(wrapped) != 1 {
		return model.EmbyFileDirWrapper{}, fmt.Errorf("expected one media work, got %d", len(wrapped))
	}
	return wrapped[0], nil
}

func (d *FC2) MkdirConfig() []driver.Item {
	return []driver.Item{
		{
			Name:     "dirName",
			Type:     conf.TypeString,
			Default:  "",
			Options:  "",
			Help:     "文件夹名称",
			Required: true,
		},
		{
			Name:     "type",
			Type:     conf.TypeSelect,
			Default:  "",
			Options:  "0,1,2",
			Help:     "0:演员;1:贩卖者;2:收藏榜",
			Required: true,
		},
		{
			Name:    "url",
			Type:    conf.TypeString,
			Default: "",
			Options: "",
			Help:    "url",
		},
	}
}

var _ driver.Driver = (*FC2)(nil)
