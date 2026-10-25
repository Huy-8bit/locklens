package collector

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/observe"
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
