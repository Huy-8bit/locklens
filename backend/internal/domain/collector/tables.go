package collector

import (
	"context"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/repository"
	"github.com/locklens/locklens/internal/targetdb"
)

// collectTables reads the table and index statistics of every database in
// scope; the main database also reads cluster-wide data (database sizes and
// ages, vacuum settings), which only it contributes metrics from.
func (w *worker) collectTables(ctx context.Context) error {
	var merged *observe.TableHealth
	var others []observe.TableHealth
	for _, db := range w.databases(ctx) {
		var h observe.TableHealth
		err := db.obs.ReadOnly(ctx, func(ctx context.Context, tx pgx.Tx, _ []int32) error {
			var err error
			h, err = targetdb.ReadTableHealth(ctx, tx, db.main)
			return err
		})
		if err != nil {
			if db.main {
				return err
			}
			w.m.log.Warn("table statistics failed", "target_id", w.target.ID, "database", db.name, "error", sanitizeError(err))
			continue
		}
		// Each database's collection is stored apart; distinct times keep
		// their rows apart.
		h.At = w.nextTableTime(h.At)
		if db.main {
			merged = &h
		} else {
			others = append(others, h)
		}
		id, hc := w.target.ID, h
		w.m.writer.Enqueue(WriteOp{TargetID: id, Collector: CollectorTables, At: h.At,
			Write: func(ctx context.Context, s Store, _ repository.IdentityCache) error {
				return s.InsertTableHealth(ctx, id, hc)
			}})
	}
	h := *merged
	var pts []observe.Point
	var total float64
	known := true
	for _, d := range h.Databases {
		var size *float64
		if d.SizeBytes != nil {
			size = observe.F(float64(*d.SizeBytes))
			total += *size
		} else {
			known = false
		}
		var age *float64
		if d.XIDAge != nil {
			age = observe.F(float64(*d.XIDAge))
		}
		pts = append(pts, observe.Point{Metric: "db.size_bytes", Label: d.DatName, Value: size},
			observe.Point{Metric: "db.xid_age", Label: d.DatName, Value: age})
	}
	if known && len(h.Databases) > 0 {
		pts = append(pts, observe.Point{Metric: "db.total_size_bytes", Value: observe.F(total)})
	} else {
		pts = append(pts, observe.Point{Metric: "db.total_size_bytes"})
	}
	// The live view has the tables and indexes of every database.
	all := h
	for _, o := range others {
		all.Tables, all.Indexes = append(slices.Clip(all.Tables), o.Tables...), append(slices.Clip(all.Indexes), o.Indexes...)
	}
	w.state.mu.Lock()
	w.state.tables = &all
	w.state.mu.Unlock()
	w.state.addPoints(h.At, pts)
	id, at := w.target.ID, h.At
	w.m.writer.Enqueue(WriteOp{TargetID: id, Collector: CollectorTables, At: at,
		Write: func(ctx context.Context, s Store, _ repository.IdentityCache) error {
			return s.InsertPoints(ctx, id, at, pts)
		}})
	return nil
}

// nextTableTime returns at, or just after the previous table collection's
// time when at is not later.
func (w *worker) nextTableTime(at time.Time) time.Time {
	if !at.After(w.lastTableAt) {
		at = w.lastTableAt.Add(time.Microsecond)
	}
	w.lastTableAt = at
	return at
}
