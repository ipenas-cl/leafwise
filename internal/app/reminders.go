package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ipenas-cl/leafwise/internal/domain"
	"github.com/ipenas-cl/leafwise/internal/ports"
)

// ReminderJob sends one reminder per (plant, task, due date).
type ReminderJob struct {
	Plants   *PlantService
	Log      ports.ReminderLog
	Notifier ports.Notifier
	Locker   ports.Locker
	Clock    ports.Clock
	Logger   *slog.Logger
	// OnRun is an optional hook for metrics.
	OnRun func(sent int, err error)
}

// RunOnce evaluates every plant and notifies about tasks due today or overdue.
func (j *ReminderJob) RunOnce(ctx context.Context) (int, error) {
	if j.Locker != nil {
		unlock, ok, err := j.Locker.TryLock(ctx, "leafwise-reminders")
		if err != nil {
			return 0, err
		}
		if !ok {
			j.Logger.DebugContext(ctx, "another replica holds the reminder lock")
			return 0, nil
		}
		defer unlock()
	}
	plants, err := j.Plants.List(ctx)
	if err != nil {
		return 0, err
	}
	today := domain.Day(j.Clock.Now())
	sent := 0
	for _, p := range plants {
		view, err := j.Plants.Plan(ctx, p)
		if err != nil {
			j.Logger.WarnContext(ctx, "plan failed", "plant", p.ID, "err", err)
			continue
		}
		for _, t := range []domain.CareTask{view.NextWater, view.NextFertilize} {
			if t.Due.After(today) {
				continue
			}
			first, err := j.Log.MarkSent(ctx, p.ID, t.Kind, t.Due)
			if err != nil {
				return sent, err
			}
			if !first {
				continue
			}
			verb := map[domain.TaskKind]string{domain.Water: "Water", domain.Fertilize: "Feed"}[t.Kind]
			n := ports.Notification{
				To: p.OwnerEmail, PlantID: p.ID,
				Subject: fmt.Sprintf("%s %s today", verb, p.Nickname),
				Body:    fmt.Sprintf("%s (%s, %s %s) is due: %s.", p.Nickname, view.Profile.CommonName, p.Exposure, p.Placement, t.Reason),
			}
			if err := j.Notifier.Notify(ctx, n); err != nil {
				j.Logger.WarnContext(ctx, "notify failed", "plant", p.ID, "err", err)
				continue
			}
			sent++
		}
	}
	return sent, nil
}

// Run loops until ctx is cancelled.
func (j *ReminderJob) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		n, err := j.RunOnce(ctx)
		if err != nil {
			j.Logger.ErrorContext(ctx, "reminder run failed", "err", err)
		} else if n > 0 {
			j.Logger.InfoContext(ctx, "reminders sent", "count", n)
		}
		if j.OnRun != nil {
			j.OnRun(n, err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
