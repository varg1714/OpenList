package javdb

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"
	"github.com/OpenListTeam/OpenList/v4/internal/av"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/open_ai"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

func (d *Javdb) getFilms(dirName string, urlFunc func(index int) string) ([]model.EmbyFileObj, error) {

	// 1. fetch films
	d.fetchFilms(dirName, urlFunc)

	return virtual_file.ListMediaFiles(d.ID, DriverName, dirName)

}

func (d *Javdb) fetchFilms(dirName string, urlFunc func(index int) string) {

	existFilmFlag := false
	nextPage := true
	var discovered []model.EmbyFileObj

	for index := 1; index <= 20 && nextPage && !existFilmFlag; index++ {

		films, tempNextPage, err := d.getJavPageInfo(urlFunc, index)
		if err != nil {
			utils.Log.Warnf("failed to query javdb films, error message: %s", err.Error())
			break
		}

		nextPage = tempNextPage

		existingWorks, err := db.ListFilmWorks(d.ID, DriverName, dirName)
		if err != nil {
			utils.Log.Warnf("failed to query existing JavDB works: %s", err)
			break
		}
		existingCodes := make(map[string]bool, len(existingWorks))
		for _, work := range existingWorks {
			existingCodes[work.Code] = true
		}

		for _, film := range films {
			if isExistingDiscoveredWork(film, existingCodes) {
				existFilmFlag = true
			} else {
				discovered = append(discovered, film)
			}
		}

	}

	for _, film := range discovered {
		work, err := buildDiscoveredWork(d.ID, dirName, film)
		if err != nil {
			utils.Log.Warnf("failed to normalize JavDB discovery %q: %s", film.GetName(), err)
			continue
		}
		if err := db.UpsertDiscoveredWork(&work); err != nil {
			utils.Log.Warnf("failed to upsert JavDB work %s: %s", work.Code, err)
			continue
		}
		if _, err := db.EnsureSingleFilmFile(work.ID); err != nil {
			utils.Log.Warnf("failed to ensure JavDB file %s: %s", work.Code, err)
		}
	}

}

func isExistingDiscoveredWork(film model.EmbyFileObj, existingCodes map[string]bool) bool {
	codePart, _ := splitName(film.Name)
	code, err := model.NormalizeMediaCode(DriverName, codePart)
	return err == nil && existingCodes[code]
}

func buildDiscoveredWork(storageID uint, primaryDir string, film model.EmbyFileObj) (model.FilmWork, error) {
	code, rawTitle := splitName(film.GetName())
	code, err := model.NormalizeMediaCode(DriverName, code)
	if err != nil {
		return model.FilmWork{}, err
	}
	return model.FilmWork{
		StorageID: storageID, Source: DriverName, Code: code,
		SourceRef: film.Url, SourceURL: film.Url, PrimaryDir: primaryDir,
		RawTitle: rawTitle, ImageURL: film.Thumb(), ReleaseDate: film.ReleaseTime,
	}, nil
}

func discoveredFilmFiles(count int) ([]model.FilmFile, error) {
	if count < 1 {
		return nil, fmt.Errorf("film file count must be positive")
	}
	files := make([]model.FilmFile, count)
	for index := range files {
		files[index].PartIndex = index + 1
		files[index].PartCount = count
	}
	return files, nil
}

