package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/locklens/locklens/internal/repository/queries"
)

// TimescaleRepository is the store of TimescaleDB setup and status of the store.
type TimescaleRepository interface {
	// SetupTimescale turns the history tables into TimescaleDB hypertables and,
	// with the Community license, compresses chunks older than compressAfter. It
	// is idempotent and runs after migrations on every start, so a store that
	// gains the extension later is converted then. Existing rows are moved into
	// chunks, which takes a while on a large store (once).
	SetupTimescale(ctx context.Context, mode TimescaleMode, compressAfter time.Duration) (TimescaleStatus, error)
	// Timescale returns the status found by SetupTimescale.
	Timescale() TimescaleStatus
}

var _ TimescaleRepository = (*Store)(nil)

// TimescaleMode chooses whether the store uses TimescaleDB.
type TimescaleMode string

// Modes: auto uses TimescaleDB when the server has it (and falls back to
// plain PostgreSQL otherwise), on requires it, off never uses it.
const (
	TimescaleAuto TimescaleMode = "auto"
	TimescaleOn   TimescaleMode = "on"
	TimescaleOff  TimescaleMode = "off"
)

// TimescaleStatus says how the store keeps its history.
type TimescaleStatus struct {
	Enabled bool   `json:"enabled"`
	Version string `json:"version,omitempty"`
	// License is "timescale" (Community: compression available) or
	// "apache" (hypertables and chunk retention only).
	License     string `json:"license,omitempty"`
	Compression bool   `json:"compression"`
	// CompressAfter is how old a chunk is before it is compressed.
	CompressAfterSeconds float64 `json:"compress_after_seconds,omitempty"`
	// Reason explains why TimescaleDB is not used.
	Reason      string   `json:"reason,omitempty"`
	Hypertables []string `json:"hypertables,omitempty"`
}

// hypertable describes how one history table is partitioned and compressed.
// Every table here has a ts column that is part of its primary key (or gets
// one, see activity_snapshots) and is only appended to.
type hypertable struct {
	table     string
	chunk     time.Duration
	segmentBy string
}

// hypertables are the history tables kept by time. Segmenting by the columns
// queries filter on keeps compressed reads selective: a chart reads one
// target's metric, not the whole chunk.
var hypertables = []hypertable{
	{"metric_points", 24 * time.Hour, "target_id, metric, label"},
	{"query_samples", 24 * time.Hour, "target_id, identity_id"},
	{"statement_collections", 7 * 24 * time.Hour, "target_id"},
	{"activity_snapshots", 24 * time.Hour, "target_id"},
	{"table_stats", 7 * 24 * time.Hour, "target_id, relid"},
	{"index_stats", 7 * 24 * time.Hour, "target_id, indexrelid"},
	{"state_snapshots", 7 * 24 * time.Hour, "target_id, kind"},
	{"load_samples", 24 * time.Hour, "target_id, wait_class"},
	{"load_buckets", 7 * 24 * time.Hour, "target_id"},
	{"query_rollups", 7 * 24 * time.Hour, "target_id, identity_id"},
	{"statement_coverage", 30 * 24 * time.Hour, "target_id"},
	{"metric_rollups_1m", 7 * 24 * time.Hour, "target_id, metric, label"},
	{"metric_rollups_1h", 30 * 24 * time.Hour, "target_id, metric, label"},
	{"log_events", 24 * time.Hour, "target_id, kind"},
}

// timescaleLockKey serialises setup between LockLens instances sharing a store.
const timescaleLockKey = 7340012412

