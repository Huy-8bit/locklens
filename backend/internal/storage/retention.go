package storage

import (
	"context"
	"fmt"
	"time"
)

// RetentionPolicy says how long data is kept.
type RetentionPolicy struct {
	// Raw history: points, samples, snapshots, table statistics.
	History time.Duration
	// Resolved incidents and their events.
	Incidents time.Duration
	// Resolved findings and collection gaps.
	Findings time.Duration
}

// RetentionResult reports deleted rows per table.
type RetentionResult map[string]int64

// historyTables are pruned by their ts column.
var historyTables = []string{"metric_points", "query_samples", "statement_collections", "activity_snapshots",
	"table_stats", "index_stats", "state_snapshots"}

// ApplyRetention deletes expired rows in batches of 5,000 with a short pause
// between batches, so it never holds long locks or starves collectors.
// Incidents keep their own evidence, so pruning raw history never changes an
// incident.
func (s *Store) ApplyRetention(ctx context.Context, p RetentionPolicy, now time.Time) (RetentionResult, error) {
	res := RetentionResult{}
	run := func(table, where string, args ...any) error {
		n, err := s.deleteInBatchesPaced(ctx, table, where, args...)
		res[table] += n
		return err
	}
	cutoff := now.Add(-p.History).UTC()
	for _, t := range historyTables {
		if err := run(t, "ts < $1", cutoff); err != nil {
			return res, err
		}
	}
	if err := run("query_identities", "last_seen < $1 AND NOT EXISTS (SELECT 1 FROM query_samples q WHERE q.identity_id = query_identities.id)", cutoff); err != nil {
		return res, err
	}
	icut := now.Add(-p.Incidents).UTC()
	if err := run("incident_events", "incident_id IN (SELECT id FROM incidents WHERE state = 'resolved' AND resolved_at < $1)", icut); err != nil {
		return res, err
	}
	if err := run("incidents", "state = 'resolved' AND resolved_at < $1", icut); err != nil {
		return res, err
	}
	fcut := now.Add(-p.Findings).UTC()
	if err := run("findings", "status = 'resolved' AND resolved_at < $1", fcut); err != nil {
		return res, err
	}
	if err := run("collection_gaps", "ended_at < $1", fcut); err != nil {
		return res, err
	}
	if err := run("auth_sessions", "expires_at < $1", now.UTC()); err != nil {
		return res, err
	}
	// Soft-deleted targets whose history has fully expired are removed.
	if err := run("targets", "deleted_at IS NOT NULL AND deleted_at < $1 AND NOT EXISTS (SELECT 1 FROM incidents i WHERE i.target_id = targets.id)", cutoff); err != nil {
		return res, err
	}
	return res, nil
}

func (s *Store) deleteInBatchesPaced(ctx context.Context, table, where string, args ...any) (int64, error) {
	var total int64
	sql := fmt.Sprintf("DELETE FROM %s WHERE ctid IN (SELECT ctid FROM %s WHERE %s LIMIT 5000)", table, table, where)
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
}

// TableSizes returns sizes of LockLens' tables (estimated row counts).
func (s *Store) TableSizes(ctx context.Context) ([]StorageStats, error) {
	rows, err := s.pool.Query(ctx, `
SELECT c.relname, pg_total_relation_size(c.oid), greatest(c.reltuples, 0)::bigint
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = current_schema() AND c.relkind = 'r'
ORDER BY 2 DESC`)
	if err != nil {
		return nil, s.red.Error(err)
	}
	defer rows.Close()
	var out []StorageStats
	for rows.Next() {
		var st StorageStats
		if err := rows.Scan(&st.Table, &st.Bytes, &st.Rows); err != nil {
			return nil, s.red.Error(err)
		}
		out = append(out, st)
	}
	return out, s.red.Error(rows.Err())
}
