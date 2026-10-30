package optimizer

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/repository"
)

// Store is the part of the monitoring store this package uses. It is
// implemented by *repository.Store; tests can substitute a fake.
type Store interface {
	GetRecommendation(ctx context.Context, targetID, id int64) (repository.Recommendation, error)
	Catalogs(ctx context.Context, targetID int64) ([]observe.Catalog, error)
	LatestPlans(ctx context.Context, targetID int64, identityIDs []int64) (map[int64]repository.PlanRow, error)
	ListRecommendations(ctx context.Context, f repository.RecommendationFilter) ([]repository.Recommendation, error)
	ListTargets(ctx context.Context) ([]repository.Target, error)
	MarkApplied(ctx context.Context, id int64, at time.Time, how string, beforeMS *float64) error
	QueryIdentity(ctx context.Context, targetID, id int64, includeText bool) (repository.QueryRow, time.Time, time.Time, error)
	SetVerification(ctx context.Context, id int64, status string, afterMS float64, at time.Time) error
	SetWhatIf(ctx context.Context, targetID, id int64, whatif, confidence any) error
	StatementMeanMS(ctx context.Context, targetID, identityID int64, from, to time.Time, minCalls int64) (float64, int, bool, error)
	StatementWorkload(ctx context.Context, targetID int64, since time.Time, limit int, includeText bool) ([]repository.WorkloadStatement, error)
	SyncRecommendations(ctx context.Context, targetID int64, datname string, now time.Time, recs []repository.RecommendationInput) error
}

// Workspace runs an operation on a target's investigation connection
// (implemented by *workspace.Service).
type Workspace interface {
	Run(ctx context.Context, id int64, owner, action, requestID string, fn func(context.Context, pgx.Tx) (any, error)) (any, error)
}

// Events receives recommendation changes for the browsers.
type Events interface {
	Publish(kind string, payload any)
}

var _ Store = (*repository.Store)(nil)
