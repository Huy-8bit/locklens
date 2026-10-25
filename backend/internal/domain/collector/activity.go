package collector

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/domain/model"
	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/domain/report"
	"github.com/locklens/locklens/internal/lib/postgres"
	"github.com/locklens/locklens/internal/repository"
)

// Counts of client backends by state. UsedConnections includes LockLens'
// own sessions because they consume max_connections slots too; every other
// count excludes them ($1).
const sqlActivityCounts = `
SELECT count(*) FILTER (WHERE backend_type = 'client backend'),
       count(*) FILTER (WHERE backend_type = 'client backend' AND pid <> ALL ($1::int[])),
       count(*) FILTER (WHERE backend_type = 'client backend' AND pid <> ALL ($1::int[]) AND state = 'active'),
       count(*) FILTER (WHERE backend_type = 'client backend' AND pid <> ALL ($1::int[]) AND state = 'idle'),
       count(*) FILTER (WHERE backend_type = 'client backend' AND pid <> ALL ($1::int[]) AND state = 'idle in transaction'),
       count(*) FILTER (WHERE backend_type = 'client backend' AND pid <> ALL ($1::int[]) AND state = 'idle in transaction (aborted)'),
       count(*) FILTER (WHERE pid <> ALL ($1::int[]) AND wait_event_type = 'Lock'),
       current_setting('max_connections')::int,
       current_setting('superuser_reserved_connections')::int
         + coalesce(nullif(current_setting('reserved_connections', true), '')::int, 0)
FROM pg_catalog.pg_stat_activity`

// Vacuum progress. Relation names resolve only in the current database.
const sqlVacuumProgress = `
SELECT p.pid, p.datname,
       CASE WHEN p.datname = current_database() THEN p.relid::regclass::text ELSE p.relid::text END,
       p.phase, p.heap_blks_total, p.heap_blks_scanned, p.heap_blks_vacuumed, p.index_vacuum_count
FROM pg_catalog.pg_stat_progress_vacuum AS p
ORDER BY p.pid`

// Client connections grouped by database, user, application, client address,
// and state; LockLens' own sessions ($1) are excluded. Bounded to 200 groups.
const sqlConnectionGroups = `
SELECT coalesce(datname, ''), coalesce(usename, ''), coalesce(application_name, ''),
       coalesce(host(client_addr), ''), coalesce(state, 'unknown'), count(*)::int,
       extract(epoch FROM max(now() - backend_start))::float8
FROM pg_catalog.pg_stat_activity
WHERE backend_type = 'client backend' AND pid <> ALL ($1::int[])
GROUP BY 1, 2, 3, 4, 5
ORDER BY 6 DESC, 1, 2, 3, 4, 5
LIMIT 200`

func readConnectionGroups(ctx context.Context, tx pgx.Tx, own []int32) ([]observe.ConnectionGroup, error) {
	rows, err := tx.Query(ctx, sqlConnectionGroups, own)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (observe.ConnectionGroup, error) {
		var g observe.ConnectionGroup
		err := r.Scan(&g.Database, &g.User, &g.Application, &g.ClientAddr, &g.State, &g.Count, &g.OldestSeconds)
		return g, err
	})
	if out == nil {
		out = []observe.ConnectionGroup{}
	}
	return out, err
}

func readActivityCounts(ctx context.Context, tx pgx.Tx, own []int32) (observe.ActivityCounts, error) {
	var c observe.ActivityCounts
	err := tx.QueryRow(ctx, sqlActivityCounts, own).Scan(&c.UsedConnections, &c.ClientTotal, &c.Active, &c.Idle,
		&c.IdleInTx, &c.IdleInTxAborted, &c.WaitingLock, &c.MaxConnections, &c.ReservedConnections)
	c.UnknownState = c.ClientTotal - c.Active - c.Idle - c.IdleInTx - c.IdleInTxAborted
	return c, err
}

func readVacuumProgress(ctx context.Context, tx pgx.Tx) ([]observe.VacuumProgress, error) {
	rows, err := tx.Query(ctx, sqlVacuumProgress)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (observe.VacuumProgress, error) {
		var v observe.VacuumProgress
		err := r.Scan(&v.PID, &v.DatName, &v.Relation, &v.Phase, &v.HeapBlksTotal, &v.HeapBlksScanned, &v.HeapBlksVacuumed, &v.IndexVacuumCount)
		return v, err
	})
	if out == nil {
		out = []observe.VacuumProgress{}
	}
	return out, err
}

