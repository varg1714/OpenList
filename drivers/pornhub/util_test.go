package pornhub

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/robertkrimen/otto"
	"github.com/tebeka/selenium"
)

type unavailableActorPage struct {
	navigatedURLs []string
}

type emptyActorFilmList struct {
	selenium.WebElement
}

func (emptyActorFilmList) FindElements(_, _ string) ([]selenium.WebElement, error) {
	return nil, nil
}

type emptyActorPage struct {
	unavailableActorPage
}

func (p *emptyActorPage) FindElement(_, value string) (selenium.WebElement, error) {
	if value == ".videoUList" {
		return emptyActorFilmList{}, nil
	}
	return p.unavailableActorPage.FindElement("", value)
}

func (p *unavailableActorPage) Get(pageURL string) error {
	p.navigatedURLs = append(p.navigatedURLs, pageURL)
	return errors.New("unexpected page navigation")
}

func (p *unavailableActorPage) FindElement(_, value string) (selenium.WebElement, error) {
	if value == ".page_next.omega" {
		return nil, nil
	}
	return nil, errors.New("no such element")
}

func TestGetVideoLinkUsesCallerContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := (&Pornhub{Addition: Addition{ServerUrl: "https://example.test"}}).getVideoLink(ctx, "view-key")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("getVideoLink error = %v, want context.Canceled", err)
	}
}

func TestRunPornhubScriptUsesCallerContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	err := runPornhubScript(ctx, otto.New(), `for (;;) {}`)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("runPornhubScript error = %v, want context.DeadlineExceeded", err)
	}
}

func TestResolveActorFilmsStopsWhenActorPageUnavailable(t *testing.T) {
	// Given
	page := &unavailableActorPage{}
	driver := &Pornhub{Addition: Addition{ServerUrl: "https://example.test"}}

	// When
	films := driver.resolveActorFilms(page, "/pornstar/deleted/videos/upload")

	// Then
	if len(films) != 0 {
		t.Fatalf("resolveActorFilms returned %d films, want 0", len(films))
	}
	if len(page.navigatedURLs) != 0 {
		t.Fatalf("resolveActorFilms navigated to %d additional pages, want 0", len(page.navigatedURLs))
	}
}

func TestResolveActorFilmsStopsWhenActorPageEmpty(t *testing.T) {
	// Given
	page := &emptyActorPage{}
	driver := &Pornhub{Addition: Addition{ServerUrl: "https://example.test"}}

	// When
	films := driver.resolveActorFilms(page, "/pornstar/deleted/videos/upload")

	// Then
	if len(films) != 0 {
		t.Fatalf("resolveActorFilms returned %d films, want 0", len(films))
	}
	if len(page.navigatedURLs) != 0 {
		t.Fatalf("resolveActorFilms navigated to %d additional pages, want 0", len(page.navigatedURLs))
	}
}

func TestGetFilmsBacksOffAfterUnavailableActorPage(t *testing.T) {
	// Given
	oldFetch := fetchPornhubActorFilms
	t.Cleanup(func() { fetchPornhubActorFilms = oldFetch })
	fetchCalls := 0
	fetchPornhubActorFilms = func(*Pornhub, string) ([]PornFilm, error) {
		fetchCalls++
		return nil, nil
	}
	driver := &Pornhub{Storage: model.Storage{ID: 987654}}
	pageKey := "/pornstar/deleted/videos/upload"
	retryKey := actorPageRetryKey(driver.ID, pageKey)
	unavailableActorPages.Del(retryKey)
	t.Cleanup(func() { unavailableActorPages.Del(retryKey) })

	// When
	first, firstErr := driver.getFilms("deleted-actor", pageKey)
	second, secondErr := driver.getFilms("deleted-actor", pageKey)

	// Then
	if firstErr != nil || secondErr != nil {
		t.Fatalf("getFilms errors = (%v, %v), want nil", firstErr, secondErr)
	}
	if len(first) != 0 || len(second) != 0 {
		t.Fatalf("getFilms lengths = (%d, %d), want (0, 0)", len(first), len(second))
	}
	if fetchCalls != 1 {
		t.Fatalf("actor page fetches = %d, want 1", fetchCalls)
	}
}
