package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/repository/queries"
)

// ChangeRepository is the store of change events and the readings change detection compares against.
type ChangeRepository interface {
	// InsertChanges stores change events and sets their IDs.
	InsertChanges(ctx context.Context, events []observe.ChangeEvent) error
	// ListChanges returns change events, the newest first.
	ListChanges(ctx context.Context, f ChangeFilter) ([]observe.ChangeEvent, error)
	// DeleteChange deletes a reported change event (detected ones stay).
	DeleteChange(ctx context.Context, id int64) (observe.ChangeEvent, error)
	// ChangeState returns the last reading a change detection compares against.
	ChangeState(ctx context.Context, targetID int64, kind string) ([]byte, error)
	// SaveChangeState keeps a reading for the next comparison.
	SaveChangeState(ctx context.Context, targetID int64, kind string, at time.Time, state []byte) error
}

var _ ChangeRepository = (*Store)(nil)

// ChangeFilter selects change events.
type ChangeFilter struct {
	From, To time.Time
	// TargetIDs limits the instances (changes for every instance are always
	// included); empty means all.
	TargetIDs []int64
	// Kinds limits the kinds; empty means all.
	Kinds []string
	Limit int
}

// InsertChanges stores change events and sets their IDs.
func (s *Store) InsertChanges(ctx context.Context, events []observe.ChangeEvent) error {
	return s.red.Error(pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		for i := range events {
			e := &events[i]
			details := e.Details
			if details == nil {
				details = map[string]any{}
			}
			raw, err := json.Marshal(details)
			if err != nil {
				return err
			}
			if err := tx.QueryRow(ctx, queries.InsertChange, e.TargetID, e.At.UTC(), e.Kind, e.Source, e.Title, raw, e.CreatedBy).Scan(&e.ID); err != nil {
				return err
			}
		}
		return nil
	}))
}

// ListChanges returns change events, the newest first.
func (s *Store) ListChanges(ctx context.Context, f ChangeFilter) ([]observe.ChangeEvent, error) {
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 500
	}
	if f.TargetIDs == nil {
		f.TargetIDs = []int64{}
	}
	if f.Kinds == nil {
		f.Kinds = []string{}
	}
	rows, err := s.pool.Query(ctx, queries.ListChanges, f.From.UTC(), f.To.UTC(), f.TargetIDs, f.Kinds, f.Limit)
	if err != nil {
		return nil, s.red.Error(err)
	}
	out, err := pgx.CollectRows(rows, scanChange)
	if out == nil {
		out = []observe.ChangeEvent{}
	}
	return out, s.red.Error(err)
}

// DeleteChange deletes a reported change event (detected ones stay).
func (s *Store) DeleteChange(ctx context.Context, id int64) (observe.ChangeEvent, error) {
	rows, err := s.pool.Query(ctx, queries.DeleteChange, id)
	if err != nil {
		return observe.ChangeEvent{}, s.red.Error(err)
	}
	e, err := pgx.CollectExactlyOneRow(rows, scanChange)
	if errors.Is(err, pgx.ErrNoRows) {
		return e, ErrNotFound
	}
	return e, s.red.Error(err)
}

func scanChange(r pgx.CollectableRow) (observe.ChangeEvent, error) {
	var e observe.ChangeEvent
	var raw []byte
	if err := r.Scan(&e.ID, &e.TargetID, &e.At, &e.Kind, &e.Source, &e.Title, &raw, &e.CreatedBy); err != nil {
		return e, err
	}
	e.At = e.At.UTC()
	return e, json.Unmarshal(raw, &e.Details)
}

// ChangeState returns the last reading a change detection compares against.
func (s *Store) ChangeState(ctx context.Context, targetID int64, kind string) ([]byte, error) {
	var raw []byte
	err := s.pool.QueryRow(ctx, queries.ChangeState, targetID, kind).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return raw, s.red.Error(err)
}

// SaveChangeState keeps a reading for the next comparison.
func (s *Store) SaveChangeState(ctx context.Context, targetID int64, kind string, at time.Time, state []byte) error {
	_, err := s.pool.Exec(ctx, queries.SaveChangeState, targetID, kind, at.UTC(), state)
	return s.red.Error(err)
}
