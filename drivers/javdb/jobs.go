package javdb

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"
	"github.com/OpenListTeam/OpenList/v4/internal/av"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

func (d *Javdb) filterFilms() error {
	utils.Log.Info("start filtering JavDB films")
	defer utils.Log.Info("finish filtering JavDB films")

	prefixes := make([]string, 0)
	for _, raw := range strings.Split(d.Filter, ",") {
		prefix := strings.ToUpper(strings.TrimSpace(raw))
		if prefix != "" {
			prefixes = append(prefixes, prefix)
		}
	}
	works, err := db.QueryFilmWorksByCodePrefixes(d.ID, DriverName, prefixes)
	if err != nil {
		return fmt.Errorf("query normalized JavDB filter works: %w", err)
	}
	utils.Log.Infof("found %d JavDB works matching filter prefixes %v, ids to delete: %v", len(works), prefixes, mediaWorkIDs(works))
	deleteErrors := make([]error, 0)
	for _, work := range works {
		if err := virtual_file.DeleteMediaWork(work.ID); err != nil {
			deleteErrors = append(deleteErrors, fmt.Errorf("delete filtered work %s: %w", work.Code, err))
		}
	}
	return errors.Join(deleteErrors...)
}

var errUnresolvedJavdbSource = errors.New("unresolved javdb source")

func (d *Javdb) fetchJavTopFilms() {
	utils.Log.Infof("start to fetch javdb top films")
	defer utils.Log.Infof("finish fetch javdb top films")

	var missedFilms []string
	defer func() {
		if len(missedFilms) > 0 {
			if err := db.CreateMissedFilms(missedFilms); err != nil {
				utils.Log.Warn("failed to create missed films:", err.Error())
			}
		}
	}()

	year := time.Now().Year()
	for currentYear := d.MatchTopFilmsStarter; currentYear <= year; currentYear++ {
		codes := av.QueryJavSql(d.SpiderServer, fmt.Sprintf("SELECT SUBSTR(name, 0, 40) FROM ranks WHERE note = 'JavDB %d TOP250'", currentYear), d.SpiderMaxWaitTime)
		missed, err := d.addFavoriteFilms(codes, []string{fmt.Sprintf("JavDB-TOP250-%d", currentYear)})
		missedFilms = append(missedFilms, missed...)
		if err != nil {
			return
		}
	}

	codes := av.QueryJavSql(d.SpiderServer, "SELECT SUBSTR(name, 0, 40) FROM ranks WHERE note = 'JavDB TOP250'", d.SpiderMaxWaitTime)
	missed, _ := d.addFavoriteFilms(codes, []string{"JavDB-TOP250"})
	missedFilms = append(missedFilms, missed...)
}

func (d *Javdb) addFavoriteFilms(codes, tags []string) ([]string, error) {
	var missed []string
	for _, code := range db.QueryUnMissedFilms(codes) {
		if strings.HasPrefix(code, "FC2-") {
			continue
		}
		file, err := d.addStar(code, tags)
		if err != nil {
			if strings.Contains(err.Error(), "未查询到") {
				missed = append(missed, code)
				continue
			}
			utils.Log.Warnf("failed to add film for code: %s, error: %s", code, err.Error())
			return missed, err
		}
		if strings.TrimSpace(file.SourceURL) == "" {
			utils.Log.Warnf("unresolved JavDB source for %s, stopping batch add", code)
			return missed, errUnresolvedJavdbSource
		}
	}
	return missed, nil
}
