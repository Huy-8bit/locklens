// Package incidents records incidents from rule findings.
//
// For each target the recorder keeps a bounded in-memory history: activity
// summaries for rule persistence and a pre-trigger buffer of recent samples.
// When a trigger rule fires it opens an incident, copies the pre-trigger
// buffer into the timeline, and keeps appending evidence (summaries, changed
// blocking graphs, workload aggregates, gaps) until the condition has been
// clear for a few samples. Cooldown regroups flapping conditions into one
// incident; duration and size limits bound each incident.
//
// Evidence is written once and never edited. Acknowledgement is separate
// metadata. In-memory buffers are lost on restart; incident metadata and
// stored events are not.
package incidents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/locklens/locklens/internal/domain/baseline"
	"github.com/locklens/locklens/internal/domain/events"
	"github.com/locklens/locklens/internal/domain/live"
	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/domain/rules"
	"github.com/locklens/locklens/internal/domain/settings"
	"github.com/locklens/locklens/internal/lib/obs"
	"github.com/locklens/locklens/internal/repository"
)

// Config bounds recording.
type Config struct {
	PreTriggerSamples   int           `json:"pre_trigger_samples"`
	ResolveAfterSamples int           `json:"resolve_after_samples"`
	Cooldown            time.Duration `json:"cooldown"`
	MaxDuration         time.Duration `json:"max_duration"`
	MaxEvents           int           `json:"max_events"`
	MaxBytes            int64         `json:"max_bytes"`
	SampleEvery         time.Duration `json:"sample_every"`
	HistorySamples      int           `json:"history_samples"`
}

// DefaultConfig returns the default limits.
func DefaultConfig() Config {
	return Config{PreTriggerSamples: 30, ResolveAfterSamples: 3, Cooldown: 10 * time.Minute, MaxDuration: 2 * time.Hour,
		MaxEvents: 1000, MaxBytes: 5 << 20, SampleEvery: time.Minute, HistorySamples: 360}
}

// Event kinds.
const (
	KindPreTrigger       = "pre_trigger"
	KindTrigger          = "trigger"
	KindSample           = "sample"
	KindBlockingSnapshot = "blocking_snapshot"
	KindFinding          = "finding"
	KindWorkload         = "workload"
	KindGap              = "gap"
	KindRetriggered      = "retriggered"
	KindPostTrigger      = "post_trigger"
	KindResolved         = "resolved"
	KindRestarted        = "recorder_restarted"
	KindTruncated        = "truncated"
	KindVisibility       = "visibility"
	KindSessions         = "sessions"
	KindLoad             = "load"
)

// Resolutions.
const (
	ResolutionCleared  = "condition cleared"
	ResolutionDuration = "duration limit reached"
)

type preSample struct {
	at       time.Time
	summary  rules.ActivitySample
	metrics  map[string]*float64
	snapshot json.RawMessage
	findings []findingRef
}

type findingRef struct {
	RuleID   string         `json:"rule_id"`
	Key      string         `json:"key"`
	Severity rules.Severity `json:"severity"`
	Title    string         `json:"title"`
	Status   rules.Status   `json:"status"`
}

type openIncident struct {
	id           int64
	ruleID       string
	openedAt     time.Time
	severity     string
	title        string
	clear        int
	events       int
	bytes        int64
	truncated    bool
	lastSampleAt time.Time
	lastGraphSig string
	findingKeys  map[string]bool
	lastWorkload time.Time
}

type targetState struct {
	// mu serialises the analyses of one target (targets are analysed
	// concurrently).
	mu sync.Mutex
	// stale: a write failed after the in-memory incidents changed; they are
	// read back from the store before the next analysis.
	stale       bool
	history     []rules.ActivitySample
	pre         []preSample
	lastAnalyze time.Time
	lastPreSig  string
	open        map[string]*openIncident
}

