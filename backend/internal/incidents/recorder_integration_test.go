//go:build integration

package incidents_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/collector"
	"github.com/locklens/locklens/internal/incidents"
	"github.com/locklens/locklens/internal/model"
	"github.com/locklens/locklens/internal/obs"
	"github.com/locklens/locklens/internal/observe"
	"github.com/locklens/locklens/internal/report"
	"github.com/locklens/locklens/internal/rules"
	"github.com/locklens/locklens/internal/storage"
)

// These tests drive the recorder with synthetic activity samples and a real
// monitoring store, so lifecycle timing is deterministic: every sample
// carries its own timestamp, 5 s apart.

const step = 5 * time.Second

var t0 = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

type thresholds struct{ t rules.Thresholds }

func (f thresholds) Thresholds() rules.Thresholds { return f.t }

type nopSink struct{}

func (nopSink) Publish(string, any) {}

func freshStore(t *testing.T) *storage.Store {
	t.Helper()
	admin := os.Getenv("LOCKLENS_IT_ADMIN_DSN")
	if admin == "" {
		t.Fatal("LOCKLENS_IT_ADMIN_DSN is required; run scripts/integration-test.sh")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	name := fmt.Sprintf("store_rec_%d", time.Now().UnixNano())
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), admin)
		if err == nil {
			_, _ = c.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
			c.Close(context.Background())
		}
	})
	u, _ := url.Parse(admin)
	u.Path = "/" + name
	s, err := storage.Open(ctx, u.String(), storage.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if _, err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return s
}

type harness struct {
	t      *testing.T
	store  *storage.Store
	target storage.Target
	rec    *incidents.Recorder
	cfg    incidents.Config
}

func newHarness(t *testing.T, cfg incidents.Config) *harness {
	s := freshStore(t)
	tg, err := s.CreateTarget(context.Background(), storage.Target{Name: "rec", DSNCiphertext: []byte{1}, DisplayHost: "h",
		DisplayPort: 5432, DisplayDatabase: "d", DisplayUser: "u", Enabled: true, IntervalSeconds: int(step / time.Second), MaxConnections: 1})
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, store: s, target: tg, cfg: cfg}
	h.rec = h.newRecorder()
	return h
}

func (h *harness) newRecorder() *incidents.Recorder {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return incidents.NewRecorder(h.store, h.cfg, thresholds{rules.Defaults()}, obs.NewMetrics(), nopSink{}, log)
}

func ptr[T any](v T) *T { return &v }

// view builds an activity collection at `at`. With blocking, pid 1 blocks 2,
// which blocks 3; root is the PID of the root blocker (a new PID models a
// different root session).
func view(at time.Time, blocking bool, root int) collector.View {
	start := t0.Add(-time.Hour)
	sess := func(pid int) model.Session {
		return model.Session{PID: pid, State: ptr("active"), BackendType: ptr("client backend"), DetailsVisible: true,
			BackendStart: &start, XactStart: ptr(at.Add(-time.Minute)), User: ptr("app"), WaitingLocks: []model.WaitingLock{}}
	}
	snap := &model.Snapshot{SchemaVersion: 1, ServerTime: at, CollectedAt: at,
		Sessions: []model.Session{sess(root), sess(root + 1), sess(root + 2)}, Edges: []model.Edge{}}
	if blocking {
		snap.Edges = []model.Edge{{Blocker: root, Waiter: root + 1}, {Blocker: root + 1, Waiter: root + 2}}
	}
	rep := report.Build(snap, 5*time.Minute)
	return collector.View{
		Activity:     &observe.Activity{At: at, Snapshot: snap, Counts: observe.ActivityCounts{UsedConnections: 5, MaxConnections: 100, ReservedConnections: 3}},
		Report:       rep,
		Capabilities: &observe.Capabilities{ReadAllStats: true, TrackActivities: true},
	}
}

// feed analyzes n samples starting at `from` and returns the time after the
// last one.
func (h *harness) feed(rec *incidents.Recorder, from time.Time, n int, blocking bool, root int) time.Time {
	at := from
	for range n {
		rec.Analyze(context.Background(), h.target, view(at, blocking, root))
		at = at.Add(step)
	}
	return at
}

func (h *harness) incidents() []storage.Incident {
	h.t.Helper()
	list, _, err := h.store.ListIncidents(context.Background(), storage.IncidentFilter{TargetID: h.target.ID, RuleID: rules.RuleBlockingChain, Limit: 100})
	if err != nil {
		h.t.Fatal(err)
	}
	return list
}

func (h *harness) kinds(id int64) map[string]int {
	h.t.Helper()
	evs, err := h.store.ListIncidentEvents(context.Background(), id, 0, 2000)
	if err != nil {
		h.t.Fatal(err)
	}
	out := map[string]int{}
	for _, e := range evs {
		out[e.Kind]++
	}
	return out
}

