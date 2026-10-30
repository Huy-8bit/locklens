package repository

import (
	"context"
	"time"

	"github.com/locklens/locklens/internal/repository/queries"
)

// MetricRollupRepository is the store of one-minute and hourly metric rollups.
type MetricRollupRepository interface {
	// RollupMetrics recomputes the one-minute rollups of buckets starting in
	// [from, to) from raw points, then the hourly rollups of the hours those
	// buckets fall in. from must be a minute boundary; recomputing a range is
	// harmless.
	RollupMetrics(ctx context.Context, targetID int64, from, to time.Time) error
	// MetricRollupWatermark returns the newest one-minute rollup bucket of a
	// target, or ok = false when nothing was rolled up yet.
	MetricRollupWatermark(ctx context.Context, targetID int64) (time.Time, bool, error)
	// SeriesStep is the step a series of [from, ...) is returned with: the
	// requested step, or the rollup resolution when that is coarser.
	SeriesStep(from time.Time, step time.Duration) time.Duration
}

var _ MetricRollupRepository = (*Store)(nil)

// Metric rollup resolutions.
const (
	MinuteRollup = time.Minute
	HourRollup   = time.Hour
)

// RollupMetrics recomputes the one-minute rollups of buckets starting in
// [from, to) from raw points, then the hourly rollups of the hours those
// buckets fall in. from must be a minute boundary; recomputing a range is
// harmless.
func (s *Store) RollupMetrics(ctx context.Context, targetID int64, from, to time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return s.red.Error(err)
	}
	defer tx.Rollback(context.WithoutCancel(ctx)) //nolint:errcheck
	// A point at ts covers the interval ending at ts: it belongs to the
	// bucket its end falls in, as in charts.
	if _, err := tx.Exec(ctx, queries.RollupMetricsMinute, targetID, from.UTC(), to.UTC()); err != nil {
		return s.red.Error(err)
	}
	hfrom, hto := from.Truncate(time.Hour), to.Truncate(time.Hour).Add(time.Hour)
	if _, err := tx.Exec(ctx, queries.RollupMetricsHour, targetID, hfrom.UTC(), hto.UTC()); err != nil {
		return s.red.Error(err)
	}
	return s.red.Error(tx.Commit(ctx))
}

// MetricRollupWatermark returns the newest one-minute rollup bucket of a
// target, or ok = false when nothing was rolled up yet.
func (s *Store) MetricRollupWatermark(ctx context.Context, targetID int64) (time.Time, bool, error) {
	var t *time.Time
	if err := s.pool.QueryRow(ctx, queries.MetricRollupWatermark, targetID).Scan(&t); err != nil {
		return time.Time{}, false, s.red.Error(err)
	}
	if t == nil {
		return time.Time{}, false, nil
	}
	return t.UTC(), true, nil
}

// SetRawRetention tells series queries how far back raw points exist, so
// older ranges are read from rollups.
func (s *Store) SetRawRetention(d time.Duration) { s.rawRetention.Store(int64(d)) }

// seriesSource picks the table a series is read from: raw points for fine
// steps over the raw history, rollups for coarse steps or older ranges. The
// returned resolution is 0 for raw points.
func (s *Store) seriesSource(from time.Time, step time.Duration, now time.Time) time.Duration {
	raw := time.Duration(s.rawRetention.Load())
	beyondRaw := raw > 0 && from.Before(now.Add(-raw))
	switch {
	case step >= 3*time.Hour:
		return HourRollup
	case step >= 5*time.Minute, beyondRaw:
		return MinuteRollup
	}
	return 0
}

// SeriesStep is the step a series of [from, ...) is returned with: the
// requested step, or the rollup resolution when that is coarser.
func (s *Store) SeriesStep(from time.Time, step time.Duration) time.Duration {
	return max(step, s.seriesSource(from, step, time.Now()))
}
