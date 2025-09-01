package virtual_file

import (
	"fmt"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"regexp"
	"strings"
	"time"
)

func GetFilms(source, dirName string, urlFunc func(index int) string, pageFunc func(urlFunc func(index int) string, index int, data []model.EmbyFileObj) ([]model.EmbyFileObj, bool, error)) ([]model.EmbyFileObj, error) {

	results := make([]model.EmbyFileObj, 0)
	films := make([]model.EmbyFileObj, 0)

	films, nextPage, err := pageFunc(urlFunc, 1, films)
	if err != nil {
		return results, err
	}

	// not exists
	for index := 2; index <= 20 && nextPage; index++ {

		films, nextPage, err = pageFunc(urlFunc, index, films)
		if err != nil {
			return results, err
		}

	}

	return convertObj(source, dirName, films, results), nil

}

func GetFilmsWithStorage(source, dirName, actorId string, urlFunc func(index int) string, pageFunc func(urlFunc func(index int) string, index int, preFilms []model.EmbyFileObj) ([]model.EmbyFileObj, bool, error), option Option) ([]model.EmbyFileObj, error) {

	results := make([]model.EmbyFileObj, 0)
	films := make([]model.EmbyFileObj, 0)

	films, nextPage, err := pageFunc(urlFunc, 1, films)
	if err != nil {
		return ConvertFilms(source, dirName, db.QueryByActor(source, dirName), results, option.CacheFile), err
	}

	var urls []string
	for _, item := range films {
		urls = append(urls, item.Url)
	}

	existFilms := db.QueryByUrls(actorId, urls)

	// not exists
	for index := 2; index <= option.MaxPageNum && nextPage && len(existFilms) == 0; index++ {

		films, nextPage, err = pageFunc(urlFunc, index, films)
		if err != nil {
			return ConvertFilms(source, dirName, db.QueryByActor(source, dirName), results, option.CacheFile), err
		}
		clear(urls)
		for _, item := range films {
			urls = append(urls, item.Url)
		}

		existFilms = db.QueryByUrls(actorId, urls)

	}
	// exist
	for index, item := range films {
		if utils.SliceContains(existFilms, item.Url) {
			if index == 0 {
				films = []model.EmbyFileObj{}
			} else {
				films = films[:index]
			}
			break
		}
	}

	if len(films) != 0 {
		err = db.CreateFilms(source, dirName, actorId, films)
		if err != nil {
			return ConvertFilms(source, dirName, db.QueryByActor(source, dirName), results, option.CacheFile), nil
		}
	}

	return ConvertFilms(source, dirName, db.QueryByActor(source, dirName), results, option.CacheFile), nil

}

func GetStorageFilms(source, dirName string, cacheFile bool) []model.EmbyFileObj {
	return ConvertFilms(source, dirName, db.QueryByActor(source, dirName), []model.EmbyFileObj{}, cacheFile)
}

func ConvertFilms(source, dirName string, films []model.Film, results []model.EmbyFileObj, cacheFile bool) []model.EmbyFileObj {

	for _, film := range films {

		thumb := ConvertFilmToEmbyFile(film, dirName)

		if cacheFile {
			_ = CacheImageAndNfo(MediaInfo{
				Source:   source,
				Dir:      dirName,
				FileName: AppendImageName(thumb.Name),
				Title:    thumb.Title,
				ImgUrl:   film.Image,
				Actors:   film.Actors,
				Release:  thumb.ReleaseTime,
				Tags:     film.Tags,
			})
		}

		results = append(results, thumb)
	}
	return results
}

func ConvertFilmToEmbyFile(film model.Film, dirName string) model.EmbyFileObj {

	thumb := model.EmbyFileObj{
		ObjThumb: model.ObjThumb{
			Object: model.Object{
				IsFolder: false,
				ID:       fmt.Sprintf("%d", film.ID),
				Size:     1417381701,
				Modified: film.CreatedAt,
				Path: func() string {
					if dirName != "" {
						return dirName
					}
					return film.Actor
				}(),
			},
			Thumbnail: model.Thumbnail{Thumbnail: film.Image},
		},
		Title: func() string {
			if film.Title != "" {
				return film.Title
			}
			return film.Name
		}(),
		Actors:      film.Actors,
		ReleaseTime: film.Date,
		Translated:  film.Title != "",
		Url:         film.Url,
		Tags:        film.Tags,
	}

	if strings.HasSuffix(film.Name, "mp4") {
		thumb.Name = AppendFilmName(CutString(ClearFilmName(film.Name)))
	} else {
		thumb.Name = AppendFilmName(CutString(film.Name))
	}
	return thumb
}

