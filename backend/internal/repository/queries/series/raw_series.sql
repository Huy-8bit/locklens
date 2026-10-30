-- One metric from raw points, per step bucket and label. A bucket with an
-- unknown sample is unknown. {{aggregate}}: see RawAggregate.
WITH buckets AS (
 SELECT label, date_bin($5::interval, ts - interval '1 microsecond', $3::timestamptz) AS bucket,
 CASE WHEN count(value)=count(*) THEN {{aggregate}} END AS value, count(*) AS samples, count(value) AS known, max(ts) AS last_sample
 FROM metric_points WHERE target_id=$1 AND metric=$2 AND ts>$3 AND ts<=$4
 GROUP BY label, bucket
)
SELECT label, bucket, value, samples, known, last_sample,
 EXISTS (SELECT 1 FROM collection_gaps g WHERE g.target_id=$1 AND g.collector=$6
 AND g.ended_at>bucket AND g.started_at<bucket+$5::interval) AS gap
FROM buckets ORDER BY label, bucket LIMIT $7
