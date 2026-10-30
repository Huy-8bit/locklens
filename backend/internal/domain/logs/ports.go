package logs

import (
	"context"
	"time"

	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/repository"
)

// Store is the part of the monitoring store the server side uses. It is
// implemented by *repository.Store; tests can substitute a fake.
type Store interface {
	InsertLogEvents(ctx context.Context, targetID, tokenID int64, hostname, version string, sources []string, events []observe.LogEvent) (int64, error)
	LogAgents(ctx context.Context, targetID int64) ([]repository.LogAgent, error)
	LogCounts(ctx context.Context, targetID int64, from, to time.Time, step time.Duration) ([]repository.LogCount, error)
	LogEvents(ctx context.Context, f repository.LogEventFilter) ([]observe.LogEvent, error)
	LogStatements(ctx context.Context, targetID int64, from, to time.Time, limit int) ([]repository.LogStatement, error)
	ChangeState(ctx context.Context, targetID int64, kind string) ([]byte, error)
}

var _ Store = (*repository.Store)(nil)
