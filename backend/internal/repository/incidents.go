package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Incident is a stored incident.
type Incident struct {
	ID             int64           `json:"id"`
	TargetID       *int64          `json:"target_id"`
	TargetName     string          `json:"target_name"`
	RuleID         string          `json:"rule_id"`
	RuleVersion    int             `json:"rule_version"`
	Severity       string          `json:"severity"`
	Title          string          `json:"title"`
	State          string          `json:"state"`
	OpenedAt       time.Time       `json:"opened_at"`
	LastSeenAt     time.Time       `json:"last_seen_at"`
	ResolvedAt     *time.Time      `json:"resolved_at"`
	Resolution     *string         `json:"resolution"`
	TriggerConfig  json.RawMessage `json:"trigger_config"`
	AcknowledgedAt *time.Time      `json:"acknowledged_at"`
	AcknowledgedBy *string         `json:"acknowledged_by"`
	AckNote        *string         `json:"ack_note"`
	EventCount     int             `json:"event_count"`
	SizeBytes      int64           `json:"size_bytes"`
	Truncated      bool            `json:"truncated"`
	Source         string          `json:"source"`
}

// IncidentEvent is one timeline entry. Payload is evidence and is never
// modified after it is written.
type IncidentEvent struct {
	ID      int64           `json:"id"`
	At      time.Time       `json:"at"`
	Kind    string          `json:"kind"`
	Summary string          `json:"summary"`
	Payload json.RawMessage `json:"payload"`
}

const incidentColumns = `id, target_id, target_name, rule_id, rule_version, severity, title, state, opened_at,
last_seen_at, resolved_at, resolution, trigger_config, acknowledged_at, acknowledged_by, ack_note, event_count,
size_bytes, truncated, source`

func scanIncident(row pgx.Row) (Incident, error) {
	var i Incident
	err := row.Scan(&i.ID, &i.TargetID, &i.TargetName, &i.RuleID, &i.RuleVersion, &i.Severity, &i.Title, &i.State,
		&i.OpenedAt, &i.LastSeenAt, &i.ResolvedAt, &i.Resolution, &i.TriggerConfig, &i.AcknowledgedAt,
		&i.AcknowledgedBy, &i.AckNote, &i.EventCount, &i.SizeBytes, &i.Truncated, &i.Source)
	return i, err
}

// CreateIncident inserts an incident and its first events atomically.
func (s *Store) CreateIncident(ctx context.Context, in Incident, events []IncidentEvent) (int64, error) {
	var id int64
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		cfg := in.TriggerConfig
		if cfg == nil {
			cfg = json.RawMessage("{}")
		}
		if err := tx.QueryRow(ctx, `
INSERT INTO incidents (target_id, target_name, rule_id, rule_version, severity, title, state, opened_at,
    last_seen_at, resolved_at, resolution, trigger_config, truncated, source)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14) RETURNING id`,
			in.TargetID, in.TargetName, in.RuleID, in.RuleVersion, in.Severity, in.Title, in.State, in.OpenedAt.UTC(),
			in.LastSeenAt.UTC(), in.ResolvedAt, in.Resolution, cfg, in.Truncated, in.Source).Scan(&id); err != nil {
			return err
		}
		return appendEvents(ctx, tx, id, events)
	})
	return id, s.red.Error(err)
}

func appendEvents(ctx context.Context, tx pgx.Tx, id int64, events []IncidentEvent) error {
	if len(events) == 0 {
		return nil
	}
	var size int64
	batch := &pgx.Batch{}
	for _, e := range events {
		p := e.Payload
		if p == nil {
			p = json.RawMessage("{}")
		}
		size += int64(len(p) + len(e.Summary))
		batch.Queue(`INSERT INTO incident_events (incident_id, ts, kind, summary, payload) VALUES ($1, $2, $3, $4, $5)`,
			id, e.At.UTC(), e.Kind, e.Summary, p)
	}
	if err := tx.SendBatch(ctx, batch).Close(); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, "UPDATE incidents SET event_count = event_count + $2, size_bytes = size_bytes + $3 WHERE id = $1",
		id, len(events), size)
	return err
}

// AppendIncidentEvents adds timeline entries and optionally updates the
// incident's last-seen time.
func (s *Store) AppendIncidentEvents(ctx context.Context, id int64, lastSeen *time.Time, events []IncidentEvent) error {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if lastSeen != nil {
			if _, err := tx.Exec(ctx, "UPDATE incidents SET last_seen_at = greatest(last_seen_at, $2) WHERE id = $1", id, lastSeen.UTC()); err != nil {
				return err
			}
		}
		return appendEvents(ctx, tx, id, events)
	})
	return s.red.Error(err)
}

// UpdateIncidentState changes lifecycle fields: resolve (resolvedAt set) or
// reopen (resolvedAt nil). Severity and title may escalate while open.
func (s *Store) UpdateIncidentState(ctx context.Context, id int64, state string, resolvedAt *time.Time, resolution *string, severity, title string, truncated bool) error {
	_, err := s.pool.Exec(ctx, `
UPDATE incidents SET state = $2, resolved_at = $3, resolution = $4, severity = $5, title = $6, truncated = truncated OR $7
WHERE id = $1`, id, state, resolvedAt, resolution, severity, title, truncated)
	return s.red.Error(err)
}