func (d *Javdb) mappingNames(dirName string, javFilms []model.EmbyFileObj) ([]model.EmbyFileObj, error) {

	if len(javFilms) == 0 {
		return javFilms, nil
	}

	var noTitleFilms []model.EmbyFileObj
	for _, film := range javFilms {
		if !film.Translated {
			noTitleFilms = append(noTitleFilms, film)
		}
	}
	if len(noTitleFilms) == 0 {
		return javFilms, nil
	}

	// 2.1 Airav 爬取
	nameMapping, err := d.getAiravNamingFilms(noTitleFilms, dirName)
	if err != nil {
		utils.Log.Infof("failed to get name mappings from airav, error: %v", err)
		return javFilms, nil
	}

	// 2.2 全量 AI 翻译/校验（airav 结果可能不是中文或不够通顺）
	var translateItems []open_ai.TranslateItem
	var translateFilms []model.EmbyFileObj
	for _, film := range noTitleFilms {
		code := splitCode(film.Title)
		_, name := splitName(film.Title)
		item := open_ai.TranslateItem{
			Origin: virtual_file.ClearFilmName(name),
		}
		if matched, exists := nameMapping[code]; exists {
			_, candidateName := splitName(matched.Title)
			item.Candidate = virtual_file.ClearFilmName(candidateName)
		}
		translateItems = append(translateItems, item)
		translateFilms = append(translateFilms, film)
	}

	translations := open_ai.BatchTranslate(translateItems)
	for i, translated := range translations {
		film := translateFilms[i]
		code, _ := splitName(film.Title)
		if translated != "" {
			title := fmt.Sprintf("%s %s", code, translated)
			embyObj := model.EmbyFileObj{
				ObjThumb: model.ObjThumb{Object: model.Object{Name: title}},
				Title:    title,
			}
			if existing, exists := nameMapping[code]; exists && existing.Synopsis != "" {
				embyObj.Synopsis = existing.Synopsis
			}
			nameMapping[code] = embyObj
		} else if _, exists := nameMapping[code]; !exists {
			nameMapping[code] = film
		}
	}

	if len(nameMapping) == 0 {
		return javFilms, nil
	}

	// 2.3 进行映射
	var savingFilms []model.EmbyFileObj
	var deletingFilms []string

	for index, film := range javFilms {
		if !film.Translated {
			code := splitCode(film.Name)
			if matchedFilm, exist := nameMapping[code]; exist {
				javFilms[index].Name = virtual_file.AppendFilmName(virtual_file.CutString(virtual_file.ClearFilmName(matchedFilm.Title)))
				javFilms[index].Title = virtual_file.ClearFilmName(matchedFilm.Title)
				if matchedFilm.Synopsis != "" {
					javFilms[index].Synopsis = matchedFilm.Synopsis
				}

				savingFilms = append(savingFilms, javFilms[index])
				deletingFilms = append(deletingFilms, film.Url)
			}
		}
	}
	if len(savingFilms) > 0 {
		err1 := db.DeleteFilmsByUrl(DriverName, dirName, deletingFilms)
		if err1 != nil {
			utils.Log.Warnf("failed to delete films:[%s], error message: %s", deletingFilms, err1.Error())
		} else {
			err2 := db.CreateFilms(DriverName, dirName, dirName, savingFilms)
			if err2 != nil {
				utils.Log.Infof("failed to save films:[%s], error message: %s", deletingFilms, err2.Error())
			}
		}
	}

	for _, film := range javFilms {
		created := virtual_file.CacheImageAndNfo(virtual_file.MediaInfo{
			Source:   DriverName,
			Dir:      dirName,
			FileName: virtual_file.AppendImageName(film.Name),
			Title:    film.Title,
			Synopsis: film.Synopsis,
			ImgUrl:   film.Thumb(),
			Actors:   []string{dirName},
			Release:  film.ReleaseTime,
		})

		if created == virtual_file.Exist && d.QuickCache {
			// 已经创建过了，后续不再创建
			break
		}

	}

	utils.Log.Infof("name mapping finished for:[%s]", dirName)

	return javFilms, err
}

func (d *Javdb) getStars() []model.EmbyFileObj {
	films, err := virtual_file.ListMediaFiles(d.ID, DriverName, "个人收藏")
	if err != nil {
		utils.Log.Warnf("failed to list JavDB favorite works: %s", err)
		return nil
	}
	return films
}

