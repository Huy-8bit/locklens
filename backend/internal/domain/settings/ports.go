package settings

import (
	"context"

	"github.com/locklens/locklens/internal/repository"
)

// Store is the part of the monitoring store this package uses. It is
// implemented by *repository.Store; tests can substitute a fake.
type Store interface {
	GetSetting(ctx context.Context, key string, dst any) error
	PutSetting(ctx context.Context, key string, value any) error
}

var _ Store = (*repository.Store)(nil)
