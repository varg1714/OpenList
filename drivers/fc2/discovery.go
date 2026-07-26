package fc2

import (
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

var (
	fetchFC2DailyPageFilms = func(driver *FC2, url string) ([]string, error) {
		return driver.getFc2DailyPageFilms(url)
	}
	addFC2Star = func(driver *FC2, code string, tags []string) (model.EmbyFileObj, error) {
		return driver.addStar(code, tags)
	}
)

func (d *FC2) getFilms(_ string, urlFunc func(index int) string) ([]model.EmbyFileObj, error) {
	filmIDs := make([]string, 0)
	seen := make(map[string]struct{})
	for page := 1; ; page++ {
		ids, err := fetchFC2DailyPageFilms(d, urlFunc(page))
		if err != nil {
			if !strings.Contains(err.Error(), "Not Found") {
				utils.Log.Warnf("影片爬取失败: %s", err)
			}
			break
		}
		added := 0
		for _, id := range ids {
			if _, exists := seen[id]; exists {
				continue
			}
			seen[id] = struct{}{}
			filmIDs = append(filmIDs, id)
			added++
		}
		if added == 0 {
			break
		}
	}

	for _, id := range db.QueryUnMissedFilms(filmIDs) {
		if _, err := addFC2Star(d, id, nil); err != nil {
			utils.Log.Warnf("failed to add FC2 discovery %q: %s", id, err)
		}
		time.Sleep(time.Duration(d.ScanTimeLimit) * time.Second)
	}
	return []model.EmbyFileObj{}, nil
}

func buildDiscoveredWork(storageID uint, primaryDir, code, sourceURL, rawTitle, imageURL string) (model.FilmWork, error) {
	canonical, err := model.NormalizeMediaCode("fc2", code)
	if err != nil {
		return model.FilmWork{}, err
	}
	return model.FilmWork{
		StorageID: storageID, Source: "fc2", Code: canonical,
		SourceRef: canonical, SourceURL: sourceURL, PrimaryDir: primaryDir,
		RawTitle: rawTitle, ImageURL: imageURL,
	}, nil
}
