package collector

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/repository"
)

// hotTables is how many of the busiest tables get per-table series.
const hotTables = 20

// sqlTableActivity reads only the cumulative counters of
// pg_stat_user_tables: no size functions and no relation locks, so it is
// cheap enough to run every minute (the full table health runs every five).
const sqlTableActivity = `
SELECT t.relid, t.schemaname, t.relname, coalesce(t.seq_scan, 0), coalesce(t.seq_tup_read, 0),
       coalesce(t.idx_scan, 0), coalesce(t.idx_tup_fetch, 0),
       t.n_tup_ins + t.n_tup_upd + t.n_tup_del, t.n_dead_tup, t.n_live_tup
FROM pg_catalog.pg_stat_user_tables AS t`

// tableCounters are the cumulative counters of one table.
type tableCounters struct {
	schema, name                                string
	seqScan, seqRead, idxScan, idxFetch, writes int64
	dead, live                                  int64
}

type tableActivityState struct {
	at     time.Time
	tables map[uint32]tableCounters
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
		if !ok || c.seqScan < p.seqScan || c.seqRead < p.seqRead || c.idxScan < p.idxScan || c.writes < p.writes || c.idxFetch < p.idxFetch {
			continue
		}
		a := observe.TableActivity{RelID: relid, Schema: c.schema, Name: c.name,
			SeqScansPerSec: float64(c.seqScan-p.seqScan) / iv, SeqRowsPerSec: float64(c.seqRead-p.seqRead) / iv,
			IdxScansPerSec: float64(c.idxScan-p.idxScan) / iv, IdxRowsPerSec: float64(c.idxFetch-p.idxFetch) / iv,
			WritesPerSec: float64(c.writes-p.writes) / iv, DeadRows: c.dead, LiveRows: c.live, IntervalSeconds: iv}
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

func (w *worker) collectTableActivity(ctx context.Context) error {
	cur := tableActivityState{tables: map[uint32]tableCounters{}}
	if err := w.obs.ReadOnly(ctx, func(ctx context.Context, tx pgx.Tx, _ []int32) error {
		rows, err := tx.Query(ctx, sqlTableActivity)
		if err != nil {
			return err
		}
		var relid uint32
		var c tableCounters
		_, err = pgx.ForEachRow(rows, []any{&relid, &c.schema, &c.name, &c.seqScan, &c.seqRead, &c.idxScan, &c.idxFetch, &c.writes, &c.dead, &c.live}, func() error {
			cur.tables[relid] = c
			return nil
		})
		return err
	}); err != nil {
		return err
	}
	cur.at = time.Now().UTC()
	hot := tableRates(w.prevTables, cur)
	w.prevTables = &cur
	if hot == nil {
		return nil // the first collection only sets the baseline counters
	}
	var pts []observe.Point
	for _, a := range hot {
		label, iv := a.Schema+"."+a.Name, a.IntervalSeconds
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
	id, at := w.target.ID, cur.at
	w.m.writer.Enqueue(WriteOp{TargetID: id, Collector: CollectorTableActivity, At: at,
		Write: func(ctx context.Context, s *repository.Store, _ repository.IdentityCache) error {
			return s.InsertPoints(ctx, id, at, pts)
		}})
	return nil
}
