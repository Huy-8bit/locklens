// Package settings holds runtime-editable configuration stored in the
// monitoring store: currently the rule thresholds.
package settings

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/locklens/locklens/internal/domain/rules"
	"github.com/locklens/locklens/internal/repository"
)

const keyThresholds = "rules.thresholds"

// Service caches settings in memory.
type Service struct {
	store *repository.Store
	mu    sync.RWMutex
	th    rules.Thresholds
}

// Load reads settings, falling back to defaults for anything unset. Stored
// values are decoded on top of the defaults, so thresholds added in newer
// versions get their default values.
//
// In demo mode the base values are rules.DemoDefaults().
func Load(ctx context.Context, store *repository.Store, demo bool) (*Service, error) {
	base := rules.Defaults()
	if demo {
		base = rules.DemoDefaults()
	}
	s := &Service{store: store, th: base}
	th := base
	err := store.GetSetting(ctx, keyThresholds, &th)
	switch {
	case errors.Is(err, repository.ErrNotFound):
	case err != nil:
		return nil, err
	default:
		if verr := th.Validate(); verr == nil {
			s.th = th
		}
	}
	return s, nil
}

// Thresholds returns the current thresholds.
func (s *Service) Thresholds() rules.Thresholds {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.th
}

// LongTransactionThreshold implements collector.Settings.
func (s *Service) LongTransactionThreshold() time.Duration {
	return time.Duration(s.Thresholds().LongTxWarnSeconds * float64(time.Second))
}

// UpdateThresholds validates and persists thresholds.
func (s *Service) UpdateThresholds(ctx context.Context, th rules.Thresholds) error {
	if err := th.Validate(); err != nil {
		return err
	}
	if err := s.store.PutSetting(ctx, keyThresholds, th); err != nil {
		return err
	}
	s.mu.Lock()
	s.th = th
	s.mu.Unlock()
	return nil
}
