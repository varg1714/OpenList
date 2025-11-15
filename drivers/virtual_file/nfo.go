package virtual_file

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/cmd/flags"
	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

func CacheImageAndNfo(mediaInfo MediaInfo) int {

	actorNfo := cacheActorNfo(mediaInfo, false)
	if actorNfo == Exist {
		return Exist
	}

	return CacheImage(mediaInfo)

}

func DeleteImageAndNfo(source, dir, fileName string) error {

	sourceName := fileName[0:strings.LastIndex(fileName, ".")]

	nameRegexp, _ := regexp.Compile("(.*?)(-cd\\d+)")

	if nameRegexp.MatchString(sourceName) {

		// 有多个文件
		sourceName = nameRegexp.ReplaceAllString(sourceName, "$1")

		filePath := filepath.Join(flags.DataDir, "emby", source, dir, GetRealName(sourceName), fmt.Sprintf("%s-cd1.nfo", sourceName))
		for i := 1; utils.Exists(filePath); {
			err := os.Remove(filePath)
			if err != nil {
				return err
			}
			i++
			filePath = filepath.Join(flags.DataDir, "emby", source, dir, GetRealName(sourceName), fmt.Sprintf("%s-cd%d.nfo", sourceName, i))
		}

		filePath = filepath.Join(flags.DataDir, "emby", source, dir, GetRealName(sourceName), fmt.Sprintf("%s-cd1.jpg", sourceName))
		for i := 1; utils.Exists(filePath); {
			err := os.Remove(filePath)
			if err != nil {
				return err
			}
			i++
			filePath = filepath.Join(flags.DataDir, "emby", source, dir, GetRealName(sourceName), fmt.Sprintf("%s-cd%d.jpg", sourceName, i))
		}

	} else {
		// 删除nfo文件
		filePath := filepath.Join(flags.DataDir, "emby", source, dir, GetRealName(sourceName), sourceName+".nfo")
		if utils.Exists(filePath) {
			err := os.Remove(filePath)
			if err != nil {
				return err
			}
		}

		// 删除img文件
		filePath = filepath.Join(flags.DataDir, "emby", source, dir, GetRealName(sourceName), sourceName+".jpg")
		if utils.Exists(filePath) {
			err := os.Remove(filePath)
			if err != nil {
				return err
			}
		}
	}

	return nil

}

func CacheImage(mediaInfo MediaInfo) int {

	if mediaInfo.ImgUrl == "" {
		return CreatedFailed
	}

	filePath := filepath.Join(flags.DataDir, "emby", mediaInfo.Source, mediaInfo.Dir, GetRealName(mediaInfo.FileName), mediaInfo.FileName)
	if utils.Exists(filePath) {
		return Exist
	}

	imgResp, err := base.RestyClient.R().SetHeaders(mediaInfo.ImgUrlHeaders).Get(mediaInfo.ImgUrl)
	if err != nil {
		utils.Log.Warnf("failed to download the image file: %s", err.Error())
		return CreatedFailed
	}

	err = os.MkdirAll(filepath.Join(flags.DataDir, "emby", mediaInfo.Source, mediaInfo.Dir, GetRealName(mediaInfo.FileName)), 0777)
	if err != nil {
		utils.Log.Warnf("failed to make directory: %s", err.Error())
		return CreatedFailed
	}

	err = os.WriteFile(filePath, imgResp.Body(), 0777)
	if err != nil {
		utils.Log.Warnf("failed to write file: %s", err.Error())
		return CreatedFailed
	}

	ext := filepath.Ext(mediaInfo.FileName)
	fileName := strings.TrimSuffix(mediaInfo.FileName, ext)

	destFilePath := filepath.Join(filepath.Dir(filePath), fmt.Sprintf("%s-background%s", fileName, ext))

	if _, err1 := os.Stat(destFilePath); err1 == nil {
		return CreatedSuccess
	}

	err1 := os.Symlink(mediaInfo.FileName, destFilePath)
	if err1 != nil {
		utils.Log.Warnf("failed to generate backgroud image, error message: %s", err1.Error())
	}

	return CreatedSuccess
}