func TestLifecycleCooldownAndRegrouping(t *testing.T) {
	h := newHarness(t, incidents.DefaultConfig())
	// Default persistence: 3 samples and 30 s, so the 7th sample fires.
	at := h.feed(h.rec, t0, 6, true, 100)
	if n := len(h.incidents()); n != 0 {
		t.Fatalf("incident opened before the persistence threshold: %d", n)
	}
	at = h.feed(h.rec, at, 1, true, 100)
	list := h.incidents()
	if len(list) != 1 || list[0].State != "open" {
		t.Fatalf("want one open incident, got %+v", list)
	}
	id := list[0].ID
	k := h.kinds(id)
	if k["pre_trigger"] == 0 || k["trigger"] != 1 || k["blocking_snapshot"] == 0 {
		t.Errorf("opening events: %v", k)
	}

	// More samples of the same chain: still one incident (deduplication).
	at = h.feed(h.rec, at, 5, true, 100)
	if n := len(h.incidents()); n != 1 {
		t.Fatalf("duplicate incident: %d", n)
	}

	// Three clear samples resolve it.
	at = h.feed(h.rec, at, 2, false, 100)
	if got := h.incidents()[0].State; got != "open" {
		t.Fatalf("resolved after 2 clear samples: %s", got)
	}
	at = h.feed(h.rec, at, 1, false, 100)
	in := h.incidents()[0]
	if in.State != "resolved" || in.Resolution == nil || *in.Resolution != incidents.ResolutionCleared {
		t.Fatalf("want resolved by clearing, got %+v", in)
	}

	// The condition returns within the cooldown: the same incident reopens.
	at = h.feed(h.rec, at.Add(time.Minute), 7, true, 100)
	list = h.incidents()
	if len(list) != 1 || list[0].ID != id || list[0].State != "open" {
		t.Fatalf("want incident %d reopened, got %+v", id, list)
	}
	if h.kinds(id)["retriggered"] != 1 {
		t.Errorf("missing retriggered event: %v", h.kinds(id))
	}
	at = h.feed(h.rec, at, 3, false, 100)

	// After the cooldown a new incident opens.
	h.feed(h.rec, at.Add(15*time.Minute), 7, true, 100)
	list = h.incidents()
	if len(list) != 2 {
		t.Fatalf("want a second incident after the cooldown, got %d", len(list))
	}
}

func TestRootChangeDoesNotResolve(t *testing.T) {
	h := newHarness(t, incidents.DefaultConfig())
	at := h.feed(h.rec, t0, 7, true, 100)
	if len(h.incidents()) != 1 {
		t.Fatal("incident did not open")
	}
	// A different root session takes over; its own finding needs persistence
	// again, but blocking never stopped.
	h.feed(h.rec, at, 10, true, 200)
	in := h.incidents()
	if len(in) != 1 || in[0].State != "open" {
		t.Fatalf("blocking continued under a new root; want the incident open, got %+v", in)
	}
	if k := h.kinds(in[0].ID); k["resolved"] != 0 || k["retriggered"] != 0 {
		t.Errorf("no resolution expected while blocking persists: %v", k)
	}
}

func TestRestartRecovery(t *testing.T) {
	h := newHarness(t, incidents.DefaultConfig())
	at := h.feed(h.rec, t0, 8, true, 100)
	list := h.incidents()
	if len(list) != 1 {
		t.Fatal("incident did not open")
	}
	// A new recorder, as after a restart: in-memory history is gone.
	rec := h.newRecorder()
	if err := rec.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if h.kinds(list[0].ID)["recorder_restarted"] != 1 {
		t.Fatal("restart not recorded")
	}
	// Blocking persists: rules need fresh history, but the incident must not
	// be resolved meanwhile, and no second incident may open.
	at = h.feed(rec, at.Add(time.Minute), 10, true, 100)
	list = h.incidents()
	if len(list) != 1 || list[0].State != "open" {
		t.Fatalf("after restart: %+v", list)
	}
	if k := h.kinds(list[0].ID); k["resolved"] != 0 || k["retriggered"] != 0 {
		t.Errorf("no resolution expected while blocking persists across the restart: %v", k)
	}
	h.feed(rec, at, 3, false, 100)
	if got := h.incidents()[0]; got.State != "resolved" {
		t.Fatalf("not resolved after clearing: %+v", got)
	}
}

func TestSizeLimitKeepsLifecycleEvents(t *testing.T) {
	cfg := incidents.DefaultConfig()
	cfg.MaxEvents = 15
	cfg.SampleEvery = 0 // a sample event for every collection
	h := newHarness(t, cfg)
	at := h.feed(h.rec, t0, 40, true, 100)
	in := h.incidents()[0]
	if !in.Truncated {
		t.Fatalf("incident should be truncated: %+v", in)
	}
	before := in.EventCount
	h.feed(h.rec, at, 3, false, 100)
	in = h.incidents()[0]
	if in.State != "resolved" {
		t.Fatalf("not resolved: %+v", in)
	}
	k := h.kinds(in.ID)
	if k["resolved"] != 1 || k["truncated"] != 1 {
		t.Errorf("lifecycle events must survive truncation: %v", k)
	}
	if in.EventCount > cfg.MaxEvents+3 || before > cfg.MaxEvents+1 {
		t.Errorf("event count %d (before resolution %d) exceeds the limit %d", in.EventCount, before, cfg.MaxEvents)
	}
}

func TestDurationLimitAndCooldown(t *testing.T) {
	cfg := incidents.DefaultConfig()
	cfg.MaxDuration = time.Minute
	h := newHarness(t, cfg)
	// Blocking for 4 minutes: recording stops after 1 minute and does not
	// restart within the 10-minute cooldown.
	at := h.feed(h.rec, t0, 48, true, 100)
	list := h.incidents()
	if len(list) != 1 || list[0].State != "resolved" || *list[0].Resolution != incidents.ResolutionDuration {
		t.Fatalf("want one incident stopped by the duration limit, got %+v", list)
	}
	// Still blocking after the cooldown: a new incident opens.
	h.feed(h.rec, at.Add(10*time.Minute), 8, true, 100)
	if n := len(h.incidents()); n != 2 {
		t.Fatalf("want a new incident after the cooldown, got %d", n)
	}
}
