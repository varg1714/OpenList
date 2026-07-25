package db

import (
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

func QueryUnMissedFilms(codes []string) []string {
	if len(codes) == 0 {
		return []string{}
	}
	var missed []string
	if err := db.Model(&model.MissedFilm{}).Where("code IN ?", codes).Pluck("code", &missed).Error; err != nil {
		utils.Log.Errorf("failed to query missed films: %s", err)
		return []string{}
	}
	blocked := make(map[string]struct{}, len(missed))
	for _, code := range missed {
		blocked[code] = struct{}{}
	}
	result := make([]string, 0, len(codes))
	for _, code := range codes {
		if _, exists := blocked[code]; !exists {
			result = append(result, code)
		}
	}
	return result
}

func CreateMissedFilms(codes []string) error {
	return errors.WithStack(db.Transaction(func(tx *gorm.DB) error {
		for _, code := range codes {
			missed := model.MissedFilm{Code: code}
			if err := tx.Where(model.MissedFilm{Code: code}).FirstOrCreate(&missed).Error; err != nil {
				return err
			}
		}
		return nil
	}))
}