func UpdateNfo(mediaInfo MediaInfo) {

	cacheResult := cacheActorNfo(mediaInfo, false)
	if cacheResult != Exist {
		return
	}

	filePath := filepath.Join(flags.DataDir, "emby", mediaInfo.Source, mediaInfo.Dir, GetRealName(mediaInfo.FileName), clearFileName(mediaInfo.FileName)+".nfo")

	file, err := os.ReadFile(filePath)
	if err != nil {
		utils.Log.Warnf("failed to read file:[%s], error message:%s", filePath, err.Error())
		return
	}

	var media Media

	err = xml.Unmarshal(file, &media)
	if err != nil {
		utils.Log.Warnf("failed to parse file[%s], error message:%s", filePath, err.Error())

		utils.Log.Infof("try to delete the old nfo file: %s, and regenerate a new one", filePath)
		err1 := os.Remove(filePath)
		if err1 != nil {
			utils.Log.Warnf("failed to delete the file:[%s], error message:%s", filePath, err.Error())
		} else {
			cacheActorNfo(mediaInfo, false)
		}

		return
	}
	if len(mediaInfo.Actors) > 0 {

		actorSet := make(map[string]bool)
		for _, actor := range mediaInfo.Actors {
			actorSet[actor] = true
		}
		for _, actor := range media.Actor {
			actorSet[actor.Name] = true
		}

		var actorInfos []Actor
		for actor := range actorSet {
			actorInfos = append(actorInfos, Actor{
				Name: actor,
			})
		}
		media.Actor = actorInfos
	}

	if mediaInfo.Release.Year() != 1 {
		media.Release = mediaInfo.Release.Format(time.DateOnly)
		media.Premiered = media.Release
		media.Year = mediaInfo.Release.Format("2006")
		media.Month = mediaInfo.Release.Format("01")
	}

	if mediaInfo.Title != "" {
		media.Title.Inner = fmt.Sprintf("<![CDATA[%s]]>", mediaInfo.Title)
		media.Plot.Inner = fmt.Sprintf("<![CDATA[%s]]>", mediaInfo.Title)
	}

	if len(mediaInfo.Tags) > 0 {
		tagSet := make(map[string]bool)
		for _, tag := range mediaInfo.Tags {
			tagSet[tag] = true
		}
		for _, tag := range media.Tag {
			tagSet[tag.Inner] = true
		}

		var tags []Inner
		for tag := range tagSet {
			tags = append(tags, Inner{
				Inner: tag,
			})
		}
		media.Tag = tags
		media.Genre = tags

	}

	mediaXml, err := mediaToXML(&media)
	if err != nil {
		utils.Log.Infof("failed to parse media info:[%v] to xml, error message:%s", media, err.Error())
		return
	}
	err = os.WriteFile(filePath, mediaXml, 0777)
	if err != nil {
		utils.Log.Infof("failed to write faile:[%v],error message:%s", mediaInfo, err.Error())
	}

}

func ClearUnUsedFiles(source, dir string, fileNames []string) {

	fileNamesSet := make(map[string]bool, len(fileNames))
	for _, name := range fileNames {
		fileNamesSet[clearFileName(name)] = true
	}

	parentDir := filepath.Join(flags.DataDir, "emby", source, dir)
	readFiles, err := os.ReadDir(parentDir)
	if err != nil {
		utils.Log.Warnf("failed to read files:%s", err.Error())
		return
	}

	for _, file := range readFiles {
		if file.IsDir() && !fileNamesSet[filepath.Base(file.Name())] {
			err1 := os.Remove(filepath.Join(parentDir, file.Name()))
			if err1 != nil {
				utils.Log.Warnf("failed to remove file:%s", file.Name())
			} else {
				utils.Log.Infof("file:%s has been deleted", file.Name())
			}
		}
	}

}

func cacheActorNfo(mediaInfo MediaInfo, force bool) int {

	fileName := mediaInfo.FileName
	if fileName == "" {
		return CreatedFailed
	}

	source := mediaInfo.Source
	filePath := filepath.Join(flags.DataDir, "emby", source, mediaInfo.Dir, GetRealName(fileName), clearFileName(fileName)+".nfo")
	if utils.Exists(filePath) && !force {
		return Exist
	}

	err := os.MkdirAll(filepath.Join(flags.DataDir, "emby", source, mediaInfo.Dir, GetRealName(fileName)), 0777)
	if err != nil {
		utils.Log.Info("nfo缓存文件夹创建失败", err)
		return CreatedFailed
	}

	var actorInfos []Actor
	for _, actor := range mediaInfo.Actors {
		actorInfos = append(actorInfos, Actor{
			Name: actor,
		})
	}

	var tags []Inner
	for _, tag := range mediaInfo.Tags {
		tags = append(tags, Inner{
			Inner: tag,
		})
	}

	releaseTime := mediaInfo.Release.Format(time.DateOnly)
	media := Media{
		Plot:      Inner{Inner: fmt.Sprintf("<![CDATA[%s]]>", mediaInfo.Title)},
		Title:     Inner{Inner: fmt.Sprintf("<![CDATA[%s]]>", mediaInfo.Title)},
		Actor:     actorInfos,
		Release:   releaseTime,
		Premiered: releaseTime,
		Year:      mediaInfo.Release.Format("2006"),
		Month:     mediaInfo.Release.Format("01"),
		Tag:       tags,
		Genre:     tags,
	}

	xml, err := mediaToXML(&media)
	if err != nil {
		utils.Log.Info("xml格式转换失败", err)
		return CreatedFailed
	}
	err = os.WriteFile(filePath, xml, 0777)
	if err != nil {
		utils.Log.Infof("文件:%s的xml缓存失败:%v", fileName, err)
	}

	return CreatedSuccess

}

func SynImageAndNfo(source, dir string, films []model.EmbyFileObj) {

	for _, film := range films {
		mediaInfo := MediaInfo{
			Source:   source,
			Dir:      dir,
			FileName: AppendImageName(film.Name),
			Title:    film.Title,
			ImgUrl:   film.Thumb(),
			Actors:   film.Actors,
			Release:  film.ReleaseTime,
			Tags:     film.Tags,
		}
		_ = cacheActorNfo(mediaInfo, true)
		_ = CacheImage(mediaInfo)
	}

}
