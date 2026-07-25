package javdb

import (
	"fmt"
	"net/url"
	"strings"
)

const (
	maxSampleImageCount          = 50
	maxSampleImageRequestsPerRun = 100
)

func sampleImageURL(imageURL string, index int) (string, error) {
	if index < 1 || index > maxSampleImageCount {
		return "", fmt.Errorf("unsupported sample image index: %d", index)
	}

	parsed, err := url.Parse(imageURL)
	if err != nil {
		return "", err
	}
	hostname := strings.ToLower(parsed.Hostname())
	trustedHost := hostname == "jdbstatic.com" || strings.HasSuffix(hostname, ".jdbstatic.com")
	if parsed.Scheme != "https" || !trustedHost || parsed.RawPath != "" || !strings.HasSuffix(parsed.Path, ".jpg") {
		return "", fmt.Errorf("unsupported cover URL: %s", imageURL)
	}

	segments := strings.Split(parsed.Path, "/")
	foundCovers := false
	for segmentIndex := range segments {
		if segments[segmentIndex] == "covers" {
			segments[segmentIndex] = "samples"
			foundCovers = true
			break
		}
	}
	if !foundCovers {
		return "", fmt.Errorf("unsupported cover URL: %s", imageURL)
	}

	parsed.Path = strings.TrimSuffix(strings.Join(segments, "/"), ".jpg") + fmt.Sprintf("_l_%d.jpg", index)
	parsed.RawPath = ""
	return parsed.String(), nil
}
