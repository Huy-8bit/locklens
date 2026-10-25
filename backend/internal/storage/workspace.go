package storage

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"time"
)

type WorkspacePolicy struct {
	TargetID   int64     `json:"target_id"`
	Metadata   bool      `json:"metadata_enabled"`
	Data       bool      `json:"data_enabled"`
	SQL        bool      `json:"sql_enabled"`
	History    bool      `json:"history_enabled"`
	Configured bool      `json:"configured"`
	Ciphertext []byte    `json:"-"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (s *Store) WorkspacePolicy(ctx context.Context, id int64) (WorkspacePolicy, error) {
	p := WorkspacePolicy{TargetID: id}
	err := s.pool.QueryRow(ctx, `SELECT metadata_enabled,data_enabled,sql_enabled,history_enabled,dsn_ciphertext,updated_at FROM workspace_policies WHERE target_id=$1`, id).Scan(&p.Metadata, &p.Data, &p.SQL, &p.History, &p.Ciphertext, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, nil
	}
	p.Configured = len(p.Ciphertext) > 0
	return p, err
}
func (s *Store) SetWorkspacePolicy(ctx context.Context, p WorkspacePolicy) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO workspace_policies(target_id,metadata_enabled,data_enabled,sql_enabled,history_enabled,dsn_ciphertext) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(target_id) DO UPDATE SET metadata_enabled=$2,data_enabled=$3,sql_enabled=$4,history_enabled=$5,dsn_ciphertext=$6,updated_at=now()`, p.TargetID, p.Metadata, p.Data, p.SQL, p.History, p.Ciphertext)
	return err
}
func (s *Store) AuditWorkspace(ctx context.Context, id int64, actor, action, status string, duration time.Duration, rows int) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO workspace_audit(target_id,actor,action,status,duration_ms,row_count) VALUES($1,$2,$3,$4,$5,$6)`, id, actor, action, status, duration.Milliseconds(), rows)
	if err == nil {
		_, err = s.pool.Exec(ctx, `DELETE FROM workspace_audit WHERE id < (SELECT coalesce(max(id),0)-10000 FROM workspace_audit)`)
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
	rows, err := s.pool.Query(ctx, `SELECT primary_id,standby_id,application_name,provenance FROM topology_links ORDER BY primary_id,standby_id LIMIT 200`)
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
	_, err := s.pool.Exec(ctx, `INSERT INTO topology_links(primary_id,standby_id,application_name,provenance) VALUES($1,$2,$3,$4) ON CONFLICT(standby_id) DO UPDATE SET primary_id=$1,application_name=$3,provenance=$4`, l.PrimaryID, l.StandbyID, l.Application, l.Provenance)
	return err
}
func (s *Store) DeleteTopologyLink(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM topology_links WHERE standby_id=$1`, id)
	return err
}
func (s *Store) HistoryBounds(ctx context.Context, id int64) (map[string]any, error) {
	var a, b *time.Time
	err := s.pool.QueryRow(ctx, `SELECT min(ts),max(ts) FROM metric_points WHERE target_id=$1`, id).Scan(&a, &b)
	return map[string]any{"from": a, "to": b}, err
}
