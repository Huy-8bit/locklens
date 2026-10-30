package retention

import (
	"context"
	"time"

	"github.com/locklens/locklens/internal/repository"
)

// Store is the part of the monitoring store this package uses. It is
// implemented by *repository.Store; tests can substitute a fake.
type Store interface {
	ApplyRetention(ctx context.Context, p repository.RetentionPolicy, now time.Time) (repository.RetentionResult, error)
}

var _ Store = (*repository.Store)(nil)
