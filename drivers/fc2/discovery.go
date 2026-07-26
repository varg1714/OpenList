package fc2

import (
	"strings"

	"github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

var fetchFC2DailyPageFilms = func(driver *FC2, url string) ([]string, error) {
	return driver.getFc2DailyPageFilms(url)
}

func (d *FC2) getFilms(primaryDir string, urlFunc func(index int) string) ([]model.EmbyFileObj, error) {
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
		work, err := buildDiscoveredWork(d.ID, primaryDir, id, id, "", "")
		if err != nil {
			utils.Log.Warnf("failed to normalize FC2 discovery %q: %s", id, err)
			continue
		}
		if err := db.UpsertDiscoveredWork(&work); err != nil {
			utils.Log.Warnf("failed to persist FC2 discovery %s: %s", work.Code, err)
			continue
		}
		if _, err := db.EnsureSingleFilmFile(work.ID); err != nil {
			utils.Log.Warnf("failed to persist FC2 file %s: %s", work.Code, err)
		}
	}
	return virtual_file.ListMediaFiles(d.ID, "fc2", primaryDir)
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
