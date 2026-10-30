package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/repository/queries"
)

// SeriesRepository is the store of metric time series, collection gaps, and the bounds of stored history.
type SeriesRepository interface {
	// QuerySeries bounds both the time window and total response cardinality. The
	// request context cancels PostgreSQL work when a client changes its selection.
	QuerySeries(ctx context.Context, targetID int64, metrics []string, from, to time.Time, step time.Duration) ([]Series, error)
	// ListGaps returns gaps overlapping (from, to].
	ListGaps(ctx context.Context, targetID int64, from, to time.Time, limit int) ([]Gap, error)
	// HistoryBounds returns the oldest and newest stored metric time of a
	// target.
	HistoryBounds(ctx context.Context, id int64) (map[string]any, error)
}

var _ SeriesRepository = (*Store)(nil)

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
	if strings.HasPrefix(metric, "table.") {
		return "table_activity"
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
		res := s.seriesSource(from, step, time.Now())
		sql := queries.RawSeries(agg)
		args := []any{targetID, m, from.UTC(), to.UTC(), max(step, res), collector, MaxSeriesPoints - total + 1}
		if res > 0 {
			table := queries.MinuteRollupTable
			if res == HourRollup {
				table = queries.HourRollupTable
			}
			sql = queries.RollupSeries(table, agg)
			args = append(args, res)
		}
		rows, err := s.pool.Query(ctx, sql, args...)
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
	rows, err := s.pool.Query(ctx, queries.ListGaps,
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

// HistoryBounds returns the oldest and newest stored metric time of a
// target.
func (s *Store) HistoryBounds(ctx context.Context, id int64) (map[string]any, error) {
	var a, b *time.Time
	err := s.pool.QueryRow(ctx, queries.HistoryBounds, id).Scan(&a, &b)
	return map[string]any{"from": a, "to": b}, err
}
