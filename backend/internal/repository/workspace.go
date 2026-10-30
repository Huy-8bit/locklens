package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/repository/queries"
)

// WorkspaceRepository is the store of investigation workspace policies and their audit log.
type WorkspaceRepository interface {
	// WorkspacePolicy returns a target's workspace policy (all off when none
	// is stored).
	WorkspacePolicy(ctx context.Context, id int64) (WorkspacePolicy, error)
	// SetWorkspacePolicy stores a target's workspace policy.
	SetWorkspacePolicy(ctx context.Context, p WorkspacePolicy) error
	// AuditWorkspace records one workspace action and trims the audit log.
	AuditWorkspace(ctx context.Context, id int64, actor, action, status string, duration time.Duration, rows int) error
}

var _ WorkspaceRepository = (*Store)(nil)

type WorkspacePolicy struct {
	TargetID int64 `json:"target_id"`
	Metadata bool  `json:"metadata_enabled"`
	Data     bool  `json:"data_enabled"`
	SQL      bool  `json:"sql_enabled"`
	History  bool  `json:"history_enabled"`
	// HypoPG allows what-if checks of index recommendations with
	// hypothetical indexes (the hypopg extension on the target).
	HypoPG     bool      `json:"hypopg_enabled"`
	Configured bool      `json:"configured"`
	Ciphertext []byte    `json:"-"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// WorkspacePolicy returns a target's workspace policy (all off when none
// is stored).
func (s *Store) WorkspacePolicy(ctx context.Context, id int64) (WorkspacePolicy, error) {
	p := WorkspacePolicy{TargetID: id}
	err := s.pool.QueryRow(ctx, queries.WorkspacePolicy, id).Scan(&p.Metadata, &p.Data, &p.SQL, &p.History, &p.HypoPG, &p.Ciphertext, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, nil
	}
	p.Configured = len(p.Ciphertext) > 0
	return p, err
}

// SetWorkspacePolicy stores a target's workspace policy.
func (s *Store) SetWorkspacePolicy(ctx context.Context, p WorkspacePolicy) error {
	_, err := s.pool.Exec(ctx, queries.SetWorkspacePolicy, p.TargetID, p.Metadata, p.Data, p.SQL, p.History, p.HypoPG, p.Ciphertext)
	return err
}

// AuditWorkspace records one workspace action and trims the audit log.
func (s *Store) AuditWorkspace(ctx context.Context, id int64, actor, action, status string, duration time.Duration, rows int) error {
	_, err := s.pool.Exec(ctx, queries.InsertWorkspaceAudit, id, actor, action, status, duration.Milliseconds(), rows)
	if err == nil {
		_, err = s.pool.Exec(ctx, queries.TrimWorkspaceAudit)
	}
	return err
}
