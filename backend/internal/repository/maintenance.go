package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/repository/queries"
)

// MaintenanceRepository is the store of maintenance windows.
type MaintenanceRepository interface {
	// MaintenanceWindows returns every maintenance window by name.
	MaintenanceWindows(ctx context.Context) ([]MaintenanceWindow, error)
	// SaveMaintenanceWindow inserts (ID 0) or replaces a window.
	SaveMaintenanceWindow(ctx context.Context, w MaintenanceWindow) (MaintenanceWindow, error)
	// DeleteMaintenanceWindow deletes a window; ErrNotFound when unknown.
	DeleteMaintenanceWindow(ctx context.Context, id int64) error
}

var _ MaintenanceRepository = (*Store)(nil)

// Kinds of maintenance windows.
const (
	MaintenanceOnce        = "once"         // from StartsAt to EndsAt
	MaintenanceWeekly      = "weekly"       // on Weekdays at StartTime for DurationMinutes
	MaintenanceAfterDeploy = "after_deploy" // DurationMinutes after each reported deploy or migration
)

// MaintenanceWindow is a period in which notifications are not sent for
// the instances, environments, and rules it covers (empty lists: all).
type MaintenanceWindow struct {
	ID           int64      `json:"id"`
	Name         string     `json:"name"`
	Enabled      bool       `json:"enabled"`
	Kind         string     `json:"kind"`
	Environments []string   `json:"environments"`
	TargetIDs    []int64    `json:"target_ids"`
	RuleIDs      []string   `json:"rule_ids"`
	StartsAt     *time.Time `json:"starts_at"`
	EndsAt       *time.Time `json:"ends_at"`
	// Weekdays are 0 (Sunday) to 6; StartTime is HH:MM in Timezone.
	Weekdays        []int     `json:"weekdays"`
	StartTime       string    `json:"start_time"`
	DurationMinutes int       `json:"duration_minutes"`
	Timezone        string    `json:"timezone"`
	CreatedBy       string    `json:"created_by"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func scanWindow(r pgx.CollectableRow) (MaintenanceWindow, error) {
	var w MaintenanceWindow
	var days []int32
	err := r.Scan(&w.ID, &w.Name, &w.Enabled, &w.Kind, &w.Environments, &w.TargetIDs, &w.RuleIDs, &w.StartsAt, &w.EndsAt,
		&days, &w.StartTime, &w.DurationMinutes, &w.Timezone, &w.CreatedBy, &w.CreatedAt, &w.UpdatedAt)
	w.Weekdays = make([]int, len(days))
	for i, d := range days {
		w.Weekdays[i] = int(d)
	}
	return w, err
}

// MaintenanceWindows returns every maintenance window by name.
func (s *Store) MaintenanceWindows(ctx context.Context) ([]MaintenanceWindow, error) {
	rows, err := s.db.Query(ctx, queries.ListMaintenanceWindows)
	if err != nil {
		return nil, s.red.Error(err)
	}
	out, err := pgx.CollectRows(rows, scanWindow)
	if out == nil {
		out = []MaintenanceWindow{}
	}
	return out, s.red.Error(err)
}

// SaveMaintenanceWindow inserts (ID 0) or replaces a window.
func (s *Store) SaveMaintenanceWindow(ctx context.Context, w MaintenanceWindow) (MaintenanceWindow, error) {
	nonNil(&w)
	var rows pgx.Rows
	var err error
	if w.ID == 0 {
		rows, err = s.db.Query(ctx, queries.InsertMaintenanceWindow, w.Name, w.Enabled, w.Kind, w.Environments, w.TargetIDs, w.RuleIDs,
			w.StartsAt, w.EndsAt, w.Weekdays, w.StartTime, w.DurationMinutes, w.Timezone, w.CreatedBy)
	} else {
		rows, err = s.db.Query(ctx, queries.UpdateMaintenanceWindow, w.ID, w.Name, w.Enabled, w.Kind, w.Environments, w.TargetIDs, w.RuleIDs,
			w.StartsAt, w.EndsAt, w.Weekdays, w.StartTime, w.DurationMinutes, w.Timezone)
	}
	if err != nil {
		return w, s.red.Error(err)
	}
	out, err := pgx.CollectExactlyOneRow(rows, scanWindow)
	if errors.Is(err, pgx.ErrNoRows) {
		return w, ErrNotFound
	}
	return out, s.red.Error(err)
}

// DeleteMaintenanceWindow deletes a window; ErrNotFound when unknown.
func (s *Store) DeleteMaintenanceWindow(ctx context.Context, id int64) error {
	tag, err := s.db.Exec(ctx, queries.DeleteMaintenanceWindow, id)
	if err != nil {
		return s.red.Error(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// nonNil sets the defaults of unset fields.
func nonNil(w *MaintenanceWindow) {
	if w.Environments == nil {
		w.Environments = []string{}
	}
	if w.TargetIDs == nil {
		w.TargetIDs = []int64{}
	}
	if w.RuleIDs == nil {
		w.RuleIDs = []string{}
	}
	if w.Weekdays == nil {
		w.Weekdays = []int{}
	}
	if w.Timezone == "" {
		w.Timezone = "UTC"
	}
}