func convertObj(source, dirName string, actor []model.EmbyFileObj, results []model.EmbyFileObj) []model.EmbyFileObj {

	for _, film := range actor {
		parse, _ := time.Parse(time.DateTime, "2024-01-02 15:04:05")
		results = append(results, model.EmbyFileObj{
			ObjThumb: model.ObjThumb{
				Object: model.Object{
					Name:     AppendFilmName(film.Name),
					IsFolder: false,
					ID:       film.ID,
					Size:     1417381701,
					Modified: parse,
					Path:     dirName,
				},
				Thumbnail: model.Thumbnail{Thumbnail: film.Thumb()},
			},
			Title:  film.Title,
			Url:    film.Url,
			Tags:   film.Tags,
			Actors: film.Actors,
		})

		_ = CacheImageAndNfo(MediaInfo{
			Source:   source,
			Dir:      dirName,
			FileName: AppendImageName(film.Name),
			Title:    film.Title,
			ImgUrl:   film.Thumb(),
			Actors:   []string{dirName},
			Release:  film.ReleaseTime,
			Tags:     film.Tags,
		})

	}
	return results

}

func clearFileName(fileName string) string {

	index := strings.LastIndex(fileName, ".")
	if index == -1 {
		return fileName
	}

	return fileName[0:index]
}

func CutString(name string) string {

	prettyNameRegexp, _ := regexp.Compile("[\\/\\\\\\*\\?\\:\\\"\\<\\>\\|]")
	name = prettyNameRegexp.ReplaceAllString(name, "")

	// 将字符串转换为 rune 切片
	runes := []rune(name)

	if len(runes) <= 70 {
		return name
	}

	// 检查长度并截取
	runes = runes[:70]

	// 将 rune 切片转换回字符串
	return string(runes)

}

func ClearFilmName(name string) string {

	if strings.HasSuffix(name, ".mp4") {
		return name[0 : len(name)-4]
	}

	if strings.HasSuffix(name, ".jpg") {
		return name[0 : len(name)-4]
	}

	if strings.HasSuffix(name, ".") {
		// 仅有.
		return name[0 : len(name)-1]
	}

	// 返回原始文件名
	return name
}

func AppendFilmName(name string) string {
	// 返回原始文件名
	return ClearFilmName(name) + ".mp4"

}

func AppendImageName(name string) string {
	return ClearFilmName(name) + ".jpg"
}

func BatchSaveFilms(driverName, dirName string, savingFilms []model.EmbyFileObj,
	filmUpdateFunc func(newFilm model.EmbyFileObj, existFilm *model.Film, mediaInfo *MediaInfo) bool,
	filmCreateFunc func(newFilm model.EmbyFileObj, mediaInfo *MediaInfo)) {

	if len(savingFilms) == 0 {
		return
	}

	var newFilmUrls []string
	for _, film := range savingFilms {
		newFilmUrls = append(newFilmUrls, film.Url)
	}

	existFilms, err := db.QueryFilmsByUrls(newFilmUrls)
	if err != nil {
		utils.Log.Warnf("failed to query exist films, error message: %s", err.Error())
	} else {

		existFilmMap := utils.Slice2Map(existFilms, func(t model.Film) string {
			return t.Url
		}, func(t model.Film) model.Film {
			return t
		})

		var creatingFilms []model.EmbyFileObj
		var updatingFilms []model.Film

		for _, film := range savingFilms {

			mediaInfo := MediaInfo{
				Source:   driverName,
				Dir:      dirName,
				FileName: AppendImageName(film.Name),
				Title:    film.Title,
				ImgUrl:   film.Thumb(),
				Actors:   film.Actors,
				Release:  film.ReleaseTime,
				Tags:     film.Tags,
			}

			if existFilm, exist := existFilmMap[film.Url]; exist {
				updateFlag := filmUpdateFunc(film, &existFilm, &mediaInfo)
				if updateFlag {
					UpdateNfo(mediaInfo)
					updatingFilms = append(updatingFilms, existFilm)
				}
			} else {
				creatingFilms = append(creatingFilms, film)
				filmCreateFunc(film, &mediaInfo)
			}

		}

		if len(creatingFilms) > 0 {
			err1 := db.CreateFilms(driverName, dirName, dirName, creatingFilms)
			if err1 != nil {
				utils.Log.Warnf("failed to create film, error message: %s", err1.Error())
			}
		}
		if len(updatingFilms) > 0 {
			for _, film := range updatingFilms {
				err1 := db.UpdateFilm(film)
				if err1 != nil {
					utils.Log.Warnf("failed to update film, error message: %s", err1.Error())
				}
			}
		}

	}
}