// Recorder evaluates the rules after each activity collection of a target and records incidents with their evidence.
type Recorder interface {
	// Adopt attaches the open incidents of a target this server starts
	// collecting (at start-up, or after taking it over from another server) and
	// records the interruption in their timelines. The collector manager calls
	// it before the target's first collection.
	Adopt(ctx context.Context, targetID int64) error
	// Forget drops the in-memory state of a target another server now
	// collects; its incidents stay open and that server adopts them.
	Forget(targetID int64)
	// Unreachable records that connecting to a target, or collecting its
	// activity, keeps failing: once the failures last target_down_seconds it
	// reports target.unreachable and, in incident mode, opens an incident. The
	// incident resolves at the next successful activity collection.
	Unreachable(ctx context.Context, t repository.Target, rc rules.Reachability)
	// Analyze evaluates rules for one target and advances incident lifecycles.
	Analyze(ctx context.Context, t repository.Target, v live.View) Analysis
}

var _ Recorder = (*recorder)(nil)

// recorder evaluates the rules after each activity collection of a target
// and records incidents with their evidence.
type recorder struct {
	store   Repositories
	cfg     Config
	th      settings.Service
	metrics *obs.Metrics
	events  events.Publisher
	log     *slog.Logger
	// baselines gives statement rules each target's query baselines; nil
	// leaves them to compare with the previous window only.
	baselines baseline.Service
	// fences gives the lease fence of each target: a server writes incidents
	// and findings of a target only while it holds the target's lease. Nil:
	// writes are not fenced.
	fences Fences

	// mu guards the targets map only; each target has its own lock, so a
	// slow analysis (store writes) of one target does not hold up the others.
	mu      sync.Mutex
	targets map[int64]*targetState
	// open counts the open incidents of all targets (the gauge).
	open atomic.Int64
}

// txn is the write in progress for one target: the store (the fenced
// transaction) and the events to publish once it committed. It travels in
// the context of the analysis (see write).
type txn struct {
	w       repository.FencedWrites
	pending []pendingEvent
}

type txnKey struct{}

// tx returns the store of the write in progress.
func tx(ctx context.Context) repository.FencedWrites { return ctx.Value(txnKey{}).(*txn).w }

// Fences gives the lease fence to write about a target under; false when
// this server does not hold (or may have lost) the target's lease.
type Fences interface {
	Fence(targetID int64) (repository.Fence, bool)
}

type pendingEvent struct {
	targetID, incidentID int64
	kind                 string
}

// NewRecorder returns a recorder. baselines and fences may be nil.
func NewRecorder(store Repositories, cfg Config, th settings.Service, baselines baseline.Service, fences Fences, metrics *obs.Metrics, events events.Publisher, log *slog.Logger) Recorder {
	return &recorder{store: store, cfg: cfg, th: th, baselines: baselines, fences: fences, metrics: metrics, events: events, log: log, targets: map[int64]*targetState{}}
}

// write runs fn with the store of the write in the context (see tx), in
// one transaction under the target's lease fence (when the recorder has
// fences): everything fn writes is committed together, and nothing when the
// lease was lost (repository.ErrLeaseLost). Incident events are published
// after the commit. Called with ts.mu held.
func (r *recorder) write(ctx context.Context, ts *targetState, targetID int64, fn func(ctx context.Context) error) error {
	x := &txn{}
	run := func(w repository.FencedWrites) error {
		x.w, x.pending = w, x.pending[:0]
		return fn(context.WithValue(ctx, txnKey{}, x))
	}
	before := len(ts.open)
	var err error
	if r.fences == nil {
		err = run(r.store)
	} else if f, ok := r.fences.Fence(targetID); !ok {
		err = repository.ErrLeaseLost
	} else {
		err = r.store.Fenced(ctx, f, run)
	}
	r.countOpen(before, ts)
	if err != nil {
		if errors.Is(err, repository.ErrLeaseLost) {
			r.metrics.FencedWrites.Inc("incidents")
		}
		ts.stale = true
		return err
	}
	if r.events != nil {
		for _, e := range x.pending {
			r.events.Publish(e.kind, map[string]any{"target_id": e.targetID, "incident_id": e.incidentID})
		}
	}
	return nil
}

// countOpen updates the open incidents gauge after a target's open
// incidents changed from before.
func (r *recorder) countOpen(before int, ts *targetState) {
	if d := len(ts.open) - before; d != 0 {
		r.metrics.IncidentsOpen.Set(float64(r.open.Add(int64(d))))
	}
}

