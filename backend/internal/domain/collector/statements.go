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

// maxStatementDeltas caps how many changed entries are stored per collection
// (largest execution-time deltas first). The rest are counted as truncated.
const maxStatementDeltas = 2000

// statementsState is what a worker remembers between collections.
type statementsState struct {
	at         time.Time
	statsReset *time.Time
	dealloc    *int64
	entries    map[observe.QueryKey]observe.StatementCounters
}

// stmtMeta is what a worker remembers about one entry between collections:
// names, the statement class, and the normalised text when the target
// collects it.
type stmtMeta struct {
	datname, user, command string
	text                   *string
}

// stmtCache holds the meta of the entries seen in the last collection.
// withText records whether texts were kept, so turning text collection on
// fetches them again.
type stmtCache struct {
	withText bool
	meta     map[observe.QueryKey]stmtMeta
}

// readStatements reads the pg_stat_statements counters and returns, per
// entry, its names, text, and command class from the cache. Texts are read
// only for entries the cache does not know (new statements, or every entry
// after text collection was turned on), to classify their command; they are
// kept only when the target collects query text.
func readStatements(ctx context.Context, tx pgx.Tx, caps observe.Capabilities, includeText bool, cache *stmtCache) (map[observe.QueryKey]observe.StatementCounters, map[observe.QueryKey][4]*string, int, error) {
	major := caps.Major()
	rows, hidden, err := targetdb.ReadStatementCounters(ctx, tx, caps.StatementsSchema, major)
	if err != nil {
		return nil, nil, 0, err
	}
	if cache.withText != includeText {
		cache.meta, cache.withText = nil, includeText
	}
	entries := map[observe.QueryKey]observe.StatementCounters{}
	names := map[observe.QueryKey][2]string{}
	for _, r := range rows {
		if prev, ok := entries[r.Key]; ok && prev.Calls >= r.Counters.Calls {
			continue // duplicate keys should not happen; keep the larger counters
		}
		entries[r.Key] = r.Counters
		names[r.Key] = [2]string{r.DatName, r.UserName}
	}
	var missing []int64
	for k := range entries {
		if _, ok := cache.meta[k]; !ok {
			missing = append(missing, k.QueryID)
		}
	}
	texts := map[observe.QueryKey]string{}
	if len(missing) > 0 {
		if texts, err = targetdb.ReadStatementTexts(ctx, tx, caps.StatementsSchema, major, missing); err != nil {
			return nil, nil, 0, err
		}
	}
	// Rebuild the cache from the current entries, so it never outgrows the
	// extension's hash table.
	next := make(map[observe.QueryKey]stmtMeta, len(entries))
	meta := make(map[observe.QueryKey][4]*string, len(entries))
	for k := range entries {
		m, known := cache.meta[k]
		if !known {
			m.command = observe.CommandOther
			if t, ok := texts[k]; ok {
				m.command = observe.ClassifyCommand(t)
				if includeText {
					m.text = &t
				}
				known = true
			}
		}
		n := names[k]
		m.datname, m.user = n[0], n[1]
		if known {
			next[k] = m // an entry whose text was not found is asked again next time
		}
		meta[k] = [4]*string{&m.datname, &m.user, m.text, &m.command}
	}
	cache.meta = next
	return entries, meta, hidden, nil
}

