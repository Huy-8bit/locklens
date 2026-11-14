package cluster

import (
	"context"
	"io"
	"log/slog"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/locklens/locklens/internal/repository"
)

var t0 = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

func ids(n int) []int64 {
	out := make([]int64, n)
	for i := range out {
		out[i] = int64(i + 1)
	}
	return out
}

func held(owner map[int64]string, since time.Time) map[int64]Holding {
	out := map[int64]Holding{}
	for id, m := range owner {
		out[id] = Holding{Holder: m, Since: since}
	}
	return out
}

func ratios(p planned) map[string]float64 {
	out := map[string]float64{}
	for m, s := range p.share {
		out[m] = p.load[m] / s
	}
	return out
}

func TestPlanSpreadsFreeTargetsByCostAndCapacity(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	targets := ids(120)
	weights := map[int64]float64{}
	for _, id := range targets {
		weights[id] = 0.2 + r.Float64()*3 // collector seconds per minute
	}
	members := []Member{{ID: "a", Capacity: 4}, {ID: "b", Capacity: 4}, {ID: "c", Capacity: 8}}
	p := plan(targets, nil, weights, members, t0)
	if len(p.owner) != 120 {
		t.Fatalf("assigned %d of 120", len(p.owner))
	}
	for m, x := range ratios(p) {
		if x < bandLow || x > bandHigh {
			t.Errorf("%s carries %.2f of its share", m, x)
		}
	}
	if p.share["c"] < 1.99*p.share["a"] || p.share["c"] > 2.01*p.share["a"] {
		t.Errorf("shares %v: c has twice a's capacity", p.share)
	}
}

func TestPlanIsDeterministic(t *testing.T) {
	targets := ids(50)
	weights := map[int64]float64{3: 9, 7: 4, 20: 1}
	a := plan(targets, nil, weights, []Member{{ID: "a", Capacity: 1}, {ID: "b", Capacity: 1}, {ID: "c", Capacity: 2}}, t0)
	b := plan(targets, nil, weights, []Member{{ID: "c", Capacity: 2}, {ID: "b", Capacity: 1}, {ID: "a", Capacity: 1}}, t0)
	for id, m := range a.owner {
		if b.owner[id] != m {
			t.Fatalf("target %d: %s vs %s: servers planning from the same inputs must agree", id, m, b.owner[id])
		}
	}
}

func TestPlanKeepsHoldingsInsideTheBand(t *testing.T) {
	// a carries 11 targets, b 9: 10% over and under their shares.
	owner := map[int64]string{}
	for _, id := range ids(20) {
		owner[id] = "a"
		if id > 11 {
			owner[id] = "b"
		}
	}
	p := plan(ids(20), held(owner, t0.Add(-time.Hour)), nil, []Member{{ID: "a", Capacity: 1}, {ID: "b", Capacity: 1}}, t0)
	for id, m := range owner {
		if p.owner[id] != m {
			t.Fatalf("target %d moved %s → %s inside the ±15%% band", id, m, p.owner[id])
		}
	}
}

func TestPlanMovesOnlyTargetsHeldLongEnough(t *testing.T) {
	// a carries 13 of 20 (1.3 of its share), b 7 (0.7).
	owner := map[int64]string{}
	for _, id := range ids(20) {
		owner[id] = "a"
		if id > 13 {
			owner[id] = "b"
		}
	}
	members := []Member{{ID: "a", Capacity: 1}, {ID: "b", Capacity: 1}}
	recent := plan(ids(20), held(owner, t0.Add(-5*time.Minute)), nil, members, t0)
	for id, m := range owner {
		if recent.owner[id] != m {
			t.Fatalf("target %d held 5 minutes moved", id)
		}
	}
	old := plan(ids(20), held(owner, t0.Add(-minHold)), nil, members, t0)
	moved := 0
	for id, m := range owner {
		if old.owner[id] != m {
			moved++
			if m != "a" {
				t.Fatalf("target %d moved from %s, the server under its share", id, m)
			}
		}
	}
	if moved == 0 {
		t.Fatal("a 30% imbalance with targets held 10 minutes must move one")
	}
	// Far over its share (a server that collected alone), a gives targets
	// at once.
	for id := range owner {
		owner[id] = "a"
	}
	urgent := plan(ids(20), held(owner, t0.Add(-time.Minute)), nil, members, t0)
	if urgent.load["b"] == 0 {
		t.Fatal("a server holding everything must give targets to a new one without waiting minHold")
	}
}