// resync reads a target's open incidents back from the store after a failed
// write left the in-memory ones uncertain.
func (r *recorder) resync(ctx context.Context, targetID int64, ts *targetState) error {
	open, err := r.store.OpenIncidents(ctx)
	if err != nil {
		return err
	}
	before := len(ts.open)
	ts.open = map[string]*openIncident{}
	for _, in := range open {
		if in.TargetID != nil && *in.TargetID == targetID {
			ts.open[in.RuleID] = adopted(in)
		}
	}
	r.countOpen(before, ts)
	ts.stale = false
	return nil
}

func adopted(in repository.Incident) *openIncident {
	return &openIncident{id: in.ID, ruleID: in.RuleID, openedAt: in.OpenedAt, severity: in.Severity,
		title: in.Title, events: in.EventCount, bytes: in.SizeBytes, truncated: in.Truncated, findingKeys: map[string]bool{}}
}

// lock returns a target's state, locked; the caller unlocks ts.mu.
func (r *recorder) lock(id int64) *targetState {
	for {
		r.mu.Lock()
		ts, ok := r.targets[id]
		if !ok {
			ts = &targetState{open: map[string]*openIncident{}}
			r.targets[id] = ts
		}
		r.mu.Unlock()
		ts.mu.Lock()
		r.mu.Lock()
		current := r.targets[id] == ts
		r.mu.Unlock()
		if current {
			return ts
		}
		ts.mu.Unlock() // forgotten meanwhile
	}
}

// Adopt attaches the open incidents of a target this server starts
// collecting (at start-up, or after taking it over from another server) and
// records the interruption in their timelines. The collector manager calls
// it before the target's first collection.
func (r *recorder) Adopt(ctx context.Context, targetID int64) error {
	open, err := r.store.OpenIncidents(ctx)
	if err != nil {
		return err
	}
	ts := r.lock(targetID)
	defer ts.mu.Unlock()
	now := time.Now().UTC()
	return r.write(ctx, ts, targetID, func(ctx context.Context) error {
		for _, in := range open {
			if in.TargetID == nil || *in.TargetID != targetID {
				continue
			}
			if _, ok := ts.open[in.RuleID]; ok {
				continue
			}
			ts.open[in.RuleID] = adopted(in)
			payload, _ := json.Marshal(map[string]any{"previous_last_seen": in.LastSeenAt, "restarted_at": now})
			ev := repository.IncidentEvent{At: now, Kind: KindRestarted, Payload: payload,
				Summary: fmt.Sprintf("LockLens restarted or another LockLens server took over collecting this instance; nothing was recorded between %s and %s, and in-memory buffers were lost.",
					in.LastSeenAt.UTC().Format(time.RFC3339), now.Format(time.RFC3339))}
			if err := tx(ctx).AppendIncidentEvents(ctx, in.ID, nil, []repository.IncidentEvent{ev}); err != nil {
				return err
			}
		}
		return nil
	})
}

// Forget drops the in-memory state of a target another server now
// collects; its incidents stay open and that server adopts them.
func (r *recorder) Forget(targetID int64) {
	r.mu.Lock()
	ts, ok := r.targets[targetID]
	delete(r.targets, targetID)
	r.mu.Unlock()
	if !ok {
		return
	}
	ts.mu.Lock()
	before := len(ts.open)
	ts.open = map[string]*openIncident{}
	r.countOpen(before, ts)
	ts.mu.Unlock()
}

// Analysis is the outcome of one analysis that the collector acts on.
type Analysis struct {
	// Burst says why the target should be collected faster now (an open
	// incident or an active problem); empty when it need not be.
	Burst string
	// LeaseLost: nothing of this analysis was recorded because this server
	// no longer holds the target's lease (fencing).
	LeaseLost bool
}