// diffStatements computes per-entry deltas.
//
// Semantics, chosen so window sums stay correct:
//   - No previous collection (first after start): baseline only, no deltas.
//   - pg_stat_statements_reset() since the previous collection: every count
//     now present accumulated after the reset, so current values are deltas.
//   - New entry: it was created (or re-created after eviction) after the
//     previous collection, so its current values are deltas.
//   - Counter decreased: the entry was evicted and re-created; current values
//     are deltas.
//   - Evictions (dealloc increased) lose the evicted entries' counts in this
//     window; that is reported, not hidden.
func diffStatements(prev *statementsState, cur statementsState, meta map[observe.QueryKey][4]*string, hidden int) observe.StatementsCollection {
	out := observe.StatementsCollection{At: cur.at, Entries: len(cur.entries), Hidden: hidden, Deltas: []observe.QueryDelta{}}
	if prev == nil {
		return out
	}
	iv := cur.at.Sub(prev.at).Seconds()
	out.Interval = &iv
	reset := prev.statsReset != nil && cur.statsReset != nil && !prev.statsReset.Equal(*cur.statsReset)
	out.Reset = reset
	if prev.dealloc != nil && cur.dealloc != nil && !reset {
		d := *cur.dealloc - *prev.dealloc
		if d < 0 {
			d = 0
		}
		out.Dealloc = &d
	}
	for k, c := range cur.entries {
		p, seen := prev.entries[k]
		var d observe.QueryDelta
		if reset || !seen || c.Calls < p.Calls {
			d = observe.QueryDelta{Calls: c.Calls, TotalMS: c.TotalMS, Rows: c.Rows, BlkHit: c.BlkHit, BlkRead: c.BlkRead, TempW: c.TempW, WAL: c.WAL}
		} else {
			d = observe.QueryDelta{Calls: c.Calls - p.Calls, TotalMS: c.TotalMS - p.TotalMS, Rows: c.Rows - p.Rows,
				BlkHit: c.BlkHit - p.BlkHit, BlkRead: c.BlkRead - p.BlkRead, TempW: c.TempW - p.TempW}
			if c.WAL != nil && p.WAL != nil {
				w := *c.WAL - *p.WAL
				d.WAL = &w
			}
		}
		if d.Calls <= 0 && d.TotalMS <= 0 {
			continue
		}
		d.Key = k
		if m, ok := meta[k]; ok {
			d.DatName, d.UserName, d.Text, d.Command = *m[0], *m[1], m[2], *m[3]
		}
		out.Deltas = append(out.Deltas, d)
	}
	slices.SortFunc(out.Deltas, func(a, b observe.QueryDelta) int { return cmp.Compare(b.TotalMS, a.TotalMS) })
	if len(out.Deltas) > maxStatementDeltas {
		out.Truncated = len(out.Deltas) - maxStatementDeltas
		out.Deltas = out.Deltas[:maxStatementDeltas]
	}
	return out
}

func (w *worker) collectStatements(ctx context.Context) error {
	var (
		entries map[observe.QueryKey]observe.StatementCounters
		meta    map[observe.QueryKey][4]*string
		hidden  int
		dealloc *int64
		reset   *time.Time
	)
	if err := w.obs.ReadOnly(ctx, func(ctx context.Context, tx pgx.Tx, _ []int32) error {
		var err error
		if entries, meta, hidden, err = readStatements(ctx, tx, *w.caps, w.target.QueryTextEnabled, &w.stmtCache); err != nil {
			return err
		}
		dealloc, reset, err = targetdb.ReadStatementsInfo(ctx, tx, w.caps.StatementsSchema)
		return err
	}); err != nil {
		return err
	}
	cur := statementsState{at: time.Now().UTC(), statsReset: reset, dealloc: dealloc, entries: entries}
	coll := diffStatements(w.prevStmt, cur, meta, hidden)
	w.prevStmt = &cur

	var pts []observe.Point
	if coll.Interval != nil && *coll.Interval > 0 {
		var calls int64
		var ms float64
		for _, d := range coll.Deltas {
			calls += d.Calls
			ms += d.TotalMS
		}
		if coll.Truncated > 0 || (coll.Dealloc != nil && *coll.Dealloc > 0) {
			// Part of the activity is not in the stored deltas.
			pts = append(pts, observe.Point{Metric: "statements.calls.per_s"}, observe.Point{Metric: "statements.exec_ms.per_s"})
		} else {
			pts = append(pts,
				observe.Point{Metric: "statements.calls.per_s", IntervalSeconds: coll.Interval, Value: observe.F(float64(calls) / *coll.Interval)},
				observe.Point{Metric: "statements.exec_ms.per_s", IntervalSeconds: coll.Interval, Value: observe.F(ms / *coll.Interval)})
		}
	}
	w.state.mu.Lock()
	h := append(w.state.statements, coll)
	if len(h) > maxStatementHistory {
		h = h[len(h)-maxStatementHistory:]
	}
	w.state.statements = append([]observe.StatementsCollection(nil), h...)
	w.state.mu.Unlock()
	w.state.addPoints(coll.At, pts)

	id, at, text := w.target.ID, coll.At, w.target.QueryTextEnabled
	w.m.writer.Enqueue(WriteOp{TargetID: id, Collector: CollectorStatements, At: at,
		Write: func(ctx context.Context, s Store, cache repository.IdentityCache) error {
			if err := s.InsertStatements(ctx, id, coll, text, cache); err != nil {
				return err
			}
			return s.InsertPoints(ctx, id, at, pts)
		}})
	return nil
}