func (w *worker) collectActivity(ctx context.Context) error {
	longTx := w.m.settings.LongTransactionThreshold()
	snap, err := w.obs.Collect(ctx, postgres.Options{IncludeQuery: w.target.QueryTextEnabled, QueryMaxLength: 2000, MinTxAge: longTx})
	if err != nil {
		return err
	}
	var counts observe.ActivityCounts
	var vacuum []observe.VacuumProgress
	var groups []observe.ConnectionGroup
	if err := w.obs.ReadOnly(ctx, func(ctx context.Context, tx pgx.Tx, own []int32) error {
		var err error
		if counts, err = readActivityCounts(ctx, tx, own); err != nil {
			return err
		}
		if groups, err = readConnectionGroups(ctx, tx, own); err != nil {
			return err
		}
		vacuum, err = readVacuumProgress(ctx, tx)
		return err
	}); err != nil {
		return err
	}
	rep := report.Build(snap, longTx)
	// All server-side series use LockLens' clock so that collectors of one
	// target line up; ages inside the report still use the server clock.
	at := snap.CollectedAt
	act := &observe.Activity{At: at, Snapshot: snap, Counts: counts, Vacuum: vacuum, Groups: groups}
	points := activityPoints(counts, rep)

	w.state.mu.Lock()
	w.state.activity, w.state.report = act, rep
	w.state.mu.Unlock()
	w.state.addPoints(at, points)

	id := w.target.ID
	w.m.writer.Enqueue(WriteOp{TargetID: id, Collector: CollectorActivity, At: at,
		Write: func(ctx context.Context, s *repository.Store, _ repository.IdentityCache) error {
			return s.InsertPoints(ctx, id, at, points)
		}})
	interesting := rep.Analysis.Summary.BlockedSessions > 0 || rep.Analysis.Summary.LongTransactions > 0
	if interesting || at.Sub(w.lastSnapshotStored) >= snapshotHeartbeat {
		w.lastSnapshotStored = at
		raw, err := json.Marshal(snap)
		if err != nil {
			return err
		}
		sum := rep.Analysis.Summary
		w.m.writer.Enqueue(WriteOp{TargetID: id, Collector: CollectorActivity, At: at,
			Write: func(ctx context.Context, s *repository.Store, _ repository.IdentityCache) error {
				return s.InsertActivitySnapshot(ctx, id, at, sum.BlockedSessions, sum.RootBlockers, sum.LongTransactions, raw)
			}})
	}
	if len(vacuum) > 0 && at.Sub(w.lastVacuumStored) >= vacuumSnapshotInterval {
		w.lastVacuumStored = at
		raw, _ := json.Marshal(vacuum)
		w.m.writer.Enqueue(WriteOp{TargetID: id, Collector: CollectorActivity, At: at,
			Write: func(ctx context.Context, s *repository.Store, _ repository.IdentityCache) error {
				return s.InsertStateSnapshot(ctx, id, "vacuum_progress", at, raw)
			}})
	}
	return nil
}

// activityPoints derives the session, connection, blocking, and transaction
// series from one activity collection.
func activityPoints(c observe.ActivityCounts, rep *report.Report) []observe.Point {
	f := func(v int) *float64 { return observe.F(float64(v)) }
	pts := []observe.Point{
		{Metric: "sessions.active", Value: f(c.Active)},
		{Metric: "sessions.idle", Value: f(c.Idle)},
		{Metric: "sessions.idle_in_transaction", Value: f(c.IdleInTx + c.IdleInTxAborted)},
		{Metric: "sessions.unknown_state", Value: f(c.UnknownState)},
		{Metric: "sessions.waiting_lock", Value: f(c.WaitingLock)},
		{Metric: "sessions.client_total", Value: f(c.ClientTotal)},
		{Metric: "connections.used", Value: f(c.UsedConnections)},
		{Metric: "connections.max", Value: f(c.MaxConnections)},
		{Metric: "connections.reserved", Value: f(c.ReservedConnections)},
	}
	if avail := c.MaxConnections - c.ReservedConnections; avail > 0 {
		pts = append(pts, observe.Point{Metric: "connections.usage_ratio", Value: observe.F(float64(c.UsedConnections) / float64(avail))})
	}
	sum := rep.Analysis.Summary
	pts = append(pts,
		observe.Point{Metric: "blocking.blocked_sessions", Value: f(sum.BlockedSessions)},
		observe.Point{Metric: "blocking.root_blockers", Value: f(sum.RootBlockers)},
		observe.Point{Metric: "transactions.long", Value: f(sum.LongTransactions)},
	)
	var maxWait, oldest, oldestIdle *float64
	open := 0
	byPID := map[int]model.Session{}
	for _, s := range rep.Snapshot.Sessions {
		byPID[s.PID] = s
	}
	for _, a := range rep.Analysis.Sessions {
		if a.LockWaitSeconds != nil && (maxWait == nil || *a.LockWaitSeconds > *maxWait) {
			maxWait = a.LockWaitSeconds
		}
		s := byPID[a.PID]
		if s.BackendType == nil || *s.BackendType != "client backend" || a.TransactionAgeSeconds == nil {
			continue
		}
		open++
		if oldest == nil || *a.TransactionAgeSeconds > *oldest {
			oldest = a.TransactionAgeSeconds
		}
		if report.IsIdleInTransaction(s.State) && (oldestIdle == nil || *a.TransactionAgeSeconds > *oldestIdle) {
			oldestIdle = a.TransactionAgeSeconds
		}
	}
	// With no waiter or no open transaction the maximum is undefined, so it
	// is stored as NULL rather than 0.
	pts = append(pts,
		observe.Point{Metric: "blocking.max_lock_wait_seconds", Value: maxWait},
		observe.Point{Metric: "transactions.open", Value: f(open)},
		observe.Point{Metric: "transactions.oldest_age_seconds", Value: oldest},
		observe.Point{Metric: "transactions.idle_in_transaction_oldest_age_seconds", Value: oldestIdle},
	)
	return pts
}
