package cache

import (
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
)

type Addition struct {
	RemotePath        string `json:"remote_path" required:"true" help:"the mount path of the downstream storage"`
	TTLHours          int    `json:"ttl_hours" required:"true" type:"number" default:"24" help:"cache validity period in hours"`
	SyncIntervalHours int    `json:"sync_interval_hours" required:"true" type:"number" default:"1" help:"background sync interval in hours, 0 to disable"`
	SyncPaths         string `json:"sync_paths" type:"text" help:"directories to sync (downstream actual paths, one per line or comma separated); empty = sync all cached"`
}

var config = driver.Config{
	Name:        "Cache",
	LocalSort:   true,
	NoUpload:    true,
	DefaultRoot: "/",
}

func init() {
	op.RegisterDriver(func() driver.Driver {
		return &Cache{
			Addition: Addition{
				TTLHours:          24,
				SyncIntervalHours: 1,
			},
		}
	})
}