// Analyze evaluates rules for one target and advances incident lifecycles.
func (r *recorder) Analyze(ctx context.Context, t repository.Target, v live.View) Analysis {
	if v.Activity == nil {
		return Analysis{}
	}
	ts := r.lock(t.ID)
	defer ts.mu.Unlock()
	if ts.stale {
		if err := r.resync(ctx, t.ID, ts); err != nil {
			r.log.Warn("incident analysis skipped: reading open incidents failed", "target_id", t.ID, "error", err.Error())
			return Analysis{}
		}
	}
	var a Analysis
	err := r.write(ctx, ts, t.ID, func(ctx context.Context) (err error) {
		a, err = r.analyze(ctx, t, ts, v)
		return err
	})
	switch {
	case errors.Is(err, repository.ErrLeaseLost):
		return Analysis{LeaseLost: true}
	case err != nil:
		r.log.Warn("incident analysis failed", "target_id", t.ID, "error", err.Error())
	}
	return a
}

func (r *recorder) analyze(ctx context.Context, t repository.Target, ts *targetState, v live.View) (Analysis, error) {
	now := v.Activity.At.UTC()
	interval := time.Duration(t.IntervalSeconds) * time.Second
	pol := r.th.Policy(t)
	th := pol.Thresholds

	sample := rules.Summarize(v.Activity, v.Report)
	ts.history = append(ts.history, sample)
	if len(ts.history) > r.cfg.HistorySamples {
		ts.history = slices.Clone(ts.history[len(ts.history)-r.cfg.HistorySamples:])
	}
	var gap *repository.IncidentEvent
	if !ts.lastAnalyze.IsZero() && now.Sub(ts.lastAnalyze) > 3*interval+15*time.Second {
		payload, _ := json.Marshal(map[string]any{"from": ts.lastAnalyze, "to": now})
		gap = &repository.IncidentEvent{At: now, Kind: KindGap, Payload: payload,
			Summary: fmt.Sprintf("No activity data between %s and %s (collection failed or LockLens was not running).",
				ts.lastAnalyze.Format(time.RFC3339), now.Format(time.RFC3339))}
	}
	ts.lastAnalyze = now

	findings := pol.Filter(rules.Evaluate(rules.Input{Now: now, Interval: interval, View: v, History: ts.history, Baselines: r.baselinesOf(t.ID)}, th))
	ids, err := tx(ctx).SyncFindings(ctx, t.ID, now, findings)
	if err != nil {
		return Analysis{}, err
	}

	refs := make([]findingRef, 0, len(findings))
	for _, f := range findings {
		if f.Status == rules.Active {
			refs = append(refs, findingRef{f.RuleID, f.Key, f.Severity, f.Title, f.Status})
		}
	}
	metrics := latestMetrics(v)
	var snapRaw json.RawMessage
	sig := graphSignature(sample)
	if sample.BlockedSessions > 0 && v.Activity.Snapshot != nil {
		snapRaw, _ = json.Marshal(v.Activity.Snapshot)
	}
	pre := preSample{at: now, summary: sample, metrics: metrics, findings: refs}
	if snapRaw != nil && sig != ts.lastPreSig {
		pre.snapshot = snapRaw
		ts.lastPreSig = sig
	}
	ts.pre = append(ts.pre, pre)
	if len(ts.pre) > r.cfg.PreTriggerSamples {
		ts.pre = slices.Clone(ts.pre[len(ts.pre)-r.cfg.PreTriggerSamples:])
	}

	for _, ruleID := range rules.AllRules {
		oi := ts.open[ruleID]
		if oi == nil && pol.Mode(ruleID) != rules.ModeIncident {
			continue
		}
		var firing []rules.Finding
		for _, f := range findings {
			if f.RuleID == ruleID && pol.Fires(f) {
				firing = append(firing, f)
			}
		}
		switch {
		case oi != nil && ruleID == rules.RuleTargetUnreachable:
			// Activity was collected: the instance is reachable again.
			if err := r.resolve(ctx, t, ts, oi, now, ResolutionCleared, nil); err != nil {
				return Analysis{}, err
			}
		case oi != nil:
			if err := r.continueIncident(ctx, t, v, ts, oi, firing, findings, ids, now, sample, metrics, refs, snapRaw, sig, gap); err != nil {
				return Analysis{}, err
			}
		case len(firing) > 0:
			if err := r.startIncident(ctx, t, v, ts, ruleID, firing, ids, now, th, gap); err != nil {
				return Analysis{}, err
			}
		}
	}
	return Analysis{Burst: burstReason(ts, findings)}, nil
}

