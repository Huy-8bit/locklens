package collector

import (
	"context"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/observe"
)

// Cumulative counters per database. The row with datid 0 (shared objects)
// exists from PostgreSQL 15; it is included like any other scope.
const sqlDatabaseCounters = `
SELECT coalesce(datid, 0)::bigint, xact_commit, xact_rollback, blks_read, blks_hit,
       temp_files, temp_bytes, deadlocks, stats_reset
FROM pg_catalog.pg_stat_database`

const sqlServerStart = `SELECT pg_catalog.pg_postmaster_start_time()`

// Up to PostgreSQL 16, checkpoint and background writer counters share one view.
const sqlBgwriter16 = `
SELECT checkpoints_timed, checkpoints_req, buffers_checkpoint, buffers_clean,
       maxwritten_clean, buffers_backend, buffers_alloc, stats_reset
FROM pg_catalog.pg_stat_bgwriter`

// From PostgreSQL 17, checkpoints moved to pg_stat_checkpointer and backend
// writes to pg_stat_io.
const sqlCheckpointer17 = `
SELECT num_timed, num_requested, buffers_written, stats_reset
FROM pg_catalog.pg_stat_checkpointer`

const sqlBgwriter17 = `
SELECT buffers_clean, maxwritten_clean, buffers_alloc, stats_reset
FROM pg_catalog.pg_stat_bgwriter`

// pg_stat_io (PostgreSQL 16+), summed over backend types, objects, and contexts.
const sqlStatIO = `
SELECT coalesce(sum(reads), 0)::float8, coalesce(sum(writes), 0)::float8,
       coalesce(sum(extends), 0)::float8, max(stats_reset)
FROM pg_catalog.pg_stat_io`

// WAL position. On a primary pg_current_wal_lsn() grows with WAL generation;
// on a standby the received position is used instead.
const sqlWALPosition = `
SELECT CASE WHEN pg_catalog.pg_is_in_recovery()
            THEN pg_catalog.pg_wal_lsn_diff(pg_catalog.pg_last_wal_receive_lsn(), '0/0')
            ELSE pg_catalog.pg_wal_lsn_diff(pg_catalog.pg_current_wal_lsn(), '0/0') END::float8`

const sqlArchiverCounters = `
SELECT archived_count, failed_count, stats_reset FROM pg_catalog.pg_stat_archiver`

// readDatabaseCounters reads all cumulative counters used by the database
// collector into one CounterSample. Scopes: "db:<oid>", "bgwriter",
// "checkpointer", "io", "wal", "archiver".
func readDatabaseCounters(ctx context.Context, tx pgx.Tx, caps observe.Capabilities) (CounterSample, error) {
	s := CounterSample{Scopes: map[string]ScopeCounters{}}
	if err := tx.QueryRow(ctx, sqlServerStart).Scan(&s.ServerStart); err != nil {
		return s, err
	}
	rows, err := tx.Query(ctx, sqlDatabaseCounters)
	if err != nil {
		return s, err
	}
	var (
		datid                                                   int64
		commit, rollback, read, hit, tempFiles, tempBytes, dead int64
		reset                                                   *time.Time
	)
	_, err = pgx.ForEachRow(rows, []any{&datid, &commit, &rollback, &read, &hit, &tempFiles, &tempBytes, &dead, &reset}, func() error {
		s.Scopes["db:"+strconv.FormatInt(datid, 10)] = ScopeCounters{StatsReset: reset, Values: map[string]float64{
			"xact_commit": float64(commit), "xact_rollback": float64(rollback), "blks_read": float64(read),
			"blks_hit": float64(hit), "temp_files": float64(tempFiles), "temp_bytes": float64(tempBytes),
			"deadlocks": float64(dead),
		}}
		return nil
	})
	if err != nil {
		return s, err
	}

	if caps.Checkpointer {
		var timed, req, written int64
		var r1, r2 *time.Time
		if err := tx.QueryRow(ctx, sqlCheckpointer17).Scan(&timed, &req, &written, &r1); err != nil {
			return s, err
		}
		s.Scopes["checkpointer"] = ScopeCounters{StatsReset: r1, Values: map[string]float64{
			"checkpoints_timed": float64(timed), "checkpoints_req": float64(req), "buffers_checkpoint": float64(written)}}
		var clean, maxw, alloc int64
		if err := tx.QueryRow(ctx, sqlBgwriter17).Scan(&clean, &maxw, &alloc, &r2); err != nil {
			return s, err
		}
		s.Scopes["bgwriter"] = ScopeCounters{StatsReset: r2, Values: map[string]float64{
			"buffers_clean": float64(clean), "maxwritten_clean": float64(maxw), "buffers_alloc": float64(alloc)}}
	} else {
		var timed, req, bcp, clean, maxw, backend, alloc int64
		var r *time.Time
		if err := tx.QueryRow(ctx, sqlBgwriter16).Scan(&timed, &req, &bcp, &clean, &maxw, &backend, &alloc, &r); err != nil {
			return s, err
		}
		s.Scopes["checkpointer"] = ScopeCounters{StatsReset: r, Values: map[string]float64{
			"checkpoints_timed": float64(timed), "checkpoints_req": float64(req), "buffers_checkpoint": float64(bcp)}}
		s.Scopes["bgwriter"] = ScopeCounters{StatsReset: r, Values: map[string]float64{
			"buffers_clean": float64(clean), "maxwritten_clean": float64(maxw), "buffers_alloc": float64(alloc),
			"buffers_backend": float64(backend)}}
	}

	if caps.StatIO {
		var reads, writes, extends float64
		var r *time.Time
		if err := tx.QueryRow(ctx, sqlStatIO).Scan(&reads, &writes, &extends, &r); err != nil {
			return s, err
		}
		s.Scopes["io"] = ScopeCounters{StatsReset: r, Values: map[string]float64{"reads": reads, "writes": writes, "extends": extends}}
	}

	var wal *float64
	if err := tx.QueryRow(ctx, sqlWALPosition).Scan(&wal); err != nil {
		return s, err
	}
	if wal != nil {
		s.Scopes["wal"] = ScopeCounters{Values: map[string]float64{"lsn_bytes": *wal}}
	}

	var archived, failed int64
	var ar *time.Time
	if err := tx.QueryRow(ctx, sqlArchiverCounters).Scan(&archived, &failed, &ar); err != nil {
		return s, err
	}
	s.Scopes["archiver"] = ScopeCounters{StatsReset: ar, Values: map[string]float64{"archived": float64(archived), "failed": float64(failed)}}
	return s, nil
}

// rateDef maps a counter to the metric derived from it.
type rateDef struct {
	counter string
	metric  string
	// delta stores the change per collection instead of a per-second rate,
	// for discrete events such as deadlocks.
	delta bool
}

var databaseRates = []rateDef{
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
func deriveDatabasePoints(prev *CounterSample, cur CounterSample, isStandby bool, maxGap time.Duration) ([]observe.Point, []string) {
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
		points = append(points, observe.Point{Metric: metric, Value: v})
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
