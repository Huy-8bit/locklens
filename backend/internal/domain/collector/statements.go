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

// maxStatementDeltas caps how many changed entries are stored per collection
// (largest execution-time deltas first). The rest are counted as truncated.
const maxStatementDeltas = 2000

// statementCounters is the cumulative state of one entry.
type statementCounters struct {
	calls   int64
	totalMS float64
	rows    int64
	hit     int64
	read    int64
	tempW   int64
	wal     *float64
}

// statementsState is what a worker remembers between collections.
type statementsState struct {
	at         time.Time
	statsReset *time.Time
	dealloc    *int64
	entries    map[observe.QueryKey]statementCounters
}

// readStatements reads pg_stat_statements. The normalised text is always read
// to classify each statement's command (select, insert, ...); it is kept only
// when the target collects query text, otherwise it is dropped here and never
// stored.
func readStatements(ctx context.Context, tx pgx.Tx, caps observe.Capabilities, includeText bool) (map[observe.QueryKey]statementCounters, map[observe.QueryKey][4]*string, int, error) {
	view := pgx.Identifier{caps.StatementsSchema, "pg_stat_statements"}.Sanitize()
	// Rows of other roles without pg_read_all_stats have a NULL queryid and
	// are counted as hidden.
	sql := `
SELECT s.userid, s.dbid, s.toplevel, s.queryid,
       coalesce(d.datname, s.dbid::text), coalesce(r.rolname, s.userid::text),
       s.calls, s.total_exec_time, s.rows, s.shared_blks_hit, s.shared_blks_read,
       s.temp_blks_written, s.wal_bytes::float8,
       s.query
FROM ` + view + ` AS s
LEFT JOIN pg_catalog.pg_database AS d ON d.oid = s.dbid
LEFT JOIN pg_catalog.pg_roles AS r ON r.oid = s.userid`
	rows, err := tx.Query(ctx, sql)
	if err != nil {
		return nil, nil, 0, err
	}
	entries := map[observe.QueryKey]statementCounters{}
	meta := map[observe.QueryKey][4]*string{}
	hidden := 0
	var (
		userid, dbid uint32
		toplevel     bool
		queryid      *int64
		datname, usr string
		c            statementCounters
		text         *string
	)
	major := caps.Major()
	_, err = pgx.ForEachRow(rows, []any{&userid, &dbid, &toplevel, &queryid, &datname, &usr,
		&c.calls, &c.totalMS, &c.rows, &c.hit, &c.read, &c.tempW, &c.wal, &text}, func() error {
		if queryid == nil {
			hidden++
			return nil
		}
		k := observe.QueryKey{ServerMajor: major, DBID: dbid, UserID: userid, TopLevel: toplevel, QueryID: *queryid}
		if prev, ok := entries[k]; ok {
			// Duplicate keys should not happen; keep the larger counters.
			if prev.calls >= c.calls {
				return nil
			}
		}
		entries[k] = c
		d, u, cmd := datname, usr, observe.CommandOther
		if text != nil {
			cmd = observe.ClassifyCommand(*text)
		}
		kept := text
		if !includeText {
			kept = nil
		}
		meta[k] = [4]*string{&d, &u, kept, &cmd}
		return nil
	})
	return entries, meta, hidden, err
}

func readStatementsInfo(ctx context.Context, tx pgx.Tx, caps observe.Capabilities) (*int64, *time.Time, error) {
	view := pgx.Identifier{caps.StatementsSchema, "pg_stat_statements_info"}.Sanitize()
	var dealloc int64
	var reset time.Time
	if err := tx.QueryRow(ctx, "SELECT dealloc, stats_reset FROM "+view).Scan(&dealloc, &reset); err != nil {
		return nil, nil, err
	}
	return &dealloc, &reset, nil
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
		if reset || !seen || c.calls < p.calls {
			d = observe.QueryDelta{Calls: c.calls, TotalMS: c.totalMS, Rows: c.rows, BlkHit: c.hit, BlkRead: c.read, TempW: c.tempW, WAL: c.wal}
		} else {
			d = observe.QueryDelta{Calls: c.calls - p.calls, TotalMS: c.totalMS - p.totalMS, Rows: c.rows - p.rows,
				BlkHit: c.hit - p.hit, BlkRead: c.read - p.read, TempW: c.tempW - p.tempW}
			if c.wal != nil && p.wal != nil {
				w := *c.wal - *p.wal
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
		entries map[observe.QueryKey]statementCounters
		meta    map[observe.QueryKey][4]*string
		hidden  int
		dealloc *int64
		reset   *time.Time
	)
	if err := w.obs.ReadOnly(ctx, func(ctx context.Context, tx pgx.Tx, _ []int32) error {
		var err error
		if entries, meta, hidden, err = readStatements(ctx, tx, *w.caps, w.target.QueryTextEnabled); err != nil {
			return err
		}
		dealloc, reset, err = readStatementsInfo(ctx, tx, *w.caps)
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
		Write: func(ctx context.Context, s *repository.Store, cache repository.IdentityCache) error {
			if err := s.InsertStatements(ctx, id, coll, text, cache); err != nil {
				return err
			}
			return s.InsertPoints(ctx, id, at, pts)
		}})
	return nil
}
