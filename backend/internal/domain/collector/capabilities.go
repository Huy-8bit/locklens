package collector

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/targetdb"
)

func (w *worker) refreshCapabilities(ctx context.Context) error {
	if w.caps != nil && time.Since(w.caps.DetectedAt) < capabilitiesMaxAge {
		return nil
	}
	return w.obs.ReadOnly(ctx, func(ctx context.Context, tx pgx.Tx, _ []int32) error {
		caps, err := targetdb.DetectCapabilities(ctx, tx)
		if err != nil {
			return err
		}
		w.caps = &caps
		w.state.mu.Lock()
		w.state.caps = &caps
		w.state.mu.Unlock()
		return nil
	})
}
