package repository

import (
	"context"

	"github.com/locklens/locklens/internal/repository/queries"
)

// TopologyRepository is the store of primary-standby links between targets.
type TopologyRepository interface {
	// TopologyLinks returns the primary-standby links between targets.
	TopologyLinks(ctx context.Context) ([]TopologyLink, error)
	// SetTopologyLink stores a primary-standby link.
	SetTopologyLink(ctx context.Context, l TopologyLink) error
	// DeleteTopologyLink deletes the link of a standby.
	DeleteTopologyLink(ctx context.Context, id int64) error
}

var _ TopologyRepository = (*Store)(nil)

// TopologyLink links a standby target to its primary.
type TopologyLink struct {
	PrimaryID   int64  `json:"primary_id"`
	StandbyID   int64  `json:"standby_id"`
	Application string `json:"application_name"`
	Provenance  string `json:"provenance"`
}

// TopologyLinks returns the primary-standby links between targets.
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

// SetTopologyLink stores a primary-standby link.
func (s *Store) SetTopologyLink(ctx context.Context, l TopologyLink) error {
	_, err := s.pool.Exec(ctx, queries.SetTopologyLink, l.PrimaryID, l.StandbyID, l.Application, l.Provenance)
	return err
}

// DeleteTopologyLink deletes the link of a standby.
func (s *Store) DeleteTopologyLink(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, queries.DeleteTopologyLink, id)
	return err
}
