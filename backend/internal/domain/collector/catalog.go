package collector

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/domain/changes"
	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/repository"
	"github.com/locklens/locklens/internal/targetdb"
)

// collectCatalog reads the catalog of every database in scope and drops the
// stored catalogs of databases that left it.
func (w *worker) collectCatalog(ctx context.Context) error {
	var collected []string
	for _, db := range w.databases(ctx) {
		var c observe.Catalog
		err := db.obs.ReadOnly(ctx, func(ctx context.Context, tx pgx.Tx, _ []int32) error {
			var err error
			c, err = targetdb.ReadCatalog(ctx, tx)
			return err
		})
		if err != nil {
			if db.main {
				return err
			}
			w.m.log.Warn("catalog failed", "target_id", w.target.ID, "database", db.name, "error", sanitizeError(err))
			continue
		}
		collected = append(collected, c.DatName)
		id := w.target.ID
		w.m.writer.Enqueue(WriteOp{TargetID: id, Collector: CollectorCatalog, At: c.At,
			Write: func(ctx context.Context, s Store, _ repository.IdentityCache) error {
				if err := s.SaveCatalog(ctx, id, c); err != nil {
					return err
				}
				return changes.ObserveSchema(ctx, s, id, c)
			}})
	}
	if keep, ok := w.scopeNames(collected); ok {
		id := w.target.ID
		w.m.writer.Enqueue(WriteOp{TargetID: id, Collector: CollectorCatalog, At: time.Now().UTC(),
			Write: func(ctx context.Context, s Store, _ repository.IdentityCache) error {
				return s.PruneCatalogs(ctx, id, keep)
			}})
	}
	return nil
}
