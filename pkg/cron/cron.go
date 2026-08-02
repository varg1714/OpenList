package cron

import (
	"sync"
	"time"

	robfig "github.com/robfig/cron/v3"
)

type Cron struct {
	expr    string
	s       *robfig.Cron
	entryID robfig.EntryID
	mu      sync.Mutex
	sched   robfig.Schedule
}

// everySchedule fires at fixed intervals without robfig's 1-second granularity.
type everySchedule struct{ d time.Duration }

func (e everySchedule) Next(t time.Time) time.Time { return t.Add(e.d) }

var parser = robfig.NewParser(robfig.SecondOptional | robfig.Minute | robfig.Hour |
	robfig.Dom | robfig.Month | robfig.Dow | robfig.Descriptor)

// chain serializes job runs and recovers panics, restoring the old ticker's
// non-overlapping execution and adding panic safety.
var chain = robfig.WithChain(
	robfig.SkipIfStillRunning(robfig.DefaultLogger),
	robfig.Recover(robfig.DefaultLogger),
)

// NewCron returns a Cron that invokes f at fixed intervals of d, starting when
// Do is called.
func NewCron(d time.Duration) *Cron {
	if d <= 0 {
		// Prevent a zero-interval busy loop: with d = 0, robfig would
		// compute Next(t) = t and spin forever.
		d = time.Second
	}
	return &Cron{
		expr:  "", // schedule path routes via c.sched, never AddFunc
		s:     robfig.New(robfig.WithParser(parser), chain),
		sched: everySchedule{d},
	}
}

// NewCronExpr returns a Cron that invokes f per the cron expression expr. Note
// that robfig's @every descriptor truncates sub-second durations to 1 second;
// use NewCron for sub-second intervals.
func NewCronExpr(expr string) (*Cron, error) {
	if _, err := parser.Parse(expr); err != nil {
		return nil, err
	}
	return &Cron{
		expr: expr,
		s:    robfig.New(robfig.WithParser(parser), chain),
	}, nil
}

func (c *Cron) Do(f func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entryID != 0 {
		return
	}
	if c.sched != nil {
		c.entryID = c.s.Schedule(c.sched, robfig.FuncJob(f))
	} else {
		id, err := c.s.AddFunc(c.expr, f)
		if err != nil {
			panic(err)
		}
		c.entryID = id
	}
	c.s.Start()
}

func (c *Cron) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.s.Remove(c.entryID)
	c.s.Stop()
}
