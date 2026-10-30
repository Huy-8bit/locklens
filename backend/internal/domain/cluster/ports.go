package cluster

import (
	"context"
	"time"

	"github.com/locklens/locklens/internal/repository"
)

// Store is the part of the monitoring store this package uses. It is
// implemented by *repository.Store; tests can substitute a fake.
type Store interface {
	Heartbeat(ctx context.Context, sv repository.Server) error
	Servers(ctx context.Context, ttl time.Duration) ([]repository.Server, error)
	HoldLease(ctx context.Context, name, holder string, ttl time.Duration) (bool, time.Time, error)
	ReleaseLease(ctx context.Context, name, holder string) error
	Leases(ctx context.Context, prefix string) ([]repository.Lease, error)
	Notify(ctx context.Context, channel, payload string) error
	Listen(ctx context.Context, channel string, fn func(payload string)) error
}

var _ Store = (*repository.Store)(nil)
