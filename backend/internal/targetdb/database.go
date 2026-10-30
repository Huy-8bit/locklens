package targetdb

import (
	"context"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/targetdb/queries"
)

// ReadDatabaseCounters reads all cumulative counters used by the database
// collector into one observe.CounterSample. Scopes: "db:<oid>", "bgwriter",
// "checkpointer", "io", "wal", "archiver".
func ReadDatabaseCounters(ctx context.Context, tx pgx.Tx, caps observe.Capabilities) (observe.CounterSample, error) {
	s := observe.CounterSample{Scopes: map[string]observe.ScopeCounters{}}
	if err := tx.QueryRow(ctx, queries.ServerStart).Scan(&s.ServerStart); err != nil {
		return s, err
	}
	rows, err := tx.Query(ctx, queries.DatabaseCounters)
	if err != nil {
		return s, err
	}
	var (
		datid                                                   int64
		commit, rollback, read, hit, tempFiles, tempBytes, dead int64
		returned, fetched, inserted, updated, deleted           int64
		reset                                                   *time.Time
	)
	_, err = pgx.ForEachRow(rows, []any{&datid, &commit, &rollback, &read, &hit, &tempFiles, &tempBytes, &dead, &returned, &fetched, &inserted, &updated, &deleted, &reset}, func() error {
		s.Scopes["db:"+strconv.FormatInt(datid, 10)] = observe.ScopeCounters{StatsReset: reset, Values: map[string]float64{
			"xact_commit": float64(commit), "xact_rollback": float64(rollback), "blks_read": float64(read),
			"blks_hit": float64(hit), "temp_files": float64(tempFiles), "temp_bytes": float64(tempBytes),
			"deadlocks": float64(dead), "tup_returned": float64(returned), "tup_fetched": float64(fetched), "tup_inserted": float64(inserted), "tup_updated": float64(updated), "tup_deleted": float64(deleted),
		}}
		return nil
	})
	if err != nil {
		return s, err
	}

	if caps.Checkpointer {
		var timed, req, written int64
		var r1, r2 *time.Time
		if err := tx.QueryRow(ctx, queries.Checkpointer17).Scan(&timed, &req, &written, &r1); err != nil {
			return s, err
		}
		s.Scopes["checkpointer"] = observe.ScopeCounters{StatsReset: r1, Values: map[string]float64{
			"checkpoints_timed": float64(timed), "checkpoints_req": float64(req), "buffers_checkpoint": float64(written)}}
		var clean, maxw, alloc int64
		if err := tx.QueryRow(ctx, queries.Bgwriter17).Scan(&clean, &maxw, &alloc, &r2); err != nil {
			return s, err
		}
		s.Scopes["bgwriter"] = observe.ScopeCounters{StatsReset: r2, Values: map[string]float64{
			"buffers_clean": float64(clean), "maxwritten_clean": float64(maxw), "buffers_alloc": float64(alloc)}}
	} else {
		var timed, req, bcp, clean, maxw, backend, alloc int64
		var r *time.Time
		if err := tx.QueryRow(ctx, queries.Bgwriter16).Scan(&timed, &req, &bcp, &clean, &maxw, &backend, &alloc, &r); err != nil {
			return s, err
		}
		s.Scopes["checkpointer"] = observe.ScopeCounters{StatsReset: r, Values: map[string]float64{
			"checkpoints_timed": float64(timed), "checkpoints_req": float64(req), "buffers_checkpoint": float64(bcp)}}
		s.Scopes["bgwriter"] = observe.ScopeCounters{StatsReset: r, Values: map[string]float64{
			"buffers_clean": float64(clean), "maxwritten_clean": float64(maxw), "buffers_alloc": float64(alloc),
			"buffers_backend": float64(backend)}}
	}

	if caps.StatIO {
		var reads, writes, extends float64
		var r *time.Time
		if err := tx.QueryRow(ctx, queries.StatIO).Scan(&reads, &writes, &extends, &r); err != nil {
			return s, err
		}
		s.Scopes["io"] = observe.ScopeCounters{StatsReset: r, Values: map[string]float64{"reads": reads, "writes": writes, "extends": extends}}
	}

	var wal *float64
	if err := tx.QueryRow(ctx, queries.WALPosition).Scan(&wal); err != nil {
		return s, err
	}
	if wal != nil {
		s.Scopes["wal"] = observe.ScopeCounters{Values: map[string]float64{"lsn_bytes": *wal}}
	}

	var archived, failed int64
	var ar *time.Time
	if err := tx.QueryRow(ctx, queries.ArchiverCounters).Scan(&archived, &failed, &ar); err != nil {
		return s, err
	}
	s.Scopes["archiver"] = observe.ScopeCounters{StatsReset: ar, Values: map[string]float64{"archived": float64(archived), "failed": float64(failed)}}
	return s, nil
}
