package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/domain/observe"
)

// RollupBucket is the size of a statement rollup bucket.
const RollupBucket = 5 * time.Minute

// RollupStatements recomputes the rollup buckets that start in [from, to)
// from raw statement samples and collections. from must be a bucket start;
// buckets are replaced, so running it again over the same range is harmless.
func (s *Store) RollupStatements(ctx context.Context, targetID int64, from, to time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return s.red.Error(err)
	}
	defer tx.Rollback(context.WithoutCancel(ctx)) //nolint:errcheck
	// A sample at ts covers (ts - interval, ts]; it belongs to the bucket its
	// end falls in, the same convention as charts.
	if _, err := tx.Exec(ctx, `
INSERT INTO query_rollups (target_id, identity_id, ts, calls, total_exec_ms, rows, shared_blks_read, shared_blks_hit, temp_blks_written)
SELECT target_id, identity_id, date_bin('5 minutes', ts - interval '1 microsecond', 'epoch'::timestamptz),
       sum(calls), sum(total_exec_ms), sum(rows), sum(shared_blks_read), sum(shared_blks_hit), sum(temp_blks_written)
FROM query_samples
WHERE target_id = $1 AND ts > $2 AND ts <= $3
GROUP BY 1, 2, 3
ON CONFLICT (target_id, identity_id, ts) DO UPDATE SET
    calls = EXCLUDED.calls, total_exec_ms = EXCLUDED.total_exec_ms, rows = EXCLUDED.rows,
    shared_blks_read = EXCLUDED.shared_blks_read, shared_blks_hit = EXCLUDED.shared_blks_hit,
    temp_blks_written = EXCLUDED.temp_blks_written`, targetID, from.UTC(), to.UTC()); err != nil {
		return s.red.Error(err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO statement_coverage (target_id, ts, covered_seconds, reset_detected)
SELECT target_id, date_bin('5 minutes', ts - interval '1 microsecond', 'epoch'::timestamptz),
       least(sum(interval_seconds), 300), bool_or(reset_detected)
FROM statement_collections
WHERE target_id = $1 AND ts > $2 AND ts <= $3 AND interval_seconds IS NOT NULL
GROUP BY 1, 2
ON CONFLICT (target_id, ts) DO UPDATE SET covered_seconds = EXCLUDED.covered_seconds, reset_detected = EXCLUDED.reset_detected`,
		targetID, from.UTC(), to.UTC()); err != nil {
		return s.red.Error(err)
	}
	return s.red.Error(tx.Commit(ctx))
}

// RollupWatermark returns the start of the newest rolled bucket of a target,
// or ok = false when nothing was rolled yet.
func (s *Store) RollupWatermark(ctx context.Context, targetID int64) (time.Time, bool, error) {
	var t *time.Time
	if err := s.pool.QueryRow(ctx, `SELECT max(ts) FROM statement_coverage WHERE target_id = $1`, targetID).Scan(&t); err != nil {
		return time.Time{}, false, s.red.Error(err)
	}
	if t == nil {
		return time.Time{}, false, nil
	}
	return t.UTC(), true, nil
}

// ActiveStatement is a statement that ran recently, with its identity.
type ActiveStatement struct {
	IdentityID int64
	Key        observe.QueryKey
	TotalMS    float64
}

// ActiveStatements returns the statements with the most execution time since
// a time, at most limit.
func (s *Store) ActiveStatements(ctx context.Context, targetID int64, since time.Time, limit int) ([]ActiveStatement, error) {
	rows, err := s.pool.Query(ctx, `
SELECT r.identity_id, q.server_major, q.dbid::bigint, q.userid::bigint, q.toplevel, q.queryid, sum(r.total_exec_ms)
FROM query_rollups r JOIN query_identities q ON q.id = r.identity_id
WHERE r.target_id = $1 AND r.ts >= $2
GROUP BY 1, 2, 3, 4, 5, 6
ORDER BY 7 DESC
LIMIT $3`, targetID, since.UTC(), limit)
	if err != nil {
		return nil, s.red.Error(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (ActiveStatement, error) {
		var a ActiveStatement
		var dbid, userid int64
		err := r.Scan(&a.IdentityID, &a.Key.ServerMajor, &dbid, &userid, &a.Key.TopLevel, &a.Key.QueryID, &a.TotalMS)
		a.Key.DBID, a.Key.UserID = uint32(dbid), uint32(userid)
		return a, err
	})
	return out, s.red.Error(err)
}

// RollupPoint is the activity of one statement in one baseline point.
type RollupPoint struct {
	IdentityID int64
	At         time.Time
	Calls      int64
	TotalMS    float64
	Rows       int64
	BlkRead    int64
	BlkHit     int64
	TempW      int64
}

// CoveragePoint is the statements coverage of one baseline point.
type CoveragePoint struct {
	At      time.Time
	Covered float64
	Reset   bool
}

// BaselinePoints aggregates the rollups of some statements into points of
// the given size within [from, to), with the target's coverage per point.
func (s *Store) BaselinePoints(ctx context.Context, targetID int64, ids []int64, from, to time.Time, point time.Duration) ([]RollupPoint, []CoveragePoint, error) {
	if point < RollupBucket {
		return nil, nil, errors.New("baseline points cannot be smaller than rollup buckets")
	}
	rows, err := s.pool.Query(ctx, `
SELECT identity_id, date_bin($4::interval, ts, 'epoch'::timestamptz), sum(calls)::bigint, sum(total_exec_ms), sum(rows)::bigint,
       sum(shared_blks_read)::bigint, sum(shared_blks_hit)::bigint, sum(temp_blks_written)::bigint
FROM query_rollups
WHERE target_id = $1 AND ts >= $2 AND ts < $3 AND identity_id = ANY ($5::bigint[])
GROUP BY 1, 2 ORDER BY 1, 2`, targetID, from.UTC(), to.UTC(), point, ids)
	if err != nil {
		return nil, nil, s.red.Error(err)
	}
	points, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (RollupPoint, error) {
		var p RollupPoint
		err := r.Scan(&p.IdentityID, &p.At, &p.Calls, &p.TotalMS, &p.Rows, &p.BlkRead, &p.BlkHit, &p.TempW)
		p.At = p.At.UTC()
		return p, err
	})
	if err != nil {
		return nil, nil, s.red.Error(err)
	}
	crow, err := s.pool.Query(ctx, `
SELECT date_bin($4::interval, ts, 'epoch'::timestamptz), sum(covered_seconds), bool_or(reset_detected)
FROM statement_coverage
WHERE target_id = $1 AND ts >= $2 AND ts < $3
GROUP BY 1 ORDER BY 1`, targetID, from.UTC(), to.UTC(), point)
	if err != nil {
		return nil, nil, s.red.Error(err)
	}
	cov, err := pgx.CollectRows(crow, func(r pgx.CollectableRow) (CoveragePoint, error) {
		var c CoveragePoint
		err := r.Scan(&c.At, &c.Covered, &c.Reset)
		c.At = c.At.UTC()
		return c, err
	})
	return points, cov, s.red.Error(err)
}
