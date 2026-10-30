package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/repository/queries"
)

// PlanRow is a saved execution plan. Plan holds the EXPLAIN output and is
// left empty in listings.
type PlanRow struct {
	ID          int64     `json:"id"`
	IdentityID  int64     `json:"identity_id"`
	CapturedAt  time.Time `json:"captured_at"`
	CapturedBy  string    `json:"captured_by"`
	Source      string    `json:"source"`
	Format      string    `json:"format"`
	Plan        string    `json:"plan,omitempty"`
	Fingerprint string    `json:"fingerprint"`
	TotalCost   *float64  `json:"total_cost"`
	Note        string    `json:"note"`
}

// SavePlan stores a plan of an identity of the target.
func (s *Store) SavePlan(ctx context.Context, targetID int64, p PlanRow) (PlanRow, error) {
	err := s.pool.QueryRow(ctx, queries.SavePlan, targetID, p.IdentityID, p.CapturedBy, p.Source, p.Format, p.Plan, p.Fingerprint, p.TotalCost, p.Note).
		Scan(&p.ID, &p.CapturedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrNotFound
	}
	p.CapturedAt = p.CapturedAt.UTC()
	return p, s.red.Error(err)
}

// ListPlans returns the saved plans of an identity, newest first, without
// their bodies.
func (s *Store) ListPlans(ctx context.Context, targetID, identityID int64) ([]PlanRow, error) {
	rows, err := s.pool.Query(ctx, queries.ListPlans, targetID, identityID)
	if err != nil {
		return nil, s.red.Error(err)
	}
	out, err := pgx.CollectRows(rows, scanPlan)
	if out == nil {
		out = []PlanRow{}
	}
	return out, s.red.Error(err)
}

// GetPlan returns one saved plan of the target with its body.
func (s *Store) GetPlan(ctx context.Context, targetID, id int64) (PlanRow, error) {
	rows, err := s.pool.Query(ctx, queries.GetPlan, targetID, id)
	if err != nil {
		return PlanRow{}, s.red.Error(err)
	}
	p, err := pgx.CollectExactlyOneRow(rows, scanPlan)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, s.red.Error(err)
}

// DeletePlan removes a saved plan of the target.
func (s *Store) DeletePlan(ctx context.Context, targetID, id int64) error {
	tag, err := s.pool.Exec(ctx, queries.DeletePlan, targetID, id)
	if err != nil {
		return s.red.Error(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// CountPlans counts the saved plans of the target's identities with a queryid.
func (s *Store) CountPlans(ctx context.Context, targetID, queryID int64) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, queries.CountPlans, targetID, queryID).Scan(&n)
	return n, s.red.Error(err)
}

func scanPlan(r pgx.CollectableRow) (PlanRow, error) {
	var p PlanRow
	err := r.Scan(&p.ID, &p.IdentityID, &p.CapturedAt, &p.CapturedBy, &p.Source, &p.Format, &p.Plan, &p.Fingerprint, &p.TotalCost, &p.Note)
	p.CapturedAt = p.CapturedAt.UTC()
	return p, err
}
