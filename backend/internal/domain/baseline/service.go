package baseline

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/repository"
)

// Params configure baselines (from the rule thresholds).
type Params struct {
	Days     float64
	Point    time.Duration
	MinCalls int64
	// Recent is left out of baselines: the current window must not be part
	// of the history it is compared with.
	Recent time.Duration
}

// maxStatements bounds the statements of one target that get a baseline
// (those with the most execution time in the last two hours).
const maxStatements = 300

// Service keeps statement rollups current and serves the query baselines of active statements.
type Service interface {
	// Get returns the baselines of a target's active statements (read-only).
	Get(targetID int64) map[observe.QueryKey]Baseline
	// Run refreshes every enabled target every interval until ctx ends.
	Run(ctx context.Context, every time.Duration)
	// Refresh brings a target's rollups up to date and rebuilds its baselines.
	Refresh(ctx context.Context, targetID int64, now time.Time) error
	// Load builds the baselines of some statements over [from, to).
	Load(ctx context.Context, targetID int64, ids []int64, from, to time.Time, p Params) (map[int64]Baseline, error)
}

var _ Service = (*service)(nil)

// service keeps statement rollups up to date and holds the baselines of
// recently active statements in memory, refreshed on a schedule.
type service struct {
	store  Repositories
	params func() Params
	log    *slog.Logger

	mu    sync.RWMutex
	cache map[int64]map[observe.QueryKey]Baseline
}

// New returns a baseline service.
func New(store Repositories, params func() Params, log *slog.Logger) Service {
	return &service{store: store, params: params, log: log, cache: map[int64]map[observe.QueryKey]Baseline{}}
}

// Get returns the baselines of a target's active statements (read-only).
func (s *service) Get(targetID int64) map[observe.QueryKey]Baseline {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cache[targetID]
}

// Run refreshes every enabled target every interval until ctx ends.
func (s *service) Run(ctx context.Context, every time.Duration) {
	t := time.NewTimer(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		targets, err := s.store.ListTargets(ctx)
		if err != nil {
			s.log.Warn("baselines: list targets failed", "error", err.Error())
		}
		for _, tg := range targets {
			if !tg.Enabled || tg.DeletedAt != nil {
				continue
			}
			if err := s.Refresh(ctx, tg.ID, time.Now().UTC()); err != nil && ctx.Err() == nil {
				s.log.Warn("baselines: refresh failed", "target_id", tg.ID, "error", err.Error())
			}
		}
		t.Reset(every)
	}
}

// Refresh brings a target's rollups up to date and rebuilds its baselines.
func (s *service) Refresh(ctx context.Context, targetID int64, now time.Time) error {
	p := s.params()
	if err := s.rollup(ctx, targetID, now, p); err != nil {
		return err
	}
	active, err := s.store.ActiveStatements(ctx, targetID, now.Add(-2*time.Hour), maxStatements)
	if err != nil {
		return err
	}
	out := make(map[observe.QueryKey]Baseline, len(active))
	if len(active) > 0 {
		ids := make([]int64, len(active))
		for i, a := range active {
			ids[i] = a.IdentityID
		}
		from, to := window(now, p)
		byID, err := s.Load(ctx, targetID, ids, from, to, p)
		if err != nil {
			return err
		}
		for _, a := range active {
			if b, ok := byID[a.IdentityID]; ok {
				out[a.Key] = b
			}
		}
	}
	s.mu.Lock()
	s.cache[targetID] = out
	s.mu.Unlock()
	return nil
}

// window is the history a baseline is built from: Days back, up to the
// start of the point before the recent, excluded period.
func window(now time.Time, p Params) (time.Time, time.Time) {
	to := now.Add(-p.Recent).Truncate(p.Point)
	from := to.Add(-time.Duration(p.Days * float64(24*time.Hour)))
	return from, to
}

// Window returns the history window a baseline built now would use.
func Window(now time.Time, p Params) (time.Time, time.Time) { return window(now, p) }

// rollup recomputes the rollup buckets since the last pass (with ten minutes
// of overlap for late writes); the first pass backfills the baseline window
// one day at a time.
func (s *service) rollup(ctx context.Context, targetID int64, now time.Time, p Params) error {
	end := now.Truncate(repository.RollupBucket).Add(repository.RollupBucket)
	last, ok, err := s.store.RollupWatermark(ctx, targetID)
	if err != nil {
		return err
	}
	from := last.Add(-10 * time.Minute)
	if !ok {
		from = now.Add(-time.Duration(p.Days*float64(24*time.Hour)) - time.Hour)
	}
	from = from.Truncate(repository.RollupBucket)
	for from.Before(end) {
		to := from.Add(24 * time.Hour)
		if to.After(end) {
			to = end
		}
		if err := s.store.RollupStatements(ctx, targetID, from, to); err != nil {
			return err
		}
		from = to
	}
	return nil
}

// Load builds the baselines of some statements over [from, to).
func (s *service) Load(ctx context.Context, targetID int64, ids []int64, from, to time.Time, p Params) (map[int64]Baseline, error) {
	points, cov, err := s.store.BaselinePoints(ctx, targetID, ids, from, to, p.Point)
	if err != nil {
		return nil, err
	}
	coverage := make(map[time.Time]float64, len(cov))
	resets := map[time.Time]bool{}
	for _, c := range cov {
		coverage[c.At] = c.Covered
		if c.Reset {
			resets[c.At] = true
		}
	}
	byID := map[int64][]Point{}
	for _, r := range points {
		byID[r.IdentityID] = append(byID[r.IdentityID], Point{At: r.At, Calls: r.Calls, TotalMS: r.TotalMS, Rows: r.Rows,
			BlkRead: r.BlkRead, BlkHit: r.BlkHit, TempW: r.TempW})
	}
	out := make(map[int64]Baseline, len(ids))
	for _, id := range ids {
		out[id] = Build(byID[id], coverage, resets, from, to, p.Point.Seconds(), p.MinCalls)
	}
	return out, nil
}
