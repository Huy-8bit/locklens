package repository

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/repository/queries"
)

// SettingRepository is the store of persisted settings.
type SettingRepository interface {
	// GetSetting decodes a JSON setting into dst. It returns ErrNotFound when the
	// key has never been set.
	GetSetting(ctx context.Context, key string, dst any) error
	// PutSetting stores value as JSON.
	PutSetting(ctx context.Context, key string, value any) error
}

var _ SettingRepository = (*Store)(nil)

// GetSetting decodes a JSON setting into dst. It returns ErrNotFound when the
// key has never been set.
func (s *Store) GetSetting(ctx context.Context, key string, dst any) error {
	var raw []byte
	err := s.pool.QueryRow(ctx, queries.GetSetting, key).Scan(&raw)
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
	_, err = s.pool.Exec(ctx, queries.PutSetting, key, raw)
	return s.red.Error(err)
}
