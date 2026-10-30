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

// Service holds the settings stored in the monitoring store (rule thresholds), cached in memory.
type Service interface {
	// Reload reads the stored settings again (after another server changed
	// them).
	Reload(ctx context.Context) error
	// Thresholds returns the current thresholds.
	Thresholds() rules.Thresholds
	// LongTransactionThreshold is the age from which a transaction is long.
	LongTransactionThreshold() time.Duration
	// UpdateThresholds validates and persists thresholds.
	UpdateThresholds(ctx context.Context, th rules.Thresholds) error
}

var _ Service = (*service)(nil)

// service caches settings in memory.
type service struct {
	store Repositories
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
// In demo mode the base values are rules.DemoDefaults(). onChange (may be
// nil) is called after the settings were changed here.
func Load(ctx context.Context, store Repositories, demo bool, onChange func()) (Service, error) {
	base := rules.Defaults()
	if demo {
		base = rules.DemoDefaults()
	}
	s := &service{store: store, base: base, th: base, changed: onChange}
	if err := s.Reload(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// Reload reads the stored settings again (after another server changed
// them). It does not call onChange, so servers do not echo changes.
func (s *service) Reload(ctx context.Context) error {
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
	return nil
}

// Thresholds returns the current thresholds.
func (s *service) Thresholds() rules.Thresholds {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.th
}

// LongTransactionThreshold is the age from which a transaction is long.
func (s *service) LongTransactionThreshold() time.Duration {
	return time.Duration(s.Thresholds().LongTxWarnSeconds * float64(time.Second))
}

// UpdateThresholds validates and persists thresholds.
func (s *service) UpdateThresholds(ctx context.Context, th rules.Thresholds) error {
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