func (d *Javdb) addStar(code string, tags []string) (model.EmbyFileObj, error) {
	canonical, err := model.NormalizeMediaCode(DriverName, code)
	if err != nil {
		return model.EmbyFileObj{}, err
	}
	if existing, findErr := db.GetFilmWorkByIdentity(d.ID, DriverName, canonical); findErr == nil {
		mergedTags := append(model.StringArray(nil), existing.Tags...)
		seen := make(map[string]bool, len(mergedTags))
		for _, tag := range mergedTags {
			seen[tag] = true
		}
		for _, tag := range tags {
			if !seen[tag] {
				mergedTags = append(mergedTags, tag)
				seen[tag] = true
			}
		}
		if len(mergedTags) != len(existing.Tags) {
			if err := db.UpdateMediaWorkTags(existing.ID, mergedTags, existing.TagVersion+1); err != nil {
				return model.EmbyFileObj{}, err
			}
			existing.Tags = mergedTags
		}
		file, err := db.EnsureSingleFilmFile(existing.ID)
		if err != nil {
			return model.EmbyFileObj{}, err
		}
		return virtual_file.ConvertMediaFileToEmbyFile(model.FilmFileWithWork{FilmFile: file, Work: existing})
	}

	javFilms, _, err := d.getJavPageInfo(func(index int) string {
		return fmt.Sprintf("https://javdb.com/search?f=download&q=%s", code)
	}, 1)
	if err != nil {
		utils.Log.Info("jav影片查询失败:", err)
		return model.EmbyFileObj{}, err
	}

	if len(javFilms) == 0 || strings.ToLower(canonical) != strings.ToLower(splitCode(javFilms[0].Name)) {
		return model.EmbyFileObj{}, errors.New(fmt.Sprintf("影片:%s未查询到", code))
	}

	cachingFilm := javFilms[0]
	_, airavFilm, err := d.getAiravNamingAddr(cachingFilm)
	if err != nil {
		utils.Log.Info("addStar: airav详情页爬取失败", err)
	}

	tempCode, name := splitName(cachingFilm.Name)
	item := open_ai.TranslateItem{
		Origin: virtual_file.ClearFilmName(name),
	}
	if airavFilm.Name != "" {
		_, candidateName := splitName(airavFilm.Title)
		item.Candidate = virtual_file.ClearFilmName(candidateName)
		cachingFilm.Synopsis = airavFilm.Synopsis
	}

	translations := open_ai.BatchTranslate([]open_ai.TranslateItem{item})
	if len(translations) > 0 && translations[0] != "" {
		translatedTitle := fmt.Sprintf("%s %s", tempCode, translations[0])
		cachingFilm.Name = translatedTitle
		cachingFilm.Title = translatedTitle
	}

	d.fetchFilmMeta(cachingFilm.Url, &cachingFilm)
	cachingFilm.Actors = append(cachingFilm.Actors, "个人收藏")
	for _, tag := range tags {
		cachingFilm.Tags = append(cachingFilm.Tags, tag)
	}

	work, err := buildDiscoveredWork(d.ID, "个人收藏", cachingFilm)
	if err != nil {
		return model.EmbyFileObj{}, err
	}
	work.Code = canonical
	if err := db.UpsertDiscoveredWork(&work); err != nil {
		return model.EmbyFileObj{}, err
	}
	translated := ""
	_, translated = splitName(cachingFilm.Title)
	if err := db.UpdateMediaWorkDetails(work.ID, translated, cachingFilm.Synopsis, model.StringArray(cachingFilm.Actors), model.StringArray(cachingFilm.Tags)); err != nil {
		return model.EmbyFileObj{}, err
	}
	work.TranslatedTitle = translated
	work.Synopsis = cachingFilm.Synopsis
	work.Actors = model.StringArray(cachingFilm.Actors)
	work.Tags = model.StringArray(cachingFilm.Tags)
	file, err := db.EnsureSingleFilmFile(work.ID)
	if err != nil {
		return model.EmbyFileObj{}, err
	}
	identity := mediaIdentity(work)
	if result := virtual_file.CacheImageAndNfo(virtual_file.MediaInfo{
		Identity: &identity, Title: model.BuildMediaTitle(work.Code, work.RawTitle, work.TranslatedTitle),
		Synopsis: work.Synopsis, ImgUrl: work.ImageURL, Actors: []string(work.Actors), Release: work.ReleaseDate, Tags: []string(work.Tags),
	}); result == virtual_file.CreatedFailed {
		utils.Log.Warnf("failed to publish JavDB favorite artifacts for %s", work.Code)
	}
	return virtual_file.ConvertMediaFileToEmbyFile(model.FilmFileWithWork{FilmFile: file, Work: work})

}

