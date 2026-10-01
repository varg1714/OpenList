package pikpak_share

import (
	"regexp"
	"strings"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"
)

// share links users paste into the "new directory" dialog.
// shareId == "" means the link must not be parsed.
var shareLinkCases = []struct {
	name    string
	link    string
	shareId string
}{
	{
		name:    "plain share link",
		link:    "https://mypikpak.com/s/shareidxx",
		shareId: "shareidxx",
	},
	{
		name:    "trailing path segment is ignored",
		link:    "https://mypikpak.com/s/shareidxx/folderidxx",
		shareId: "shareidxx",
	},
	{
		name:    "share id with dash, underscore and digits",
		link:    "https://mypikpak.com/s/testShareId-123_x",
		shareId: "testShareId-123_x",
	},
	{
		name:    "share link with view parameters",
		link:    "https://mypikpak.com/s/shareidxx?view=list&order=size_desc",
		shareId: "shareidxx",
	},
	{
		name:    "http scheme and subdomain",
		link:    "http://app.mypikpak.com/s/shareidxx",
		shareId: "shareidxx",
	},
	{
		name:    "net domain",
		link:    "https://mypikpak.net/s/shareidxx",
		shareId: "shareidxx",
	},
	{
		name: "drive path is not a share link",
		link: "https://mypikpak.com/drive/all",
	},
	{
		name: "other host",
		link: "https://example.com/s/shareidxx",
	},
	{
		name: "lookalike host",
		link: "https://evilmypikpak.com/s/shareidxx",
	},
	{
		name: "115 share link",
		link: "https://115.com/s/fake115share?password=abcd",
	},
}

func defaultPattern(t *testing.T) string {
	t.Helper()
	cfg := (&PikPakShare{}).Config()
	if cfg.LinkParse == nil {
		t.Fatal("PikPakShare must declare a default link parse pattern")
	}
	return cfg.LinkParse.Pattern
}

func TestDefaultLinkParsePatternExtractsShareId(t *testing.T) {
	re := regexp.MustCompile(defaultPattern(t))
	index := -1
	for i, name := range re.SubexpNames() {
		if name == "shareId" {
			index = i
		}
	}
	if index < 0 {
		t.Fatal("pattern must have a named capture group 'shareId'")
	}
	for _, c := range shareLinkCases {
		t.Run(c.name, func(t *testing.T) {
			match := re.FindStringSubmatch(c.link)
			if c.shareId == "" {
				if match != nil {
					t.Fatalf("link %q must not match, got %v", c.link, match)
				}
				return
			}
			if match == nil {
				t.Fatalf("link %q must match", c.link)
			}
			if got := match[index]; got != c.shareId {
				t.Fatalf("shareId = %q, want %q", got, c.shareId)
			}
		})
	}
}

// the frontend fills the directory form by matching capture group names against
// the mkdir config item names, unknown groups are silently dropped
func TestLinkParseGroupNamesAreMkdirConfigFields(t *testing.T) {
	re := regexp.MustCompile(defaultPattern(t))
	fields := make(map[string]bool)
	for _, item := range virtual_file.GetMkdirConfig() {
		fields[item.Name] = true
	}
	for i, name := range re.SubexpNames() {
		if i == 0 || name == "" {
			continue
		}
		if !fields[name] {
			t.Errorf("capture group %q is not a mkdir config field, the frontend would ignore it", name)
		}
	}
}

// the frontend builds the pattern with new RegExp(), Go specific syntax breaks it
func TestDefaultLinkParsePatternUsesJSSyntax(t *testing.T) {
	pattern := defaultPattern(t)
	for _, goOnly := range []string{"(?P<", "(?i", "(?m", "(?s", "(?U"} {
		if strings.Contains(pattern, goOnly) {
			t.Errorf("pattern contains Go only syntax %q: %s", goOnly, pattern)
		}
	}
}

func TestLinkParseAdditionOverridesDefaultPattern(t *testing.T) {
	const custom = `^https://custom\.example/s/(?<shareId>.+)$`
	re := regexp.MustCompile(custom)

	d := &PikPakShare{}
	d.LinkParse = custom

	cfg := d.Config()
	if cfg.LinkParse == nil {
		t.Fatal("Config() must return the custom link parse pattern")
	}
	if cfg.LinkParse.Pattern != custom {
		t.Fatalf("pattern = %q, want the custom pattern", cfg.LinkParse.Pattern)
	}
	if !re.MatchString("https://custom.example/s/abc") {
		t.Fatal("sanity check of the custom pattern failed")
	}

	if other := (&PikPakShare{}).Config(); other.LinkParse != nil && other.LinkParse.Pattern == custom {
		t.Fatal("the custom pattern leaked into the package level default config")
	}
}
