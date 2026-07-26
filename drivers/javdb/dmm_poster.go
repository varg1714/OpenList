package javdb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

const (
	maxDMMPosterBytes     = 16 << 20
	minDMMMonoPosterBytes = 50 << 10
	maxDMMPosterDimension = 12000
	maxDMMPosterPixels    = 60_000_000
)

var (
	dmmFilmCodePattern       = regexp.MustCompile(`(?i)^([a-z0-9]+)-([0-9]+)$`)
	errDMMMonoPosterUnusable = errors.New("DMM mono poster is unusable")
)

func dmmPosterCID(code string) (string, error) {
	matches := dmmFilmCodePattern.FindStringSubmatch(code)
	if len(matches) != 3 {
		return "", fmt.Errorf("unsupported DMM film code: %q", code)
	}
	numeric := matches[2]
	if len(numeric) < 5 {
		numeric = strings.Repeat("0", 5-len(numeric)) + numeric
	}
	return strings.ToLower(matches[1]) + numeric, nil
}

func dmmMonoPosterCID(code string) (string, error) {
	matches := dmmFilmCodePattern.FindStringSubmatch(code)
	if len(matches) != 3 {
		return "", fmt.Errorf("unsupported DMM film code: %q", code)
	}
	return strings.ToLower(matches[1]) + matches[2], nil
}

func dmmPosterCandidates(cid string) []string {
	firstCID := "1" + cid
	const root = "https://awsimgsrc.dmm.co.jp/pics_dig/digital/video/"
	return []string{
		root + firstCID + "/" + firstCID + "ps.jpg",
		root + cid + "/" + cid + "ps.jpg",
	}
}

func dmmMonoPosterCandidates(cid string) []string {
	const root = "https://pics.dmm.co.jp/mono/movie/adult/"
	return []string{
		root + "118" + cid + "/118" + cid + "pl.jpg",
		root + "1" + cid + "/1" + cid + "pl.jpg",
	}
}

func isDMMNoImageRedirect(response *http.Response) bool {
	if response == nil || response.StatusCode < http.StatusMultipleChoices || response.StatusCode >= http.StatusBadRequest {
		return false
	}
	location, err := url.Parse(response.Header.Get("Location"))
	return err == nil && location.Scheme == "https" && location.Host == "pics.dmm.com" && location.Path == "/mono/noimage/movie/adult_pl.jpg"
}

func cropDMMMonoPoster(content []byte) ([]byte, error) {
	if len(content) < minDMMMonoPosterBytes {
		return nil, fmt.Errorf("%w: response is too small: %d bytes", errDMMMonoPosterUnusable, len(content))
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(content))
	if err != nil {
		return nil, fmt.Errorf("invalid DMM mono poster image: %w", err)
	}
	if config.Width != 800 {
		return nil, fmt.Errorf("%w: unexpected width: got %d, want 800", errDMMMonoPosterUnusable, config.Width)
	}
	if config.Height <= 0 || config.Height > maxDMMPosterDimension ||
		int64(config.Width)*int64(config.Height) > maxDMMPosterPixels {
		return nil, fmt.Errorf("%w: dimensions exceed limits: %dx%d", errDMMMonoPosterUnusable, config.Width, config.Height)
	}
	decoded, _, err := image.Decode(bytes.NewReader(content))
	if err != nil {
		return nil, fmt.Errorf("invalid DMM mono poster image data: %w", err)
	}
	bounds := decoded.Bounds()
	cropped := image.NewRGBA(image.Rect(0, 0, 380, bounds.Dy()))
	draw.Draw(cropped, cropped.Bounds(), decoded, image.Pt(bounds.Min.X+420, bounds.Min.Y), draw.Src)

	var output bytes.Buffer
	if err := jpeg.Encode(&output, cropped, &jpeg.Options{Quality: 95}); err != nil {
		return nil, fmt.Errorf("encode cropped DMM mono poster: %w", err)
	}
	return output.Bytes(), nil
}

func (d *Javdb) downloadDMMPoster(ctx context.Context, candidate string) ([]byte, bool, error) {
	client := d.client
	if client == nil {
		client = newSampleImageClient()
	}
	response, err := client.R().SetContext(ctx).SetDoNotParseResponse(true).Get(candidate)
	if err != nil {
		if response != nil && isDMMNoImageRedirect(response.RawResponse) {
			return nil, true, err
		}
		return nil, false, err
	}
	body := response.RawBody()
	defer body.Close()

	if response.StatusCode() != http.StatusOK {
		definitiveMiss := response.StatusCode() == http.StatusNotFound || response.StatusCode() == http.StatusGone
		return nil, definitiveMiss, fmt.Errorf("DMM poster request returned HTTP %d", response.StatusCode())
	}
	if response.RawResponse.ContentLength > maxDMMPosterBytes {
		return nil, false, fmt.Errorf("DMM poster exceeds maximum size of %d bytes", maxDMMPosterBytes)
	}
	content, err := io.ReadAll(io.LimitReader(body, maxDMMPosterBytes+1))
	if err != nil {
		return nil, false, err
	}
	if len(content) > maxDMMPosterBytes {
		return nil, false, fmt.Errorf("DMM poster exceeds maximum size of %d bytes", maxDMMPosterBytes)
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(content))
	if err != nil {
		return nil, false, fmt.Errorf("invalid DMM poster image: %w", err)
	}
	if config.Width <= 0 || config.Height <= 0 ||
		config.Width > maxDMMPosterDimension || config.Height > maxDMMPosterDimension ||
		int64(config.Width)*int64(config.Height) > maxDMMPosterPixels {
		return nil, false, fmt.Errorf("DMM poster dimensions exceed limits: %dx%d", config.Width, config.Height)
	}
	if _, _, err := image.Decode(bytes.NewReader(content)); err != nil {
		return nil, false, fmt.Errorf("invalid DMM poster image data: %w", err)
	}
	return content, false, nil
}

func (d *Javdb) downloadDMMMonoPoster(ctx context.Context, candidate string) ([]byte, bool, error) {
	client := d.client
	if client == nil {
		client = newSampleImageClient()
	}
	response, err := client.R().SetContext(ctx).SetDoNotParseResponse(true).Get(candidate)
	if err != nil {
		if response != nil && isDMMNoImageRedirect(response.RawResponse) {
			return nil, true, err
		}
		return nil, false, err
	}
	body := response.RawBody()
	defer body.Close()

	if response.StatusCode() != http.StatusOK {
		definitiveMiss := response.StatusCode() == http.StatusNotFound || response.StatusCode() == http.StatusGone
		return nil, definitiveMiss, fmt.Errorf("DMM mono poster request returned HTTP %d", response.StatusCode())
	}
	if response.RawResponse.ContentLength > maxDMMPosterBytes {
		return nil, false, fmt.Errorf("DMM mono poster exceeds maximum size of %d bytes", maxDMMPosterBytes)
	}
	content, err := io.ReadAll(io.LimitReader(body, maxDMMPosterBytes+1))
	if err != nil {
		return nil, false, err
	}
	if len(content) > maxDMMPosterBytes {
		return nil, false, fmt.Errorf("DMM mono poster exceeds maximum size of %d bytes", maxDMMPosterBytes)
	}
	cropped, err := cropDMMMonoPoster(content)
	if err != nil {
		return nil, errors.Is(err, errDMMMonoPosterUnusable), err
	}
	return cropped, false, nil
}
