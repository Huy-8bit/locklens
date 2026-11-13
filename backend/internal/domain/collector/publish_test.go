package collector

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/locklens/locklens/internal/domain/cluster"
	"github.com/locklens/locklens/internal/domain/live"
	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/lib/obs"
	"github.com/locklens/locklens/internal/repository"
)

func TestPublishableKeepsWhatTheAPIAndExportRead(t *testing.T) {
	one := 1.0
	pts := make([]live.TimedValue, 50)
	for i := range pts {
		pts[i] = live.TimedValue{At: time.Unix(int64(i), 0), Value: &one}
	}
	v := live.View{
		Points:    map[string][]live.TimedValue{"sessions.active": pts, "short": pts[:1]},
		Totals:    map[string]float64{"xact.commit.per_s": 12},
		Load:      []observe.LoadBucket{{}},
		Connected: true,
		Statements: []observe.StatementsCollection{
			{Deltas: []observe.QueryDelta{{TotalMS: 1}}},
			{Deltas: []observe.QueryDelta{{TotalMS: 5}, {TotalMS: 50}, {TotalMS: 20}}},
		},
	}
	p := publishable(v, 2)
	if got := p.Points["sessions.active"]; len(got) != 2 || got[1].At != pts[49].At {
		t.Fatalf("points: %d kept, last %v", len(got), got)
	}
	if len(p.Points["short"]) != 1 || p.Load != nil || p.Totals["xact.commit.per_s"] != 12 || !p.Connected {
		t.Fatalf("view: %+v", p)
	}
	if len(p.Statements) != 1 || len(p.Statements[0].Deltas) != 2 || p.Statements[0].Deltas[0].TotalMS != 50 || p.Statements[0].Deltas[1].TotalMS != 20 {
		t.Fatalf("statements: %+v", p.Statements)
	}
	if len(v.Points["sessions.active"]) != 50 || len(v.Statements) != 2 {
		t.Fatal("publishable must not change the view it was given")
	}
	if p := publishable(v, 0); p.Statements != nil {
		t.Fatal("without statement export, no statements are published")
	}
}

type publishStore struct {
	repository.ClusterRepository
	saved   [][]int64
	touched [][]int64
}

func (p *publishStore) SaveLiveStates(_ context.Context, _ string, _ time.Time, ids []int64, _ [][]byte) error {
	if len(ids) > 0 {
		p.saved = append(p.saved, ids)
	}
	return nil
}

func (p *publishStore) TouchLiveStates(_ context.Context, _ string, _ time.Time, ids []int64) error {
	if len(ids) > 0 {
		p.touched = append(p.touched, ids)
	}
	return nil
}

type fixedNode struct{ cluster.Node }

func (fixedNode) ID() string  { return "a" }
func (fixedNode) Alone() bool { return false }

func TestPublishOnceWritesChangedViewsInOneStatement(t *testing.T) {
	ps := &publishStore{}
	m := NewManager(Deps{Store: Repositories{ClusterRepository: ps}, Cluster: fixedNode{}, Metrics: obs.NewMetrics(),
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))}, Config{}).(*manager)
	for _, id := range []int64{1, 2, 3} {
		m.states[id] = newState()
		m.workers[id] = &running{}
	}
	m.publishOnce(context.Background())
	if len(ps.saved) != 1 || len(ps.saved[0]) != 3 || len(ps.touched) != 0 {
		t.Fatalf("first publish: saved %v touched %v", ps.saved, ps.touched)
	}
	// Nothing changed: one touch, no rewrite.
	m.publishOnce(context.Background())
	if len(ps.saved) != 1 || len(ps.touched) != 1 || len(ps.touched[0]) != 3 {
		t.Fatalf("unchanged: saved %v touched %v", ps.saved, ps.touched)
	}
	// One view changes: only it is rewritten.
	m.states[2].mu.Lock()
	m.states[2].connectError = ptr("lost")
	m.states[2].mu.Unlock()
	m.publishOnce(context.Background())
	if len(ps.saved) != 2 || len(ps.saved[1]) != 1 || ps.saved[1][0] != 2 || len(ps.touched[1]) != 2 {
		t.Fatalf("one changed: saved %v touched %v", ps.saved, ps.touched)
	}
}

func ptr[T any](v T) *T { return &v }
