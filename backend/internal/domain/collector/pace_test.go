package collector

import (
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/locklens/locklens/internal/repository"
)

func pacedWorker() (*worker, *job) {
	m := &Manager{budget: 0.1, burstDur: time.Minute, log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		workers: map[int64]*running{}, states: map[int64]*State{}}
	st := newState()
	w := &worker{m: m, state: st, target: repository.Target{ID: 1}}
	j := &job{name: CollectorStatements, base: 10 * time.Second, every: 10 * time.Second, burst: 3 * time.Second, slow: 1}
	w.jobs = []*job{j, {name: CollectorTables, base: 5 * time.Minute, every: 5 * time.Minute, slow: 1}}
	m.states[1] = st
	return w, j
}

func TestPaceSlowsDownOverBudgetAndRecovers(t *testing.T) {
	w, j := pacedWorker()
	// Two slow runs are tolerated; the third in a row slows the collector.
	for range 2 {
		w.pace(j, 2*time.Second)
	}
	if j.every != 10*time.Second {
		t.Fatalf("slowed too early: %v", j.every)
	}
	w.pace(j, 2*time.Second)
	if j.slow != 2 || j.every != 20*time.Second || !strings.Contains(j.status.ThrottleReason, "2× less often") {
		t.Fatalf("after 3 slow runs: slow %d every %v reason %q", j.slow, j.every, j.status.ThrottleReason)
	}
	// Never slower than maxSlow.
	for range 20 {
		w.pace(j, time.Minute)
	}
	if j.slow != maxSlow {
		t.Fatalf("slow = %d", j.slow)
	}
	// Cheap again: back to normal step by step, then the reason clears.
	for range 60 {
		w.pace(j, time.Millisecond)
	}
	if j.slow != 1 || j.every != 10*time.Second || j.status.ThrottleReason != "" {
		t.Fatalf("recovered: slow %d every %v reason %q", j.slow, j.every, j.status.ThrottleReason)
	}
}

func TestBurstShortensIntervalsAndWakesTheWorker(t *testing.T) {
	w, j := pacedWorker()
	j.next = time.Now().Add(9 * time.Second)
	w.m.Burst(1, "open incident")
	select {
	case <-w.state.wake:
	default:
		t.Fatal("a starting burst must wake the worker")
	}
	w.applyBurst()
	if j.every != 3*time.Second || time.Until(j.next) > 3*time.Second || !j.status.Burst {
		t.Fatalf("burst: every %v next in %v", j.every, time.Until(j.next))
	}
	if tables := w.jobs[1]; tables.every != 5*time.Minute {
		t.Fatal("collectors without a burst interval keep theirs")
	}
	if b := w.state.Snapshot().Burst; b == nil || b.Reason != "open incident" {
		t.Fatalf("view burst = %+v", b)
	}
	// Extending a running burst does not wake the worker again.
	w.m.Burst(1, "still open")
	select {
	case <-w.state.wake:
		t.Fatal("extension must not wake")
	default:
	}
	// Adaptive sampling off: no burst.
	w2, _ := pacedWorker()
	w2.m.burstDur = 0
	w2.m.Burst(1, "x")
	if _, ok := w2.state.burst(time.Now()); ok {
		t.Fatal("burst with adaptive sampling off")
	}
}
