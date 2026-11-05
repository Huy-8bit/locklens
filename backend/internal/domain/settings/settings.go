// Package settings holds runtime-editable configuration stored in the
// monitoring store: the rule thresholds, the rule modes, and their overrides
// per environment and per instance.
package settings

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/locklens/locklens/internal/domain/rules"
	"github.com/locklens/locklens/internal/repository"
)

const (
	keyThresholds = "rules.thresholds"
	keyModes      = "rules.modes"
	keyOverrides  = "rules.overrides"
)

// Alerting is the alerting configuration besides the global thresholds:
// the global rule modes and the overrides.
type Alerting struct {
	Modes     map[string]string `json:"modes"`
	Overrides rules.Overrides   `json:"overrides"`
}

// Service holds the settings stored in the monitoring store, cached in
// memory.
type Service interface {
	// Reload reads the stored settings again (after another server changed
	// them). It does not call onChange, so servers do not echo changes.
	Reload(ctx context.Context) error
	// Thresholds returns the global thresholds.
	Thresholds() rules.Thresholds
	// Alerting returns the global rule modes and the overrides.
	Alerting() Alerting
	// Policy returns the thresholds and rule modes that apply to a target.
	Policy(t repository.Target) rules.Policy
	// LongTransactionThreshold is the age from which a transaction of a
	// target is long.
	LongTransactionThreshold(t repository.Target) time.Duration
	// UpdateThresholds validates and persists the global thresholds.
	UpdateThresholds(ctx context.Context, th rules.Thresholds) error
	// UpdateAlerting validates and persists the rule modes and overrides.
	UpdateAlerting(ctx context.Context, a Alerting) error
}

var _ Service = (*service)(nil)

// service caches settings in memory.
type service struct {
	store Repositories
	base  rules.Thresholds
	mu    sync.RWMutex
	th    rules.Thresholds
	al    Alerting
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
	s := &service{store: store, base: base, th: base, al: emptyAlerting(), changed: onChange}
	if err := s.Reload(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

func emptyAlerting() Alerting {
	return Alerting{Modes: map[string]string{}, Overrides: rules.Overrides{Environments: map[string]rules.Override{}, Targets: map[int64]rules.Override{}}}
}

func (s *service) Reload(ctx context.Context) error {
	th := s.base
	err := s.store.GetSetting(ctx, keyThresholds, &th)
	switch {
	case errors.Is(err, repository.ErrNotFound):
		th = s.base
	case err != nil:
		return err
	case th.Validate() != nil:
		th = s.Thresholds() // keep the current thresholds
	}
	al := emptyAlerting()
	if err := s.store.GetSetting(ctx, keyModes, &al.Modes); err != nil && !errors.Is(err, repository.ErrNotFound) {
		return err
	}
	if err := s.store.GetSetting(ctx, keyOverrides, &al.Overrides); err != nil && !errors.Is(err, repository.ErrNotFound) {
		return err
	}
	normalize(&al)
	s.mu.Lock()
	s.th, s.al = th, al
	s.mu.Unlock()
	return nil
}

func normalize(a *Alerting) {
	if a.Modes == nil {
		a.Modes = map[string]string{}
	}
	if a.Overrides.Environments == nil {
		a.Overrides.Environments = map[string]rules.Override{}
	}
	if a.Overrides.Targets == nil {
		a.Overrides.Targets = map[int64]rules.Override{}
	}
}

func (s *service) Thresholds() rules.Thresholds {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.th
}

func (s *service) Alerting() Alerting {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.al
}

func (s *service) Policy(t repository.Target) rules.Policy {
	s.mu.RLock()
	th, al := s.th, s.al
	s.mu.RUnlock()
	var env, tgt *rules.Override
	if o, ok := al.Overrides.Environments[t.Environment]; ok {
		env = &o
	}
	if o, ok := al.Overrides.Targets[t.ID]; ok {
		tgt = &o
	}
	p, err := rules.Effective(th, al.Modes, env, tgt)
	if err != nil {
		// Overrides are validated when saved; one that no longer fits
		// (the global thresholds changed) is ignored rather than stopping
		// the rules.
		p, _ = rules.Effective(th, al.Modes, nil, nil)
	}
	return p
}

func (s *service) LongTransactionThreshold(t repository.Target) time.Duration {
	return time.Duration(s.Policy(t).Thresholds.LongTxWarnSeconds * float64(time.Second))
}

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
	s.notify()
	return nil
}

func (s *service) UpdateAlerting(ctx context.Context, a Alerting) error {
	normalize(&a)
	if err := (rules.Override{Modes: a.Modes}).Validate(); err != nil {
		return err
	}
	if err := a.Overrides.Validate(s.Thresholds()); err != nil {
		return err
	}
	if err := s.store.PutSetting(ctx, keyModes, a.Modes); err != nil {
		return err
	}
	if err := s.store.PutSetting(ctx, keyOverrides, a.Overrides); err != nil {
		return err
	}
	s.mu.Lock()
	s.al = a
	s.mu.Unlock()
	s.notify()
	return nil
}

func (s *service) notify() {
	if s.changed != nil {
		s.changed()
	}
}
