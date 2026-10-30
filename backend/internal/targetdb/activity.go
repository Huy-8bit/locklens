package targetdb

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/targetdb/queries"
)

func ReadConnectionGroups(ctx context.Context, tx pgx.Tx, own []int32) ([]observe.ConnectionGroup, error) {
	rows, err := tx.Query(ctx, queries.ConnectionGroups, own)
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

func ReadActivityCounts(ctx context.Context, tx pgx.Tx, own []int32) (observe.ActivityCounts, error) {
	var c observe.ActivityCounts
	err := tx.QueryRow(ctx, queries.ActivityCounts, own).Scan(&c.UsedConnections, &c.ClientTotal, &c.Active, &c.Idle,
		&c.IdleInTx, &c.IdleInTxAborted, &c.WaitingLock, &c.MaxConnections, &c.ReservedConnections)
	c.UnknownState = c.ClientTotal - c.Active - c.Idle - c.IdleInTx - c.IdleInTxAborted
	return c, err
}

func ReadVacuumProgress(ctx context.Context, tx pgx.Tx) ([]observe.VacuumProgress, error) {
	rows, err := tx.Query(ctx, queries.VacuumProgress)
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
