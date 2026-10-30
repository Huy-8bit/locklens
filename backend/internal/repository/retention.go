package repository

import (
	"context"
	"time"

	"github.com/locklens/locklens/internal/repository/queries"
)

// RetentionRepository is the store of expiry of old data and the size of the store.
type RetentionRepository interface {
	// ApplyRetention deletes expired rows in batches of 5,000 with a short pause
	// between batches, so it never holds long locks or starves collectors.
	// Incidents keep their own evidence, so pruning raw history never changes an
	// incident.
	ApplyRetention(ctx context.Context, p RetentionPolicy, now time.Time) (RetentionResult, error)
	// TableSizes returns sizes of LockLens' tables (estimated row counts).
	TableSizes(ctx context.Context) ([]StorageStats, error)
}

var _ RetentionRepository = (*Store)(nil)

// RetentionPolicy says how long data is kept.
type RetentionPolicy struct {
	// Raw history: points, samples, snapshots, table statistics.
	History time.Duration
	// Resolved incidents and their events.
	Incidents time.Duration
	// Resolved findings and collection gaps.
	Findings time.Duration
	// Statement rollups (query baselines); at least History.
	Rollups time.Duration
	// One-minute and one-hour metric rollups; at least History (and the
	// hourly ones at least as long as the one-minute ones).
	MetricMinute time.Duration
	MetricHour   time.Duration
}

// RetentionResult reports deleted rows per table.
type RetentionResult map[string]int64

// ApplyRetention deletes expired rows in batches of 5,000 with a short pause
// between batches, so it never holds long locks or starves collectors.
// Incidents keep their own evidence, so pruning raw history never changes an
// incident.
func (s *Store) ApplyRetention(ctx context.Context, p RetentionPolicy, now time.Time) (RetentionResult, error) {
	res := RetentionResult{}
	run := func(table, sql string, args ...any) error {
		n, err := s.deleteInBatchesPaced(ctx, sql, args...)
		res[table] += n
		return err
	}
	cutoff := now.Add(-p.History).UTC()
	for _, t := range queries.HistoryTables {
		if s.isHypertable(t) {
			// Whole chunks older than the cutoff are dropped: no row-by-row
			// delete, no bloat. Rows of the chunk that straddles the cutoff
			// stay until that chunk ends (at most one chunk interval longer).
			var n int64
			if err := s.pool.QueryRow(ctx, queries.DropChunksOlderThan, t, cutoff).Scan(&n); err != nil {
				return res, s.red.Error(err)
			}
			res[t+" (chunks)"] += n
			continue
		}
		if err := run(t, queries.DeleteOlderThan(t), cutoff); err != nil {
			return res, err
		}
	}
	// prune removes rows older than cutoff: whole chunks of a hypertable,
	// batches otherwise.
	prune := func(t string, cutoff time.Time) error {
		if s.isHypertable(t) {
			var n int64
			if err := s.pool.QueryRow(ctx, queries.DropChunksOlderThan, t, cutoff).Scan(&n); err != nil {
				return s.red.Error(err)
			}
			res[t+" (chunks)"] += n
			return nil
		}
		return run(t, queries.DeleteOlderThan(t), cutoff)
	}
	rcut := now.Add(-max(p.Rollups, p.History)).UTC()
	for _, t := range queries.RollupTables {
		if err := prune(t, rcut); err != nil {
			return res, err
		}
	}
	minute := max(p.MetricMinute, p.History)
	if err := prune(queries.MinuteRollupTable, now.Add(-minute).UTC()); err != nil {
		return res, err
	}
	if err := prune(queries.HourRollupTable, now.Add(-max(p.MetricHour, minute)).UTC()); err != nil {
		return res, err
	}
	// Identities stay while rollups or saved plans refer to them (baselines
	// and plans outlive samples). Plans are kept as long as incidents.
	if err := run("query_plans", queries.PurgeOldPlans, now.Add(-p.Incidents).UTC()); err != nil {
		return res, err
	}
	if err := run("query_identities", queries.PurgeUnreferencedIdentities, cutoff); err != nil {
		return res, err
	}
	icut := now.Add(-p.Incidents).UTC()
	if err := run("incident_events", queries.PurgeResolvedIncidentEvents, icut); err != nil {
		return res, err
	}
	if err := run("incidents", queries.PurgeResolvedIncidents, icut); err != nil {
		return res, err
	}
	fcut := now.Add(-p.Findings).UTC()
	if err := run("findings", queries.PurgeResolvedFindings, fcut); err != nil {
		return res, err
	}
	if err := run("recommendations", queries.PurgeClosedRecommendations, fcut); err != nil {
		return res, err
	}
	if err := run("collection_gaps", queries.PurgeOldGaps, fcut); err != nil {
		return res, err
	}
	// Changes explain incidents, so they are kept as long as incidents.
	if err := run("change_events", queries.PurgeOldChanges, icut); err != nil {
		return res, err
	}
	if err := run("notification_deliveries", queries.PurgeOldDeliveries, fcut); err != nil {
		return res, err
	}
	if err := run("notification_silences", queries.PurgeEndedSilences, fcut); err != nil {
		return res, err
	}
	if err := run("auth_sessions", queries.PurgeExpiredSessions, now.UTC()); err != nil {
		return res, err
	}
	// Soft-deleted targets whose history has fully expired are removed.
	if err := run("targets", queries.PurgeDeletedTargets, cutoff); err != nil {
		return res, err
	}
	return res, nil
}

// deleteInBatchesPaced repeats a batched delete (at most 5,000 rows) until a
// batch deletes fewer, pausing between batches.
func (s *Store) deleteInBatchesPaced(ctx context.Context, sql string, args ...any) (int64, error) {
	var total int64
	for {
		tag, err := s.pool.Exec(ctx, sql, args...)
		if err != nil {
			return total, s.red.Error(err)
		}
		total += tag.RowsAffected()
		if tag.RowsAffected() < 5000 {
			return total, nil
		}
		select {
		case <-ctx.Done():
			return total, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// StorageStats reports the size of the main tables.
type StorageStats struct {
	Table string `json:"table"`
	Bytes int64  `json:"bytes"`
	Rows  int64  `json:"estimated_rows"`
	// CompressedBefore and CompressedAfter are the sizes of the compressed
	// chunks before and after compression (TimescaleDB); nil when nothing
	// is compressed.
	CompressedBefore *int64 `json:"compressed_before_bytes,omitempty"`
	CompressedAfter  *int64 `json:"compressed_after_bytes,omitempty"`
}

// TableSizes returns sizes of LockLens' tables (estimated row counts).
func (s *Store) TableSizes(ctx context.Context) ([]StorageStats, error) {
	q := queries.TableSizes
	if s.timescale.Enabled {
		q = queries.TableSizesTimescale
	}
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, s.red.Error(err)
	}
	defer rows.Close()
	var out []StorageStats
	for rows.Next() {
		var st StorageStats
		if err := rows.Scan(&st.Table, &st.Bytes, &st.Rows, &st.CompressedBefore, &st.CompressedAfter); err != nil {
			return nil, s.red.Error(err)
		}
		out = append(out, st)
	}
	return out, s.red.Error(rows.Err())
}
