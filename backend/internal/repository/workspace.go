package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/repository/queries"
)

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

func (s *Store) WorkspacePolicy(ctx context.Context, id int64) (WorkspacePolicy, error) {
	p := WorkspacePolicy{TargetID: id}
	err := s.pool.QueryRow(ctx, queries.WorkspacePolicy, id).Scan(&p.Metadata, &p.Data, &p.SQL, &p.History, &p.HypoPG, &p.Ciphertext, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, nil
	}
	p.Configured = len(p.Ciphertext) > 0
	return p, err
}
func (s *Store) SetWorkspacePolicy(ctx context.Context, p WorkspacePolicy) error {
	_, err := s.pool.Exec(ctx, queries.SetWorkspacePolicy, p.TargetID, p.Metadata, p.Data, p.SQL, p.History, p.HypoPG, p.Ciphertext)
	return err
}
func (s *Store) AuditWorkspace(ctx context.Context, id int64, actor, action, status string, duration time.Duration, rows int) error {
	_, err := s.pool.Exec(ctx, queries.InsertWorkspaceAudit, id, actor, action, status, duration.Milliseconds(), rows)
	if err == nil {
		_, err = s.pool.Exec(ctx, queries.TrimWorkspaceAudit)
	}
	return err
}

type TopologyLink struct {
	PrimaryID   int64  `json:"primary_id"`
	StandbyID   int64  `json:"standby_id"`
	Application string `json:"application_name"`
	Provenance  string `json:"provenance"`
}

func (s *Store) TopologyLinks(ctx context.Context) ([]TopologyLink, error) {
	rows, err := s.pool.Query(ctx, queries.TopologyLinks)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TopologyLink{}
	for rows.Next() {
		var l TopologyLink
		if err = rows.Scan(&l.PrimaryID, &l.StandbyID, &l.Application, &l.Provenance); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
func (s *Store) SetTopologyLink(ctx context.Context, l TopologyLink) error {
	_, err := s.pool.Exec(ctx, queries.SetTopologyLink, l.PrimaryID, l.StandbyID, l.Application, l.Provenance)
	return err
}
func (s *Store) DeleteTopologyLink(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, queries.DeleteTopologyLink, id)
	return err
}
func (s *Store) HistoryBounds(ctx context.Context, id int64) (map[string]any, error) {
	var a, b *time.Time
	err := s.pool.QueryRow(ctx, queries.HistoryBounds, id).Scan(&a, &b)
	return map[string]any{"from": a, "to": b}, err
}
