package pornhub

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	gocache "github.com/OpenListTeam/go-cache"
)

const unavailableActorRetryInterval = 5 * time.Minute

var (
	unavailableActorPages  = gocache.NewMemCache(gocache.WithShards[struct{}](8))
	fetchPornhubActorFilms = func(driver *Pornhub, pageKey string) ([]PornFilm, error) {
		return driver.getActorFilms(pageKey)
	}
)

func (d *Pornhub) getFilms(dirName, pageKey string) ([]model.EmbyFileObj, error) {
	var films []PornFilm
	isPlaylist := strings.Contains(pageKey, "/playlist/")

	if isPlaylist {
		key := strings.ReplaceAll(pageKey, "/playlist/", "")
		playListFilms, err := d.getPlayListFilms(key)
		if err != nil {
			return virtual_file.ListMediaFiles(d.ID, DriverName, dirName)
		}
		films = playListFilms
	} else {
		if strings.Contains(pageKey, "/model/") {
			pageKey += "/videos"
		}

		retryKey := actorPageRetryKey(d.ID, pageKey)
		if _, unavailable := unavailableActorPages.Get(retryKey); unavailable {
			return virtual_file.ListMediaFiles(d.ID, DriverName, dirName)
		}

		actorFilms, err := fetchPornhubActorFilms(d, pageKey)
		if err != nil {
			unavailableActorPages.Set(retryKey, struct{}{}, gocache.WithEx[struct{}](unavailableActorRetryInterval))
			return virtual_file.ListMediaFiles(d.ID, DriverName, dirName)
		}
		films = actorFilms
		if len(films) == 0 {
			unavailableActorPages.Set(retryKey, struct{}{}, gocache.WithEx[struct{}](unavailableActorRetryInterval))
		}
	}
	if isPlaylist {
		for index := range films {
			films[index].Tags = append(films[index].Tags, dirName)
		}
	}

	if len(films) == 0 {
		return virtual_file.ListMediaFiles(d.ID, DriverName, dirName)
	}

	for _, film := range films {
		canonicalURL, err := canonicalVideoURL(d.ServerUrl, film.SourceURL)
		if err != nil {
			utils.Log.Warnf("failed to normalize Pornhub URL %q: %s", film.SourceURL, err)
			continue
		}
		film.SourceURL = canonicalURL
		work, err := buildDiscoveredWork(d.ID, dirName, film)
		if err != nil {
			utils.Log.Warnf("failed to normalize Pornhub discovery %q: %s", film.ViewKey, err)
			continue
		}
		if err := db.UpsertDiscoveredWork(&work); err != nil {
			utils.Log.Warnf("failed to upsert Pornhub work %s: %s", work.Code, err)
			continue
		}
		if _, err := db.EnsureSingleFilmFile(work.ID); err != nil {
			utils.Log.Warnf("failed to ensure Pornhub file %s: %s", work.Code, err)
			continue
		}
	}

	return virtual_file.ListMediaFiles(d.ID, DriverName, dirName)
}

func actorPageRetryKey(storageID uint, pageKey string) string {
	return fmt.Sprintf("%d:%s", storageID, pageKey)
}

func buildDiscoveredWork(storageID uint, primaryDir string, film PornFilm) (model.FilmWork, error) {
	code, err := model.NormalizeMediaCode(DriverName, film.ViewKey)
	if err != nil {
		return model.FilmWork{}, err
	}
	canonical, err := canonicalVideoURL("", film.SourceURL)
	if err != nil {
		return model.FilmWork{}, err
	}
	return model.FilmWork{
		StorageID: storageID, Source: DriverName, Code: code,
		SourceRef: code, SourceURL: canonical, PrimaryDir: primaryDir,
		RawTitle: film.Title, ImageURL: film.Image,
		Actors: func() model.StringArray {
			if strings.TrimSpace(film.Username) != "" {
				return model.StringArray{film.Username}
			}
			return model.StringArray{primaryDir}
		}(),
		Tags:        model.StringArray(film.Tags),
		ReleaseDate: time.Now(),
	}, nil
}

func canonicalVideoURL(base, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("missing Pornhub source URL")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid Pornhub source URL: %w", err)
	}
	if !parsed.IsAbs() {
		if base == "" {
			return "", fmt.Errorf("invalid Pornhub source URL: %q", raw)
		}
		root, err := url.Parse(strings.TrimRight(base, "/"))
		if err != nil || !root.IsAbs() || root.Host == "" {
			return "", fmt.Errorf("invalid Pornhub server URL: %q", base)
		}
		parsed = root.ResolveReference(parsed)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.Path == "" {
		return "", fmt.Errorf("invalid Pornhub source URL: %q", raw)
	}
	return parsed.String(), nil
}

func convertFilms(actor string, films []PornFilm) ([]model.EmbyFileObj, error) {
	return utils.SliceConvert(films, func(src PornFilm) (model.EmbyFileObj, error) {
		return model.EmbyFileObj{
			ObjThumb: model.ObjThumb{
				Object: model.Object{
					Name:     src.ViewKey,
					IsFolder: false,
					Size:     622857143,
					Modified: time.Now(),
				},
				Thumbnail: model.Thumbnail{Thumbnail: src.Image},
			},
			ReleaseTime: time.Now(),
			Code:        src.ViewKey,
			SourceRef:   src.ViewKey,
			SourceURL:   src.SourceURL,
			Url:         src.SourceURL,
			Actors: func() []string {
				if src.Username != "" {
					return []string{src.Username}
				}
				return []string{actor}
			}(),
			Title: src.Title,
			Tags:  append([]string(nil), src.Tags...),
		}, nil
	})
}
