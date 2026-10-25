// Package retention periodically removes expired data from the monitoring
// store.
package retention

import (
	"context"
	"log/slog"
	"time"

	"github.com/locklens/locklens/internal/repository"
)

// Run applies the policy every interval until ctx is cancelled. The first
// run happens one minute after start so startup is not slowed down.
func Run(ctx context.Context, store *repository.Store, p repository.RetentionPolicy, every time.Duration, log *slog.Logger) {
	timer := time.NewTimer(time.Minute)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		start := time.Now()
		res, err := store.ApplyRetention(ctx, p, time.Now())
		var total int64
		for _, n := range res {
			total += n
		}
		if err != nil && ctx.Err() == nil {
			log.Warn("retention cleanup failed", "error", err.Error(), "deleted_rows", total)
		} else if total > 0 {
			log.Info("retention cleanup", "deleted_rows", total, "duration", time.Since(start).String())
		}
		timer.Reset(every)
	}
}