func TestPlanNeverMovesATargetPastBalance(t *testing.T) {
	// a holds one heavy target and nothing else; b holds three light ones.
	// a is far over its share, but moving the heavy target would leave b
	// busier than a was: nothing moves (and nothing can come back).
	owner := map[int64]string{1: "a", 2: "b", 3: "b", 4: "b"}
	weights := map[int64]float64{1: 30, 2: 1, 3: 1, 4: 1}
	p := plan(ids(4), held(owner, t0.Add(-time.Hour)), weights, []Member{{ID: "a", Capacity: 1}, {ID: "b", Capacity: 1}}, t0)
	if p.owner[1] != "a" {
		t.Fatalf("the heavy target moved to %s", p.owner[1])
	}
}

func TestPlanRespectsMaxTargets(t *testing.T) {
	owner := map[int64]string{1: "a", 2: "a", 3: "a", 4: "a"}
	current := held(owner, t0.Add(-time.Hour))
	current[4] = Holding{Holder: "a", Since: t0.Add(-time.Minute)} // the newest goes first
	p := plan(ids(6), current, nil, []Member{{ID: "a", Capacity: 1, MaxTargets: 3}, {ID: "b", Capacity: 1, MaxTargets: 2}}, t0)
	count := map[string]int{}
	for _, m := range p.owner {
		count[m]++
	}
	if count["a"] != 3 || count["b"] != 2 || len(p.owner) != 5 {
		t.Fatalf("counts %v of %d assigned: limits 3 and 2", count, len(p.owner))
	}
	if p.owner[4] == "a" {
		t.Fatal("over its limit, a must give up its newest target")
	}
}

// Many rounds of planning on noisy weights (the measured averages wobble)
// settle: after the start nothing moves, and no target ever comes back to a
// collector it left.
func TestPlanDoesNotFlap(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 9))
	targets := ids(300)
	base := map[int64]float64{}
	for _, id := range targets {
		base[id] = 0.1 + r.ExpFloat64() // a few expensive targets, many cheap
	}
	members := []Member{{ID: "a", Capacity: 4}, {ID: "b", Capacity: 4}, {ID: "c", Capacity: 4}}
	current := map[int64]Holding{}
	// a collected alone first.
	for _, id := range targets {
		current[id] = Holding{Holder: "a", Since: t0}
	}
	left := map[int64]map[string]bool{}
	now := t0
	lateMoves := 0
	for round := range 400 { // 100 minutes of syncs every 15 s
		now = now.Add(15 * time.Second)
		weights := map[int64]float64{}
		for id, w := range base {
			weights[id] = w * (0.9 + 0.2*r.Float64())
		}
		p := plan(targets, current, weights, members, now)
		for _, id := range targets {
			from, to := current[id].Holder, p.owner[id]
			if to == "" {
				t.Fatalf("round %d: target %d unassigned", round, id)
			}
			if from == to {
				continue
			}
			if left[id][to] {
				t.Fatalf("round %d: target %d went back to %s", round, id, to)
			}
			if left[id] == nil {
				left[id] = map[string]bool{}
			}
			left[id][from] = true
			current[id] = Holding{Holder: to, Since: now}
			if round >= 200 {
				lateMoves++
			}
		}
	}
	if lateMoves > 0 {
		t.Fatalf("%d moves in the second half: the plan does not settle", lateMoves)
	}
	p := plan(targets, current, base, members, now)
	for m, x := range ratios(p) {
		if x < bandLow || x > bandHigh {
			t.Errorf("settled with %s at %.2f of its share", m, x)
		}
	}
}