func createStarFilm(cachingFilm *model.EmbyFileObj) error {
	cachingFilm.Name = virtual_file.AppendFilmName(virtual_file.CutString(virtual_file.ClearFilmName(cachingFilm.Name)))
	return db.CreateFilms(DriverName, "个人收藏", "个人收藏", []model.EmbyFileObj{*cachingFilm})
}

func (d *Javdb) updateExistFilm(existFilm *model.Film, actors, tags []string) {

	embyFile := virtual_file.ConvertFilmToEmbyFile(*existFilm, "")

	updateTagFlag := false
	updateActorTag := false

	existTagMap := make(map[string]bool)
	for _, tag := range embyFile.Tags {
		existTagMap[tag] = true
	}
	for _, tag := range tags {
		if !existTagMap[tag] {
			embyFile.Tags = append(embyFile.Tags, tag)
			updateTagFlag = true
		}
	}

	existActorMap := make(map[string]bool)
	for _, actor := range embyFile.Actors {
		existActorMap[actor] = true
	}
	for _, actor := range actors {
		if !existActorMap[actor] {
			embyFile.Actors = append(embyFile.Actors, actor)
			updateActorTag = true
		}
	}

	if !updateTagFlag && !updateActorTag {
		return
	}

	virtual_file.UpdateNfo(virtual_file.MediaInfo{
		Source:   DriverName,
		Dir:      embyFile.Path,
		FileName: virtual_file.AppendImageName(embyFile.Name),
		Title:    embyFile.Title,
		Synopsis: embyFile.Synopsis,
		Actors:   embyFile.Actors,
		Release:  embyFile.ReleaseTime,
		Tags:     embyFile.Tags,
	})

	existFilm.Tags = embyFile.Tags
	existFilm.Actors = embyFile.Actors

	err1 := db.UpdateFilm(*existFilm)
	if err1 != nil {
		utils.Log.Warnf("failed to update films:[%s], error message: %s", tags, err1.Error())
	}

}

func (d *Javdb) getMagnet(file model.Obj, reMatchFilmMeta bool) (string, error) {
	mediaFile, err := mediaFileFromObj(file)
	if err != nil {
		return "", err
	}
	if !reMatchFilmMeta {
		selected, err := db.GetSelectedSourceMagnet(mediaFile.WorkID)
		if err == nil {
			return selected.MagnetURI, nil
		}
	}
	magnets, err := d.mediaMagnets(context.Background(), mediaFile.Work)
	if err != nil {
		return "", err
	}
	if err := db.UpsertSourceMagnets(mediaFile.WorkID, magnets); err != nil {
		return "", err
	}
	selected, err := db.GetSelectedSourceMagnet(mediaFile.WorkID)
	if err != nil {
		return "", err
	}
	return selected.MagnetURI, nil
}

func (d *Javdb) cacheMagnet(javdbMeta av.Meta, embyObj *model.EmbyFileObj) (string, error) {
	if embyObj.WorkID == 0 {
		return "", errors.New("media work identity is missing")
	}
	magnets := sourceMagnetsFromMeta(javdbMeta)
	if len(magnets) == 0 {
		return "", errors.New("JavDB returned no source magnets")
	}
	if err := db.UpsertSourceMagnets(embyObj.WorkID, magnets); err != nil {
		return "", err
	}
	selected, err := db.GetSelectedSourceMagnet(embyObj.WorkID)
	if err != nil {
		return "", err
	}
	return selected.MagnetURI, nil
}

