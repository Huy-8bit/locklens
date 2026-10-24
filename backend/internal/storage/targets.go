package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Target is a monitored PostgreSQL instance as stored. DSNCiphertext is only
// ever decrypted by the collector and the connection test; it is never sent to
// clients.
type Target struct {
	ID               int64
	Name             string
	Description      string
	DSNCiphertext    []byte
	DisplayHost      string
	DisplayPort      int
	DisplayDatabase  string
	DisplayUser      string
	Enabled          bool
	IntervalSeconds  int
	QueryTextEnabled bool
	MaxConnections   int
	NodeExporterURL  *string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	DeletedAt        *time.Time
}

// ErrNameTaken is returned when another active target has the same name.
var ErrNameTaken = errors.New("a target with this name already exists")

const targetColumns = `id, name, description, dsn_ciphertext, display_host, display_port, display_database,
display_user, enabled, interval_seconds, query_text_enabled, max_connections, node_exporter_url,
created_at, updated_at, deleted_at`

func scanTarget(row pgx.Row) (Target, error) {
	var t Target
	err := row.Scan(&t.ID, &t.Name, &t.Description, &t.DSNCiphertext, &t.DisplayHost, &t.DisplayPort,
		&t.DisplayDatabase, &t.DisplayUser, &t.Enabled, &t.IntervalSeconds, &t.QueryTextEnabled,
		&t.MaxConnections, &t.NodeExporterURL, &t.CreatedAt, &t.UpdatedAt, &t.DeletedAt)
	return t, err
}

func (s *Store) mapWriteErr(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "targets_name_unique" {
		return ErrNameTaken
	}
	return s.red.Error(err)
}

// CreateTarget inserts a target and its initial status.
func (s *Store) CreateTarget(ctx context.Context, t Target) (Target, error) {
	var out Target
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		out, err = scanTarget(tx.QueryRow(ctx, `
INSERT INTO targets (name, description, dsn_ciphertext, display_host, display_port, display_database,
    display_user, enabled, interval_seconds, query_text_enabled, max_connections, node_exporter_url)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
RETURNING `+targetColumns,
			t.Name, t.Description, t.DSNCiphertext, t.DisplayHost, t.DisplayPort, t.DisplayDatabase,
			t.DisplayUser, t.Enabled, t.IntervalSeconds, t.QueryTextEnabled, t.MaxConnections, t.NodeExporterURL))
		if err != nil {
			return err
		}
		state := "pending"
		if !t.Enabled {
			state = "disabled"
		}
		_, err = tx.Exec(ctx, "INSERT INTO target_status (target_id, state) VALUES ($1, $2)", out.ID, state)
		return err
	})
	if err != nil {
		return Target{}, s.mapWriteErr(err)
	}
	return out, nil
}

// UpdateTarget replaces the editable fields of an active target.
func (s *Store) UpdateTarget(ctx context.Context, t Target) (Target, error) {
	out, err := scanTarget(s.pool.QueryRow(ctx, `
UPDATE targets SET name = $2, description = $3, dsn_ciphertext = $4, display_host = $5, display_port = $6,
    display_database = $7, display_user = $8, enabled = $9, interval_seconds = $10,
    query_text_enabled = $11, max_connections = $12, node_exporter_url = $13, updated_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING `+targetColumns,
		t.ID, t.Name, t.Description, t.DSNCiphertext, t.DisplayHost, t.DisplayPort, t.DisplayDatabase,
		t.DisplayUser, t.Enabled, t.IntervalSeconds, t.QueryTextEnabled, t.MaxConnections, t.NodeExporterURL))
	if errors.Is(err, pgx.ErrNoRows) {
		return Target{}, ErrNotFound
	}
	if err != nil {
		return Target{}, s.mapWriteErr(err)
	}
	return out, nil
}

// GetTarget returns an active (not deleted) target.
func (s *Store) GetTarget(ctx context.Context, id int64) (Target, error) {
	t, err := scanTarget(s.pool.QueryRow(ctx, "SELECT "+targetColumns+" FROM targets WHERE id = $1 AND deleted_at IS NULL", id))
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, s.red.Error(err)
}

// GetTargetByName returns an active target by case-insensitive name.
func (s *Store) GetTargetByName(ctx context.Context, name string) (Target, error) {
	t, err := scanTarget(s.pool.QueryRow(ctx, "SELECT "+targetColumns+" FROM targets WHERE lower(name) = lower($1) AND deleted_at IS NULL", name))
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, s.red.Error(err)
}

// ListTargets returns active targets ordered by name.
func (s *Store) ListTargets(ctx context.Context) ([]Target, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+targetColumns+" FROM targets WHERE deleted_at IS NULL ORDER BY lower(name)")
	if err != nil {
		return nil, s.red.Error(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Target, error) { return scanTarget(r) })
	return out, s.red.Error(err)
}

