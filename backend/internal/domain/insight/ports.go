package insight

import (
	"context"
	"time"

	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/repository"
)

// Store is the part of the monitoring store this package uses. It is
// implemented by *repository.Store; tests can substitute a fake.
type Store interface {
	CountPlans(ctx context.Context, targetID, queryID int64) (int, error)
	ListChanges(ctx context.Context, f repository.ChangeFilter) ([]observe.ChangeEvent, error)
	ListFindings(ctx context.Context, f repository.FindingFilter) ([]repository.FindingRow, error)
	LogAgents(ctx context.Context, targetID int64) ([]repository.LogAgent, error)
	LogEvents(ctx context.Context, f repository.LogEventFilter) ([]observe.LogEvent, error)
	ListGaps(ctx context.Context, targetID int64, from, to time.Time, limit int) ([]repository.Gap, error)
	LoadRows(ctx context.Context, targetID int64, from, to time.Time, step time.Duration, dim repository.LoadDimension, f repository.LoadFilter) ([]repository.LoadRow, []repository.LoadTicks, error)
}

var _ Store = (*repository.Store)(nil)
