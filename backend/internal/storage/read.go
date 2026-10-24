package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/observe"
)

// SeriesPoint is one downsampled bucket. Value is nil when every sample in
// the bucket was unknown; buckets without samples are absent (a gap).
type SeriesPoint struct {
	T       time.Time `json:"t"`
	V       *float64  `json:"v"`
	Samples int       `json:"n"`
	Known   int       `json:"known"`
}

// Series is one metric (and label) over time.
type Series struct {
	Metric string        `json:"metric"`
	Label  string        `json:"label,omitempty"`
	Agg    string        `json:"aggregation"`
	Points []SeriesPoint `json:"points"`
}

// aggFor picks the downsampling aggregate from the metric name: discrete
// counts (".delta") are summed, maxima keep the maximum, everything else is
// averaged.
func aggFor(metric string) string {
	switch {
	case strings.HasSuffix(metric, ".delta"):
		return "sum"
	case strings.Contains(metric, "max_") || strings.Contains(metric, "oldest"):
		return "max"
	}
	return "avg"
}

// QuerySeries returns downsampled series for metrics in (from, to].
func (s *Store) QuerySeries(ctx context.Context, targetID int64, metrics []string, from, to time.Time, step time.Duration) ([]Series, error) {
	out := []Series{}
	for _, m := range metrics {
		agg := aggFor(m)
		rows, err := s.pool.Query(ctx, fmt.Sprintf(`
SELECT label, date_bin($5::interval, ts, '2000-01-01 00:00:00+00') AS bucket, %s(value), count(*), count(value)
FROM metric_points
WHERE target_id = $1 AND metric = $2 AND ts > $3 AND ts <= $4
GROUP BY label, bucket
ORDER BY label, bucket`, agg), targetID, m, from.UTC(), to.UTC(), step)
		if err != nil {
			return nil, s.red.Error(err)
		}
		byLabel := map[string]*Series{}
		var order []string
		var (
			label        string
			bucket       time.Time
			value        *float64
			count, known int
		)
		_, err = pgx.ForEachRow(rows, []any{&label, &bucket, &value, &count, &known}, func() error {
			sr, ok := byLabel[label]
			if !ok {
				sr = &Series{Metric: m, Label: label, Agg: agg, Points: []SeriesPoint{}}
				byLabel[label] = sr
				order = append(order, label)
			}
			sr.Points = append(sr.Points, SeriesPoint{T: bucket.UTC(), V: value, Samples: count, Known: known})
			return nil
		})
		if err != nil {
			return nil, s.red.Error(err)
		}
		if len(order) == 0 {
			out = append(out, Series{Metric: m, Agg: agg, Points: []SeriesPoint{}})
		}
		for _, l := range order {
			out = append(out, *byLabel[l])
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

// QueryRow is one identity aggregated over a window, with the previous
// equal-length window when compared.
type QueryRow struct {
	IdentityID  int64    `json:"identity_id"`
	ServerMajor int      `json:"server_major"`
	DatName     string   `json:"datname"`
	UserName    string   `json:"username"`
	TopLevel    bool     `json:"toplevel"`
	QueryID     int64    `json:"queryid,string"`
	Text        *string  `json:"query_text"`
	Calls       int64    `json:"calls"`
	TotalMS     float64  `json:"total_exec_ms"`
	Rows        int64    `json:"rows"`
	BlkHit      int64    `json:"shared_blks_hit"`
	BlkRead     int64    `json:"shared_blks_read"`
	TempW       int64    `json:"temp_blks_written"`
	WALBytes    *float64 `json:"wal_bytes"`
	Share       *float64 `json:"share_of_total_time"`
	PrevCalls   *int64   `json:"previous_calls"`
	PrevTotalMS *float64 `json:"previous_total_exec_ms"`
}

// QueryListParams selects and sorts the workload list.
type QueryListParams struct {
	TargetID    int64
	From, To    time.Time
	Compare     bool
	Filter      string
	IncludeText bool
	Sort        string
	Desc        bool
	Limit       int
	Offset      int
	// ExcludeUser hides statements of one role (LockLens' monitoring role).
	ExcludeUser string
}

var querySorts = map[string]string{
	"total_time":   "cur.total_ms",
	"calls":        "cur.calls",
	"mean_time":    "cur.total_ms / NULLIF(cur.calls, 0)",
	"rows":         "cur.rows",
	"shared_read":  "cur.rd",
	"temp_written": "cur.temp",
	"wal_bytes":    "cur.wal",
	"mean_ratio":   "(cur.total_ms / NULLIF(cur.calls, 0)) / NULLIF(prev.total_ms / NULLIF(prev.calls, 0), 0)",
	"calls_ratio":  "cur.calls::float8 / NULLIF(prev.calls, 0)",
	"total_ratio":  "cur.total_ms / NULLIF(prev.total_ms, 0)",
}

// ValidQuerySort reports whether a sort key is accepted.
func ValidQuerySort(s string) bool { _, ok := querySorts[s]; return ok }

// ListQueries aggregates query samples. Text is returned only when
// IncludeText is set (the caller checks the target's policy).
func (s *Store) ListQueries(ctx context.Context, p QueryListParams) ([]QueryRow, int, error) {
	order, ok := querySorts[p.Sort]
	if !ok {
		order = querySorts["total_time"]
	}
	dir := "ASC"
	if p.Desc {
		dir = "DESC"
	}
	window := p.To.Sub(p.From)
	prevFrom := p.From.Add(-window)
	sql := `
WITH cur AS (
    SELECT identity_id, sum(calls) AS calls, sum(total_exec_ms) AS total_ms, sum(rows) AS rows,
           sum(shared_blks_hit) AS hit, sum(shared_blks_read) AS rd, sum(temp_blks_written) AS temp,
           sum(wal_bytes)::float8 AS wal
    FROM query_samples WHERE target_id = $1 AND ts > $2 AND ts <= $3 GROUP BY identity_id
), prev AS (
    SELECT identity_id, sum(calls) AS calls, sum(total_exec_ms) AS total_ms
    FROM query_samples WHERE $4::bool AND target_id = $1 AND ts > $5 AND ts <= $2 GROUP BY identity_id
), tot AS (SELECT sum(total_ms) AS t FROM cur)
SELECT i.id, i.server_major, i.datname, i.username, i.toplevel, i.queryid,
       CASE WHEN $6::bool THEN i.query_text END,
       cur.calls, cur.total_ms, cur.rows, cur.hit, cur.rd, cur.temp, cur.wal,
       cur.total_ms / NULLIF(tot.t, 0), prev.calls, prev.total_ms,
       count(*) OVER ()
FROM cur
JOIN query_identities AS i ON i.id = cur.identity_id
LEFT JOIN prev ON prev.identity_id = cur.identity_id
CROSS JOIN tot
WHERE ($7 = '' OR i.datname ILIKE '%' || $7 || '%' OR i.username ILIKE '%' || $7 || '%'
      OR i.queryid::text = $7 OR ($6::bool AND i.query_text ILIKE '%' || $7 || '%'))
  AND ($10 = '' OR i.username <> $10)
ORDER BY ` + order + ` ` + dir + ` NULLS LAST, i.id
LIMIT $8 OFFSET $9`
	rows, err := s.pool.Query(ctx, sql, p.TargetID, p.From.UTC(), p.To.UTC(), p.Compare, prevFrom.UTC(), p.IncludeText,
		strings.TrimSpace(p.Filter), p.Limit, p.Offset, p.ExcludeUser)
	if err != nil {
		return nil, 0, s.red.Error(err)
	}
	total := 0
	out := []QueryRow{}
	for rows.Next() {
		var q QueryRow
		if err := rows.Scan(&q.IdentityID, &q.ServerMajor, &q.DatName, &q.UserName, &q.TopLevel, &q.QueryID, &q.Text,
			&q.Calls, &q.TotalMS, &q.Rows, &q.BlkHit, &q.BlkRead, &q.TempW, &q.WALBytes, &q.Share, &q.PrevCalls,
			&q.PrevTotalMS, &total); err != nil {
			rows.Close()
			return nil, 0, s.red.Error(err)
		}
		out = append(out, q)
	}
	return out, total, s.red.Error(rows.Err())
}

// Coverage summarises statement collections in a window.
type Coverage struct {
	WindowSeconds  float64 `json:"window_seconds"`
	CoveredSeconds float64 `json:"covered_seconds"`
	Collections    int     `json:"collections"`
	Resets         int     `json:"resets"`
	Evicted        int64   `json:"evicted_entries"`
	Hidden         int     `json:"hidden_entries_max"`
}

// StatementCoverage returns coverage for (from, to].
func (s *Store) StatementCoverage(ctx context.Context, targetID int64, from, to time.Time) (Coverage, error) {
	c := Coverage{WindowSeconds: to.Sub(from).Seconds()}
	err := s.pool.QueryRow(ctx, `
SELECT coalesce(sum(interval_seconds), 0), count(*), count(*) FILTER (WHERE reset_detected),
       coalesce(sum(dealloc_delta), 0), coalesce(max(hidden), 0)
FROM statement_collections WHERE target_id = $1 AND ts > $2 AND ts <= $3`, targetID, from.UTC(), to.UTC()).
		Scan(&c.CoveredSeconds, &c.Collections, &c.Resets, &c.Evicted, &c.Hidden)
	c.CoveredSeconds = min(c.CoveredSeconds, c.WindowSeconds)
	return c, s.red.Error(err)
}

// QueryIdentity returns one identity.
func (s *Store) QueryIdentity(ctx context.Context, targetID, id int64, includeText bool) (QueryRow, time.Time, time.Time, error) {
	var q QueryRow
	var first, last time.Time
	err := s.pool.QueryRow(ctx, `
SELECT id, server_major, datname, username, toplevel, queryid, CASE WHEN $3 THEN query_text END, first_seen, last_seen
FROM query_identities WHERE target_id = $1 AND id = $2`, targetID, id, includeText).
		Scan(&q.IdentityID, &q.ServerMajor, &q.DatName, &q.UserName, &q.TopLevel, &q.QueryID, &q.Text, &first, &last)
	if errors.Is(err, pgx.ErrNoRows) {
		return q, first, last, ErrNotFound
	}
	return q, first, last, s.red.Error(err)
}

// QueryBucket is one time bucket of an identity's activity.
type QueryBucket struct {
	T        time.Time `json:"t"`
	Calls    int64     `json:"calls"`
	TotalMS  float64   `json:"total_exec_ms"`
	Rows     int64     `json:"rows"`
	Interval float64   `json:"covered_seconds"`
}

// QueryBuckets returns downsampled activity of one identity.
func (s *Store) QueryBuckets(ctx context.Context, targetID, id int64, from, to time.Time, step time.Duration) ([]QueryBucket, error) {
	rows, err := s.pool.Query(ctx, `
SELECT date_bin($5::interval, ts, '2000-01-01 00:00:00+00') AS b, sum(calls), sum(total_exec_ms), sum(rows), sum(interval_seconds)
FROM query_samples WHERE target_id = $1 AND identity_id = $2 AND ts > $3 AND ts <= $4
GROUP BY b ORDER BY b`, targetID, id, from.UTC(), to.UTC(), step)
	if err != nil {
		return nil, s.red.Error(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (QueryBucket, error) {
		var b QueryBucket
		err := r.Scan(&b.T, &b.Calls, &b.TotalMS, &b.Rows, &b.Interval)
		b.T = b.T.UTC()
		return b, err
	})
	if out == nil {
		out = []QueryBucket{}
	}
	return out, s.red.Error(err)
}

// WindowTotals aggregates one identity in a window.
func (s *Store) WindowTotals(ctx context.Context, targetID, id int64, from, to time.Time) (calls int64, totalMS float64, rows int64, err error) {
	err = s.pool.QueryRow(ctx, `
SELECT coalesce(sum(calls), 0), coalesce(sum(total_exec_ms), 0), coalesce(sum(rows), 0)
FROM query_samples WHERE target_id = $1 AND identity_id = $2 AND ts > $3 AND ts <= $4`, targetID, id, from.UTC(), to.UTC()).
		Scan(&calls, &totalMS, &rows)
	return calls, totalMS, rows, s.red.Error(err)
}

// SnapshotMeta describes a stored activity snapshot.
type SnapshotMeta struct {
	ID               int64     `json:"id"`
	At               time.Time `json:"at"`
	BlockedSessions  int       `json:"blocked_sessions"`
	RootBlockers     int       `json:"root_blockers"`
	LongTransactions int       `json:"long_transactions"`
}

// ListActivitySnapshots returns snapshot metadata in (from, to], newest first.
func (s *Store) ListActivitySnapshots(ctx context.Context, targetID int64, from, to time.Time, onlyBlocking bool, limit int) ([]SnapshotMeta, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, ts, blocked_sessions, root_blockers, long_transactions FROM activity_snapshots
WHERE target_id = $1 AND ts > $2 AND ts <= $3 AND (NOT $4 OR blocked_sessions > 0)
ORDER BY ts DESC LIMIT $5`, targetID, from.UTC(), to.UTC(), onlyBlocking, limit)
	if err != nil {
		return nil, s.red.Error(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (SnapshotMeta, error) {
		var m SnapshotMeta
		err := r.Scan(&m.ID, &m.At, &m.BlockedSessions, &m.RootBlockers, &m.LongTransactions)
		return m, err
	})
	if out == nil {
		out = []SnapshotMeta{}
	}
	return out, s.red.Error(err)
}

// GetActivitySnapshot returns a stored snapshot document.
func (s *Store) GetActivitySnapshot(ctx context.Context, targetID, id int64) (SnapshotMeta, json.RawMessage, error) {
	var m SnapshotMeta
	var raw json.RawMessage
	err := s.pool.QueryRow(ctx, `
SELECT id, ts, blocked_sessions, root_blockers, long_transactions, snapshot FROM activity_snapshots
WHERE target_id = $1 AND id = $2`, targetID, id).Scan(&m.ID, &m.At, &m.BlockedSessions, &m.RootBlockers, &m.LongTransactions, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, nil, ErrNotFound
	}
	return m, raw, s.red.Error(err)
}

// LatestActivitySnapshot returns the newest stored snapshot.
func (s *Store) LatestActivitySnapshot(ctx context.Context, targetID int64) (SnapshotMeta, json.RawMessage, error) {
	var m SnapshotMeta
	var raw json.RawMessage
	err := s.pool.QueryRow(ctx, `
SELECT id, ts, blocked_sessions, root_blockers, long_transactions, snapshot FROM activity_snapshots
WHERE target_id = $1 ORDER BY ts DESC LIMIT 1`, targetID).Scan(&m.ID, &m.At, &m.BlockedSessions, &m.RootBlockers, &m.LongTransactions, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, nil, ErrNotFound
	}
	return m, raw, s.red.Error(err)
}

// LatestState returns the newest state snapshot of a kind.
func (s *Store) LatestState(ctx context.Context, targetID int64, kind string) (time.Time, json.RawMessage, error) {
	var ts time.Time
	var raw json.RawMessage
	err := s.pool.QueryRow(ctx, `SELECT ts, payload FROM state_snapshots WHERE target_id = $1 AND kind = $2 ORDER BY ts DESC LIMIT 1`,
		targetID, kind).Scan(&ts, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return ts, nil, ErrNotFound
	}
	return ts, raw, s.red.Error(err)
}

// LatestTableHealth rebuilds the newest table health collection from rows.
func (s *Store) LatestTableHealth(ctx context.Context, targetID int64) (*observe.TableHealth, error) {
	ts, raw, err := s.LatestState(ctx, targetID, "table_health")
	if err != nil {
		return nil, err
	}
	var meta struct {
		DatName    string                `json:"datname"`
		SizeSource string                `json:"size_source"`
		Databases  []observe.DatabaseAge `json:"databases"`
		Settings   map[string]string     `json:"settings"`
		Warnings   []string              `json:"warnings"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, err
	}
	h := &observe.TableHealth{At: ts.UTC(), DatName: meta.DatName, SizeSource: meta.SizeSource, Databases: meta.Databases,
		Settings: meta.Settings, Warnings: meta.Warnings, Tables: []observe.TableStat{}, Indexes: []observe.IndexStat{}}
	rows, err := s.pool.Query(ctx, `
SELECT relid, datname, schemaname, relname, n_live_tup, n_dead_tup, n_mod_since_analyze, last_vacuum, last_autovacuum,
       last_analyze, last_autoanalyze, vacuum_count, autovacuum_count, analyze_count, autoanalyze_count,
       table_bytes, indexes_bytes, total_bytes, frozenxid_age
FROM table_stats WHERE target_id = $1 AND ts = $2 ORDER BY total_bytes DESC NULLS LAST`, targetID, ts)
	if err != nil {
		return nil, s.red.Error(err)
	}
	h.Tables, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (observe.TableStat, error) {
		var t observe.TableStat
		err := r.Scan(&t.RelID, &t.DatName, &t.Schema, &t.Name, &t.LiveTuples, &t.DeadTuples, &t.ModSinceAnalyze,
			&t.LastVacuum, &t.LastAutovacuum, &t.LastAnalyze, &t.LastAutoanalyze, &t.VacuumCount, &t.AutovacuumCount,
			&t.AnalyzeCount, &t.AutoanalyzeCount, &t.TableBytes, &t.IndexesBytes, &t.TotalBytes, &t.FrozenXIDAge)
		return t, err
	})
	if err != nil {
		return nil, s.red.Error(err)
	}
	rows, err = s.pool.Query(ctx, `
SELECT indexrelid, relid, schemaname, relname, indexrelname, idx_scan, index_bytes, is_unique, is_primary, is_constraint
FROM index_stats WHERE target_id = $1 AND ts = $2 ORDER BY index_bytes DESC NULLS LAST`, targetID, ts)
	if err != nil {
		return nil, s.red.Error(err)
	}
	h.Indexes, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (observe.IndexStat, error) {
		var i observe.IndexStat
		err := r.Scan(&i.IndexRelID, &i.RelID, &i.Schema, &i.Table, &i.Name, &i.Scans, &i.Bytes, &i.IsUnique, &i.IsPrimary, &i.IsConstraint)
		return i, err
	})
	if h.Tables == nil {
		h.Tables = []observe.TableStat{}
	}
	if h.Indexes == nil {
		h.Indexes = []observe.IndexStat{}
	}
	return h, s.red.Error(err)
}

// TargetCounts are per-target counts for the fleet view.
type TargetCounts struct {
	OpenIncidents    int `json:"open_incidents"`
	CriticalFindings int `json:"critical_findings"`
	WarningFindings  int `json:"warning_findings"`
	InfoFindings     int `json:"info_findings"`
}

// CountsByTarget returns open incidents and active findings per target.
func (s *Store) CountsByTarget(ctx context.Context) (map[int64]TargetCounts, error) {
	out := map[int64]TargetCounts{}
	rows, err := s.pool.Query(ctx, `SELECT target_id, count(*) FROM incidents WHERE state = 'open' AND target_id IS NOT NULL GROUP BY target_id`)
	if err != nil {
		return nil, s.red.Error(err)
	}
	var id int64
	var n int
	if _, err := pgx.ForEachRow(rows, []any{&id, &n}, func() error {
		c := out[id]
		c.OpenIncidents = n
		out[id] = c
		return nil
	}); err != nil {
		return nil, s.red.Error(err)
	}
	rows, err = s.pool.Query(ctx, `SELECT target_id, severity, count(*) FROM findings WHERE status = 'active' GROUP BY target_id, severity`)
	if err != nil {
		return nil, s.red.Error(err)
	}
	var sev string
	_, err = pgx.ForEachRow(rows, []any{&id, &sev, &n}, func() error {
		c := out[id]
		switch sev {
		case "critical":
			c.CriticalFindings = n
		case "warning":
			c.WarningFindings = n
		default:
			c.InfoFindings = n
		}
		out[id] = c
		return nil
	})
	return out, s.red.Error(err)
}
