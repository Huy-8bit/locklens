package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/repository/queries"
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

// ValidQuerySort reports whether a sort key is accepted.
func ValidQuerySort(s string) bool { _, ok := queries.QuerySorts[s]; return ok }

// ListQueries aggregates query samples. Text is returned only when
// IncludeText is set (the caller checks the target's policy).
func (s *Store) ListQueries(ctx context.Context, p QueryListParams) ([]QueryRow, int, error) {
	window := p.To.Sub(p.From)
	prevFrom := p.From.Add(-window)
	rows, err := s.pool.Query(ctx, queries.ListQueries(p.Sort, p.Desc), p.TargetID, p.From.UTC(), p.To.UTC(), p.Compare, prevFrom.UTC(), p.IncludeText,
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
	rows, err := s.pool.Query(ctx, queries.CommandTotals, targetID, from.UTC(), to.UTC(), excludeUser)
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
	rows, err = s.pool.Query(ctx, queries.CommandBuckets, targetID, from.UTC(), to.UTC(), excludeUser, step)
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
	rows, err = s.pool.Query(ctx, queries.CommandCoverageBuckets, targetID, from.UTC(), to.UTC(), step)
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
	err := s.pool.QueryRow(ctx, queries.StatementCoverage, targetID, from.UTC(), to.UTC()).
		Scan(&c.CoveredSeconds, &c.Collections, &c.Resets, &c.Evicted, &c.Hidden)
	c.CoveredSeconds = min(c.CoveredSeconds, c.WindowSeconds)
	return c, s.red.Error(err)
}

// QueryIdentity returns one identity.
func (s *Store) QueryIdentity(ctx context.Context, targetID, id int64, includeText bool) (QueryRow, time.Time, time.Time, error) {
	var q QueryRow
	var first, last time.Time
	err := s.pool.QueryRow(ctx, queries.QueryIdentity, targetID, id, includeText).
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
	rows, err := s.pool.Query(ctx, queries.QueryRefs, targetID, majors, dbids, userids, tops, qids, includeText)
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

// QueryBuckets returns downsampled activity of one identity. Steps of five
// minutes or more, and ranges older than the raw history, are read from the
// five-minute rollups (completed with raw samples after the newest rolled-up
// bucket); there a bucket's covered seconds are the target's collection
// coverage.
func (s *Store) QueryBuckets(ctx context.Context, targetID, id int64, from, to time.Time, step time.Duration) ([]QueryBucket, error) {
	raw := time.Duration(s.rawRetention.Load())
	if step >= RollupBucket || (raw > 0 && from.Before(time.Now().Add(-raw))) {
		return s.queryRollupBuckets(ctx, targetID, id, from, to, max(step, RollupBucket))
	}
	rows, err := s.pool.Query(ctx, queries.QueryBuckets, targetID, id, from.UTC(), to.UTC(), step)
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

func (s *Store) queryRollupBuckets(ctx context.Context, targetID, id int64, from, to time.Time, step time.Duration) ([]QueryBucket, error) {
	rows, err := s.pool.Query(ctx, queries.QueryRollupBuckets, targetID, id, from.UTC(), to.UTC(), step)
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

// QueryStep is the step QueryBuckets returns for a request.
func (s *Store) QueryStep(from time.Time, step time.Duration) time.Duration {
	raw := time.Duration(s.rawRetention.Load())
	if step >= RollupBucket || (raw > 0 && from.Before(time.Now().Add(-raw))) {
		return max(step, RollupBucket)
	}
	return step
}

// WindowTotals aggregates one identity in a window.
func (s *Store) WindowTotals(ctx context.Context, targetID, id int64, from, to time.Time) (calls int64, totalMS float64, rows int64, err error) {
	err = s.pool.QueryRow(ctx, queries.WindowTotals, targetID, id, from.UTC(), to.UTC()).
		Scan(&calls, &totalMS, &rows)
	return calls, totalMS, rows, s.red.Error(err)
}

// IdentityKey returns the pg_stat_statements key of a stored identity.
func (s *Store) IdentityKey(ctx context.Context, targetID, id int64) (observe.QueryKey, error) {
	var k observe.QueryKey
	var dbid, userid int64
	err := s.pool.QueryRow(ctx, queries.IdentityKey,
		targetID, id).Scan(&k.ServerMajor, &dbid, &userid, &k.TopLevel, &k.QueryID)
	if errors.Is(err, pgx.ErrNoRows) {
		return k, ErrNotFound
	}
	k.DBID, k.UserID = uint32(dbid), uint32(userid)
	return k, s.red.Error(err)
}
