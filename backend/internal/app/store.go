package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/locklens/locklens/internal/config"
	"github.com/locklens/locklens/internal/repository"
)

// OpenStore connects to the store and applies migrations. With ts, it then
// sets up TimescaleDB (see repository.SetupTimescale); nil skips that step,
// for commands that only read.
func OpenStore(ctx context.Context, dsn string, ts *config.StoreTimescale, log *slog.Logger) (*repository.Store, error) {
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
	if ts == nil {
		return store, nil
	}
	// Moving existing history into chunks happens once and can take minutes
	// on a large store, so it is not bound by the connect timeout.
	start := time.Now()
	st, err := store.SetupTimescale(ctx, repository.TimescaleMode(ts.Mode), ts.CompressAfter)
	if err != nil {
		store.Close()
		return nil, fmt.Errorf("set up TimescaleDB in the monitoring store: %w", err)
	}
	if st.Enabled {
		log.Info("monitoring store uses TimescaleDB", "version", st.Version, "license", st.License, "hypertables", len(st.Hypertables),
			"compression", st.Compression, "compress_after", ts.CompressAfter.String(), "setup_duration", time.Since(start).Round(time.Millisecond).String())
	} else {
		log.Info("monitoring store uses plain PostgreSQL tables", "reason", st.Reason)
	}
	return store, nil
}
