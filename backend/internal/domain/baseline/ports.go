package baseline

import (
	"context"
	"time"

	"github.com/locklens/locklens/internal/repository"
)

// Store is the part of the monitoring store this package uses. It is
// implemented by *repository.Store; tests can substitute a fake.
type Store interface {
	ActiveStatements(ctx context.Context, targetID int64, since time.Time, limit int) ([]repository.ActiveStatement, error)
	BaselinePoints(ctx context.Context, targetID int64, ids []int64, from, to time.Time, point time.Duration) ([]repository.RollupPoint, []repository.CoveragePoint, error)
	ListTargets(ctx context.Context) ([]repository.Target, error)
	RollupStatements(ctx context.Context, targetID int64, from, to time.Time) error
	RollupWatermark(ctx context.Context, targetID int64) (time.Time, bool, error)
}

var _ Store = (*repository.Store)(nil)
