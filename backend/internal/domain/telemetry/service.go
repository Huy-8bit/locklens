package telemetry

import (
	"context"
	"slices"
	"time"

	"github.com/locklens/locklens/internal/domain/collector"
	"github.com/locklens/locklens/internal/repository"
)

// Service builds the export of the instances this server collects.
type Service interface {
	// Collect returns the metric families of the instances this server
	// collects, or of one of them (targetID > 0).
	Collect(ctx context.Context, targetID int64) ([]Family, error)
}

var _ Service = (*service)(nil)

// service implements Service.
type service struct {
	store      Repositories
	collectors collector.Manager
	top        int
}

// New returns the service; top is how many statements per instance get
// statement metrics (0: none).
func New(store Repositories, collectors collector.Manager, top int) Service {
	return &service{store: store, collectors: collectors, top: top}
}

func (s *service) Collect(ctx context.Context, targetID int64) ([]Family, error) {
	views := s.collectors.Collecting()
	targets, err := s.store.ListTargets(ctx)
	if err != nil {
		return nil, err
	}
	in := Input{Now: time.Now(), TopStatements: s.top}
	var ids []int64
	for _, t := range targets {
		v, ok := views[t.ID]
		if !ok || !t.Enabled || targetID > 0 && t.ID != targetID {
			continue
		}
		ids = append(ids, t.ID)
		in.Targets = append(in.Targets, Target{ID: t.ID, Name: t.Name, Environment: t.Environment, IntervalSeconds: t.IntervalSeconds, View: v})
	}
	if len(ids) == 0 {
		return Build(in), nil
	}
	fs, err := s.store.ActiveFindingCounts(ctx, ids)
	if err != nil {
		return nil, err
	}
	is, err := s.store.OpenIncidentCounts(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range in.Targets {
		t := &in.Targets[i]
		t.Findings = slices.DeleteFunc(slices.Clone(fs), func(c repository.RuleCount) bool { return c.TargetID != t.ID })
		t.Incidents = slices.DeleteFunc(slices.Clone(is), func(c repository.RuleCount) bool { return c.TargetID != t.ID })
	}
	return Build(in), nil
}
