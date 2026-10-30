package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/repository/queries"
)

// Token scopes.
const (
	TokenEvents = "events" // report change events
	TokenIngest = "ingest" // ship log events
)

// APIToken is a stored machine token (without its secret).
type APIToken struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Scope      string     `json:"scope"`
	TargetID   *int64     `json:"target_id"`
	Prefix     string     `json:"prefix"`
	CreatedBy  string     `json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
}

func scanToken(r pgx.CollectableRow) (APIToken, error) {
	var t APIToken
	err := r.Scan(&t.ID, &t.Name, &t.Scope, &t.TargetID, &t.Prefix, &t.CreatedBy, &t.CreatedAt, &t.LastUsedAt, &t.RevokedAt)
	return t, err
}

// CreateToken stores a token by the hash of its secret.
func (s *Store) CreateToken(ctx context.Context, name, scope string, targetID *int64, hash []byte, prefix, by string) (APIToken, error) {
	rows, err := s.pool.Query(ctx, queries.InsertToken, name, scope, targetID, hash, prefix, by)
	if err != nil {
		return APIToken{}, s.red.Error(err)
	}
	t, err := pgx.CollectExactlyOneRow(rows, scanToken)
	return t, s.red.Error(err)
}

// ListTokens returns every token, live ones first.
func (s *Store) ListTokens(ctx context.Context) ([]APIToken, error) {
	rows, err := s.pool.Query(ctx, queries.ListTokens)
	if err != nil {
		return nil, s.red.Error(err)
	}
	out, err := pgx.CollectRows(rows, scanToken)
	if out == nil {
		out = []APIToken{}
	}
	return out, s.red.Error(err)
}

// TokenByHash returns the live token with a secret hash and records its use.
func (s *Store) TokenByHash(ctx context.Context, hash []byte) (APIToken, error) {
	rows, err := s.pool.Query(ctx, queries.GetTokenByHash, hash)
	if err != nil {
		return APIToken{}, s.red.Error(err)
	}
	t, err := pgx.CollectExactlyOneRow(rows, scanToken)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrNotFound
	}
	if err != nil {
		return t, s.red.Error(err)
	}
	_, err = s.pool.Exec(ctx, queries.TouchToken, t.ID)
	return t, s.red.Error(err)
}

// RevokeToken revokes a token; ErrNotFound when it is unknown or revoked.
func (s *Store) RevokeToken(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, queries.RevokeToken, id)
	if err != nil {
		return s.red.Error(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
