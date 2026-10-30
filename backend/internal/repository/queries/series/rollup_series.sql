-- One metric from a rollup table ({{table}}), completed with raw points after
-- the newest rolled-up minute (rollups run every minute), all aggregated into
-- the request's steps. Without rollups it reads raw points only.
-- $1 target, $2 metric, $3 from, $4 to, $5 step, $6 collector, $7 limit,
-- $8 resolution. {{aggregate}}: see RollupAggregate.
WITH cut AS (
 -- The end of the newest rolled-up minute; when minute rollups have expired,
 -- the end of the newest row of this table; without rollups, from.
 SELECT date_bin($8::interval, coalesce(
   (SELECT max(ts) + interval '1 minute' FROM metric_rollups_1m WHERE target_id=$1 AND metric=$2 AND ts >= $3::timestamptz - $8::interval),
   (SELECT max(ts) + $8::interval FROM {{table}} WHERE target_id=$1 AND metric=$2 AND ts >= $3::timestamptz - $8::interval AND ts < $4),
   $3::timestamptz), 'epoch'::timestamptz) AS c
), src AS (
 SELECT label, ts AS b, samples, known, vsum, vmax, vlast, last_ts, wsum, wint, with_interval
 FROM {{table}}, cut WHERE target_id=$1 AND metric=$2 AND ts >= date_bin($8::interval, $3::timestamptz, 'epoch'::timestamptz) AND ts < cut.c
 UNION ALL
 SELECT label, date_bin($8::interval, ts - interval '1 microsecond', 'epoch'::timestamptz), count(*), count(value), sum(value), max(value),
        (array_agg(value ORDER BY ts DESC))[1], max(ts), sum(value * interval_seconds), sum(interval_seconds), count(interval_seconds)
 FROM metric_points, cut WHERE target_id=$1 AND metric=$2 AND ts > greatest(cut.c, $3::timestamptz) AND ts <= $4
 GROUP BY label, 2
), buckets AS (
 SELECT label, date_bin($5::interval, b, $3::timestamptz) AS bucket,
 CASE WHEN sum(known) = sum(samples) THEN {{aggregate}} END AS value, sum(samples)::bigint AS samples, sum(known)::bigint AS known, max(last_ts) AS last_sample
 FROM src GROUP BY label, bucket
)
SELECT label, bucket, value, samples, known, last_sample,
 EXISTS (SELECT 1 FROM collection_gaps g WHERE g.target_id=$1 AND g.collector=$6
 AND g.ended_at>bucket AND g.started_at<bucket+$5::interval) AS gap
FROM buckets ORDER BY label, bucket LIMIT $7
