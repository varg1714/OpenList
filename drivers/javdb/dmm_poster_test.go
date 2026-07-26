package javdb

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/go-resty/resty/v2"
	"github.com/stretchr/testify/require"
)

func TestDMMPosterCIDAndCandidateOrder(t *testing.T) {
	cid, err := dmmPosterCID("MIDV-169")
	if err != nil || cid != "midv00169" {
		t.Fatalf("CID = %q, error = %v", cid, err)
	}
	want := []string{
		"https://awsimgsrc.dmm.co.jp/pics_dig/digital/video/1midv00169/1midv00169ps.jpg",
		"https://awsimgsrc.dmm.co.jp/pics_dig/digital/video/midv00169/midv00169ps.jpg",
	}
	if got := dmmPosterCandidates(cid); !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
	monoCID, err := dmmMonoPosterCID("ABF-007")
	if err != nil || monoCID != "abf007" {
		t.Fatalf("mono CID = %q, error = %v", monoCID, err)
	}
	for _, invalid := range []string{"MIDV", "MIDV-ABC", "MIDV-169.mp4", "MIDV-169 title.mp4"} {
		if _, err := dmmPosterCID(invalid); err == nil {
			t.Errorf("dmmPosterCID(%q) error = nil", invalid)
		}
	}
}

func TestDMMPosterSearchImageURL(t *testing.T) {
	html := `<div class="border-b border-dotted border-gray-300 extra-class"><img src="https://pics.dmm.co.jp/mono/movie/adult/1abf007/1abf007ps.jpg?cache=1"></div>`
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(html))
	}))
	defer server.Close()

	got, err := newMediaJobDriver(t, server).fetchDmmPosterSearchImageURL("ABF-007")
	if err != nil {
		t.Fatal(err)
	}
	want := "https://pics.dmm.co.jp/mono/movie/adult/1abf007/1abf007pl.jpg?cache=1"
	if got != want {
		t.Fatalf("search image URL = %q, want %q", got, want)
	}
}

func TestCropDMMMonoPoster(t *testing.T) {
	if _, err := cropDMMMonoPoster(bytes.Repeat([]byte{1}, minDMMMonoPosterBytes-1)); err == nil {
		t.Fatal("small response body accepted")
	}
	cropped, err := cropDMMMonoPoster(segmentedCompositeJPEG(t, 541))
	if err != nil {
		t.Fatal(err)
	}
	croppedImage, format, err := image.Decode(bytes.NewReader(cropped))
	if err != nil {
		t.Fatal(err)
	}
	if format != "jpeg" || croppedImage.Bounds().Dx() != 380 || croppedImage.Bounds().Dy() != 541 {
		t.Fatalf("cropped image = %s %dx%d", format, croppedImage.Bounds().Dx(), croppedImage.Bounds().Dy())
	}
}

func TestDownloadDMMMonoPosterTreatsNoImageRedirectAsDefinitiveMiss(t *testing.T) {
	// Given
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, "https://pics.dmm.com/mono/noimage/movie/adult_pl.jpg", http.StatusFound)
	}))
	defer server.Close()
	driver := newMediaJobDriver(t, server)
	driver.client.SetRedirectPolicy(resty.NoRedirectPolicy())

	// When
	content, definitiveMiss, err := driver.downloadDMMMonoPoster(context.Background(), "https://pics.dmm.co.jp/mono/movie/adult/1suke089/1suke089pl.jpg")

	// Then
	require.Error(t, err)
	require.Empty(t, content)
	require.True(t, definitiveMiss)
}

func TestDownloadDMMMonoPosterTreatsOtherRedirectAsTransientError(t *testing.T) {
	// Given
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, "https://pics.dmm.com/mono/movie/adult/other.jpg", http.StatusFound)
	}))
	defer server.Close()
	driver := newMediaJobDriver(t, server)
	driver.client.SetRedirectPolicy(resty.NoRedirectPolicy())

	// When
	content, definitiveMiss, err := driver.downloadDMMMonoPoster(context.Background(), "https://pics.dmm.co.jp/mono/movie/adult/1suke089/1suke089pl.jpg")

	// Then
	require.Error(t, err)
	require.Empty(t, content)
	require.False(t, definitiveMiss)
}

func segmentedCompositeJPEG(t *testing.T, height int) []byte {
	t.Helper()
	frame := image.NewRGBA(image.Rect(0, 0, 800, height))
	for y := range height {
		for x := range 800 {
			pixel := color.RGBA{R: 255, A: 255}
			if x >= 420 {
				pixel = color.RGBA{B: 255, A: 255}
			}
			frame.SetRGBA(x, y, pixel)
		}
	}
	var output bytes.Buffer
	if err := jpeg.Encode(&output, frame, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	if output.Len() < minDMMMonoPosterBytes {
		output.Write(make([]byte, minDMMMonoPosterBytes-output.Len()))
	}
	return output.Bytes()
}
