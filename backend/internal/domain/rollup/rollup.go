// Package rollup keeps the downsampled metric history current: one-minute
// and one-hour rollups of every target's raw metric points, so long ranges
// read few rows and stay available after raw points expire.
package rollup

import (
	"context"
	"log/slog"
	"time"
)

// overlap is recomputed on every pass, so points written late (the write
// queue, a slow collector) are included.
const overlap = 3 * time.Minute

// backfillChunk bounds one rollup statement on the first pass.
const backfillChunk = 6 * time.Hour

// Run rolls up every target every interval until ctx is cancelled. The first
// pass backfills the raw history (at most rawRetention).
func Run(ctx context.Context, store Store, every, rawRetention time.Duration, log *slog.Logger) {
	timer := time.NewTimer(20 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		targets, err := store.ListTargets(ctx)
		if err != nil && ctx.Err() == nil {
			log.Warn("metric rollups: list targets failed", "error", err.Error())
		}
		for _, t := range targets {
			if t.DeletedAt != nil {
				continue
			}
			if err := Target(ctx, store, t.ID, time.Now().UTC(), rawRetention); err != nil && ctx.Err() == nil {
				log.Warn("metric rollups failed", "target_id", t.ID, "error", err.Error())
			}
		}
		timer.Reset(every)
	}
}

// Target brings one target's rollups up to now.
func Target(ctx context.Context, store Store, targetID int64, now time.Time, rawRetention time.Duration) error {
	end := now.Truncate(time.Minute).Add(time.Minute)
	last, ok, err := store.MetricRollupWatermark(ctx, targetID)
	if err != nil {
		return err
	}
	from := last.Add(-overlap)
	if !ok {
		from = now.Add(-rawRetention)
	}
	from = from.Truncate(time.Minute)
	for from.Before(end) {
		to := from.Add(backfillChunk)
		if to.After(end) {
			to = end
		}
		if err := store.RollupMetrics(ctx, targetID, from, to); err != nil {
			return err
		}
		from = to
	}
	return nil
}
