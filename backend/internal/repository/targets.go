package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/locklens/locklens/internal/repository/queries"
)

// TargetRepository is the store of monitored targets, their status, and per-target counts.
type TargetRepository interface {
	// CreateTarget inserts a target and its initial status.
	CreateTarget(ctx context.Context, t Target) (Target, error)
	// UpdateTarget replaces the editable fields of an active target.
	UpdateTarget(ctx context.Context, t Target) (Target, error)
	// GetTarget returns an active (not deleted) target.
	GetTarget(ctx context.Context, id int64) (Target, error)
	// GetTargetByName returns an active target by case-insensitive name.
	GetTargetByName(ctx context.Context, name string) (Target, error)
	// ListTargets returns active targets ordered by name.
	ListTargets(ctx context.Context) ([]Target, error)
	// SoftDeleteTarget stops collection and hides the target. History and
	// incidents remain until retention or an explicit purge.
	SoftDeleteTarget(ctx context.Context, id int64) error
	// PurgeTarget permanently removes a target (active or soft-deleted) and its
	// history, deleting in batches so the store is never locked for long. With
	// keepIncidents, incidents survive with their target name and evidence;
	// otherwise they are removed too.
	PurgeTarget(ctx context.Context, id int64, keepIncidents bool) error
	// SaveTargetStatus overwrites the status row.
	SaveTargetStatus(ctx context.Context, st TargetStatus) error
	// SaveTargetStatuses overwrites many status rows in one round trip. When
	// the batch fails it writes them one by one, so one failing row (a target
	// deleted meanwhile) does not hold back the others; it returns the IDs
	// whose row could not be written and the last such error. With fences
	// (fences[i] for sts[i]), a row is written only while its target's lease
	// is held under the fence; the IDs of the others are returned as fenced.
	SaveTargetStatuses(ctx context.Context, sts []TargetStatus, fences []Fence) (failed, fenced []int64, err error)
	// GetTargetStatus returns the status of one target.
	GetTargetStatus(ctx context.Context, id int64) (TargetStatus, error)
	// ListTargetStatuses returns statuses keyed by target ID.
	ListTargetStatuses(ctx context.Context) (map[int64]TargetStatus, error)
	// CountsByTarget returns open incidents and active findings per target.
	CountsByTarget(ctx context.Context) (map[int64]TargetCounts, error)
}

var _ TargetRepository = (*Store)(nil)

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
	Environment      string
	// DatabaseScope says which databases per-database data is collected
	// from: DatabaseConnected (the DSN's), DatabaseAll, or DatabaseList
	// (Databases).
	DatabaseScope string
	Databases     []string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	DeletedAt     *time.Time
}

// envOrDefault stores targets without an environment label as "default".
func envOrDefault(e string) string {
	if e == "" {
		return "default"
	}
	return e
}

// Database scopes of a target.
const (
	DatabaseConnected = "connected"
	DatabaseAll       = "all"
	DatabaseList      = "list"
)

func scopeOrDefault(s string) string {
	if s == "" {
		return DatabaseConnected
	}
	return s
}

func databasesOrEmpty(d []string) []string {
	if d == nil {
		return []string{}
	}
	return d
}

// ErrNameTaken is returned when another active target has the same name.
var ErrNameTaken = errors.New("a target with this name already exists")

func scanTarget(row pgx.Row) (Target, error) {
	var t Target
	err := row.Scan(&t.ID, &t.Name, &t.Description, &t.DSNCiphertext, &t.DisplayHost, &t.DisplayPort,
		&t.DisplayDatabase, &t.DisplayUser, &t.Enabled, &t.IntervalSeconds, &t.QueryTextEnabled,
		&t.MaxConnections, &t.NodeExporterURL, &t.CreatedAt, &t.UpdatedAt, &t.DeletedAt, &t.Environment, &t.DatabaseScope, &t.Databases)
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
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		out, err = scanTarget(tx.QueryRow(ctx, queries.CreateTarget,
			t.Name, t.Description, t.DSNCiphertext, t.DisplayHost, t.DisplayPort, t.DisplayDatabase,
			t.DisplayUser, t.Enabled, t.IntervalSeconds, t.QueryTextEnabled, t.MaxConnections, t.NodeExporterURL, envOrDefault(t.Environment),
			scopeOrDefault(t.DatabaseScope), databasesOrEmpty(t.Databases)))
		if err != nil {
			return err
		}
		state := "pending"
		if !t.Enabled {
			state = "disabled"
		}
		_, err = tx.Exec(ctx, queries.InsertTargetStatus, out.ID, state)
		return err
	})
	if err != nil {
		return Target{}, s.mapWriteErr(err)
	}
	return out, nil
}

