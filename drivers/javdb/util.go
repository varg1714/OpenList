package javdb

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
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

func (d *Javdb) getStars() []model.EmbyFileObj {
	films, err := virtual_file.ListMediaFiles(d.ID, DriverName, "个人收藏")
	if err != nil {
		utils.Log.Warnf("failed to list JavDB favorite works: %s", err)
		return nil
	}
	return films
}

var searchJavdbFilms = func(d *Javdb, code string) ([]model.EmbyFileObj, error) {
	films, _, err := d.getJavPageInfo(func(index int) string {
		return fmt.Sprintf("https://javdb.com/search?f=download&q=%s", code)
	}, 1)
	return films, err
}

func (d *Javdb) addStar(code string, tags []string) (model.EmbyFileObj, error) {
	canonical, err := model.NormalizeMediaCode(DriverName, code)
	if err != nil {
		return model.EmbyFileObj{}, err
	}
	if existing, findErr := db.GetFilmWorkByIdentity(d.ID, DriverName, canonical); findErr == nil {
		mergedTags := mergeTags(existing.Tags, tags)
		if len(mergedTags) != len(existing.Tags) {
			if err := db.MergePendingMediaWorkTags(existing.ID, mergedTags); err != nil {
				return model.EmbyFileObj{}, err
			}
			existing.Tags = mergedTags
		}
		if existing.SourceURL == "" {
			existing = resolveExistingStar(d, existing, code)
		}
		file, err := db.EnsureSingleFilmFile(existing.ID)
		if err != nil {
			return model.EmbyFileObj{}, err
		}
		return virtual_file.ConvertMediaFileToEmbyFile(model.FilmFileWithWork{FilmFile: file, Work: existing})
	}

	javFilms, err := searchJavdbFilms(d, code)
	if err != nil {
		utils.Log.Info("jav影片查询失败:", err)
		if isTransientJavdbSearchError(err) {
			return persistUnresolvedStar(d, canonical, tags)
		}
		return model.EmbyFileObj{}, err
	}

	if !javdbSearchMatchesCode(canonical, javFilms) {
		return model.EmbyFileObj{}, errors.New(fmt.Sprintf("影片:%s未查询到", code))
	}

	return persistDiscoveredStar(d, canonical, tags, javFilms[0])

}

func resolveExistingStar(d *Javdb, existing model.FilmWork, code string) model.FilmWork {
	films, err := searchJavdbFilms(d, code)
	if err != nil || !javdbSearchMatchesCode(existing.Code, films) {
		return existing
	}
	discovered, err := buildDiscoveredWork(existing.StorageID, existing.PrimaryDir, films[0])
	if err != nil {
		return existing
	}
	discovered.Tags = existing.Tags
	if err := db.UpsertDiscoveredWork(&discovered); err != nil {
		utils.Log.Warnf("failed to resolve JavDB star %s: %s", existing.Code, err)
		return existing
	}
	stored, err := db.GetFilmWork(existing.ID)
	if err != nil {
		return existing
	}
	return stored
}

func javdbSearchMatchesCode(code string, films []model.EmbyFileObj) bool {
	return len(films) > 0 && strings.EqualFold(code, splitCode(films[0].Name))
}

func persistDiscoveredStar(d *Javdb, canonical string, tags []string, film model.EmbyFileObj) (model.EmbyFileObj, error) {
	work, err := buildDiscoveredWork(d.ID, "个人收藏", film)
	if err != nil {
		return model.EmbyFileObj{}, err
	}
	work.Code = canonical
	work.Tags = mergeTags(nil, tags)
	return persistStarWork(work)
}

func persistUnresolvedStar(d *Javdb, canonical string, tags []string) (model.EmbyFileObj, error) {
	utils.Log.Warnf("persisting unresolved JavDB star %s after search failure", canonical)
	return persistStarWork(model.FilmWork{
		StorageID: d.ID, Source: DriverName, Code: canonical,
		SourceRef: canonical, PrimaryDir: "个人收藏", Tags: mergeTags(nil, tags),
	})
}

func persistStarWork(work model.FilmWork) (model.EmbyFileObj, error) {
	if err := db.UpsertDiscoveredWork(&work); err != nil {
		return model.EmbyFileObj{}, err
	}
	file, err := db.EnsureSingleFilmFile(work.ID)
	if err != nil {
		return model.EmbyFileObj{}, err
	}
	return virtual_file.ConvertMediaFileToEmbyFile(model.FilmFileWithWork{FilmFile: file, Work: work})
}

func isTransientJavdbSearchError(err error) bool {
	if err == nil {
		return false
	}
	switch err.Error() {
	case http.StatusText(http.StatusForbidden),
		http.StatusText(http.StatusUnauthorized),
		http.StatusText(http.StatusTooManyRequests),
		http.StatusText(http.StatusBadGateway),
		http.StatusText(http.StatusServiceUnavailable),
		http.StatusText(http.StatusGatewayTimeout):
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "timeout") || strings.Contains(msg, "connection")
}

func mergeTags(existing model.StringArray, incoming []string) model.StringArray {
	merged := append(model.StringArray(nil), existing...)
	seen := make(map[string]bool, len(merged)+len(incoming))
	for _, tag := range merged {
		seen[tag] = true
	}
	for _, tag := range incoming {
		if tag != "" && !seen[tag] {
			merged = append(merged, tag)
			seen[tag] = true
		}
	}
	return merged
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
