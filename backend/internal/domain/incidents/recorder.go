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
	"github.com/locklens/locklens/internal/domain/collector"
	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/domain/rules"
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

// ThresholdSource provides the current rule thresholds.
type ThresholdSource interface {
	Thresholds() rules.Thresholds
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

// Recorder implements collector.Analyzer.
type Recorder struct {
	store   Store
	cfg     Config
	th      ThresholdSource
	metrics *obs.Metrics
	events  collector.EventSink
	log     *slog.Logger
	// baselines gives statement rules each target's query baselines; nil
	// leaves them to compare with the previous window only.
	baselines BaselineSource
	// burster collects a target faster while it has a problem; nil: never.
	burster Burster

	mu      sync.Mutex
	targets map[int64]*targetState
}

// NewRecorder returns a recorder.
func NewRecorder(store Store, cfg Config, th ThresholdSource, metrics *obs.Metrics, events collector.EventSink, log *slog.Logger) *Recorder {
	return &Recorder{store: store, cfg: cfg, th: th, metrics: metrics, events: events, log: log, targets: map[int64]*targetState{}}
}

// BaselineSource returns the query baselines of a target.
type BaselineSource interface {
	Get(targetID int64) map[observe.QueryKey]baseline.Baseline
}

// Burster starts or extends faster collection of a target.
type Burster interface {
	Burst(targetID int64, reason string)
}

// SetBurster sets who collects targets faster during problems; call it
// before Run.
func (r *Recorder) SetBurster(b Burster) { r.burster = b }

// SetBaselines sets the source of query baselines; call it before Run.
func (r *Recorder) SetBaselines(b BaselineSource) { r.baselines = b }

func (r *Recorder) target(id int64) *targetState {
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
func (r *Recorder) Adopt(ctx context.Context, targetID int64) error {
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
func (r *Recorder) Forget(targetID int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.targets, targetID)
	r.updateOpenGauge()
}

func (r *Recorder) updateOpenGauge() {
	n := 0
	for _, t := range r.targets {
		n += len(t.open)
	}
	r.metrics.IncidentsOpen.Set(float64(n))
}

// Analyze evaluates rules for one target and advances incident lifecycles.
func (r *Recorder) Analyze(ctx context.Context, t repository.Target, v collector.View) {
	if v.Activity == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.analyze(ctx, t, v); err != nil {
		r.log.Warn("incident analysis failed", "target_id", t.ID, "error", err.Error())
	}
}

func (r *Recorder) analyze(ctx context.Context, t repository.Target, v collector.View) error {
	ts := r.target(t.ID)
	now := v.Activity.At.UTC()
	interval := time.Duration(t.IntervalSeconds) * time.Second
	th := r.th.Thresholds()

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

	findings := rules.Evaluate(rules.Input{Now: now, Interval: interval, View: v, History: ts.history, Baselines: r.baselinesOf(t.ID)}, th)
	ids, err := r.store.SyncFindings(ctx, t.ID, now, findings)
	if err != nil {
		return err
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

	for _, ruleID := range rules.TriggerRules {
		var firing []rules.Finding
		for _, f := range findings {
			if f.RuleID == ruleID && f.Status == rules.Active && f.Trigger {
				firing = append(firing, f)
			}
		}
		oi := ts.open[ruleID]
		switch {
		case oi != nil:
			if err := r.continueIncident(ctx, t, v, ts, oi, firing, findings, ids, now, sample, metrics, refs, snapRaw, sig, gap); err != nil {
				return err
			}
		case len(firing) > 0:
			if err := r.startIncident(ctx, t, v, ts, ruleID, firing, ids, now, th, gap); err != nil {
				return err
			}
		}
	}
	r.updateOpenGauge()
	r.burstIfNeeded(t.ID, ts, findings)
	return nil
}

// burstReasons are the active findings that make a target worth watching
// closely even without an incident.
var burstReasons = map[string]string{
	rules.RuleLongRunning:    "a statement is running for long",
	rules.RuleBlockingChain:  "sessions are blocked",
	rules.RuleIdleBlocker:    "an idle transaction blocks sessions",
	rules.RuleMeanRegression: "a statement became slower",
}

// burstIfNeeded asks for faster collection while the target has an open
// incident or an active problem, so the evidence around it is denser.
func (r *Recorder) burstIfNeeded(targetID int64, ts *targetState, findings []rules.Finding) {
	if r.burster == nil {
		return
	}
	for _, oi := range ts.open {
		r.burster.Burst(targetID, "open incident: "+oi.title)
		return
	}
	for _, f := range findings {
		if why, ok := burstReasons[f.RuleID]; ok && f.Status == rules.Active {
			r.burster.Burst(targetID, why)
			return
		}
	}
}

func (r *Recorder) publish(targetID, incidentID int64, kind string) {
	if r.events != nil {
		r.events.Publish(kind, map[string]any{"target_id": targetID, "incident_id": incidentID})
	}
}

func (r *Recorder) baselinesOf(targetID int64) map[observe.QueryKey]baseline.Baseline {
	if r.baselines == nil {
		return nil
	}
	return r.baselines.Get(targetID)
}