func (d *Javdb) updateFilmMeta(javdbMeta av.Meta, embyObj *model.EmbyFileObj) {

	actorMapping := make(map[string]string)
	for _, actor := range javdbMeta.Actors {
		actorMapping[actor.Id] = actor.Name
	}
	for _, actor := range embyObj.Actors {
		actorMapping[actor] = actor
	}

	actors := db.QueryActor(strconv.Itoa(int(d.ID)))
	for _, actor := range actors {
		if actorMapping[actor.Url] != "" {
			actorMapping[actor.Url] = actor.Name
		}
	}

	var actorNames []string
	for _, name := range actorMapping {
		actorNames = append(actorNames, name)
	}

	var tags []string
	tagMap := make(map[string]bool)

	for _, tag := range embyObj.Tags {
		tagMap[tag] = true
	}

	if len(javdbMeta.Magnets) > 0 {
		for _, tag := range javdbMeta.Magnets[0].GetTags() {
			tagMap[tag] = true
		}
	}

	for tag := range tagMap {
		tags = append(tags, tag)
	}

	virtual_file.UpdateNfo(virtual_file.MediaInfo{
		Source:   DriverName,
		Dir:      embyObj.Path,
		FileName: virtual_file.AppendImageName(embyObj.Name),
		Title:    embyObj.Title,
		Synopsis: embyObj.Synopsis,
		Actors:   actorNames,
		Release:  embyObj.ReleaseTime,
		Tags:     tags,
	})

	tempId, err1 := strconv.ParseInt(embyObj.ID, 10, 64)
	if err1 == nil {
		err1 = db.UpdateFilmActorsAndTags(uint(tempId), actorNames, tags, false)
		if err1 != nil {
			utils.Log.Warnf("failed to save film: %s, error message: %s", embyObj.GetName(), err1.Error())
		}
	} else {
		utils.Log.Warnf("failed to parse films: %s id to int, error message: %s", embyObj.GetName(), err1.Error())
	}

}

func (d *Javdb) addSubtitleTag(name string) {
	code := av.GetFilmCode(name)
	film, err := db.QueryFilmByCode(DriverName, code)
	if err != nil {
		utils.Log.Warnf("failed to query film for subtitle tag: %s", err.Error())
		return
	}

	for _, tag := range film.Tags {
		if tag == model.TagSubtitle {
			return
		}
	}

	newTags := append(film.Tags, model.TagSubtitle)
	subtitleOnly := len(film.Tags) == 0

	err = db.UpdateFilmTags(film.ID, newTags, subtitleOnly)
	if err != nil {
		utils.Log.Warnf("failed to add subtitle tag to film %s: %s", name, err.Error())
	}
}

func (d *Javdb) deleteFilm(dir, fileName, id string) error {
	err := db.DeleteFilmById(id)
	if err != nil {
		utils.Log.Infof("failed to delete film:[%s], error message:[%s]", fileName, err.Error())
		return err
	}

	err = virtual_file.DeleteImageAndNfo(DriverName, dir, fileName)
	if err != nil {
		utils.Log.Infof("failed to delete film nfo:[%s], error message:[%s]", fileName, err)
		return err
	}
	return nil
}

func (d *Javdb) tryAcquireLink(ctx context.Context, file model.Obj, args model.LinkArgs, magnetGetter func(obj model.Obj) (string, error)) (*model.Link, error) {
	mediaFile, err := mediaFileFromObj(file)
	if err != nil {
		return nil, err
	}
	link, err := d.cloudPlayMedia(ctx, args, d.CloudPlayDriverType, mediaFile)
	if err != nil {
		utils.Log.Infof("The first cloud drive download failed:[%s]", err.Error())
		if d.BackPlayDriverType != "" {
			utils.Log.Infof("using the second cloud drive instead.")
			return d.cloudPlayMedia(ctx, args, d.BackPlayDriverType, mediaFile)
		}
	}

	return link, err
}

// set cookies raw
func setCookieRaw(cookieRaw string) []*http.Cookie {
	// 可以添加多个cookie
	var cookies []*http.Cookie
	cookieList := strings.Split(cookieRaw, "; ")
	for _, item := range cookieList {
		keyValue := strings.Split(item, "=")
		// fmt.Println(keyValue)
		name := keyValue[0]
		valueList := keyValue[1:]
		cookieItem := http.Cookie{
			Name:  name,
			Value: strings.Join(valueList, "="),
		}
		cookies = append(cookies, &cookieItem)
	}
	return cookies
}

func splitName(sourceName string) (string, string) {

	index := strings.Index(sourceName, " ")
	if index <= 0 || index == len(sourceName)-1 {
		return sourceName, sourceName
	}

	return sourceName[:index], sourceName[index+1:]

}

func splitCode(sourceName string) string {

	code, _ := splitName(sourceName)
	return code

}
