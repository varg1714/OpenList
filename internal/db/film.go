package db

import (
	"fmt"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

func CreateFilms(source string, actor, actorId string, models []model.EmbyFileObj) error {

	if len(models) == 0 {
		return nil
	}

	films := make([]model.Film, 0)

	for _, obj := range models {
		films = append(films, model.Film{
			Url:       obj.Url,
			Name:      obj.GetName(),
			Image:     obj.Thumb(),
			Source:    source,
			Actor:     actor,
			ActorId:   actorId,
			CreatedAt: obj.Modified,
			Date:      obj.ReleaseTime,
			Title:     obj.Title,
			Synopsis:  obj.Synopsis,
			Actors:    obj.Actors,
			Tags:      obj.Tags,
		})
	}

	return errors.WithStack(db.CreateInBatches(&films, 100).Error)

}

func QueryFilmsByActorAndNamePrefix(source, actor, namePrefix string) []model.Film {
	films := make([]model.Film, 0)
	db.Where("source = ?", source).
		Where("actor = ?", actor).
		Where("name LIKE ?", namePrefix+"%").
		Find(&films)
	return films
}

func QueryByActor(source string, actor string) []model.Film {

	films := make([]model.Film, 0)
	film := model.Film{
		Source: source,
		Actor:  actor,
	}

	db.Where(film).Order("created_at desc").Find(&films)

	return films

}

func QueryFilmByCode(source string, code string) (model.Film, error) {

	var film model.Film

	tx := db.Where("source = ? ", source).Where("name like ?", fmt.Sprintf("%s%%", code)).First(&film)

	return film, tx.Error

}

func QueryIncompleteFilms(source string, batchSize int) ([]model.Film, error) {

	films := make([]model.Film, 0)
	err := db.Where("source = ?", source).
		Where(`(date is null or (title is null or title = "") or (actors is null))`).
		Limit(batchSize).
		Find(&films).Error

	return films, err

}

func UpdateFilm(film model.Film) error {
	return db.Model(&film).Updates(model.Film{
		Date:         film.Date,
		Title:        film.Title,
		Synopsis:     film.Synopsis,
		Actors:       film.Actors,
		Tags:         film.Tags,
		SubtitleOnly: film.SubtitleOnly,
	}).Error
}

func UpdateFilmTags(id uint, tags model.StringArray, subtitleOnly bool) error {
	return db.Model(&model.Film{}).Where("id = ?", id).Updates(map[string]any{
		"tags":          tags,
		"subtitle_only": subtitleOnly,
	}).Error
}

// UpdateFilmActorsAndTags 更新影片的 actors/tags/subtitle_only 列。
// subtitle_only 始终写入（即使为 false），以修正 struct 形式 Updates 会跳过 bool 零值、
// 导致 subtitle_only 无法被重置回 false 的问题。actors/tags 仅在非空时写入，保持与旧
// struct 形式一致的零值跳过语义，避免把已有值覆盖为 NULL。不触碰 date/title/synopsis 列。
func UpdateFilmActorsAndTags(id uint, actors, tags model.StringArray, subtitleOnly bool) error {
	updates := map[string]any{
		"subtitle_only": subtitleOnly,
	}
	if len(actors) > 0 {
		updates["actors"] = actors
	}
	if len(tags) > 0 {
		updates["tags"] = tags
	}
	return db.Model(&model.Film{}).Where("id = ?", id).Updates(updates).Error
}

func QueryByUrls(actor string, urls []string) []string {

	films := make([]model.Film, 0)
	db.Select("url").Where("url IN (?)", urls).Where("actor_id = ?", actor).Find(&films)

	result := make([]string, 0)

	for _, film := range films {
		result = append(result, film.Url)
	}

	return result

}

func QueryFilmsByUrls(urls []string) ([]model.Film, error) {

	var res []model.Film
	tx := db.Where("url IN (?)", urls).Find(&res)

	return res, errors.WithStack(tx.Error)

}

func QueryFilmsByNamePrefix(source string, prefixes []string) ([]model.Film, error) {

	var films []model.Film

	nameQuery := db.Where("name like ?", prefixes[0]+"%")
	for _, p := range prefixes[1:] {
		nameQuery = nameQuery.Or("name like ?", p+"%")
	}

	query := db.Where(db.Where("source = ?", source)).Where(db.Where(nameQuery))

	return films, query.Find(&films).Error

}

func DeleteFilmsByActor(source string, actor string) error {

	return errors.WithStack(db.Where("source = ?", source).Where("actor = ?", actor).Delete(&model.Film{}).Error)

}

func DeleteFilmsByUrl(source, actor string, urls []string) error {

	return errors.WithStack(db.Where("source = ?", source).Where("actor = ?", actor).Where("url in ?", urls).Delete(&model.Film{}).Error)

}

func DeleteFilmById(id string) error {
	return errors.WithStack(db.Delete(&model.Film{}, id).Error)
}

func DeleteFilmsByCode(source, actor, code string) error {

	if code == "" {
		return nil
	}

	return errors.WithStack(db.Where("source = ?", source).
		Where("actor = ?", actor).
		Where("url like ?", fmt.Sprintf("%s%%", code)).
		Delete(&model.Film{}).Error)

}

func QueryUnSaveFilms(fileIds []string, dir string) []string {

	return queryNotExistDataWithCondition(fileIds, fmt.Sprintf("select url from x_films where actor = '%s'", dir))

}

func QueryNoMagnetFilms(fileIds []string) []string {

	return queryNotExistData(fileIds, "x_magnet_caches", "code")

}

func QueryUnMissedFilms(fileIds []string) []string {
	if len(fileIds) == 0 {
		return []string{}
	}
	var missed []string
	if err := db.Model(&model.MissedFilm{}).Where("code IN ?", fileIds).Pluck("code", &missed).Error; err != nil {
		utils.Log.Errorf("failed to query missed films: %s", err)
		return []string{}
	}
	blocked := make(map[string]struct{}, len(missed))
	for _, code := range missed {
		blocked[code] = struct{}{}
	}
	result := make([]string, 0, len(fileIds))
	for _, code := range fileIds {
		if _, exists := blocked[code]; !exists {
			result = append(result, code)
		}
	}
	return result
}

func CreateMissedFilms(fileIds []string) error {
	return errors.WithStack(db.Transaction(func(tx *gorm.DB) error {
		for _, fileID := range fileIds {
			missed := model.MissedFilm{Code: fileID}
			if err := tx.Where(model.MissedFilm{Code: fileID}).FirstOrCreate(&missed).Error; err != nil {
				return err
			}
		}
		return nil
	}))
}

func queryNotExistData(fileIds []string, dbName, columnName string) []string {
	return queryNotExistDataWithCondition(fileIds, fmt.Sprintf("select %s from %s", columnName, dbName))
}

func queryNotExistDataWithCondition(fileIds []string, sql string) []string {

	if len(fileIds) == 0 {
		return []string{}
	}

	var result []string
	var placeHolders []string
	var tempIds []any

	for _, fileId := range fileIds {
		placeHolders = append(placeHolders, "(?)")
		tempIds = append(tempIds, fileId)
	}

	query := fmt.Sprintf(`with temp(id) as (values %s)
select temp.id
from temp
where temp.id not in (%s);`, strings.Join(placeHolders, ","), sql)

	err := db.Raw(query, tempIds...).Scan(&result).Error
	if err != nil {
		utils.Log.Errorf("sql查询失败:%s", err.Error())
	}

	return result
}

func QueryNotMatchTagFilms(source string, url []string, tag string, limit int) ([]model.Film, error) {

	var result []model.Film

	tx := db.Where("source = ?", source).Where("tags is null or tags not like ?", fmt.Sprintf("%%%s%%", tag))
	if len(url) > 0 {
		tx = tx.Where("url in ?", url)
	}
	if limit > 0 {
		tx = tx.Limit(limit)
	}

	find := tx.Find(&result)
	return result, errors.WithStack(find.Error)

}

func QueryEmptySynopsisFilms(source string, scanInterval time.Duration, limit int) ([]model.Film, error) {

	var films []model.Film
	err := db.Where("source = ?", source).
		Where("(synopsis is null or synopsis = '')").
		Where("(synopsis_excluded = false or synopsis_excluded is null)").
		Where("(synopsis_scan_at is null or synopsis_scan_at < ?)", time.Now().Add(-scanInterval)).
		Order("date desc").
		Limit(limit).
		Find(&films).Error

	return films, errors.WithStack(err)

}

func UpdateFilmSynopsis(filmId uint, synopsis string) error {
	return db.Model(&model.Film{}).Where("id = ?", filmId).Updates(map[string]interface{}{
		"synopsis":         synopsis,
		"synopsis_scan_at": time.Now(),
	}).Error
}

func MarkSynopsisExcluded(filmId uint) error {
	return db.Model(&model.Film{}).Where("id = ?", filmId).Updates(map[string]interface{}{
		"synopsis_excluded": true,
		"synopsis_scan_at":  time.Now(),
	}).Error
}

func UpdateSynopsisScanAt(filmId uint) error {
	return db.Model(&model.Film{}).Where("id = ?", filmId).Update("synopsis_scan_at", time.Now()).Error
}

func QuerySampleImageFilms(source string, scanInterval time.Duration, limit int) ([]model.Film, error) {
	var films []model.Film
	err := db.Where("source = ?", source).
		Where("image is not null and image <> ''").
		Where("(sample_image_complete = false or sample_image_complete is null)").
		Where("(sample_image_scan_at is null or sample_image_scan_at < ?)", time.Now().Add(-scanInterval)).
		Order("date desc, id desc").
		Limit(limit).
		Find(&films).Error

	return films, errors.WithStack(err)
}

func QueryDMMPosterFilms(retryInterval time.Duration, limit int) ([]model.Film, error) {
	var films []model.Film
	query := db.Where("source = ?", "javdb").
		Where("(dmm_poster_status IS NULL OR dmm_poster_status = '' OR dmm_poster_status = ? OR (dmm_poster_status = ? AND (dmm_poster_scan_at IS NULL OR dmm_poster_scan_at < ?)))",
			model.DMMPosterStatusPending,
			model.DMMPosterStatusTransientError,
			time.Now().Add(-retryInterval),
		).
		Order("date desc, id desc")
	if limit > 0 {
		query = query.Limit(limit)
	}
	err := query.Find(&films).Error
	return films, errors.WithStack(err)
}

func UpdateDMMPosterStatus(filmID uint, status string) error {
	return db.Model(&model.Film{}).Where("id = ?", filmID).Updates(map[string]interface{}{
		"dmm_poster_status":  status,
		"dmm_poster_scan_at": time.Now(),
	}).Error
}

type FC2SampleImageGroup struct {
	Source           string
	Actor            string
	URL              string `gorm:"column:url"`
	SampleImageCount int
}

func QueryFC2SampleImageGroups(scanInterval time.Duration, limit int) ([]FC2SampleImageGroup, error) {
	var groups []FC2SampleImageGroup
	query := db.Model(&model.Film{}).
		Select("source, actor, url, MAX(COALESCE(sample_image_count, 0)) AS sample_image_count").
		Where("source = ?", "fc2").
		Where("url IS NOT NULL AND url <> ''").
		Group("source, actor, url").
		Having("SUM(CASE WHEN sample_image_complete = ? THEN 1 ELSE 0 END) = 0", true).
		Having("MAX(sample_image_scan_at) IS NULL OR MAX(sample_image_scan_at) < ?", time.Now().Add(-scanInterval)).
		Order("actor ASC, url ASC")
	if limit > 0 {
		query = query.Limit(limit)
	}

	return groups, errors.WithStack(query.Scan(&groups).Error)
}

func UpdateFC2SampleImageGroupProgress(actor, url string, count int, complete bool) error {
	updates := map[string]interface{}{
		"sample_image_count": gorm.Expr(
			"CASE WHEN COALESCE(sample_image_count, 0) < ? THEN ? ELSE COALESCE(sample_image_count, 0) END",
			count,
			count,
		),
	}
	if complete {
		updates["sample_image_complete"] = true
	}
	return db.Model(&model.Film{}).
		Where("source = ? AND actor = ? AND url = ?", "fc2", actor, url).
		Updates(updates).Error
}

func MarkFC2SampleImageGroupComplete(actor, url string) error {
	return db.Model(&model.Film{}).
		Where("source = ? AND actor = ? AND url = ?", "fc2", actor, url).
		Updates(map[string]interface{}{
			"sample_image_complete": true,
			"sample_image_scan_at":  time.Now(),
		}).Error
}

func UpdateFC2SampleImageGroupScanAt(actor, url string) error {
	return db.Model(&model.Film{}).
		Where("source = ? AND actor = ? AND url = ?", "fc2", actor, url).
		Update("sample_image_scan_at", time.Now()).Error
}

func UpdateSampleImageProgress(filmId uint, count int, complete bool) error {
	updates := map[string]interface{}{
		"sample_image_count": gorm.Expr(
			"CASE WHEN COALESCE(sample_image_count, 0) < ? THEN ? ELSE COALESCE(sample_image_count, 0) END",
			count,
			count,
		),
	}
	if complete {
		updates["sample_image_complete"] = true
	}
	return db.Model(&model.Film{}).Where("id = ?", filmId).Updates(updates).Error
}

func MarkSampleImageComplete(filmId uint) error {
	return db.Model(&model.Film{}).Where("id = ?", filmId).Updates(map[string]interface{}{
		"sample_image_complete": true,
		"sample_image_scan_at":  time.Now(),
	}).Error
}

func UpdateSampleImageScanAt(filmId uint) error {
	return db.Model(&model.Film{}).Where("id = ?", filmId).Update("sample_image_scan_at", time.Now()).Error
}

func QueryNoTagFilms(source string, limit int) ([]model.Film, error) {

	var result []model.Film

	tx := db.Where("source = ?", source).Where("(tags is null or tags = '[]' or subtitle_only = ?)", true).Where("name not like 'FC2-%'").Limit(limit).Find(&result)

	return result, errors.WithStack(tx.Error)

}