// UpdateTarget replaces the editable fields of an active target.
func (s *Store) UpdateTarget(ctx context.Context, t Target) (Target, error) {
	out, err := scanTarget(s.db.QueryRow(ctx, queries.UpdateTarget,
		t.ID, t.Name, t.Description, t.DSNCiphertext, t.DisplayHost, t.DisplayPort, t.DisplayDatabase,
		t.DisplayUser, t.Enabled, t.IntervalSeconds, t.QueryTextEnabled, t.MaxConnections, t.NodeExporterURL, envOrDefault(t.Environment),
		scopeOrDefault(t.DatabaseScope), databasesOrEmpty(t.Databases)))
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
	t, err := scanTarget(s.db.QueryRow(ctx, queries.GetTarget, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, s.red.Error(err)
}

// GetTargetByName returns an active target by case-insensitive name.
func (s *Store) GetTargetByName(ctx context.Context, name string) (Target, error) {
	t, err := scanTarget(s.db.QueryRow(ctx, queries.GetTargetByName, name))
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, s.red.Error(err)
}

// ListTargets returns active targets ordered by name.
func (s *Store) ListTargets(ctx context.Context) ([]Target, error) {
	rows, err := s.db.Query(ctx, queries.ListTargets)
	if err != nil {
		return nil, s.red.Error(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Target, error) { return scanTarget(r) })
	return out, s.red.Error(err)
}

// SoftDeleteTarget stops collection and hides the target. History and
// incidents remain until retention or an explicit purge.
func (s *Store) SoftDeleteTarget(ctx context.Context, id int64) error {
	tag, err := s.db.Exec(ctx, queries.SoftDeleteTarget, id)
	if err != nil {
		return s.red.Error(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	_, err = s.db.Exec(ctx, queries.DisableTargetStatus, id)
	return s.red.Error(err)
}

// PurgeTarget permanently removes a target (active or soft-deleted) and its
// history, deleting in batches so the store is never locked for long. With
// keepIncidents, incidents survive with their target name and evidence;
// otherwise they are removed too.
func (s *Store) PurgeTarget(ctx context.Context, id int64, keepIncidents bool) error {
	var exists bool
	if err := s.db.QueryRow(ctx, queries.TargetExists, id).Scan(&exists); err != nil {
		return s.red.Error(err)
	}
	if !exists {
		return ErrNotFound
	}
	if _, err := s.db.Exec(ctx, queries.DisableTargetForPurge, id); err != nil {
		return s.red.Error(err)
	}
	tables := queries.PurgeTables
	if !keepIncidents {
		tables = append([]string{"incidents"}, tables...)
	}
	for _, table := range tables {
		if _, err := s.deleteInBatches(ctx, queries.DeleteTargetRows(table), id); err != nil {
			return err
		}
	}
	if _, err := s.deleteInBatches(ctx, queries.DeleteTargetRows("query_identities"), id); err != nil {
		return err
	}
	_, err := s.db.Exec(ctx, queries.DeleteTarget, id)
	return s.red.Error(err)
}

// deleteInBatches repeats a batched delete (at most 5,000 rows) until a batch
// deletes fewer.
func (s *Store) deleteInBatches(ctx context.Context, sql string, args ...any) (int64, error) {
	var total int64
	for {
		tag, err := s.db.Exec(ctx, sql, args...)
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
	_, err := s.db.Exec(ctx, queries.SaveTargetStatus, statusArgs(st)...)
	return s.red.Error(err)
}

// SaveTargetStatuses overwrites many status rows in one round trip; see
// TargetRepository.
func (s *Store) SaveTargetStatuses(ctx context.Context, sts []TargetStatus, fences []Fence) (failed, fenced []int64, err error) {
	if len(sts) == 0 {
		return nil, nil, nil
	}
	b := &pgx.Batch{}
	for i, st := range sts {
		b.Queue(statusQuery(fences != nil), statusArgsFenced(st, fences, i)...)
	}
	br := s.db.SendBatch(ctx, b)
	var lost []int64
	var batchErr error
	for _, st := range sts {
		if fenced, err := s.statusResult(br, fences != nil); err != nil {
			batchErr = err
			break
		} else if fenced {
			lost = append(lost, st.TargetID)
		}
	}
	if err := br.Close(); batchErr == nil && err == nil {
		return nil, lost, nil
	}
	// One row failed (the batch is one transaction): write them one by one.
	lost = nil
	var last error
	for i, st := range sts {
		f, err := s.saveStatus(ctx, statusQuery(fences != nil), statusArgsFenced(st, fences, i), fences != nil)
		switch {
		case err != nil:
			failed, last = append(failed, st.TargetID), err
		case f:
			lost = append(lost, st.TargetID)
		}
	}
	return failed, lost, s.red.Error(last)
}

func statusQuery(fenced bool) string {
	if fenced {
		return queries.SaveTargetStatusFenced
	}
	return queries.SaveTargetStatus
}

func statusArgsFenced(st TargetStatus, fences []Fence, i int) []any {
	args := statusArgs(st)
	if fences != nil {
		args = append(args, fences[i].Holder, fences[i].Epoch)
	}
	return args
}

// saveStatus writes one status row; fenced when the fenced statement wrote
// no row.
func (s *Store) saveStatus(ctx context.Context, sql string, args []any, fenced bool) (bool, error) {
	if !fenced {
		_, err := s.db.Exec(ctx, sql, args...)
		return false, err
	}
	var id int64
	err := s.db.QueryRow(ctx, sql, args...).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}
	return false, err
}

// statusResult reads one status write of a batch: fenced when the fenced
// statement wrote no row.
func (s *Store) statusResult(br pgx.BatchResults, fenced bool) (bool, error) {
	if !fenced {
		_, err := br.Exec()
		return false, err
	}
	var id int64
	err := br.QueryRow().Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}
	return false, err
}

func statusArgs(st TargetStatus) []any {
	caps, colls := st.Capabilities, st.Collectors
	if caps == nil {
		caps = json.RawMessage("{}")
	}
	if colls == nil {
		colls = json.RawMessage("{}")
	}
	return []any{st.TargetID, st.State, st.ServerVersion, st.ServerVersionNum, st.IsStandby, caps, colls,
		st.LastAttemptAt, st.LastSuccessAt, st.LastError, st.LastErrorAt}
}

func scanStatus(row pgx.Row) (TargetStatus, error) {
	var st TargetStatus
	err := row.Scan(&st.TargetID, &st.State, &st.ServerVersion, &st.ServerVersionNum, &st.IsStandby,
		&st.Capabilities, &st.Collectors, &st.LastAttemptAt, &st.LastSuccessAt, &st.LastError, &st.LastErrorAt, &st.UpdatedAt)
	return st, err
}

// GetTargetStatus returns the status of one target.
func (s *Store) GetTargetStatus(ctx context.Context, id int64) (TargetStatus, error) {
	st, err := scanStatus(s.db.QueryRow(ctx, queries.GetTargetStatus, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return st, ErrNotFound
	}
	return st, s.red.Error(err)
}

// ListTargetStatuses returns statuses keyed by target ID.
func (s *Store) ListTargetStatuses(ctx context.Context) (map[int64]TargetStatus, error) {
	rows, err := s.db.Query(ctx, queries.ListTargetStatuses)
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

// TargetCounts are per-target counts for the fleet view.
type TargetCounts struct {
	OpenIncidents    int `json:"open_incidents"`
	CriticalFindings int `json:"critical_findings"`
	WarningFindings  int `json:"warning_findings"`
	InfoFindings     int `json:"info_findings"`
}

// CountsByTarget returns open incidents and active findings per target.
func (s *Store) CountsByTarget(ctx context.Context) (map[int64]TargetCounts, error) {
	out := map[int64]TargetCounts{}
	rows, err := s.db.Query(ctx, queries.OpenIncidentsByTarget)
	if err != nil {
		return nil, s.red.Error(err)
	}
	var id int64
	var n int
	if _, err := pgx.ForEachRow(rows, []any{&id, &n}, func() error {
		c := out[id]
		c.OpenIncidents = n
		out[id] = c
		return nil
	}); err != nil {
		return nil, s.red.Error(err)
	}
	rows, err = s.db.Query(ctx, queries.ActiveFindingsByTarget)
	if err != nil {
		return nil, s.red.Error(err)
	}
	var sev string
	_, err = pgx.ForEachRow(rows, []any{&id, &sev, &n}, func() error {
		c := out[id]
		switch sev {
		case "critical":
			c.CriticalFindings = n
		case "warning":
			c.WarningFindings = n
		default:
			c.InfoFindings = n
		}
		out[id] = c
		return nil
	})
	return out, s.red.Error(err)
}
