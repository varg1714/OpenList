package pornhub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"
	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/cron"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

type Pornhub struct {
	model.Storage
	Addition
	AccessToken string
	ShareToken  string
	DriveId     string
	cron        *cron.Cron
}

func (d *Pornhub) Config() driver.Config {
	return config
}

func (d *Pornhub) GetAddition() driver.Additional {
	return &d.Addition
}

func (d *Pornhub) Init(ctx context.Context) error {

	duration := time.Minute * time.Duration(d.MatchFilmTagScanTime)
	if duration <= 0 {
		duration = time.Minute * 60
	}

	d.cron = cron.NewCron(duration)
	d.cron.Do(func() {
		d.reMatchTags()
	})

	return nil
}

func (d *Pornhub) Drop(ctx context.Context) error {
	if d.cron != nil {
		d.cron.Stop()
	}
	return nil
}

func (d *Pornhub) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {

	categories := make(map[string]model.Actor)
	results := make([]model.Obj, 0)

	dirName := dir.GetName()

	actors := db.QueryActor(strconv.Itoa(int(d.ID)))
	for _, actor := range actors {
		categories[actor.Name] = actor
	}

	if d.RootID.GetRootId() == dirName {
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
	} else if categories[dirName].Url != "" {
		// 自定义目录
		films, err := d.getFilms(dirName, categories[dirName].Url)
		if err != nil {
			return nil, err
		}

		if d.SyncNfo {
			virtual_file.SynImageAndNfo(DriverName, dirName, films)
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

func (d *Pornhub) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {

	cacheTime := time.Second * time.Duration(d.LinkCacheTime)
	videoLink := &model.Link{
		URL:        d.MockedLink,
		Expiration: &cacheTime,
	}

	if d.MockedByMatchUa != "" && !virtual_file.AllowUA(args.Header.Get("User-Agent"), d.MockedByMatchUa) && d.MockedLink != "" {
		return videoLink, nil
	}

	if d.Mocked && d.MockedLink != "" {
		return videoLink, nil
	}

	if embyFile, ok := file.(*model.EmbyFileObj); ok {
		link, err := d.getVideoLink(embyFile.Url)
		if err != nil {
			utils.Log.Warnf("failed to get video link: %v", err.Error())
			return videoLink, nil
		}

		videoLink.URL = link
		videoLink.Header = http.Header{
			"Referer": []string{d.ServerUrl},
		}
		return videoLink, nil
	}

	return nil, errors.New("invalid file type")

}

func (d *Pornhub) Remove(ctx context.Context, obj model.Obj) error {

	if !obj.IsDir() {
		return nil
	}

	err := db.DeleteActor(strconv.Itoa(int(d.ID)), obj.GetName())
	if err != nil {
		return err
	}

	return db.DeleteFilmsByActor(DriverName, obj.GetName())

}

func (d *Pornhub) MakeDir(ctx context.Context, parentDir model.Obj, dirName string) error {

	var param MakeDirParam
	err := json.Unmarshal([]byte(dirName), &param)
	if err != nil {
		return err
	}

	name := param.DirName
	url := param.Url
	actorType := param.Type

	if actorType == PlayList {
		// playlist
		url = fmt.Sprintf("/playlist/%s", url)
	} else if actorType == Model {
		// actor
		url = fmt.Sprintf("/model/%s", url)
	} else if actorType == PornStar {
		url = fmt.Sprintf("/pornstar/%s/videos/upload", url)
	} else {
		return errors.New("illegal actorType")
	}

	return db.CreateActor(strconv.Itoa(int(d.ID)), name, url)

}

func (d *Pornhub) MkdirConfig() []driver.Item {
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
			Default:  "1",
			Options:  "0,1,2",
			Help:     "0:播放列表;1:演员;2:明星",
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

var _ driver.Driver = (*Pornhub)(nil)
