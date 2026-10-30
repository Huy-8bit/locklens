package collector

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/repository"
	"github.com/locklens/locklens/internal/targetdb"
)

// rateDef maps a counter to the metric derived from it.
type rateDef struct {
	counter string
	metric  string
	// delta stores the change per collection instead of a per-second rate,
	// for discrete events such as deadlocks.
	delta bool
}

var databaseRates = []rateDef{
	{"tup_returned", "tuples.returned.per_s", false},
	{"tup_fetched", "tuples.fetched.per_s", false},
	{"tup_inserted", "tuples.inserted.per_s", false},
	{"tup_updated", "tuples.updated.per_s", false},
	{"tup_deleted", "tuples.deleted.per_s", false},

	{"xact_commit", "xact.commit.per_s", false},
	{"xact_rollback", "xact.rollback.per_s", false},
	{"blks_read", "blocks.read.per_s", false},
	{"blks_hit", "blocks.hit.per_s", false},
	{"temp_files", "temp.files.per_s", false},
	{"temp_bytes", "temp.bytes.per_s", false},
	{"deadlocks", "deadlocks.delta", true},
	{"checkpoints_timed", "checkpoints.timed.delta", true},
	{"checkpoints_req", "checkpoints.requested.delta", true},
	{"buffers_checkpoint", "checkpoint.buffers_written.per_s", false},
	{"buffers_clean", "bgwriter.buffers_clean.per_s", false},
	{"maxwritten_clean", "bgwriter.maxwritten_clean.delta", true},
	{"buffers_alloc", "buffers.alloc.per_s", false},
	{"buffers_backend", "buffers.backend.per_s", false},
	{"reads", "io.reads.per_s", false},
	{"writes", "io.writes.per_s", false},
	{"extends", "io.extends.per_s", false},
	{"archived", "archiver.archived.delta", true},
	{"failed", "archiver.failed.delta", true},
}

// deriveDatabasePoints turns two counter samples into points. A counter
// absent from the current sample (for example buffers_backend on 17+) yields
// no point; a counter whose change is unknown yields a NULL point.
func deriveDatabasePoints(prev *observe.CounterSample, cur observe.CounterSample, isStandby bool, maxGap time.Duration) ([]observe.Point, []string) {
	present := map[string]bool{}
	for _, sc := range cur.Scopes {
		for k := range sc.Values {
			present[k] = true
		}
	}
	var points []observe.Point
	reasons := map[string]bool{}
	add := func(metric string, d DeltaResult, delta bool) {
		var v *float64
		if delta {
			v = d.Delta
		} else {
			v = d.Rate()
		}
		if v == nil && d.Reason != "" {
			reasons[d.Reason] = true
		}
		var interval *float64
		if d.Elapsed > 0 {
			interval = observe.F(d.Elapsed.Seconds())
		}
		points = append(points, observe.Point{Metric: metric, Value: v, IntervalSeconds: interval})
	}
	for _, def := range databaseRates {
		if !present[def.counter] {
			continue
		}
		add(def.metric, Delta(prev, cur, def.counter, maxGap), def.delta)
	}
	hit := Delta(prev, cur, "blks_hit", maxGap)
	read := Delta(prev, cur, "blks_read", maxGap)
	points = append(points, observe.Point{Metric: "cache.hit_ratio", Value: Ratio(hit, read)})
	if present["lsn_bytes"] {
		metric := "wal.bytes.per_s"
		if isStandby {
			metric = "wal.received_bytes.per_s"
		}
		add(metric, Delta(prev, cur, "lsn_bytes", maxGap), false)
	}
	var rs []string
	for r := range reasons {
		rs = append(rs, r)
	}
	return points, rs
}

func (w *worker) collectDatabase(ctx context.Context) error {
	if err := w.refreshCapabilities(ctx); err != nil {
		return err
	}
	var cur observe.CounterSample
	if err := w.obs.ReadOnly(ctx, func(ctx context.Context, tx pgx.Tx, _ []int32) error {
		var err error
		cur, err = targetdb.ReadDatabaseCounters(ctx, tx, *w.caps)
		return err
	}); err != nil {
		return err
	}
	cur.At = time.Now().UTC()
	maxGap := 3 * w.jobByName(CollectorDatabase).every
	points, reasons := deriveDatabasePoints(w.prevDB, cur, w.caps.IsStandby, maxGap)
	prev := w.prevDB
	w.prevDB = &cur
	w.state.addPoints(cur.At, points)
	id, at := w.target.ID, cur.At
	w.m.writer.Enqueue(WriteOp{TargetID: id, Collector: CollectorDatabase, At: at,
		Write: func(ctx context.Context, s Repositories, _ repository.IdentityCache) error {
			return s.InsertPoints(ctx, id, at, points)
		}})
	if prev != nil && (slices.Contains(reasons, ReasonRestart) || slices.Contains(reasons, ReasonStatsReset) || slices.Contains(reasons, ReasonGap)) {
		from := prev.At
		reason := "counter rates unknown: " + strings.Join(reasons, ", ")
		w.m.writer.Enqueue(WriteOp{TargetID: id, Collector: CollectorDatabase, At: at,
			Write: func(ctx context.Context, s Repositories, _ repository.IdentityCache) error {
				return s.InsertGap(ctx, id, CollectorDatabase, from, at, reason)
			}})
	}
	return nil
}
