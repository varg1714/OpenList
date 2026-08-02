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

func NewCron(d time.Duration) *Cron {
	if d <= 0 {
		d = time.Second
	}
	parser := robfig.NewParser(robfig.SecondOptional | robfig.Minute | robfig.Hour |
		robfig.Dom | robfig.Month | robfig.Dow | robfig.Descriptor)
	return &Cron{
		expr:  "@every " + d.String(),
		s:     robfig.New(robfig.WithParser(parser)),
		sched: everySchedule{d},
	}
}

func NewCronExpr(expr string) (*Cron, error) {
	parser := robfig.NewParser(robfig.SecondOptional | robfig.Minute | robfig.Hour |
		robfig.Dom | robfig.Month | robfig.Dow | robfig.Descriptor)
	if _, err := parser.Parse(expr); err != nil {
		return nil, err
	}
	return &Cron{
		expr: expr,
		s:    robfig.New(robfig.WithParser(parser)),
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
