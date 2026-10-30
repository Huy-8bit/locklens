package collector

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/domain/model"
	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/domain/report"
	"github.com/locklens/locklens/internal/repository"
	"github.com/locklens/locklens/internal/targetdb"
)

func (w *worker) collectActivity(ctx context.Context) error {
	longTx := w.m.settings.LongTransactionThreshold()
	snap, err := w.obs.Collect(ctx, targetdb.Options{IncludeQuery: w.target.QueryTextEnabled, QueryMaxLength: 2000, MinTxAge: longTx})
	if err != nil {
		return err
	}
	var counts observe.ActivityCounts
	var vacuum []observe.VacuumProgress
	var groups []observe.ConnectionGroup
	if err := w.obs.ReadOnly(ctx, func(ctx context.Context, tx pgx.Tx, own []int32) error {
		var err error
		if counts, err = targetdb.ReadActivityCounts(ctx, tx, own); err != nil {
			return err
		}
		if groups, err = targetdb.ReadConnectionGroups(ctx, tx, own); err != nil {
			return err
		}
		vacuum, err = targetdb.ReadVacuumProgress(ctx, tx)
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
		Write: func(ctx context.Context, s Repositories, _ repository.IdentityCache) error {
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
			Write: func(ctx context.Context, s Repositories, _ repository.IdentityCache) error {
				return s.InsertActivitySnapshot(ctx, id, at, sum.BlockedSessions, sum.RootBlockers, sum.LongTransactions, raw)
			}})
	}
	if len(vacuum) > 0 && at.Sub(w.lastVacuumStored) >= vacuumSnapshotInterval {
		w.lastVacuumStored = at
		raw, _ := json.Marshal(vacuum)
		w.m.writer.Enqueue(WriteOp{TargetID: id, Collector: CollectorActivity, At: at,
			Write: func(ctx context.Context, s Repositories, _ repository.IdentityCache) error {
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
