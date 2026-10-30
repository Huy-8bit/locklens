package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/repository/queries"
)

// NotificationChannel is a destination for incident notifications. The
// destination's address and credentials are in ConfigCiphertext, sealed
// with the master key; Display is a non-secret description of it.
type NotificationChannel struct {
	ID               int64     `json:"id"`
	Name             string    `json:"name"`
	Kind             string    `json:"kind"`
	Enabled          bool      `json:"enabled"`
	MinSeverity      string    `json:"min_severity"`
	Environments     []string  `json:"environments"`
	OnOpen           bool      `json:"on_open"`
	OnEscalate       bool      `json:"on_escalate"`
	OnResolve        bool      `json:"on_resolve"`
	ReminderSeconds  int       `json:"reminder_seconds"`
	ConfigCiphertext []byte    `json:"-"`
	Display          string    `json:"display"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

const channelColumns = `id, name, kind, enabled, min_severity, environments, on_open, on_escalate, on_resolve,
reminder_seconds, config_ciphertext, display, created_at, updated_at`

func scanChannel(row pgx.Row) (NotificationChannel, error) {
	var c NotificationChannel
	err := row.Scan(&c.ID, &c.Name, &c.Kind, &c.Enabled, &c.MinSeverity, &c.Environments, &c.OnOpen, &c.OnEscalate, &c.OnResolve,
		&c.ReminderSeconds, &c.ConfigCiphertext, &c.Display, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrNotFound
	}
	if c.Environments == nil {
		c.Environments = []string{}
	}
	return c, err
}

// ListNotificationChannels returns every channel by name.
func (s *Store) ListNotificationChannels(ctx context.Context) ([]NotificationChannel, error) {
	rows, err := s.pool.Query(ctx, queries.ListNotificationChannels)
	if err != nil {
		return nil, s.red.Error(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (NotificationChannel, error) { return scanChannel(r) })
	if out == nil {
		out = []NotificationChannel{}
	}
	return out, s.red.Error(err)
}

// GetNotificationChannel returns one channel.
func (s *Store) GetNotificationChannel(ctx context.Context, id int64) (NotificationChannel, error) {
	c, err := scanChannel(s.pool.QueryRow(ctx, queries.GetNotificationChannel, id))
	if errors.Is(err, ErrNotFound) {
		return c, err
	}
	return c, s.red.Error(err)
}

// SaveNotificationChannel creates a channel (ID 0) or updates one. An update
// with nil ConfigCiphertext keeps the stored destination. ErrConflict means
// the name is taken.
func (s *Store) SaveNotificationChannel(ctx context.Context, c NotificationChannel) (NotificationChannel, error) {
	if c.Environments == nil {
		c.Environments = []string{}
	}
	var row pgx.Row
	if c.ID == 0 {
		row = s.pool.QueryRow(ctx, queries.InsertNotificationChannel, c.Name, c.Kind, c.Enabled, c.MinSeverity, c.Environments, c.OnOpen, c.OnEscalate, c.OnResolve, c.ReminderSeconds, c.ConfigCiphertext, c.Display)
	} else {
		row = s.pool.QueryRow(ctx, queries.UpdateNotificationChannel, c.ID, c.Name, c.Enabled, c.MinSeverity, c.Environments, c.OnOpen, c.OnEscalate, c.OnResolve, c.ReminderSeconds, c.ConfigCiphertext, c.Display)
	}
	out, err := scanChannel(row)
	switch {
	case isUniqueViolation(err):
		return out, ErrConflict
	case errors.Is(err, ErrNotFound):
		return out, err
	}
	return out, s.red.Error(err)
}

// DeleteNotificationChannel removes a channel and its delivery history.
func (s *Store) DeleteNotificationChannel(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, queries.DeleteNotificationChannel, id)
	if err != nil {
		return s.red.Error(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Silence stops notifications for an instance, a rule, or both, for a time.
// A nil TargetID or RuleID matches every instance or rule.
type Silence struct {
	ID         int64     `json:"id"`
	TargetID   *int64    `json:"target_id"`
	TargetName *string   `json:"target_name"`
	RuleID     *string   `json:"rule_id"`
	StartsAt   time.Time `json:"starts_at"`
	EndsAt     time.Time `json:"ends_at"`
	Reason     string    `json:"reason"`
	CreatedBy  string    `json:"created_by"`
	CreatedAt  time.Time `json:"created_at"`
}

func scanSilence(r pgx.CollectableRow) (Silence, error) {
	var x Silence
	err := r.Scan(&x.ID, &x.TargetID, &x.TargetName, &x.RuleID, &x.StartsAt, &x.EndsAt, &x.Reason, &x.CreatedBy, &x.CreatedAt)
	return x, err
}

// ListSilences returns silences that have not ended (all of them when
// includeEnded), the latest ending first.
func (s *Store) ListSilences(ctx context.Context, includeEnded bool, limit int) ([]Silence, error) {
	rows, err := s.pool.Query(ctx, queries.ListSilences, includeEnded, limit)
	if err != nil {
		return nil, s.red.Error(err)
	}
	out, err := pgx.CollectRows(rows, scanSilence)
	if out == nil {
		out = []Silence{}
	}
	return out, s.red.Error(err)
}

// CreateSilence adds a silence that starts now. Silences use the database
// clock throughout (start, end, and matching), so a clock difference between
// LockLens and its store never keeps an ended silence active.
func (s *Store) CreateSilence(ctx context.Context, x Silence) (Silence, error) {
	var id int64
	if err := s.pool.QueryRow(ctx, queries.CreateSilence, x.TargetID, x.RuleID, x.EndsAt.UTC(), x.Reason, x.CreatedBy).Scan(&id); err != nil {
		return x, s.red.Error(err)
	}
	rows, err := s.pool.Query(ctx, queries.GetSilence, id)
	if err != nil {
		return x, s.red.Error(err)
	}
	out, err := pgx.CollectExactlyOneRow(rows, scanSilence)
	return out, s.red.Error(err)
}

// EndSilence ends a silence now (it stays in the history).
func (s *Store) EndSilence(ctx context.Context, id int64) error {
	// A silence ended right after it was created (or whose start is ahead of
	// the database clock) gets its start moved back, so it ends now and the
	// ends_at > starts_at constraint holds.
	tag, err := s.pool.Exec(ctx, queries.EndSilence, id)
	if err != nil {
		return s.red.Error(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ActiveSilence returns a silence that covers the target and rule now (by
// the database clock).
func (s *Store) ActiveSilence(ctx context.Context, targetID *int64, ruleID string) (*Silence, error) {
	rows, err := s.pool.Query(ctx, queries.ActiveSilence, targetID, ruleID)
	if err != nil {
		return nil, s.red.Error(err)
	}
	out, err := pgx.CollectRows(rows, scanSilence)
	if err != nil || len(out) == 0 {
		return nil, s.red.Error(err)
	}
	return &out[0], nil
}

// Delivery is one notification attempt.
type Delivery struct {
	ID          int64     `json:"id"`
	ChannelID   int64     `json:"channel_id"`
	ChannelName string    `json:"channel_name"`
	IncidentID  *int64    `json:"incident_id"`
	Event       string    `json:"event"`
	Severity    *string   `json:"severity"`
	Status      string    `json:"status"`
	Attempts    int       `json:"attempts"`
	Error       *string   `json:"error"`
	At          time.Time `json:"at"`
}

// InsertDelivery records a notification attempt.
func (s *Store) InsertDelivery(ctx context.Context, d Delivery) error {
	_, err := s.pool.Exec(ctx, queries.InsertDelivery, d.ChannelID, d.IncidentID, d.Event, d.Severity, d.Status, d.Attempts, d.Error, d.At.UTC())
	return s.red.Error(err)
}

// ListDeliveries returns the latest deliveries, of one incident when
// incidentID is set.
func (s *Store) ListDeliveries(ctx context.Context, incidentID *int64, limit int) ([]Delivery, error) {
	rows, err := s.pool.Query(ctx, queries.ListDeliveries, incidentID, limit)
	if err != nil {
		return nil, s.red.Error(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Delivery, error) {
		var d Delivery
		err := r.Scan(&d.ID, &d.ChannelID, &d.ChannelName, &d.IncidentID, &d.Event, &d.Severity, &d.Status, &d.Attempts, &d.Error, &d.At)
		return d, err
	})
	if out == nil {
		out = []Delivery{}
	}
	return out, s.red.Error(err)
}

// OpenUnacknowledgedIncidents returns open incidents nobody acknowledged
// (candidates for reminders).
func (s *Store) OpenUnacknowledgedIncidents(ctx context.Context) ([]Incident, error) {
	rows, err := s.pool.Query(ctx, queries.OpenUnacknowledgedIncidents)
	if err != nil {
		return nil, s.red.Error(err)
	}
	var out []Incident
	for rows.Next() {
		in, err := scanIncident(rows)
		if err != nil {
			rows.Close()
			return nil, s.red.Error(err)
		}
		out = append(out, in)
	}
	return out, s.red.Error(rows.Err())
}
