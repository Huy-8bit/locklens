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
	store Store
	base  rules.Thresholds
	mu    sync.RWMutex
	th    rules.Thresholds
	// changed is told after a change (other servers reload then).
	changed func()
}

// Load reads settings, falling back to defaults for anything unset. Stored
// values are decoded on top of the defaults, so thresholds added in newer
// versions get their default values.
//
// In demo mode the base values are rules.DemoDefaults().
func Load(ctx context.Context, store Store, demo bool) (*Service, error) {
	base := rules.Defaults()
	if demo {
		base = rules.DemoDefaults()
	}
	s := &Service{store: store, base: base, th: base}
	if err := s.Reload(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// Reload reads the stored settings again (after another server changed
// them).
func (s *Service) Reload(ctx context.Context) error {
	th := s.base
	err := s.store.GetSetting(ctx, keyThresholds, &th)
	switch {
	case errors.Is(err, repository.ErrNotFound):
		th = s.base
	case err != nil:
		return err
	case th.Validate() != nil:
		return nil // keep the current thresholds
	}
	s.mu.Lock()
	s.th = th
	s.mu.Unlock()
	if s.changed != nil {
		s.changed()
	}
	return nil
}

// OnChange sets a function called after the settings were changed here.
func (s *Service) OnChange(fn func()) { s.changed = fn }

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
	if s.changed != nil {
		s.changed()
	}
	return nil
}