// AcknowledgeIncident stores user metadata; it never touches evidence.
func (s *Store) AcknowledgeIncident(ctx context.Context, id int64, by, note string) (Incident, error) {
	i, err := scanIncident(s.pool.QueryRow(ctx, `
UPDATE incidents SET acknowledged_at = now(), acknowledged_by = $2, ack_note = nullif($3, '')
WHERE id = $1 RETURNING `+incidentColumns, id, by, note))
	if errors.Is(err, pgx.ErrNoRows) {
		return i, ErrNotFound
	}
	return i, s.red.Error(err)
}

// GetIncident returns one incident.
func (s *Store) GetIncident(ctx context.Context, id int64) (Incident, error) {
	i, err := scanIncident(s.pool.QueryRow(ctx, "SELECT "+incidentColumns+" FROM incidents WHERE id = $1", id))
	if errors.Is(err, pgx.ErrNoRows) {
		return i, ErrNotFound
	}
	return i, s.red.Error(err)
}

// IncidentFilter selects incidents.
type IncidentFilter struct {
	TargetID int64
	State    string
	RuleID   string
	From, To *time.Time
	Limit    int
	Offset   int
}

// ListIncidents returns a page of incidents (newest first) and the total.
func (s *Store) ListIncidents(ctx context.Context, f IncidentFilter) ([]Incident, int, error) {
	rows, err := s.pool.Query(ctx, `
SELECT `+incidentColumns+`, count(*) OVER ()
FROM incidents
WHERE ($1 = 0 OR target_id = $1) AND ($2 = '' OR state = $2) AND ($3 = '' OR rule_id = $3)
  AND ($4::timestamptz IS NULL OR coalesce(resolved_at, 'infinity') >= $4)
  AND ($5::timestamptz IS NULL OR opened_at <= $5)
ORDER BY opened_at DESC, id DESC
LIMIT $6 OFFSET $7`, f.TargetID, f.State, f.RuleID, f.From, f.To, f.Limit, f.Offset)
	if err != nil {
		return nil, 0, s.red.Error(err)
	}
	total := 0
	out := []Incident{}
	for rows.Next() {
		var i Incident
		if err := rows.Scan(&i.ID, &i.TargetID, &i.TargetName, &i.RuleID, &i.RuleVersion, &i.Severity, &i.Title, &i.State,
			&i.OpenedAt, &i.LastSeenAt, &i.ResolvedAt, &i.Resolution, &i.TriggerConfig, &i.AcknowledgedAt,
			&i.AcknowledgedBy, &i.AckNote, &i.EventCount, &i.SizeBytes, &i.Truncated, &i.Source, &total); err != nil {
			rows.Close()
			return nil, 0, s.red.Error(err)
		}
		out = append(out, i)
	}
	return out, total, s.red.Error(rows.Err())
}

// ListIncidentEvents returns events in time order, after an event ID.
func (s *Store) ListIncidentEvents(ctx context.Context, id int64, afterID int64, limit int) ([]IncidentEvent, error) {
	if limit <= 0 || limit > 2000 {
		limit = 2000
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, ts, kind, summary, payload FROM incident_events
WHERE incident_id = $1 AND id > $2 ORDER BY ts, id LIMIT $3`, id, afterID, limit)
	if err != nil {
		return nil, s.red.Error(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (IncidentEvent, error) {
		var e IncidentEvent
		err := r.Scan(&e.ID, &e.At, &e.Kind, &e.Summary, &e.Payload)
		return e, err
	})
	if out == nil {
		out = []IncidentEvent{}
	}
	return out, s.red.Error(err)
}

// OpenIncidents returns every open incident (restart recovery).
func (s *Store) OpenIncidents(ctx context.Context) ([]Incident, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+incidentColumns+" FROM incidents WHERE state = 'open' AND source = 'recorder' ORDER BY id")
	if err != nil {
		return nil, s.red.Error(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Incident, error) { return scanIncident(r) })
	return out, s.red.Error(err)
}

// LatestResolvedIncident returns the most recent resolved incident of a rule
// on a target resolved after since, for regrouping flapping conditions.
func (s *Store) LatestResolvedIncident(ctx context.Context, targetID int64, ruleID string, since time.Time) (Incident, error) {
	i, err := scanIncident(s.pool.QueryRow(ctx, `
SELECT `+incidentColumns+` FROM incidents
WHERE target_id = $1 AND rule_id = $2 AND state = 'resolved' AND resolved_at >= $3 AND source = 'recorder'
ORDER BY resolved_at DESC LIMIT 1`, targetID, ruleID, since.UTC()))
	if errors.Is(err, pgx.ErrNoRows) {
		return i, ErrNotFound
	}
	return i, s.red.Error(err)
}

// CountOpenIncidents returns the number of open incidents.
func (s *Store) CountOpenIncidents(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, "SELECT count(*) FROM incidents WHERE state = 'open'").Scan(&n)
	return n, s.red.Error(err)
}
