package repository

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
)

// GetSetting decodes a JSON setting into dst. It returns ErrNotFound when the
// key has never been set.
func (s *Store) GetSetting(ctx context.Context, key string, dst any) error {
	var raw []byte
	err := s.pool.QueryRow(ctx, "SELECT value FROM settings WHERE key = $1", key).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return s.red.Error(err)
	}
	return json.Unmarshal(raw, dst)
}

// PutSetting stores value as JSON.
func (s *Store) PutSetting(ctx context.Context, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
INSERT INTO settings (key, value) VALUES ($1, $2)
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`, key, raw)
	return s.red.Error(err)
}
