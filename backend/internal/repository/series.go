package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// SeriesPoint represents a bucket in (from, to]. Unknown or interrupted buckets
// remain null, even if other samples in that bucket are known.
type SeriesPoint struct {
	T       time.Time `json:"t"`
	V       *float64  `json:"v"`
	Samples int       `json:"n"`
	Known   int       `json:"known"`
	Gap     bool      `json:"gap,omitempty"`
}

type Series struct {
	Metric     string        `json:"metric"`
	Label      string        `json:"label,omitempty"`
	Agg        string        `json:"aggregation"`
	Collector  string        `json:"collector"`
	LastSample *time.Time    `json:"last_sample_at,omitempty"`
	Points     []SeriesPoint `json:"points"`
}

// MetricAggregation is deliberately conservative. Pressure gauges retain peaks,
// sizes retain the last observation, and rates use measured elapsed intervals.
func MetricAggregation(metric string) string {
	switch {
	case strings.HasSuffix(metric, ".delta"):
		return "sum"
	case strings.HasSuffix(metric, ".per_s"):
		return "time_weighted_mean"
	case metric == "cache.hit_ratio":
		return "mean_interval_ratio"
	case strings.HasPrefix(metric, "sessions."), strings.HasPrefix(metric, "connections."),
		strings.HasPrefix(metric, "blocking."), strings.HasPrefix(metric, "transactions."),
		strings.Contains(metric, "lag"), strings.Contains(metric, "delay"), strings.Contains(metric, "max_"),
		strings.HasSuffix(metric, "utilization_ratio"), strings.HasSuffix(metric, "used_ratio"):
		return "max"
	default:
		return "last"
	}
}

func MetricCollector(metric string) string {
	if strings.HasPrefix(metric, "db.") {
		return "tables"
	}
	for _, p := range []string{"sessions.", "connections.", "blocking.", "transactions."} {
		if strings.HasPrefix(metric, p) {
			return "activity"
		}
	}
	if strings.HasPrefix(metric, "host.") {
		return "host"
	}
	if strings.HasPrefix(metric, "statements.") {
		return "statements"
	}
	for _, p := range []string{"replication.", "standby.", "slots."} {
		if strings.HasPrefix(metric, p) {
			return "replication"
		}
	}
	return "database"
}

const MaxSeriesPoints = 12000

var ErrSeriesLimit = errors.New("response exceeds 12000 points; request fewer metrics or a larger step")

// QuerySeries bounds both the time window and total response cardinality. The
// request context cancels PostgreSQL work when a client changes its selection.
func (s *Store) QuerySeries(ctx context.Context, targetID int64, metrics []string, from, to time.Time, step time.Duration) ([]Series, error) {
	if step <= 0 || !to.After(from) || to.Sub(from) > 31*24*time.Hour || len(metrics) > 20 {
		return nil, errors.New("invalid series bounds")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out := []Series{}
	total := 0
	for _, m := range metrics {
		agg, collector := MetricAggregation(m), MetricCollector(m)
		expression := "(array_agg(value ORDER BY ts DESC))[1]"
		switch agg {
		case "sum", "max":
			expression = agg + "(value)"
		case "mean_interval_ratio":
			expression = "avg(value)"
		case "time_weighted_mean":
			expression = "CASE WHEN count(interval_seconds)=count(*) THEN sum(value * interval_seconds) / NULLIF(sum(interval_seconds),0) END"
		}
		rows, err := s.pool.Query(ctx, fmt.Sprintf(`
WITH buckets AS (
 SELECT label, date_bin($5::interval, ts - interval '1 microsecond', $3::timestamptz) AS bucket,
 CASE WHEN count(value)=count(*) THEN %s END AS value, count(*) AS samples, count(value) AS known, max(ts) AS last_sample
 FROM metric_points WHERE target_id=$1 AND metric=$2 AND ts>$3 AND ts<=$4
 GROUP BY label, bucket
)
SELECT label, bucket, value, samples, known, last_sample,
 EXISTS (SELECT 1 FROM collection_gaps g WHERE g.target_id=$1 AND g.collector=$6
 AND g.ended_at>bucket AND g.started_at<bucket+$5::interval) AS gap
FROM buckets ORDER BY label, bucket LIMIT $7`, expression), targetID, m, from.UTC(), to.UTC(), step, collector, MaxSeriesPoints-total+1)
		if err != nil {
			return nil, s.red.Error(err)
		}
		byLabel := map[string]*Series{}
		var order []string
		for rows.Next() {
			var label string
			var p SeriesPoint
			var last time.Time
			if err := rows.Scan(&label, &p.T, &p.V, &p.Samples, &p.Known, &last, &p.Gap); err != nil {
				rows.Close()
				return nil, s.red.Error(err)
			}
			total++
			if total > MaxSeriesPoints {
				rows.Close()
				return nil, ErrSeriesLimit
			}
			if p.Gap {
				p.V = nil
			}
			sr := byLabel[label]
			if sr == nil {
				sr = &Series{Metric: m, Label: label, Agg: agg, Collector: collector, Points: []SeriesPoint{}}
				byLabel[label] = sr
				order = append(order, label)
			}
			p.T = p.T.UTC()
			last = last.UTC()
			sr.LastSample = &last
			sr.Points = append(sr.Points, p)
		}
		if err := rows.Err(); err != nil {
			return nil, s.red.Error(err)
		}
		rows.Close()
		if len(order) == 0 {
			out = append(out, Series{Metric: m, Agg: agg, Collector: collector, Points: []SeriesPoint{}})
		}
		for _, label := range order {
			out = append(out, *byLabel[label])
		}
	}
	return out, nil
}

// Gap is a recorded collection gap.
type Gap struct {
	Collector string    `json:"collector"`
	From      time.Time `json:"from"`
	To        time.Time `json:"to"`
	Reason    string    `json:"reason"`
}

// ListGaps returns gaps overlapping (from, to].
func (s *Store) ListGaps(ctx context.Context, targetID int64, from, to time.Time, limit int) ([]Gap, error) {
	rows, err := s.pool.Query(ctx, `
SELECT collector, started_at, ended_at, reason FROM collection_gaps
WHERE target_id = $1 AND ended_at > $2 AND started_at <= $3 ORDER BY ended_at DESC LIMIT $4`,
		targetID, from.UTC(), to.UTC(), limit)
	if err != nil {
		return nil, s.red.Error(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Gap, error) {
		var g Gap
		err := r.Scan(&g.Collector, &g.From, &g.To, &g.Reason)
		return g, err
	})
	if out == nil {
		out = []Gap{}
	}
	return out, s.red.Error(err)
}

// CommandBucket is one time bucket of one command class.
type CommandBucket struct {
	T       time.Time `json:"t"`
	Command string    `json:"command"`
	Calls   int64     `json:"calls"`
	TotalMS float64   `json:"total_exec_ms"`
	Rows    int64     `json:"rows"`
}

// CoverageBucket is the statement-collection time inside one bucket. A
// bucket with coverage and no command rows had no statement activity (zero);
// a bucket without coverage is unknown.
type CoverageBucket struct {
	T       time.Time `json:"t"`
	Covered float64   `json:"covered_seconds"`
}
