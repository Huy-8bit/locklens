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
	"fmt"
	"log/slog"
	"slices"
	"sync"
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

	mu      sync.Mutex
	targets map[int64]*targetState
}

// NewRecorder returns a recorder. baselines may be nil.
func NewRecorder(store Repositories, cfg Config, th settings.Service, baselines baseline.Service, metrics *obs.Metrics, events events.Publisher, log *slog.Logger) Recorder {
	return &recorder{store: store, cfg: cfg, th: th, baselines: baselines, metrics: metrics, events: events, log: log, targets: map[int64]*targetState{}}
}

func (r *recorder) target(id int64) *targetState {
	ts, ok := r.targets[id]
	if !ok {
		ts = &targetState{open: map[string]*openIncident{}}
		r.targets[id] = ts
	}
	return ts
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
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UTC()
	for _, in := range open {
		if in.TargetID == nil || *in.TargetID != targetID {
			continue
		}
		ts := r.target(targetID)
		if _, ok := ts.open[in.RuleID]; ok {
			continue
		}
		ts.open[in.RuleID] = &openIncident{id: in.ID, ruleID: in.RuleID, openedAt: in.OpenedAt, severity: in.Severity,
			title: in.Title, events: in.EventCount, bytes: in.SizeBytes, truncated: in.Truncated, findingKeys: map[string]bool{}}
		payload, _ := json.Marshal(map[string]any{"previous_last_seen": in.LastSeenAt, "restarted_at": now})
		ev := repository.IncidentEvent{At: now, Kind: KindRestarted, Payload: payload,
			Summary: fmt.Sprintf("LockLens restarted or another LockLens server took over collecting this instance; nothing was recorded between %s and %s, and in-memory buffers were lost.",
				in.LastSeenAt.UTC().Format(time.RFC3339), now.Format(time.RFC3339))}
		if err := r.store.AppendIncidentEvents(ctx, in.ID, nil, []repository.IncidentEvent{ev}); err != nil {
			return err
		}
	}
	r.updateOpenGauge()
	return nil
}

// Forget drops the in-memory state of a target another server now
// collects; its incidents stay open and that server adopts them.
func (r *recorder) Forget(targetID int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.targets, targetID)
	r.updateOpenGauge()
}

func (r *recorder) updateOpenGauge() {
	n := 0
	for _, t := range r.targets {
		n += len(t.open)
	}
	r.metrics.IncidentsOpen.Set(float64(n))
}

// Analysis is the outcome of one analysis that the collector acts on.
type Analysis struct {
	// Burst says why the target should be collected faster now (an open
	// incident or an active problem); empty when it need not be.
	Burst string
}

// Analyze evaluates rules for one target and advances incident lifecycles.
func (r *recorder) Analyze(ctx context.Context, t repository.Target, v live.View) Analysis {
	if v.Activity == nil {
		return Analysis{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	a, err := r.analyze(ctx, t, v)
	if err != nil {
		r.log.Warn("incident analysis failed", "target_id", t.ID, "error", err.Error())
	}
	return a
}

func (r *recorder) analyze(ctx context.Context, t repository.Target, v live.View) (Analysis, error) {
	ts := r.target(t.ID)
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
	ids, err := r.store.SyncFindings(ctx, t.ID, now, findings)
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
	r.updateOpenGauge()
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
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.unreachable(ctx, t, pol, fs, now); err != nil {
		r.log.Warn("recording an unreachable instance failed", "target_id", t.ID, "error", err.Error())
	}
}

func (r *recorder) unreachable(ctx context.Context, t repository.Target, pol rules.Policy, fs []rules.Finding, now time.Time) error {
	ids, err := r.store.SyncRuleFindings(ctx, t.ID, now, rules.RuleTargetUnreachable, fs)
	if err != nil {
		return err
	}
	if !pol.Fires(fs[0]) {
		return nil
	}
	ts := r.target(t.ID)
	oi := ts.open[rules.RuleTargetUnreachable]
	if oi == nil {
		err := r.startIncident(ctx, t, live.View{}, ts, rules.RuleTargetUnreachable, fs, ids, now, pol.Thresholds, nil)
		r.updateOpenGauge()
		return err
	}
	if now.Sub(oi.lastSampleAt) < downNoteEvery {
		// Still down: the incident was seen now (its duration grows).
		return r.appendEvents(ctx, oi, now, nil, true)
	}
	oi.lastSampleAt = now
	oi.title = fs[0].Title
	if err := r.store.UpdateIncidentState(ctx, oi.id, "open", nil, nil, oi.severity, oi.title, oi.truncated); err != nil {
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

func (r *recorder) publish(targetID, incidentID int64, kind string) {
	if r.events != nil {
		r.events.Publish(kind, map[string]any{"target_id": targetID, "incident_id": incidentID})
	}
}

func (r *recorder) baselinesOf(targetID int64) map[observe.QueryKey]baseline.Baseline {
	if r.baselines == nil {
		return nil
	}
	return r.baselines.Get(targetID)
}
