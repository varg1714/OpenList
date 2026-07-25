package fc2

import (
	"fmt"
	"net/url"
)

const (
	maxSampleImageCount          = 50
	maxSampleImageRequestsPerRun = 100
)

func validateScreenshotURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf("invalid screenshot URL: %q", rawURL)
	}
	return nil
}
