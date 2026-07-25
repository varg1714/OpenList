package javdb

import (
	"context"
	"crypto/tls"
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
	"github.com/OpenListTeam/OpenList/v4/pkg/cron"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/emirpasic/gods/v2/maps/linkedhashmap"
	"github.com/go-resty/resty/v2"
)

type Javdb struct {
	model.Storage
	Addition
	AccessToken      string
	ShareToken       string
	DriveId          string
	cron             *cron.Cron
	matchTopFilmCorn *cron.Cron
	client           *resty.Client
}

func (d *Javdb) Config() driver.Config {
	return config
}

func (d *Javdb) GetAddition() driver.Additional {
	return &d.Addition
}

func (d *Javdb) Init(ctx context.Context) error {
	if d.client == nil {
		d.client = newSampleImageClient()
	}

	duration := time.Minute * time.Duration(d.SubtitleScanTime)
	if duration <= 0 {
		duration = time.Minute * 60
	}

	d.cron = cron.NewCron(duration)
	d.cron.Do(func() {
		if err := d.filterFilms(); err != nil {
			utils.Log.Warnf("failed to filter normalized JavDB works: %s", err)
		}
		d.scanTranslations()
		d.scanMediaSynopsis()
		d.scanMediaMetadataAndMagnets()
		d.scanMediaSubtitles()
		if err := d.syncConfiguredNFOs(); err != nil {
			utils.Log.Warnf("failed to synchronize JavDB NFOs: %s", err)
		}
		d.scanMediaSampleImages()
		d.scanMediaDMMPosters()
	})

	matchTopFilmsTimer := time.Hour * time.Duration(d.MatchTopFilmsTimer)
	if matchTopFilmsTimer <= 0 {
		matchTopFilmsTimer = time.Hour * 24
	}

	d.matchTopFilmCorn = cron.NewCron(matchTopFilmsTimer)
	d.matchTopFilmCorn.Do(func() {
		d.fetchJavTopFilms()
	})

	return nil
}

func newSampleImageClient() *resty.Client {
	return base.NewRestyClient().
		SetRetryCount(0).
		SetTLSClientConfig(&tls.Config{MinVersion: tls.VersionTLS12}).
		SetRedirectPolicy(resty.NoRedirectPolicy())
}

func (d *Javdb) Drop(ctx context.Context) error {
	if d.cron != nil {
		d.cron.Stop()
	}
	if d.matchTopFilmCorn != nil {
		d.matchTopFilmCorn.Stop()
	}
	return nil
}

func (d *Javdb) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {

	categories := linkedhashmap.New[string, model.Actor]()
	results := make([]model.Obj, 0)

	dirName := dir.GetName()

	actors := db.QueryActor(strconv.Itoa(int(d.ID)))
	for _, actor := range actors {
		categories.Put(actor.Name, actor)
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
		// 1. 关注演员
		categories.Each(func(name string, actor model.Actor) {
			results = append(results, &model.ObjThumb{
				Object: model.Object{
					Name:     name,
					IsFolder: true,
					ID:       name,
					Size:     622857143,
					Modified: actor.Model.UpdatedAt,
				},
			})
		})
		return results, nil
	} else if dirName == "个人收藏" {
		// 2. 个人收藏
		films, err := virtual_file.ListMediaFiles(d.ID, DriverName, "个人收藏")
		if err != nil {
			return nil, err
		}
		return utils.SliceConvert(virtual_file.WrapMediaFiles(films), func(src model.EmbyFileDirWrapper) (model.Obj, error) {
			return &src, nil
		})
	} else if actor, exist := categories.Get(dirName); exist {
		// 自定义目录
		url := actor.Url
		if !strings.HasPrefix(url, "http") {
			url = "https://javdb.com/actors/" + url + "?page=%d&sort_type=0"
		}

		films, err := d.getFilms(dirName, func(index int) string {
			return fmt.Sprintf(url, index)
		})
		if err != nil {
			utils.Log.Info("影片获取失败", err)
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

func (d *Javdb) Get(ctx context.Context, path string) (model.Obj, error) {
	return virtual_file.ResolveMediaActorTreeObj(d.ID, DriverName, path, d.RootID.GetRootId(), d.Storage.Modified)
}

func (d *Javdb) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {

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
	link, err := d.cloudPlayMedia(ctx, args, d.CloudPlayDriverType, mediaFile)
	if err != nil && d.FallbackPlay && d.MockedLink != "" {
		return mockedLink, nil
	}
	return link, err
}

func (d *Javdb) Remove(ctx context.Context, obj model.Obj) error {
	if group, ok := obj.(*model.EmbyFileDirWrapper); ok && len(group.EmbyFiles) > 0 {
		return virtual_file.DeleteMediaWork(group.EmbyFiles[0].WorkID)
	}
	if mediaFile, ok := obj.(*model.EmbyFileObj); ok && mediaFile.WorkID != 0 {
		return virtual_file.DeleteMediaFile(mediaFile.FilmFileID)
	}
	works, err := db.ListFilmWorks(d.ID, DriverName, obj.GetName())
	if err != nil {
		return err
	}
	for _, work := range works {
		if err := virtual_file.DeleteMediaWork(work.ID); err != nil {
			return err
		}
	}
	return db.DeleteActor(strconv.Itoa(int(d.ID)), obj.GetName())
}

func (d *Javdb) MakeDir(ctx context.Context, parentDir model.Obj, dirName string) error {

	var req CreatActorReq
	err := utils.Json.Unmarshal([]byte(dirName), &req)
	if err != nil {
		return err
	}

	return db.CreateActor(strconv.Itoa(int(d.ID)), req.ActorName, req.ActorId)

}

func (d *Javdb) Put(ctx context.Context, dstDir model.Obj, stream model.FileStreamer, up driver.UpdateProgress) (model.Obj, error) {
	star, err := d.addStar(stream.GetName(), []string{})
	if err != nil {
		return nil, err
	}
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

func (d *Javdb) MkdirConfig() []driver.Item {
	return []driver.Item{
		{
			Name:     "actorName",
			Type:     conf.TypeString,
			Default:  "",
			Options:  "",
			Help:     "演员名称",
			Required: true,
		},
		{
			Name:     "actorId",
			Type:     conf.TypeString,
			Default:  "",
			Options:  "",
			Help:     "演员ID",
			Required: true,
		},
	}
}

var _ driver.Driver = (*Javdb)(nil)
