package repository

import (
	"context"
	"time"
)

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
	if _, err := tx.Exec(ctx, `
INSERT INTO metric_rollups_1m (target_id, metric, label, ts, samples, known, vsum, vmax, vlast, last_ts, wsum, wint, with_interval)
SELECT target_id, metric, label, date_bin('1 minute', ts - interval '1 microsecond', 'epoch'::timestamptz),
       count(*), count(value), sum(value), max(value), (array_agg(value ORDER BY ts DESC))[1], max(ts),
       sum(value * interval_seconds), sum(interval_seconds), count(interval_seconds)
FROM metric_points
WHERE target_id = $1 AND ts > $2 AND ts <= $3
GROUP BY target_id, metric, label, 4
ON CONFLICT (target_id, metric, label, ts) DO UPDATE SET
    samples = EXCLUDED.samples, known = EXCLUDED.known, vsum = EXCLUDED.vsum, vmax = EXCLUDED.vmax,
    vlast = EXCLUDED.vlast, last_ts = EXCLUDED.last_ts, wsum = EXCLUDED.wsum, wint = EXCLUDED.wint,
    with_interval = EXCLUDED.with_interval`, targetID, from.UTC(), to.UTC()); err != nil {
		return s.red.Error(err)
	}
	hfrom, hto := from.Truncate(time.Hour), to.Truncate(time.Hour).Add(time.Hour)
	if _, err := tx.Exec(ctx, `
INSERT INTO metric_rollups_1h (target_id, metric, label, ts, samples, known, vsum, vmax, vlast, last_ts, wsum, wint, with_interval)
SELECT target_id, metric, label, date_bin('1 hour', ts, 'epoch'::timestamptz),
       sum(samples), sum(known), sum(vsum), max(vmax), (array_agg(vlast ORDER BY last_ts DESC))[1], max(last_ts),
       sum(wsum), sum(wint), sum(with_interval)
FROM metric_rollups_1m
WHERE target_id = $1 AND ts >= $2 AND ts < $3
GROUP BY target_id, metric, label, 4
ON CONFLICT (target_id, metric, label, ts) DO UPDATE SET
    samples = EXCLUDED.samples, known = EXCLUDED.known, vsum = EXCLUDED.vsum, vmax = EXCLUDED.vmax,
    vlast = EXCLUDED.vlast, last_ts = EXCLUDED.last_ts, wsum = EXCLUDED.wsum, wint = EXCLUDED.wint,
    with_interval = EXCLUDED.with_interval`, targetID, hfrom.UTC(), hto.UTC()); err != nil {
		return s.red.Error(err)
	}
	return s.red.Error(tx.Commit(ctx))
}

// MetricRollupWatermark returns the newest one-minute rollup bucket of a
// target, or ok = false when nothing was rolled up yet.
func (s *Store) MetricRollupWatermark(ctx context.Context, targetID int64) (time.Time, bool, error) {
	var t *time.Time
	if err := s.pool.QueryRow(ctx, `SELECT max(ts) FROM metric_rollups_1m WHERE target_id = $1`, targetID).Scan(&t); err != nil {
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

// rollupExpression aggregates rollup rows like MetricAggregation aggregates
// raw points.
func rollupExpression(agg string) string {
	switch agg {
	case "sum":
		return "sum(vsum)"
	case "max":
		return "max(vmax)"
	case "mean_interval_ratio":
		return "sum(vsum) / NULLIF(sum(known), 0)"
	case "time_weighted_mean":
		return "CASE WHEN sum(with_interval) = sum(samples) THEN sum(wsum) / NULLIF(sum(wint), 0) END"
	}
	return "(array_agg(vlast ORDER BY last_ts DESC))[1]"
}

// rollupSeriesSQL reads one metric from a rollup table, completed with raw
// points after the newest rolled-up minute (rollups run every minute), all
// aggregated into the request's steps. Without rollups it reads raw points
// only. $1 target, $2 metric, $3 from, $4 to, $5 step, $6 collector, $7
// limit, $8 resolution.
func rollupSeriesSQL(table, expression string) string {
	return `
WITH cut AS (
 -- The end of the newest rolled-up minute; when minute rollups have expired,
 -- the end of the newest row of this table; without rollups, from.
 SELECT date_bin($8::interval, coalesce(
   (SELECT max(ts) + interval '1 minute' FROM metric_rollups_1m WHERE target_id=$1 AND metric=$2 AND ts >= $3::timestamptz - $8::interval),
   (SELECT max(ts) + $8::interval FROM ` + table + ` WHERE target_id=$1 AND metric=$2 AND ts >= $3::timestamptz - $8::interval AND ts < $4),
   $3::timestamptz), 'epoch'::timestamptz) AS c
), src AS (
 SELECT label, ts AS b, samples, known, vsum, vmax, vlast, last_ts, wsum, wint, with_interval
 FROM ` + table + `, cut WHERE target_id=$1 AND metric=$2 AND ts >= date_bin($8::interval, $3::timestamptz, 'epoch'::timestamptz) AND ts < cut.c
 UNION ALL
 SELECT label, date_bin($8::interval, ts - interval '1 microsecond', 'epoch'::timestamptz), count(*), count(value), sum(value), max(value),
        (array_agg(value ORDER BY ts DESC))[1], max(ts), sum(value * interval_seconds), sum(interval_seconds), count(interval_seconds)
 FROM metric_points, cut WHERE target_id=$1 AND metric=$2 AND ts > greatest(cut.c, $3::timestamptz) AND ts <= $4
 GROUP BY label, 2
), buckets AS (
 SELECT label, date_bin($5::interval, b, $3::timestamptz) AS bucket,
 CASE WHEN sum(known) = sum(samples) THEN ` + expression + ` END AS value, sum(samples)::bigint AS samples, sum(known)::bigint AS known, max(last_ts) AS last_sample
 FROM src GROUP BY label, bucket
)
SELECT label, bucket, value, samples, known, last_sample,
 EXISTS (SELECT 1 FROM collection_gaps g WHERE g.target_id=$1 AND g.collector=$6
 AND g.ended_at>bucket AND g.started_at<bucket+$5::interval) AS gap
FROM buckets ORDER BY label, bucket LIMIT $7`
}
