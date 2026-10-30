package workspace

import (
	"context"
	"time"

	"github.com/locklens/locklens/internal/repository"
)

// Store is the part of the monitoring store this package uses. It is
// implemented by *repository.Store; tests can substitute a fake.
type Store interface {
	AuditWorkspace(ctx context.Context, id int64, actor, action, status string, duration time.Duration, rows int) error
	GetTarget(ctx context.Context, id int64) (repository.Target, error)
	SetWorkspacePolicy(ctx context.Context, p repository.WorkspacePolicy) error
	WorkspacePolicy(ctx context.Context, id int64) (repository.WorkspacePolicy, error)
}

var _ Store = (*repository.Store)(nil)
