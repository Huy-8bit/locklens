package repository

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/repository/queries"
)

// SnapshotRepository is the store of stored activity snapshots and state snapshots.
type SnapshotRepository interface {
	// ListActivitySnapshots returns snapshot metadata in (from, to], newest first.
	ListActivitySnapshots(ctx context.Context, targetID int64, from, to time.Time, onlyBlocking bool, limit int) ([]SnapshotMeta, error)
	// GetActivitySnapshot returns a stored snapshot document.
	GetActivitySnapshot(ctx context.Context, targetID, id int64) (SnapshotMeta, json.RawMessage, error)
	// LatestActivitySnapshot returns the newest stored snapshot.
	LatestActivitySnapshot(ctx context.Context, targetID int64) (SnapshotMeta, json.RawMessage, error)
	// LatestState returns the newest state snapshot of a kind.
	LatestState(ctx context.Context, targetID int64, kind string) (time.Time, json.RawMessage, error)
	// LatestTableHealth rebuilds the newest table health collection of every
	// database from rows: the tables and indexes of all of them, with the
	// cluster-wide data (database ages, settings) of the main database.
	LatestTableHealth(ctx context.Context, targetID int64) (*observe.TableHealth, error)
}

var _ SnapshotRepository = (*Store)(nil)

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
	rows, err := s.pool.Query(ctx, queries.ListActivitySnapshots, targetID, from.UTC(), to.UTC(), onlyBlocking, limit)
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
	err := s.pool.QueryRow(ctx, queries.GetActivitySnapshot, targetID, id).Scan(&m.ID, &m.At, &m.BlockedSessions, &m.RootBlockers, &m.LongTransactions, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, nil, ErrNotFound
	}
	return m, raw, s.red.Error(err)
}

// LatestActivitySnapshot returns the newest stored snapshot.
func (s *Store) LatestActivitySnapshot(ctx context.Context, targetID int64) (SnapshotMeta, json.RawMessage, error) {
	var m SnapshotMeta
	var raw json.RawMessage
	err := s.pool.QueryRow(ctx, queries.LatestActivitySnapshot, targetID).Scan(&m.ID, &m.At, &m.BlockedSessions, &m.RootBlockers, &m.LongTransactions, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, nil, ErrNotFound
	}
	return m, raw, s.red.Error(err)
}

// LatestState returns the newest state snapshot of a kind.
func (s *Store) LatestState(ctx context.Context, targetID int64, kind string) (time.Time, json.RawMessage, error) {
	var ts time.Time
	var raw json.RawMessage
	err := s.pool.QueryRow(ctx, queries.LatestState,
		targetID, kind).Scan(&ts, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return ts, nil, ErrNotFound
	}
	return ts, raw, s.red.Error(err)
}

// LatestTableHealth rebuilds the newest table health collection of every
// database from rows: the tables and indexes of all of them, with the
// cluster-wide data (database ages, settings) of the main database.
func (s *Store) LatestTableHealth(ctx context.Context, targetID int64) (*observe.TableHealth, error) {
	rows, err := s.pool.Query(ctx, queries.LatestTableHealthStates, targetID)
	if err != nil {
		return nil, s.red.Error(err)
	}
	type state struct {
		ts   time.Time
		meta tableHealthMeta
	}
	states, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (state, error) {
		var st state
		var raw []byte
		if err := r.Scan(&st.ts, &raw); err != nil {
			return st, err
		}
		return st, json.Unmarshal(raw, &st.meta)
	})
	if err != nil {
		return nil, s.red.Error(err)
	}
	if len(states) == 0 {
		return nil, ErrNotFound
	}
	// The main database is the one that read cluster-wide data; the newest
	// collection stands in for it before one did.
	main, newest := -1, 0
	for i, st := range states {
		if st.ts.After(states[newest].ts) {
			newest = i
		}
		if len(st.meta.Databases) > 0 && (main < 0 || st.ts.After(states[main].ts)) {
			main = i
		}
	}
	if main < 0 {
		main = newest
	}
	m := states[main]
	h := &observe.TableHealth{At: m.ts.UTC(), DatName: m.meta.DatName, SizeSource: m.meta.SizeSource, Databases: m.meta.Databases,
		Settings: m.meta.Settings, Warnings: m.meta.Warnings, Tables: []observe.TableStat{}, Indexes: []observe.IndexStat{}}
	for _, st := range states {
		if st.ts.After(h.At) {
			h.At = st.ts.UTC()
		}
		tables, indexes, err := s.tableHealthRows(ctx, targetID, st.ts)
		if err != nil {
			return nil, err
		}
		h.Tables, h.Indexes = append(h.Tables, tables...), append(h.Indexes, indexes...)
	}
	slices.SortStableFunc(h.Tables, func(a, b observe.TableStat) int { return cmpDesc(a.TotalBytes, b.TotalBytes) })
	slices.SortStableFunc(h.Indexes, func(a, b observe.IndexStat) int { return cmpDesc(a.Bytes, b.Bytes) })
	return h, nil
}

// tableHealthMeta is the state snapshot of a table health collection.
type tableHealthMeta struct {
	DatName    string                `json:"datname"`
	SizeSource string                `json:"size_source"`
	Databases  []observe.DatabaseAge `json:"databases"`
	Settings   map[string]string     `json:"settings"`
	Warnings   []string              `json:"warnings"`
}

// tableHealthRows returns the table and index rows of one collection.
func (s *Store) tableHealthRows(ctx context.Context, targetID int64, ts time.Time) ([]observe.TableStat, []observe.IndexStat, error) {
	rows, err := s.pool.Query(ctx, queries.LatestTableStats, targetID, ts)
	if err != nil {
		return nil, nil, s.red.Error(err)
	}
	tables, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (observe.TableStat, error) {
		var t observe.TableStat
		err := r.Scan(&t.RelID, &t.DatName, &t.Schema, &t.Name, &t.LiveTuples, &t.DeadTuples, &t.ModSinceAnalyze,
			&t.LastVacuum, &t.LastAutovacuum, &t.LastAnalyze, &t.LastAutoanalyze, &t.VacuumCount, &t.AutovacuumCount,
			&t.AnalyzeCount, &t.AutoanalyzeCount, &t.TableBytes, &t.IndexesBytes, &t.TotalBytes, &t.FrozenXIDAge)
		return t, err
	})
	if err != nil {
		return nil, nil, s.red.Error(err)
	}
	rows, err = s.pool.Query(ctx, queries.LatestIndexStats, targetID, ts)
	if err != nil {
		return nil, nil, s.red.Error(err)
	}
	indexes, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (observe.IndexStat, error) {
		var i observe.IndexStat
		err := r.Scan(&i.IndexRelID, &i.RelID, &i.DatName, &i.Schema, &i.Table, &i.Name, &i.Scans, &i.Bytes, &i.IsUnique, &i.IsPrimary, &i.IsConstraint)
		return i, err
	})
	return tables, indexes, s.red.Error(err)
}

// cmpDesc orders known sizes largest first, unknown ones last.
func cmpDesc(a, b *int64) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return 1
	case b == nil:
		return -1
	}
	return cmp.Compare(*b, *a)
}
