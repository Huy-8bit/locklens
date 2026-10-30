package targets

import (
	"context"

	"github.com/locklens/locklens/internal/repository"
)

// Store is the part of the monitoring store this package uses. It is
// implemented by *repository.Store; tests can substitute a fake.
type Store interface {
	ClearQueryTexts(ctx context.Context, targetID int64) error
	CreateTarget(ctx context.Context, t repository.Target) (repository.Target, error)
	GetTarget(ctx context.Context, id int64) (repository.Target, error)
	GetTargetByName(ctx context.Context, name string) (repository.Target, error)
	GetTargetStatus(ctx context.Context, id int64) (repository.TargetStatus, error)
	ListTargetStatuses(ctx context.Context) (map[int64]repository.TargetStatus, error)
	ListTargets(ctx context.Context) ([]repository.Target, error)
	PurgeTarget(ctx context.Context, id int64, keepIncidents bool) error
	SaveTargetStatus(ctx context.Context, st repository.TargetStatus) error
	SoftDeleteTarget(ctx context.Context, id int64) error
	UpdateTarget(ctx context.Context, t repository.Target) (repository.Target, error)
}

var _ Store = (*repository.Store)(nil)
