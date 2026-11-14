package collector

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/locklens/locklens/internal/domain/incidents"
	"github.com/locklens/locklens/internal/domain/live"
	"github.com/locklens/locklens/internal/domain/rules"
	"github.com/locklens/locklens/internal/lib/obs"
	"github.com/locklens/locklens/internal/repository"
)

type listTargets struct {
	repository.TargetRepository
	targets []repository.Target
}

func (l listTargets) ListTargets(context.Context) ([]repository.Target, error) { return l.targets, nil }

// slowAdopt blocks Adopt until release is closed.
type slowAdopt struct {
	adopting chan int64
	release  chan struct{}
}

func (r *slowAdopt) Adopt(_ context.Context, id int64) error {
	r.adopting <- id
	<-r.release
	return nil
}
func (r *slowAdopt) Forget(int64)                                                       {}
func (r *slowAdopt) Unreachable(context.Context, repository.Target, rules.Reachability) {}
func (r *slowAdopt) Analyze(context.Context, repository.Target, live.View) incidents.Analysis {
	return incidents.Analysis{}
}

// Taking over targets (adopting their open incidents) must not block API
// reads of the live views.
func TestSyncDoesNotHoldTheLockWhileAdopting(t *testing.T) {
	rec := &slowAdopt{adopting: make(chan int64, 4), release: make(chan struct{})}
	targets := []repository.Target{{ID: 1, Enabled: true, IntervalSeconds: 10}, {ID: 2, Enabled: true, IntervalSeconds: 10}}
	m := NewManager(Deps{Store: Repositories{TargetRepository: listTargets{targets: targets}}, Recorder: rec,
		Metrics: obs.NewMetrics(), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}, Config{}).(*manager)
	m.states[1] = newState()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		m.sync(ctx)
		close(done)
	}()
	select {
	case <-rec.adopting:
	case <-time.After(5 * time.Second):
		t.Fatal("Adopt was not called")
	}

	read := make(chan bool, 1)
	go func() {
		_, ok := m.View(context.Background(), 1)
		read <- ok
	}()
	select {
	case ok := <-read:
		if !ok {
			t.Fatal("View found no state for target 1")
		}
	case <-time.After(time.Second):
		t.Fatal("View blocked while sync was adopting incidents")
	}

	// Let sync finish; the workers start on a cancelled context and stop at once.
	cancel()
	close(rec.release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("sync did not finish")
	}
	m.stopAll()
}
