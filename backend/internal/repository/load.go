package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/repository/queries"
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
	tag, err := tx.Exec(ctx, queries.InsertLoadBucketTicks, targetID, b.At.UTC(), b.Ticks, b.IntervalSeconds)
	if err != nil {
		return s.red.Error(err)
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	if n > 0 {
		if _, err := tx.Exec(ctx, queries.InsertLoadSample,
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

// ValidLoadDimension reports whether d is a known dimension.
func ValidLoadDimension(d string) bool { _, ok := queries.LoadDimensions[d]; return ok }

// LoadFilter narrows the samples, for example to one wait class or statement.
type LoadFilter struct {
	WaitClass string
	QueryID   *int64
}

// LoadRow is the samples of one group in one time bucket. Key holds the
// dimension's values (see queries.LoadDimensions); WaitClass is always included so a
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
	sql, err := queries.LoadRows(string(dim))
	if err != nil {
		return nil, nil, err
	}
	var class *string
	if f.WaitClass != "" {
		class = &f.WaitClass
	}
	rows, err := s.pool.Query(ctx, sql, targetID, from.UTC(), to.UTC(), step, class, f.QueryID)
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
	trows, err := s.pool.Query(ctx, queries.LoadTicks, targetID, from.UTC(), to.UTC(), step)
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
	rows, err := s.pool.Query(ctx, queries.QueryRefsByActivity, targetID, dbids, userids, qids, includeText)
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
