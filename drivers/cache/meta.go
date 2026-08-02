package cache

import (
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
)

type Addition struct {
	RemotePath string `json:"remote_path" required:"true" help:"the mount path of the downstream storage"`
	TTLHours   int    `json:"ttl_hours" required:"true" type:"number" default:"24" help:"cache validity period in hours; scheduled scans re-fetch a directory only after it has expired"`
	SyncPaths  string `json:"sync_paths" type:"text" help:"directories to show when browsing (downstream actual paths, one per line or comma separated); empty = show all cached; scheduled scanning is handled by a ScheduledSync storage pointing at this one"`
}

var config = driver.Config{
	Name:        "Cache",
	LocalSort:   true,
	NoUpload:    true,
	DefaultRoot: "/",
}

func init() {
	op.RegisterDriver(func() driver.Driver {
		return &Cache{}
	})
}
