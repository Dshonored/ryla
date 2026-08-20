// Package schedule declares this application's recurring work.
//
// Tasks live in code rather than a crontab, so they are reviewed, versioned and
// deployed with everything else — and a task calling a function that no longer
// exists fails to compile instead of failing quietly at three in the morning.
package schedule

import (
	"context"
	"time"

	"github.com/Dshonored/ryla/cache"
	"github.com/Dshonored/ryla/queue"
	ryschedule "github.com/Dshonored/ryla/schedule"

	"ryla-site/app"
)

// Register declares every scheduled task.
//
// Run them with `ry schedule:run`, either as a long-lived process or from a
// system cron with --once.
func Register(a *app.App, s *ryschedule.Scheduler) {
	// Housekeeping that ships by default. Both are cheap, and without them the
	// tables they clean grow without limit.
	s.Every(time.Hour, "prune-sessions", func(ctx context.Context) error {
		store, ok := a.Session.(interface{ Prune() (int64, error) })
		if !ok {
			return nil
		}
		_, err := store.Prune()
		return err
	})

	s.Every(time.Hour, "prune-cache", func(ctx context.Context) error {
		store, ok := a.Cache.(*cache.DB)
		if !ok {
			return nil
		}
		_, err := store.Prune(ctx)
		return err
	})

	// Your tasks go here:
	//
	//	s.Daily("03:00", "send-digest", func(ctx context.Context) error {
	//	    return a.Queue.Dispatch(ctx, &jobs.SendDigest{})
	//	})
	//
	//	s.Every(15*time.Minute, "sync-inventory", func(ctx context.Context) error {
	//	    return nil
	//	})
	_ = queue.Default
}
