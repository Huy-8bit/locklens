package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/domain/observe"
)

// InsertLoadBucket stores one bucket of active session samples. Writing the
// same bucket twice (a retried write) keeps the first copy.
func (s *Store) InsertLoadBucket(ctx context.Context, targetID int64, b observe.LoadBucket) error {
	if b.Ticks <= 0 {
		return nil
	}
	n := len(b.Groups)
	classes, events, backends, datnames, usenames, apps := make([]string, n), make([]string, n), make([]string, n), make([]string, n), make([]string, n), make([]string, n)
	dbids, userids, qids := make([]*int64, n), make([]*int64, n), make([]*int64, n)
	samples := make([]int32, n)
	for i, g := range b.Groups {
		classes[i], events[i], backends[i], datnames[i], usenames[i], apps[i] = g.WaitClass, g.WaitEvent, g.BackendType, g.DatName, g.UserName, g.Application
		if g.DBID != 0 {
			v := int64(g.DBID)
			dbids[i] = &v
		}
		if g.UserID != 0 {
			v := int64(g.UserID)
			userids[i] = &v
		}
		if g.QueryID != 0 {
			v := g.QueryID
			qids[i] = &v
		}
		samples[i] = int32(g.Samples)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return s.red.Error(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	tag, err := tx.Exec(ctx, `
INSERT INTO load_buckets (target_id, ts, ticks, interval_seconds) VALUES ($1, $2, $3, $4)
ON CONFLICT DO NOTHING`, targetID, b.At.UTC(), b.Ticks, b.IntervalSeconds)
	if err != nil {
		return s.red.Error(err)
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	if n > 0 {
		if _, err := tx.Exec(ctx, `
INSERT INTO load_samples (target_id, ts, wait_class, wait_event, backend_type, dbid, datname, userid, usename, application_name, queryid, samples)
SELECT $1, $2, c, e, b, d::oid, dn, u::oid, un, a, q, n
FROM unnest($3::text[], $4::text[], $5::text[], $6::bigint[], $7::text[], $8::bigint[], $9::text[], $10::text[], $11::bigint[], $12::int[])
  AS x(c, e, b, d, dn, u, un, a, q, n)`,
			targetID, b.At.UTC(), classes, events, backends, dbids, datnames, userids, usenames, apps, qids, samples); err != nil {
			return s.red.Error(err)
		}
	}
	return s.red.Error(tx.Commit(ctx))
}

// LoadDimension names a grouping of load samples.
type LoadDimension string

// Load dimensions. The SQL columns of each are fixed here; nothing from a
// request is ever placed in the statement text.
const (
	LoadByClass       LoadDimension = "class"
	LoadByEvent       LoadDimension = "event"
	LoadByQuery       LoadDimension = "query"
	LoadByUser        LoadDimension = "user"
	LoadByDatabase    LoadDimension = "database"
	LoadByApplication LoadDimension = "application"
	LoadByBackend     LoadDimension = "backend"
)

var loadColumns = map[LoadDimension][]string{
	LoadByClass:       {},
	LoadByEvent:       {"wait_event"},
	LoadByQuery:       {"coalesce(dbid::bigint, 0)", "coalesce(userid::bigint, 0)", "coalesce(queryid, 0)", "max(datname)", "max(usename)"},
	LoadByUser:        {"usename"},
	LoadByDatabase:    {"datname"},
	LoadByApplication: {"application_name"},
	LoadByBackend:     {"backend_type"},
}

// ValidLoadDimension reports whether d is a known dimension.
func ValidLoadDimension(d string) bool { _, ok := loadColumns[LoadDimension(d)]; return ok }

// LoadFilter narrows the samples, for example to one wait class or statement.
type LoadFilter struct {
	WaitClass string
	QueryID   *int64
}

// LoadRow is the samples of one group in one time bucket. Key holds the
// dimension's values (see loadColumns); WaitClass is always included so a
// group can be broken down by class.
type LoadRow struct {
	T         time.Time
	Key       []string
	WaitClass string
	Samples   int64
}

// LoadTicks is the number of samples taken in one time bucket.
type LoadTicks struct {
	T     time.Time
	Ticks int64
}

// LoadRows returns samples in [from, to) summed per step bucket, dimension
// value, and wait class, and the ticks per step bucket.
func (s *Store) LoadRows(ctx context.Context, targetID int64, from, to time.Time, step time.Duration, dim LoadDimension, f LoadFilter) ([]LoadRow, []LoadTicks, error) {
	cols, ok := loadColumns[dim]
	if !ok {
		return nil, nil, fmt.Errorf("unknown load dimension %q", dim)
	}
	args := []any{targetID, from.UTC(), to.UTC(), step}
	where := "target_id = $1 AND ts >= $2 AND ts < $3"
	if f.WaitClass != "" {
		args = append(args, f.WaitClass)
		where += fmt.Sprintf(" AND wait_class = $%d", len(args))
	}
	if f.QueryID != nil {
		args = append(args, *f.QueryID)
		where += fmt.Sprintf(" AND queryid = $%d", len(args))
	}
	// Grouping columns are the dimension's plain columns; aggregate label
	// columns (max(...)) are selected but not grouped.
	var sel, group []string
	for _, c := range cols {
		sel = append(sel, c+"::text")
		if !strings.HasPrefix(c, "max(") {
			group = append(group, c)
		}
	}
	keySel := ""
	if len(sel) > 0 {
		keySel = ", ARRAY[" + strings.Join(sel, ", ") + "]"
	} else {
		keySel = ", ARRAY[]::text[]"
	}
	groupBy := "1, wait_class"
	if len(group) > 0 {
		groupBy += ", " + strings.Join(group, ", ")
	}
	q := `SELECT date_bin($4::interval, ts, $2::timestamptz), wait_class` + keySel + `, sum(samples)::bigint
FROM load_samples WHERE ` + where + `
GROUP BY ` + groupBy + `
ORDER BY 1
LIMIT 200000`
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, nil, s.red.Error(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (LoadRow, error) {
		var x LoadRow
		err := r.Scan(&x.T, &x.WaitClass, &x.Key, &x.Samples)
		x.T = x.T.UTC()
		return x, err
	})
	if err != nil {
		return nil, nil, s.red.Error(err)
	}
	trows, err := s.pool.Query(ctx, `
SELECT date_bin($4::interval, ts, $2::timestamptz), sum(ticks)::bigint
FROM load_buckets WHERE target_id = $1 AND ts >= $2 AND ts < $3
GROUP BY 1 ORDER BY 1`, targetID, from.UTC(), to.UTC(), step)
	if err != nil {
		return nil, nil, s.red.Error(err)
	}
	ticks, err := pgx.CollectRows(trows, func(r pgx.CollectableRow) (LoadTicks, error) {
		var x LoadTicks
		err := r.Scan(&x.T, &x.Ticks)
		x.T = x.T.UTC()
		return x, err
	})
	return out, ticks, s.red.Error(err)
}

// StatementRef identifies a statement seen in pg_stat_activity.
type StatementRef struct {
	DBID, UserID uint32
	QueryID      int64
}

// QueryRefsByActivity resolves statements seen in pg_stat_activity to their
// stored pg_stat_statements identities: the top-level identity with the same
// database, user, and queryid, the most recently seen when a major upgrade
// left several.
func (s *Store) QueryRefsByActivity(ctx context.Context, targetID int64, refs []StatementRef, includeText bool) ([]QueryRef, error) {
	if len(refs) == 0 {
		return []QueryRef{}, nil
	}
	dbids, userids, qids := make([]int64, len(refs)), make([]int64, len(refs)), make([]int64, len(refs))
	for i, r := range refs {
		dbids[i], userids[i], qids[i] = int64(r.DBID), int64(r.UserID), r.QueryID
	}
	rows, err := s.pool.Query(ctx, `
SELECT q.server_major, q.dbid::bigint, q.userid::bigint, q.toplevel, q.queryid, q.id, q.datname, q.username, q.command,
       CASE WHEN $5 THEN q.query_text END
FROM unnest($2::bigint[], $3::bigint[], $4::bigint[]) AS k(d, u, qid)
CROSS JOIN LATERAL (
    SELECT * FROM query_identities i
    WHERE i.target_id = $1 AND i.dbid = k.d::oid AND i.userid = k.u::oid AND i.queryid = k.qid AND i.toplevel
    ORDER BY i.last_seen DESC LIMIT 1
) AS q`, targetID, dbids, userids, qids, includeText)
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
