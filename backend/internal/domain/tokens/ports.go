package tokens

import (
	"context"

	"github.com/locklens/locklens/internal/repository"
)

// Store is the part of the monitoring store this package uses. It is
// implemented by *repository.Store; tests can substitute a fake.
type Store interface {
	CreateToken(ctx context.Context, name, scope string, targetID *int64, hash []byte, prefix, by string) (repository.APIToken, error)
	ListTokens(ctx context.Context) ([]repository.APIToken, error)
	TokenByHash(ctx context.Context, hash []byte) (repository.APIToken, error)
	RevokeToken(ctx context.Context, id int64) error
}

var _ Store = (*repository.Store)(nil)