func TestSelfFencingAfterMissedRenewals(t *testing.T) {
	ctx := context.Background()
	st := newMem()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := New(Repositories{ClusterRepository: st}, Self{ID: "a"}, log, nil).(*node)
	b := New(Repositories{ClusterRepository: st}, Self{ID: "b"}, log, nil).(*node)
	a.now, b.now = st.clock, st.clock
	a.beat(ctx)
	if _, err := a.Assign(ctx, []int64{1}); err != nil {
		t.Fatal(err)
	}
	f, ok := a.Fence(1)
	if !ok || f.Holder != "a" || f.Epoch == 0 {
		t.Fatalf("fence after renewal: %+v %v", f, ok)
	}
	if _, ok := a.Fence(2); ok {
		t.Fatal("no fence for a target not held")
	}
	// The last renewal is older than the lease minus the margin, on a's own
	// clock. Alone, a keeps writing (nobody can take over; the store still
	// checks the fence) ...
	a.renewed = time.Now().Add(-(targetLeaseTTL - fenceMargin))
	if _, ok := a.Fence(1); !ok {
		t.Fatal("a server alone does not fence itself")
	}
	// ... with another server alive, it stops.
	b.beat(ctx)
	a.beat(ctx)
	a.renewed = time.Now().Add(-(targetLeaseTTL - fenceMargin))
	if _, ok := a.Fence(1); ok {
		t.Fatal("self-fencing: the lease may have expired")
	}
	// b takes the target over once the lease expired: a new epoch.
	st.advance(targetLeaseTTL + time.Second)
	b.beat(ctx)
	if got, _ := b.Assign(ctx, []int64{1}); !got[1] {
		t.Fatal("b did not take the expired lease")
	}
	if fb, _ := b.Fence(1); fb.Epoch == f.Epoch {
		t.Fatalf("epoch %d kept across holders", fb.Epoch)
	}
}

func TestTargetWeightsAreAveragedAndStored(t *testing.T) {
	ctx := context.Background()
	st := newMem()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := New(Repositories{ClusterRepository: st}, Self{ID: "a"}, log, nil).(*node)
	a.beat(ctx)
	if _, err := a.Assign(ctx, []int64{1, 2}); err != nil {
		t.Fatal(err)
	}
	// First full minute: 6 s of collector time on target 1, none on 2.
	a.weighedAt = time.Now().Add(-time.Minute)
	a.weighed = map[int64]bool{1: true, 2: true}
	a.Busy(1, 6*time.Second)
	if _, err := a.Assign(ctx, []int64{1, 2}); err != nil {
		t.Fatal(err)
	}
	w1 := st.weights[1].BusyPerMin
	if w1 == nil || *w1 < 5.9 || *w1 > 6.01 {
		t.Fatalf("target 1 weight %v, want about 6 s/min", w1)
	}
	if w2 := st.weights[2].BusyPerMin; w2 == nil || *w2 != 0 {
		t.Fatalf("target 2 weight %v, want 0", w2)
	}
	// Next minute: 1 s. The average moves by weightAlpha towards it.
	a.weighedAt = time.Now().Add(-time.Minute)
	a.Busy(1, time.Second)
	if _, err := a.Assign(ctx, []int64{1, 2}); err != nil {
		t.Fatal(err)
	}
	want := 6 + weightAlpha*(1-6)
	if got := *st.weights[1].BusyPerMin; got < want-0.05 || got > want+0.05 {
		t.Fatalf("averaged weight %.2f, want %.2f", got, want)
	}
	// An override wins over the measurement.
	o := 50.0
	st.weights[2] = repository.TargetWeight{BusyPerMin: st.weights[2].BusyPerMin, Override: &o}
	if v, _ := st.weights[2].Value(); v != 50 {
		t.Fatalf("override not used: %v", v)
	}
}
