package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/locklens/locklens/internal/repository"
)

// OpenStore connects to the store and applies migrations.
func OpenStore(ctx context.Context, dsn string, log *slog.Logger) (*repository.Store, error) {
	octx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	store, err := repository.Open(octx, dsn, repository.Options{ConnectTimeout: 10 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("open monitoring store: %w", err)
	}
	applied, err := store.Migrate(octx)
	if err != nil {
		store.Close()
		return nil, fmt.Errorf("migrate monitoring store: %w", err)
	}
	if len(applied) > 0 {
		log.Info("monitoring store migrated", "applied_versions", applied)
	}
	return store, nil
}
