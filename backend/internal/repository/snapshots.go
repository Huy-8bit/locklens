package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/domain/observe"
)

// SnapshotMeta describes a stored activity snapshot.
type SnapshotMeta struct {
	ID               int64     `json:"id"`
	At               time.Time `json:"at"`
	BlockedSessions  int       `json:"blocked_sessions"`
	RootBlockers     int       `json:"root_blockers"`
	LongTransactions int       `json:"long_transactions"`
}

// ListActivitySnapshots returns snapshot metadata in (from, to], newest first.
func (s *Store) ListActivitySnapshots(ctx context.Context, targetID int64, from, to time.Time, onlyBlocking bool, limit int) ([]SnapshotMeta, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, ts, blocked_sessions, root_blockers, long_transactions FROM activity_snapshots
WHERE target_id = $1 AND ts > $2 AND ts <= $3 AND (NOT $4 OR blocked_sessions > 0)
ORDER BY ts DESC LIMIT $5`, targetID, from.UTC(), to.UTC(), onlyBlocking, limit)
	if err != nil {
		return nil, s.red.Error(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (SnapshotMeta, error) {
		var m SnapshotMeta
		err := r.Scan(&m.ID, &m.At, &m.BlockedSessions, &m.RootBlockers, &m.LongTransactions)
		return m, err
	})
	if out == nil {
		out = []SnapshotMeta{}
	}
	return out, s.red.Error(err)
}

// GetActivitySnapshot returns a stored snapshot document.
func (s *Store) GetActivitySnapshot(ctx context.Context, targetID, id int64) (SnapshotMeta, json.RawMessage, error) {
	var m SnapshotMeta
	var raw json.RawMessage
	err := s.pool.QueryRow(ctx, `
SELECT id, ts, blocked_sessions, root_blockers, long_transactions, snapshot FROM activity_snapshots
WHERE target_id = $1 AND id = $2`, targetID, id).Scan(&m.ID, &m.At, &m.BlockedSessions, &m.RootBlockers, &m.LongTransactions, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, nil, ErrNotFound
	}
	return m, raw, s.red.Error(err)
}

// LatestActivitySnapshot returns the newest stored snapshot.
func (s *Store) LatestActivitySnapshot(ctx context.Context, targetID int64) (SnapshotMeta, json.RawMessage, error) {
	var m SnapshotMeta
	var raw json.RawMessage
	err := s.pool.QueryRow(ctx, `
SELECT id, ts, blocked_sessions, root_blockers, long_transactions, snapshot FROM activity_snapshots
WHERE target_id = $1 ORDER BY ts DESC LIMIT 1`, targetID).Scan(&m.ID, &m.At, &m.BlockedSessions, &m.RootBlockers, &m.LongTransactions, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, nil, ErrNotFound
	}
	return m, raw, s.red.Error(err)
}

// LatestState returns the newest state snapshot of a kind.
func (s *Store) LatestState(ctx context.Context, targetID int64, kind string) (time.Time, json.RawMessage, error) {
	var ts time.Time
	var raw json.RawMessage
	err := s.pool.QueryRow(ctx, `SELECT ts, payload FROM state_snapshots WHERE target_id = $1 AND kind = $2 ORDER BY ts DESC LIMIT 1`,
		targetID, kind).Scan(&ts, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return ts, nil, ErrNotFound
	}
	return ts, raw, s.red.Error(err)
}

// LatestTableHealth rebuilds the newest table health collection from rows.
func (s *Store) LatestTableHealth(ctx context.Context, targetID int64) (*observe.TableHealth, error) {
	ts, raw, err := s.LatestState(ctx, targetID, "table_health")
	if err != nil {
		return nil, err
	}
	var meta struct {
		DatName    string                `json:"datname"`
		SizeSource string                `json:"size_source"`
		Databases  []observe.DatabaseAge `json:"databases"`
		Settings   map[string]string     `json:"settings"`
		Warnings   []string              `json:"warnings"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, err
	}
	h := &observe.TableHealth{At: ts.UTC(), DatName: meta.DatName, SizeSource: meta.SizeSource, Databases: meta.Databases,
		Settings: meta.Settings, Warnings: meta.Warnings, Tables: []observe.TableStat{}, Indexes: []observe.IndexStat{}}
	rows, err := s.pool.Query(ctx, `
SELECT relid, datname, schemaname, relname, n_live_tup, n_dead_tup, n_mod_since_analyze, last_vacuum, last_autovacuum,
       last_analyze, last_autoanalyze, vacuum_count, autovacuum_count, analyze_count, autoanalyze_count,
       table_bytes, indexes_bytes, total_bytes, frozenxid_age
FROM table_stats WHERE target_id = $1 AND ts = $2 ORDER BY total_bytes DESC NULLS LAST`, targetID, ts)
	if err != nil {
		return nil, s.red.Error(err)
	}
	h.Tables, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (observe.TableStat, error) {
		var t observe.TableStat
		err := r.Scan(&t.RelID, &t.DatName, &t.Schema, &t.Name, &t.LiveTuples, &t.DeadTuples, &t.ModSinceAnalyze,
			&t.LastVacuum, &t.LastAutovacuum, &t.LastAnalyze, &t.LastAutoanalyze, &t.VacuumCount, &t.AutovacuumCount,
			&t.AnalyzeCount, &t.AutoanalyzeCount, &t.TableBytes, &t.IndexesBytes, &t.TotalBytes, &t.FrozenXIDAge)
		return t, err
	})
	if err != nil {
		return nil, s.red.Error(err)
	}
	rows, err = s.pool.Query(ctx, `
SELECT indexrelid, relid, schemaname, relname, indexrelname, idx_scan, index_bytes, is_unique, is_primary, is_constraint
FROM index_stats WHERE target_id = $1 AND ts = $2 ORDER BY index_bytes DESC NULLS LAST`, targetID, ts)
	if err != nil {
		return nil, s.red.Error(err)
	}
	h.Indexes, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (observe.IndexStat, error) {
		var i observe.IndexStat
		err := r.Scan(&i.IndexRelID, &i.RelID, &i.Schema, &i.Table, &i.Name, &i.Scans, &i.Bytes, &i.IsUnique, &i.IsPrimary, &i.IsConstraint)
		return i, err
	})
	if h.Tables == nil {
		h.Tables = []observe.TableStat{}
	}
	if h.Indexes == nil {
		h.Indexes = []observe.IndexStat{}
	}
	return h, s.red.Error(err)
}
