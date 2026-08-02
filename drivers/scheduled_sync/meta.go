package scheduled_sync

import (
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
)

type Addition struct {
	RemotePath    string  `json:"remote_path" required:"true" help:"the mount path of the downstream storage"`
	SyncCronExpr  string  `json:"sync_cron_expr" required:"true" help:"cron expression for scheduled scan, e.g. 0 3 * * *"`
	SyncPaths     string  `json:"sync_paths" type:"text" help:"directories to scan (downstream actual paths, one per line or comma separated); empty = walk from downstream root"`
	Refresh       bool    `json:"refresh" default:"true" help:"pass Refresh=true to downstream List calls; for Cache downstream this force-refreshes cache rows"`
	ListRateLimit float64 `json:"list_rate_limit" type:"number" default:"0" help:"max List requests per second to the downstream during a scan, 0 = unlimited"`
}

var config = driver.Config{
	Name:        "ScheduledSync",
	NoUpload:    true,
	DefaultRoot: "/",
}

func init() {
	op.RegisterDriver(func() driver.Driver {
		return &ScheduledSync{
			Addition: Addition{Refresh: true},
		}
	})
}
