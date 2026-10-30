package collector

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/repository"
	"github.com/locklens/locklens/internal/targetdb"
)

// hotTables is how many of the busiest tables get per-table series.
const hotTables = 20

type tableActivityState struct {
	at     time.Time
	tables map[uint32]observe.TableCounters
}

// tableRates are the rates of one table between two collections, the
// busiest first.
func tableRates(prev *tableActivityState, cur tableActivityState) []observe.TableActivity {
	if prev == nil {
		return nil
	}
	iv := cur.at.Sub(prev.at).Seconds()
	if iv <= 0 {
		return nil
	}
	var out []observe.TableActivity
	for relid, c := range cur.tables {
		p, ok := prev.tables[relid]
		// A counter that went down was reset (pg_stat_reset); its rates are
		// unknown for this interval and the table is skipped.
		if !ok || c.SeqScan < p.SeqScan || c.SeqRead < p.SeqRead || c.IdxScan < p.IdxScan || c.Writes < p.Writes || c.IdxFetch < p.IdxFetch {
			continue
		}
		a := observe.TableActivity{RelID: relid, Schema: c.Schema, Name: c.Name,
			SeqScansPerSec: float64(c.SeqScan-p.SeqScan) / iv, SeqRowsPerSec: float64(c.SeqRead-p.SeqRead) / iv,
			IdxScansPerSec: float64(c.IdxScan-p.IdxScan) / iv, IdxRowsPerSec: float64(c.IdxFetch-p.IdxFetch) / iv,
			WritesPerSec: float64(c.Writes-p.Writes) / iv, DeadRows: c.Dead, LiveRows: c.Live, IntervalSeconds: iv}
		if a.SeqRowsPerSec+a.IdxRowsPerSec+a.WritesPerSec+a.SeqScansPerSec+a.IdxScansPerSec == 0 {
			continue
		}
		out = append(out, a)
	}
	slices.SortFunc(out, func(a, b observe.TableActivity) int {
		if c := cmp.Compare(b.SeqRowsPerSec+b.IdxRowsPerSec+b.WritesPerSec, a.SeqRowsPerSec+a.IdxRowsPerSec+a.WritesPerSec); c != 0 {
			return c
		}
		return cmp.Compare(a.RelID, b.RelID)
	})
	if len(out) > hotTables {
		out = out[:hotTables]
	}
	return out
}

// collectTableActivity reads the table counters of every database in scope
// and keeps the busiest tables of each.
func (w *worker) collectTableActivity(ctx context.Context) error {
	if w.prevTables == nil {
		w.prevTables = map[string]*tableActivityState{}
	}
	var hot []observe.TableActivity
	var at time.Time
	for _, db := range w.databases(ctx) {
		var cur tableActivityState
		err := db.obs.ReadOnly(ctx, func(ctx context.Context, tx pgx.Tx, _ []int32) error {
			var err error
			cur.tables, err = targetdb.ReadTableCounters(ctx, tx)
			return err
		})
		if err != nil {
			if db.main {
				return err
			}
			w.m.log.Warn("table counters failed", "target_id", w.target.ID, "database", db.name, "error", sanitizeError(err))
			continue
		}
		cur.at = time.Now().UTC()
		at = cur.at
		rates := tableRates(w.prevTables[db.name], cur)
		w.prevTables[db.name] = &cur
		for i := range rates {
			rates[i].DatName = db.name
		}
		hot = append(hot, rates...)
	}
	if len(hot) == 0 {
		return nil // the first collection only sets the baseline counters
	}
	slices.SortStableFunc(hot, func(a, b observe.TableActivity) int {
		return cmp.Compare(b.SeqRowsPerSec+b.IdxRowsPerSec+b.WritesPerSec, a.SeqRowsPerSec+a.IdxRowsPerSec+a.WritesPerSec)
	})
	var pts []observe.Point
	for _, a := range hot {
		label, iv := a.DatName+"."+a.Schema+"."+a.Name, a.IntervalSeconds
		pts = append(pts,
			observe.Point{Metric: "table.seq_scans.per_s", Label: label, IntervalSeconds: &iv, Value: observe.F(a.SeqScansPerSec)},
			observe.Point{Metric: "table.seq_rows.per_s", Label: label, IntervalSeconds: &iv, Value: observe.F(a.SeqRowsPerSec)},
			observe.Point{Metric: "table.idx_scans.per_s", Label: label, IntervalSeconds: &iv, Value: observe.F(a.IdxScansPerSec)},
			observe.Point{Metric: "table.writes.per_s", Label: label, IntervalSeconds: &iv, Value: observe.F(a.WritesPerSec)},
			observe.Point{Metric: "table.dead_rows", Label: label, Value: observe.F(float64(a.DeadRows))})
	}
	w.state.mu.Lock()
	w.state.tableActivity = hot
	w.state.mu.Unlock()
	id := w.target.ID
	w.m.writer.Enqueue(WriteOp{TargetID: id, Collector: CollectorTableActivity, At: at,
		Write: func(ctx context.Context, s Repositories, _ repository.IdentityCache) error {
			return s.InsertPoints(ctx, id, at, pts)
		}})
	return nil
}