// SoftDeleteTarget stops collection and hides the target. History and
// incidents remain until retention or an explicit purge.
func (s *Store) SoftDeleteTarget(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, "UPDATE targets SET deleted_at = now(), enabled = false, updated_at = now() WHERE id = $1 AND deleted_at IS NULL", id)
	if err != nil {
		return s.red.Error(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	_, err = s.pool.Exec(ctx, "UPDATE target_status SET state = 'disabled', updated_at = now() WHERE target_id = $1", id)
	return s.red.Error(err)
}

// purgeTables lists history tables deleted in batches before the target row.
var purgeTables = []string{
	"metric_points", "query_samples", "statement_collections", "activity_snapshots",
	"table_stats", "index_stats", "state_snapshots", "collection_gaps", "findings",
}

// PurgeTarget permanently removes a target (active or soft-deleted) and its
// history, deleting in batches so the store is never locked for long. With
// keepIncidents, incidents survive with their target name and evidence;
// otherwise they are removed too.
func (s *Store) PurgeTarget(ctx context.Context, id int64, keepIncidents bool) error {
	var exists bool
	if err := s.pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM targets WHERE id = $1)", id).Scan(&exists); err != nil {
		return s.red.Error(err)
	}
	if !exists {
		return ErrNotFound
	}
	if _, err := s.pool.Exec(ctx, "UPDATE targets SET enabled = false, deleted_at = coalesce(deleted_at, now()) WHERE id = $1", id); err != nil {
		return s.red.Error(err)
	}
	tables := purgeTables
	if !keepIncidents {
		tables = append([]string{"incidents"}, tables...)
	}
	for _, table := range tables {
		if _, err := s.deleteInBatches(ctx, table, "target_id = $1", id); err != nil {
			return err
		}
	}
	if _, err := s.deleteInBatches(ctx, "query_identities", "target_id = $1", id); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, "DELETE FROM targets WHERE id = $1", id)
	return s.red.Error(err)
}

// deleteInBatches deletes matching rows 5,000 at a time and returns the count.
func (s *Store) deleteInBatches(ctx context.Context, table, where string, args ...any) (int64, error) {
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
		if err := ctx.Err(); err != nil {
			return total, err
		}
	}
}

// TargetStatus is the monitoring state of a target as last recorded.
type TargetStatus struct {
	TargetID         int64
	State            string
	ServerVersion    *string
	ServerVersionNum *int
	IsStandby        *bool
	Capabilities     json.RawMessage
	Collectors       json.RawMessage
	LastAttemptAt    *time.Time
	LastSuccessAt    *time.Time
	LastError        *string
	LastErrorAt      *time.Time
	UpdatedAt        time.Time
}

// SaveTargetStatus overwrites the status row.
func (s *Store) SaveTargetStatus(ctx context.Context, st TargetStatus) error {
	caps, colls := st.Capabilities, st.Collectors
	if caps == nil {
		caps = json.RawMessage("{}")
	}
	if colls == nil {
		colls = json.RawMessage("{}")
	}
	_, err := s.pool.Exec(ctx, `
INSERT INTO target_status (target_id, state, server_version, server_version_num, is_standby, capabilities,
    collectors, last_attempt_at, last_success_at, last_error, last_error_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, now())
ON CONFLICT (target_id) DO UPDATE SET state = EXCLUDED.state, server_version = EXCLUDED.server_version,
    server_version_num = EXCLUDED.server_version_num, is_standby = EXCLUDED.is_standby,
    capabilities = EXCLUDED.capabilities, collectors = EXCLUDED.collectors,
    last_attempt_at = EXCLUDED.last_attempt_at, last_success_at = EXCLUDED.last_success_at,
    last_error = EXCLUDED.last_error, last_error_at = EXCLUDED.last_error_at, updated_at = now()`,
		st.TargetID, st.State, st.ServerVersion, st.ServerVersionNum, st.IsStandby, caps, colls,
		st.LastAttemptAt, st.LastSuccessAt, st.LastError, st.LastErrorAt)
	return s.red.Error(err)
}

const statusColumns = `target_id, state, server_version, server_version_num, is_standby, capabilities, collectors,
last_attempt_at, last_success_at, last_error, last_error_at, updated_at`

func scanStatus(row pgx.Row) (TargetStatus, error) {
	var st TargetStatus
	err := row.Scan(&st.TargetID, &st.State, &st.ServerVersion, &st.ServerVersionNum, &st.IsStandby,
		&st.Capabilities, &st.Collectors, &st.LastAttemptAt, &st.LastSuccessAt, &st.LastError, &st.LastErrorAt, &st.UpdatedAt)
	return st, err
}

// GetTargetStatus returns the status of one target.
func (s *Store) GetTargetStatus(ctx context.Context, id int64) (TargetStatus, error) {
	st, err := scanStatus(s.pool.QueryRow(ctx, "SELECT "+statusColumns+" FROM target_status WHERE target_id = $1", id))
	if errors.Is(err, pgx.ErrNoRows) {
		return st, ErrNotFound
	}
	return st, s.red.Error(err)
}

// ListTargetStatuses returns statuses keyed by target ID.
func (s *Store) ListTargetStatuses(ctx context.Context) (map[int64]TargetStatus, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+statusColumns+" FROM target_status")
	if err != nil {
		return nil, s.red.Error(err)
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (TargetStatus, error) { return scanStatus(r) })
	if err != nil {
		return nil, s.red.Error(err)
	}
	out := make(map[int64]TargetStatus, len(list))
	for _, st := range list {
		out[st.TargetID] = st
	}
	return out, nil
}
