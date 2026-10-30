package collector

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/domain/changes"
	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/repository"
	"github.com/locklens/locklens/internal/targetdb"
)

// collectChanges reads the settings and server facts; the write compares
// them with the previous reading and records what changed.
func (w *worker) collectChanges(ctx context.Context) error {
	var st observe.ServerState
	err := w.obs.ReadOnly(ctx, func(ctx context.Context, tx pgx.Tx, _ []int32) error {
		var err error
		st, err = targetdb.ReadServerState(ctx, tx)
		return err
	})
	if err != nil {
		return err
	}
	id := w.target.ID
	w.m.writer.Enqueue(WriteOp{TargetID: id, Collector: CollectorChanges, At: st.At,
		Write: func(ctx context.Context, s Repositories, _ repository.IdentityCache) error {
			return changes.ObserveServer(ctx, s, id, st)
		}})
	return nil
}
