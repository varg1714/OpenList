package main

import (
	"flag"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
)

var fanartNamePattern = regexp.MustCompile(`^fanart([1-9][0-9]*)\.jpg$`)

type repairResult struct {
	LeafDirectories  int
	Swapped          int
	AlreadyLandscape int
	NoLandscape      int
	Skipped          int
}

func main() {
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: %s ROOT_DIRECTORY\n", os.Args[0])
		fmt.Fprintln(flag.CommandLine.Output(), "Recursively repairs fanart1.jpg in leaf directories. Files are modified directly.")
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}

	result, err := repairRoot(flag.Arg(0), os.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fanart repair failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf(
		"done: leaf_directories=%d swapped=%d already_landscape=%d no_landscape=%d skipped=%d\n",
		result.LeafDirectories,
		result.Swapped,
		result.AlreadyLandscape,
		result.NoLandscape,
		result.Skipped,
	)
}

func repairRoot(root string, output io.Writer) (repairResult, error) {
	rootInfo, err := os.Stat(root)
	if err != nil {
		return repairResult{}, err
	}
	if !rootInfo.IsDir() {
		return repairResult{}, fmt.Errorf("root is not a directory: %s", root)
	}

	result := repairResult{}
	err = walkLeafDirectories(filepath.Clean(root), func(directory string) error {
		result.LeafDirectories++
		status, err := repairLeafDirectory(directory, output)
		if err != nil {
			return err
		}
		switch status {
		case "swapped":
			result.Swapped++
		case "already_landscape":
			result.AlreadyLandscape++
		case "no_landscape":
			result.NoLandscape++
		default:
			result.Skipped++
		}
		return nil
	})
	return result, err
}

func walkLeafDirectories(directory string, visit func(string) error) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	hasChildDirectory := false
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		hasChildDirectory = true
		if err := walkLeafDirectories(filepath.Join(directory, entry.Name()), visit); err != nil {
			return err
		}
	}
	if !hasChildDirectory {
		return visit(directory)
	}
	return nil
}

func repairLeafDirectory(directory string, output io.Writer) (string, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return "", err
	}
	type indexedFanart struct {
		index int
		path  string
	}
	fanarts := make([]indexedFanart, 0)
	for _, entry := range entries {
		matches := fanartNamePattern.FindStringSubmatch(entry.Name())
		if len(matches) != 2 {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		index, err := strconv.Atoi(matches[1])
		if err != nil {
			continue
		}
		fanarts = append(fanarts, indexedFanart{index: index, path: filepath.Join(directory, entry.Name())})
	}
	sort.Slice(fanarts, func(i, j int) bool { return fanarts[i].index < fanarts[j].index })
	if len(fanarts) == 0 || fanarts[0].index != 1 {
		return "skipped", nil
	}

	first := fanarts[0].path
	width, height, err := imageDimensions(first)
	if err != nil {
		fmt.Fprintf(output, "skip %s: cannot read fanart1 dimensions: %v\n", directory, err)
		return "skipped", nil
	}
	if width > height {
		return "already_landscape", nil
	}

	for _, candidate := range fanarts[1:] {
		width, height, err := imageDimensions(candidate.path)
		if err != nil || width <= height {
			continue
		}
		if err := swapFiles(first, candidate.path); err != nil {
			return "", fmt.Errorf("swap %s with %s: %w", first, candidate.path, err)
		}
		fmt.Fprintf(output, "swap %s <-> %s\n", first, candidate.path)
		return "swapped", nil
	}
	return "no_landscape", nil
}

func imageDimensions(path string) (int, int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()
	config, _, err := image.DecodeConfig(file)
	if err != nil {
		return 0, 0, err
	}
	return config.Width, config.Height, nil
}

func swapFiles(first, second string) error {
	temporary, err := os.CreateTemp(filepath.Dir(first), ".fanart-repair-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Remove(temporaryPath); err != nil {
		return err
	}
	if err := os.Rename(first, temporaryPath); err != nil {
		return err
	}
	if err := os.Rename(second, first); err != nil {
		_ = os.Rename(temporaryPath, first)
		return err
	}
	if err := os.Rename(temporaryPath, second); err != nil {
		_ = os.Rename(first, second)
		_ = os.Rename(temporaryPath, first)
		return err
	}
	return nil
}
