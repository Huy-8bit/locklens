package onboard

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/domain/targets"
	"github.com/locklens/locklens/internal/repository"
)

// Store is the part of the monitoring store this package uses. It is
// implemented by *repository.Store; tests can substitute a fake.
type Store interface {
	ManagedRoles(ctx context.Context, systemIdentifier string) (map[string]repository.ManagedRole, error)
	PutManagedRole(ctx context.Context, m repository.ManagedRole) error
}

// Targets adds a monitored instance (implemented by *targets.Service).
type Targets interface {
	Create(ctx context.Context, in targets.Input) (targets.View, error)
}

// Workspace configures and runs investigation access (implemented by
// *workspace.Service).
type Workspace interface {
	Configure(ctx context.Context, id int64, p repository.WorkspacePolicy, dsn string) error
	Run(ctx context.Context, id int64, owner, action, requestID string, fn func(context.Context, pgx.Tx) (any, error)) (any, error)
}

var _ Store = (*repository.Store)(nil)
