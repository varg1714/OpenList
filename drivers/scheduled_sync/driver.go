package scheduled_sync

import (
	"context"
	"strings"

	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/cron"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/pkg/errors"
	"golang.org/x/time/rate"
)

type ScheduledSync struct {
	model.Storage
	Addition
	cron    *cron.Cron
	limiter *rate.Limiter
}

func (d *ScheduledSync) Config() driver.Config { return config }

func (d *ScheduledSync) GetAddition() driver.Additional { return &d.Addition }

func (d *ScheduledSync) Init(ctx context.Context) error {
	if strings.TrimSpace(d.RemotePath) == "" {
		return errors.New("remote path must not be empty")
	}
	d.RemotePath = utils.FixAndCleanPath(d.RemotePath)
	expr := strings.TrimSpace(d.SyncCronExpr)
	if expr == "" {
		return errors.New("sync_cron_expr must not be empty")
	}
	if d.cron != nil {
		d.cron.Stop()
		d.cron = nil
	}
	d.limiter = nil
	if d.ListRateLimit > 0 {
		// burst=1：严格按速率放行，每个 List 调用等待令牌
		d.limiter = rate.NewLimiter(rate.Limit(d.ListRateLimit), 1)
	}
	c, err := cron.NewCronExpr(expr)
	if err != nil {
		return errors.Wrapf(err, "scheduled_sync: invalid sync_cron_expr %q", utils.SanitizeHTML(expr))
	}
	d.cron = c
	d.cron.Do(d.scan)
	return nil
}

func (d *ScheduledSync) Drop(ctx context.Context) error {
	if d.cron != nil {
		d.cron.Stop()
		d.cron = nil
	}
	return nil
}

func (d *ScheduledSync) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {
	return nil, nil
}

func (d *ScheduledSync) Get(ctx context.Context, path string) (model.Obj, error) {
	return nil, errs.NotImplement
}

func (d *ScheduledSync) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
	return nil, errs.NotImplement
}

var _ driver.Driver = (*ScheduledSync)(nil)
