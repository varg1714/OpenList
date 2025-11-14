package javdb

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"
	"github.com/OpenListTeam/OpenList/v4/internal/av"
	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/emby"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/cron"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/emirpasic/gods/v2/maps/linkedhashmap"
)

type Javdb struct {
	model.Storage
	Addition
	AccessToken      string
	ShareToken       string
	DriveId          string
	cron             *cron.Cron
	matchTopFilmCorn *cron.Cron
}

func (d *Javdb) Config() driver.Config {
	return config
}

func (d *Javdb) GetAddition() driver.Additional {
	return &d.Addition
}

func (d *Javdb) Init(ctx context.Context) error {

	duration := time.Minute * time.Duration(d.SubtitleScanTime)
	if duration <= 0 {
		duration = time.Minute * 60
	}

	d.cron = cron.NewCron(duration)
	d.cron.Do(func() {
		d.reMatchSubtitles()
		if d.RefreshNfo {
			d.refreshNfo()
		}
		d.filterFilms()
		d.reMatchTags()

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
		return utils.SliceConvert(virtual_file.WrapEmbyFilms(d.getStars()), func(src model.EmbyFileDirWrapper) (model.Obj, error) {
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

		return utils.SliceConvert(virtual_file.WrapEmbyFilms(films), func(src model.EmbyFileDirWrapper) (model.Obj, error) {
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

	firstMagnet := ""
	firstLink, err2 := d.tryAcquireLink(ctx, file, args, func(obj model.Obj) (string, error) {
		magnet, err := d.getMagnet(obj, false)
		firstMagnet = magnet
		return magnet, err
	})

	if err2 != nil {
		utils.Log.Infof("The first magnet download failed:[%s], using the second magnet instead.", err2.Error())
		sukeMeta, _ := av.GetMetaFromSuke(file.GetName())
		magnets := sukeMeta.Magnets
		if len(magnets) > 0 && firstMagnet != magnets[0].GetMagnet() {
			secondLink, err3 := d.tryAcquireLink(ctx, file, args, func(obj model.Obj) (string, error) {
				return magnets[0].GetMagnet(), nil
			})
			if err3 != nil {
				utils.Log.Infof("The second magnet download failed:[%s].", err3.Error())
				if d.FallbackPlay {
					return mockedLink, nil
				}
			}
			return secondLink, err3

		}
	}

	return firstLink, err2
}

func (d *Javdb) Remove(ctx context.Context, obj model.Obj) error {

	if obj.IsDir() {
		if dirWrapper, ok := obj.(*model.EmbyFileDirWrapper); !ok {
			err := db.DeleteActor(strconv.Itoa(int(d.ID)), obj.GetName())
			if err != nil {
				return err
			}

			return db.DeleteFilmsByActor(DriverName, obj.GetName())
		} else {
			for _, file := range dirWrapper.EmbyFiles {
				err2 := d.deleteFilm(file.GetPath(), file.GetName(), file.GetID())
				if err2 != nil {
					return err2
				}
			}
		}

	} else {

		err2 := d.deleteFilm(obj.GetPath(), obj.GetName(), obj.GetID())
		if err2 != nil {
			return err2
		}

	}

	return nil

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
	if err == nil && d.EmbyServers != "" {
		emby.Refresh(d.EmbyServers)
	}

	dirWrapper := virtual_file.WrapEmbyFilms([]model.EmbyFileObj{star})[0]
	return &dirWrapper, err

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
