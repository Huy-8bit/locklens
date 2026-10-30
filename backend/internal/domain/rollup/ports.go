package rollup

import (
	"context"
	"time"

	"github.com/locklens/locklens/internal/repository"
)

// Store is the part of the monitoring store this package uses. It is
// implemented by *repository.Store; tests can substitute a fake.
type Store interface {
	ListTargets(ctx context.Context) ([]repository.Target, error)
	MetricRollupWatermark(ctx context.Context, targetID int64) (time.Time, bool, error)
	RollupMetrics(ctx context.Context, targetID int64, from, to time.Time) error
}

var _ Store = (*repository.Store)(nil)