// downNoteEvery is how often an open target.unreachable incident records
// that the instance is still unreachable.
const downNoteEvery = 5 * time.Minute

// Unreachable records that connecting to a target, or collecting its
// activity, keeps failing: once the failures last target_down_seconds it
// reports target.unreachable and, in incident mode, opens an incident. The
// incident resolves at the next successful activity collection.
func (r *recorder) Unreachable(ctx context.Context, t repository.Target, rc rules.Reachability) {
	pol := r.th.Policy(t)
	if pol.Mode(rules.RuleTargetUnreachable) == rules.ModeOff {
		return
	}
	now := time.Now().UTC()
	fs := rules.Unreachable(rc, now, pol.Thresholds)
	if len(fs) == 0 {
		return
	}
	ts := r.lock(t.ID)
	defer ts.mu.Unlock()
	if ts.stale {
		if err := r.resync(ctx, t.ID, ts); err != nil {
			r.log.Warn("recording an unreachable instance skipped: reading open incidents failed", "target_id", t.ID, "error", err.Error())
			return
		}
	}
	err := r.write(ctx, ts, t.ID, func(ctx context.Context) error { return r.unreachable(ctx, t, ts, pol, fs, now) })
	if err != nil {
		r.log.Warn("recording an unreachable instance failed", "target_id", t.ID, "error", err.Error())
	}
}

func (r *recorder) unreachable(ctx context.Context, t repository.Target, ts *targetState, pol rules.Policy, fs []rules.Finding, now time.Time) error {
	ids, err := tx(ctx).SyncRuleFindings(ctx, t.ID, now, rules.RuleTargetUnreachable, fs)
	if err != nil {
		return err
	}
	if !pol.Fires(fs[0]) {
		return nil
	}
	oi := ts.open[rules.RuleTargetUnreachable]
	if oi == nil {
		return r.startIncident(ctx, t, live.View{}, ts, rules.RuleTargetUnreachable, fs, ids, now, pol.Thresholds, nil)
	}
	if now.Sub(oi.lastSampleAt) < downNoteEvery {
		// Still down: the incident was seen now (its duration grows).
		return r.appendEvents(ctx, oi, now, nil, true)
	}
	oi.lastSampleAt = now
	oi.title = fs[0].Title
	if err := tx(ctx).UpdateIncidentState(ctx, oi.id, "open", nil, nil, oi.severity, oi.title, oi.truncated); err != nil {
		return err
	}
	ev := repository.IncidentEvent{At: now, Kind: KindSample, Summary: fs[0].Title + ". " + fs[0].Observed[0]}
	return r.appendEvents(ctx, oi, now, []repository.IncidentEvent{ev}, true)
}

// burstReasons are the active findings that make a target worth watching
// closely even without an incident.
var burstReasons = map[string]string{
	rules.RuleLongRunning:    "a statement is running for long",
	rules.RuleBlockingChain:  "sessions are blocked",
	rules.RuleIdleBlocker:    "an idle transaction blocks sessions",
	rules.RuleMeanRegression: "a statement became slower",
}

// burstReason says why a target should be collected faster: it has an
// open incident or an active problem, so the evidence around it is denser.
func burstReason(ts *targetState, findings []rules.Finding) string {
	for _, oi := range ts.open {
		return "open incident: " + oi.title
	}
	for _, f := range findings {
		if why, ok := burstReasons[f.RuleID]; ok && f.Status == rules.Active {
			return why
		}
	}
	return ""
}

// publish records an incident change: in the notification outbox, in the
// transaction of the change, and as an event for the browsers once it
// committed.
func (r *recorder) publish(ctx context.Context, targetID, incidentID int64, kind string) error {
	if err := tx(ctx).EnqueueNotification(ctx, incidentID, kind); err != nil {
		return err
	}
	x := ctx.Value(txnKey{}).(*txn)
	x.pending = append(x.pending, pendingEvent{targetID: targetID, incidentID: incidentID, kind: kind})
	return nil
}

func (r *recorder) baselinesOf(targetID int64) map[observe.QueryKey]baseline.Baseline {
	if r.baselines == nil {
		return nil
	}
	return r.baselines.Get(targetID)
}
