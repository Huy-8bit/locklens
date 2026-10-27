package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/domain/observe"
)

// QueryRow is one identity aggregated over a window, with the previous
// equal-length window when compared.
type QueryRow struct {
	IdentityID  int64    `json:"identity_id"`
	ServerMajor int      `json:"server_major"`
	DatName     string   `json:"datname"`
	UserName    string   `json:"username"`
	TopLevel    bool     `json:"toplevel"`
	QueryID     int64    `json:"queryid,string"`
	Command     string   `json:"command"`
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
	// Command restricts the list to one command class; empty means all.
	Command string
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
SELECT i.id, i.server_major, i.datname, i.username, i.toplevel, i.queryid, i.command,
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
  AND ($11 = '' OR i.command = $11)
ORDER BY ` + order + ` ` + dir + ` NULLS LAST, i.id
LIMIT $8 OFFSET $9`
	rows, err := s.pool.Query(ctx, sql, p.TargetID, p.From.UTC(), p.To.UTC(), p.Compare, prevFrom.UTC(), p.IncludeText,
		strings.TrimSpace(p.Filter), p.Limit, p.Offset, p.ExcludeUser, p.Command)
	if err != nil {
		return nil, 0, s.red.Error(err)
	}
	total := 0
	out := []QueryRow{}
	for rows.Next() {
		var q QueryRow
		if err := rows.Scan(&q.IdentityID, &q.ServerMajor, &q.DatName, &q.UserName, &q.TopLevel, &q.QueryID, &q.Command, &q.Text,
			&q.Calls, &q.TotalMS, &q.Rows, &q.BlkHit, &q.BlkRead, &q.TempW, &q.WALBytes, &q.Share, &q.PrevCalls,
			&q.PrevTotalMS, &total); err != nil {
			rows.Close()
			return nil, 0, s.red.Error(err)
		}
		out = append(out, q)
	}
	return out, total, s.red.Error(rows.Err())
}

// CommandTotals is the workload of one command class in a window.
type CommandTotals struct {
	Command    string   `json:"command"`
	Calls      int64    `json:"calls"`
	TotalMS    float64  `json:"total_exec_ms"`
	Rows       int64    `json:"rows"`
	Statements int      `json:"statements"`
	MeanMS     *float64 `json:"mean_exec_ms"`
}

// CommandBreakdown aggregates statement deltas by command class: totals for
// (from, to], per-bucket sums, and per-bucket coverage. Statements of
// excludeUser are left out.
func (s *Store) CommandBreakdown(ctx context.Context, targetID int64, from, to time.Time, step time.Duration, excludeUser string) ([]CommandTotals, []CommandBucket, []CoverageBucket, error) {
	totals := []CommandTotals{}
	rows, err := s.pool.Query(ctx, `
SELECT i.command, sum(q.calls)::bigint, sum(q.total_exec_ms), sum(q.rows)::bigint, count(DISTINCT q.identity_id)
FROM query_samples q JOIN query_identities i ON i.id = q.identity_id
WHERE q.target_id = $1 AND q.ts > $2 AND q.ts <= $3 AND ($4 = '' OR i.username <> $4)
GROUP BY i.command ORDER BY 3 DESC`, targetID, from.UTC(), to.UTC(), excludeUser)
	if err != nil {
		return nil, nil, nil, s.red.Error(err)
	}
	for rows.Next() {
		var c CommandTotals
		if err := rows.Scan(&c.Command, &c.Calls, &c.TotalMS, &c.Rows, &c.Statements); err != nil {
			rows.Close()
			return nil, nil, nil, s.red.Error(err)
		}
		if c.Calls > 0 {
			m := c.TotalMS / float64(c.Calls)
			c.MeanMS = &m
		}
		totals = append(totals, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, nil, s.red.Error(err)
	}
	buckets := []CommandBucket{}
	rows, err = s.pool.Query(ctx, `
SELECT date_bin($5::interval, q.ts, $2), i.command, sum(q.calls)::bigint, sum(q.total_exec_ms), sum(q.rows)::bigint
FROM query_samples q JOIN query_identities i ON i.id = q.identity_id
WHERE q.target_id = $1 AND q.ts > $2 AND q.ts <= $3 AND ($4 = '' OR i.username <> $4)
GROUP BY 1, 2 ORDER BY 1, 2`, targetID, from.UTC(), to.UTC(), excludeUser, step)
	if err != nil {
		return nil, nil, nil, s.red.Error(err)
	}
	for rows.Next() {
		var b CommandBucket
		if err := rows.Scan(&b.T, &b.Command, &b.Calls, &b.TotalMS, &b.Rows); err != nil {
			rows.Close()
			return nil, nil, nil, s.red.Error(err)
		}
		buckets = append(buckets, b)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, nil, s.red.Error(err)
	}
	coverage := []CoverageBucket{}
	rows, err = s.pool.Query(ctx, `
SELECT date_bin($4::interval, ts, $2), sum(interval_seconds)
FROM statement_collections WHERE target_id = $1 AND ts > $2 AND ts <= $3 AND interval_seconds IS NOT NULL
GROUP BY 1 ORDER BY 1`, targetID, from.UTC(), to.UTC(), step)
	if err != nil {
		return nil, nil, nil, s.red.Error(err)
	}
	defer rows.Close()
	for rows.Next() {
		var c CoverageBucket
		if err := rows.Scan(&c.T, &c.Covered); err != nil {
			return nil, nil, nil, s.red.Error(err)
		}
		coverage = append(coverage, c)
	}
	return totals, buckets, coverage, s.red.Error(rows.Err())
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
SELECT id, server_major, datname, username, toplevel, queryid, command, CASE WHEN $3 THEN query_text END, first_seen, last_seen
FROM query_identities WHERE target_id = $1 AND id = $2`, targetID, id, includeText).
		Scan(&q.IdentityID, &q.ServerMajor, &q.DatName, &q.UserName, &q.TopLevel, &q.QueryID, &q.Command, &q.Text, &first, &last)
	if errors.Is(err, pgx.ErrNoRows) {
		return q, first, last, ErrNotFound
	}
	return q, first, last, s.red.Error(err)
}

