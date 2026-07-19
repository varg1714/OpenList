package main

import (
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestRepairRootPromotesFirstLandscapeByNumericOrder(t *testing.T) {
	root := t.TempDir()
	leaf := filepath.Join(root, "actor", "movie")
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestJPEG(t, filepath.Join(leaf, "fanart1.jpg"), 400, 600, color.RGBA{R: 255, A: 255})
	writeTestJPEG(t, filepath.Join(leaf, "fanart2.jpg"), 400, 600, color.RGBA{G: 255, A: 255})
	writeTestJPEG(t, filepath.Join(leaf, "fanart3.jpg"), 600, 400, color.RGBA{B: 255, A: 255})
	writeTestJPEG(t, filepath.Join(leaf, "fanart10.jpg"), 800, 450, color.RGBA{R: 255, G: 255, A: 255})

	result, err := repairRoot(root, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if result.Swapped != 1 {
		t.Fatalf("swapped = %d, want 1", result.Swapped)
	}
	assertTestDimensions(t, filepath.Join(leaf, "fanart1.jpg"), 600, 400)
	assertTestDimensions(t, filepath.Join(leaf, "fanart3.jpg"), 400, 600)
	assertTestDimensions(t, filepath.Join(leaf, "fanart10.jpg"), 800, 450)
}

func TestRepairRootSkipsNonLeafDirectory(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "actor")
	child := filepath.Join(parent, "movie")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestJPEG(t, filepath.Join(parent, "fanart1.jpg"), 400, 600, color.RGBA{R: 255, A: 255})
	writeTestJPEG(t, filepath.Join(parent, "fanart2.jpg"), 600, 400, color.RGBA{B: 255, A: 255})

	result, err := repairRoot(root, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if result.Swapped != 0 {
		t.Fatalf("swapped = %d, want 0", result.Swapped)
	}
	assertTestDimensions(t, filepath.Join(parent, "fanart1.jpg"), 400, 600)
}

func writeTestJPEG(t *testing.T, path string, width, height int, fill color.RGBA) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetRGBA(x, y, fill)
		}
	}
	if err := jpeg.Encode(file, img, &jpeg.Options{Quality: 90}); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func assertTestDimensions(t *testing.T, path string, wantWidth, wantHeight int) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	config, _, err := image.DecodeConfig(file)
	if err != nil {
		t.Fatal(err)
	}
	if config.Width != wantWidth || config.Height != wantHeight {
		t.Fatalf("dimensions at %s = %dx%d, want %dx%d", path, config.Width, config.Height, wantWidth, wantHeight)
	}
}