// SetupTimescale turns the history tables into TimescaleDB hypertables and,
// with the Community license, compresses chunks older than compressAfter. It
// is idempotent and runs after migrations on every start, so a store that
// gains the extension later is converted then. Existing rows are moved into
// chunks, which takes a while on a large store (once).
func (s *Store) SetupTimescale(ctx context.Context, mode TimescaleMode, compressAfter time.Duration) (TimescaleStatus, error) {
	st := TimescaleStatus{}
	if mode == TimescaleOff {
		st.Reason = "turned off (LOCKLENS_STORE_TIMESCALE=off)"
		s.timescale = st
		return st, nil
	}
	fail := func(reason string, err error) (TimescaleStatus, error) {
		st.Reason = reason
		s.timescale = st
		if mode == TimescaleOn {
			if err == nil {
				err = errors.New(reason)
			}
			return st, fmt.Errorf("TimescaleDB is required (LOCKLENS_STORE_TIMESCALE=on): %w", err)
		}
		return st, nil
	}
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return st, s.red.Error(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, queries.AdvisoryLock, timescaleLockKey); err != nil {
		return st, s.red.Error(err)
	}
	defer conn.Exec(context.WithoutCancel(ctx), queries.AdvisoryUnlock, timescaleLockKey) //nolint:errcheck

	var available bool
	var preload string
	if err := conn.QueryRow(ctx, queries.TimescaleAvailable).Scan(&available, &preload); err != nil {
		return st, s.red.Error(err)
	}
	if !available {
		return fail("the store's PostgreSQL server does not have the timescaledb extension", nil)
	}
	if !strings.Contains(preload, "timescaledb") {
		return fail("timescaledb is installed but not in shared_preload_libraries", nil)
	}
	if _, err := conn.Exec(ctx, queries.CreateTimescaleExtension); err != nil {
		return fail("could not create the timescaledb extension (it needs a superuser once: CREATE EXTENSION timescaledb)", s.red.Error(err))
	}
	if err := conn.QueryRow(ctx, queries.TimescaleVersion).
		Scan(&st.Version, &st.License); err != nil {
		return st, s.red.Error(err)
	}
	st.Enabled = true
	st.Compression = st.License == "timescale" && compressAfter > 0
	if st.Compression {
		st.CompressAfterSeconds = compressAfter.Seconds()
	}

	existing := map[string]bool{}
	rows, err := conn.Query(ctx, queries.ExistingHypertables)
	if err != nil {
		return st, s.red.Error(err)
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return st, s.red.Error(err)
	}
	for _, n := range names {
		existing[n] = true
	}
	for _, h := range hypertables {
		if !existing[h.table] {
			if err := s.convert(ctx, conn, h); err != nil {
				return st, fmt.Errorf("convert %s to a hypertable: %w", h.table, err)
			}
		}
		if st.Compression {
			if err := s.compress(ctx, conn, h, compressAfter); err != nil {
				return st, fmt.Errorf("compression for %s: %w", h.table, err)
			}
		}
		st.Hypertables = append(st.Hypertables, h.table)
	}
	s.timescale = st
	return st, nil
}

// convert makes one table a hypertable, moving its rows into chunks.
func (s *Store) convert(ctx context.Context, conn *pgxpool.Conn, h hypertable) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return s.red.Error(err)
	}
	defer tx.Rollback(context.WithoutCancel(ctx)) //nolint:errcheck
	if h.table == "activity_snapshots" {
		// A hypertable's unique keys must include its time column; snapshots
		// are still looked up by id (with the target).
		if _, err := tx.Exec(ctx, queries.DropActivitySnapshotsPrimaryKey); err != nil {
			return s.red.Error(err)
		}
	}
	if _, err := tx.Exec(ctx, queries.CreateHypertable, h.table, h.chunk); err != nil {
		return s.red.Error(err)
	}
	return s.red.Error(tx.Commit(ctx))
}

// compress enables compression on a hypertable and its background policy.
func (s *Store) compress(ctx context.Context, conn *pgxpool.Conn, h hypertable, after time.Duration) error {
	var on bool
	if err := conn.QueryRow(ctx, queries.CompressionEnabled, h.table).Scan(&on); err != nil {
		return s.red.Error(err)
	}
	if !on {
		// The identifiers are fixed in this file, never input.
		if _, err := conn.Exec(ctx, queries.EnableCompression(h.table, h.segmentBy)); err != nil {
			return s.red.Error(err)
		}
	}
	// Replace the policy when the delay changed.
	if _, err := conn.Exec(ctx, queries.RemoveCompressionPolicy, h.table); err != nil {
		return s.red.Error(err)
	}
	if _, err := conn.Exec(ctx, queries.AddCompressionPolicy, h.table, after); err != nil {
		return s.red.Error(err)
	}
	return nil
}

// Timescale returns the status found by SetupTimescale.
func (s *Store) Timescale() TimescaleStatus { return s.timescale }

func (s *Store) isHypertable(table string) bool {
	if !s.timescale.Enabled {
		return false
	}
	for _, h := range s.timescale.Hypertables {
		if h == table {
			return true
		}
	}
	return false
}