// QueryRef is a stored query identity found by its pg_stat_statements key.
type QueryRef struct {
	Key        observe.QueryKey `json:"key"`
	IdentityID int64            `json:"identity_id"`
	DatName    string           `json:"datname"`
	UserName   string           `json:"username"`
	Command    string           `json:"command"`
	Text       *string          `json:"query_text"`
}

// QueryRefs resolves pg_stat_statements keys to stored identities of one
// target. Keys that were never stored are left out.
func (s *Store) QueryRefs(ctx context.Context, targetID int64, keys []observe.QueryKey, includeText bool) ([]QueryRef, error) {
	if len(keys) == 0 {
		return []QueryRef{}, nil
	}
	majors := make([]int32, len(keys))
	dbids := make([]int64, len(keys))
	userids := make([]int64, len(keys))
	tops := make([]bool, len(keys))
	qids := make([]int64, len(keys))
	for i, k := range keys {
		majors[i], dbids[i], userids[i], tops[i], qids[i] = int32(k.ServerMajor), int64(k.DBID), int64(k.UserID), k.TopLevel, k.QueryID
	}
	rows, err := s.pool.Query(ctx, `
SELECT q.server_major, q.dbid::bigint, q.userid::bigint, q.toplevel, q.queryid, q.id, q.datname, q.username, q.command,
       CASE WHEN $7 THEN q.query_text END
FROM query_identities q
JOIN unnest($2::int[], $3::bigint[], $4::bigint[], $5::bool[], $6::bigint[]) AS k(m, d, u, t, qid)
  ON q.server_major = k.m AND q.dbid = k.d::oid AND q.userid = k.u::oid AND q.toplevel = k.t AND q.queryid = k.qid
WHERE q.target_id = $1`, targetID, majors, dbids, userids, tops, qids, includeText)
	if err != nil {
		return nil, s.red.Error(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (QueryRef, error) {
		var q QueryRef
		var dbid, userid int64
		err := r.Scan(&q.Key.ServerMajor, &dbid, &userid, &q.Key.TopLevel, &q.Key.QueryID, &q.IdentityID, &q.DatName, &q.UserName, &q.Command, &q.Text)
		q.Key.DBID, q.Key.UserID = uint32(dbid), uint32(userid)
		return q, err
	})
	if out == nil {
		out = []QueryRef{}
	}
	return out, s.red.Error(err)
}

// QueryBucket is one time bucket of an identity's activity.
type QueryBucket struct {
	Gap      bool      `json:"gap,omitempty"`
	T        time.Time `json:"t"`
	Calls    int64     `json:"calls"`
	TotalMS  float64   `json:"total_exec_ms"`
	Rows     int64     `json:"rows"`
	Interval float64   `json:"covered_seconds"`
}

// QueryBuckets returns downsampled activity of one identity.
func (s *Store) QueryBuckets(ctx context.Context, targetID, id int64, from, to time.Time, step time.Duration) ([]QueryBucket, error) {
	rows, err := s.pool.Query(ctx, `
WITH buckets AS (
 SELECT date_bin($5::interval, ts - interval '1 microsecond', $3::timestamptz) AS b, sum(calls) AS calls, sum(total_exec_ms) AS ms, sum(rows) AS rows, sum(interval_seconds) AS covered
 FROM query_samples WHERE target_id = $1 AND identity_id = $2 AND ts > $3 AND ts <= $4
 GROUP BY b
)
SELECT b,calls,ms,rows,covered,EXISTS(SELECT 1 FROM collection_gaps g WHERE g.target_id=$1 AND g.collector='statements' AND g.ended_at>b AND g.started_at<b+$5::interval)
FROM buckets ORDER BY b LIMIT 1001`, targetID, id, from.UTC(), to.UTC(), step)
	if err != nil {
		return nil, s.red.Error(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (QueryBucket, error) {
		var b QueryBucket
		err := r.Scan(&b.T, &b.Calls, &b.TotalMS, &b.Rows, &b.Interval, &b.Gap)
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

// IdentityKey returns the pg_stat_statements key of a stored identity.
func (s *Store) IdentityKey(ctx context.Context, targetID, id int64) (observe.QueryKey, error) {
	var k observe.QueryKey
	var dbid, userid int64
	err := s.pool.QueryRow(ctx, `SELECT server_major, dbid::bigint, userid::bigint, toplevel, queryid FROM query_identities WHERE target_id = $1 AND id = $2`,
		targetID, id).Scan(&k.ServerMajor, &dbid, &userid, &k.TopLevel, &k.QueryID)
	if errors.Is(err, pgx.ErrNoRows) {
		return k, ErrNotFound
	}
	k.DBID, k.UserID = uint32(dbid), uint32(userid)
	return k, s.red.Error(err)
}
