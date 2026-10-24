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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/locklens/locklens/internal/collector"
	"github.com/locklens/locklens/internal/obs"
	"github.com/locklens/locklens/internal/rules"
	"github.com/locklens/locklens/internal/storage"
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
	store   *storage.Store
	cfg     Config
	th      ThresholdSource
	metrics *obs.Metrics
	events  collector.EventSink
	log     *slog.Logger

	mu      sync.Mutex
	targets map[int64]*targetState
}

// NewRecorder returns a recorder.
func NewRecorder(store *storage.Store, cfg Config, th ThresholdSource, metrics *obs.Metrics, events collector.EventSink, log *slog.Logger) *Recorder {
	return &Recorder{store: store, cfg: cfg, th: th, metrics: metrics, events: events, log: log, targets: map[int64]*targetState{}}
}

func (r *Recorder) target(id int64) *targetState {
	ts, ok := r.targets[id]
	if !ok {
		ts = &targetState{open: map[string]*openIncident{}}
		r.targets[id] = ts
	}
	return ts
}

// Recover reattaches incidents that were open when LockLens stopped and
// records the interruption in their timelines.
func (r *Recorder) Recover(ctx context.Context) error {
	open, err := r.store.OpenIncidents(ctx)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UTC()
	for _, in := range open {
		if in.TargetID == nil {
			continue
		}
		ts := r.target(*in.TargetID)
		ts.open[in.RuleID] = &openIncident{id: in.ID, ruleID: in.RuleID, openedAt: in.OpenedAt, severity: in.Severity,
			title: in.Title, events: in.EventCount, bytes: in.SizeBytes, truncated: in.Truncated, findingKeys: map[string]bool{}}
		payload, _ := json.Marshal(map[string]any{"previous_last_seen": in.LastSeenAt, "restarted_at": now})
		ev := storage.IncidentEvent{At: now, Kind: KindRestarted, Payload: payload,
			Summary: fmt.Sprintf("LockLens restarted; nothing was recorded between %s and %s, and in-memory buffers were lost.",
				in.LastSeenAt.UTC().Format(time.RFC3339), now.Format(time.RFC3339))}
		if err := r.store.AppendIncidentEvents(ctx, in.ID, nil, []storage.IncidentEvent{ev}); err != nil {
			return err
		}
	}
	r.updateOpenGauge()
	return nil
}

func (r *Recorder) updateOpenGauge() {
	n := 0
	for _, t := range r.targets {
		n += len(t.open)
	}
	r.metrics.IncidentsOpen.Set(float64(n))
}

// Analyze evaluates rules for one target and advances incident lifecycles.
func (r *Recorder) Analyze(ctx context.Context, t storage.Target, v collector.View) {
	if v.Activity == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.analyze(ctx, t, v); err != nil {
		r.log.Warn("incident analysis failed", "target_id", t.ID, "error", err.Error())
	}
}

func (r *Recorder) analyze(ctx context.Context, t storage.Target, v collector.View) error {
	ts := r.target(t.ID)
	now := v.Activity.At.UTC()
	interval := time.Duration(t.IntervalSeconds) * time.Second
	th := r.th.Thresholds()

	sample := rules.Summarize(v.Activity, v.Report)
	ts.history = append(ts.history, sample)
	if len(ts.history) > r.cfg.HistorySamples {
		ts.history = slices.Clone(ts.history[len(ts.history)-r.cfg.HistorySamples:])
	}
	var gap *storage.IncidentEvent
	if !ts.lastAnalyze.IsZero() && now.Sub(ts.lastAnalyze) > 3*interval+15*time.Second {
		payload, _ := json.Marshal(map[string]any{"from": ts.lastAnalyze, "to": now})
		gap = &storage.IncidentEvent{At: now, Kind: KindGap, Payload: payload,
			Summary: fmt.Sprintf("No activity data between %s and %s (collection failed or LockLens was not running).",
				ts.lastAnalyze.Format(time.RFC3339), now.Format(time.RFC3339))}
	}
	ts.lastAnalyze = now

	findings := rules.Evaluate(rules.Input{Now: now, Interval: interval, View: v, History: ts.history}, th)
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
	return nil
}

func latestMetrics(v collector.View) map[string]*float64 {
	keys := []string{"connections.used", "connections.usage_ratio", "sessions.active", "sessions.idle_in_transaction",
		"sessions.waiting_lock", "blocking.blocked_sessions", "blocking.max_lock_wait_seconds", "transactions.oldest_age_seconds",
		"xact.commit.per_s", "xact.rollback.per_s", "temp.bytes.per_s", "deadlocks.delta", "wal.bytes.per_s",
		"statements.calls.per_s", "statements.exec_ms.per_s", "slots.max_retained_bytes", "replication.max_replay_lag_bytes",
		"host.cpu_utilization_ratio", "host.memory_available_bytes"}
	out := map[string]*float64{}
	for _, k := range keys {
		if tv, ok := v.Latest(k); ok {
			out[k] = tv.Value
		}
	}
	return out
}

// graphSignature identifies a blocking graph shape so unchanged graphs are
// not stored again.
func graphSignature(s rules.ActivitySample) string {
	h := sha256.New()
	for _, root := range s.Roots {
		fmt.Fprintf(h, "%s|%d|%v|%s;", root.Key, root.Transitive, root.Members, deref(root.State))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func (r *Recorder) startIncident(ctx context.Context, t storage.Target, v collector.View, ts *targetState, ruleID string,
	firing []rules.Finding, ids map[[2]string]int64, now time.Time, th rules.Thresholds, gap *storage.IncidentEvent) error {
	// Regroup with a recently resolved incident of the same rule, unless it
	// ended because of the duration limit (then wait for the cooldown).
	prev, err := r.store.LatestResolvedIncident(ctx, t.ID, ruleID, now.Add(-r.cfg.Cooldown))
	switch {
	case err == nil && prev.Resolution != nil && *prev.Resolution == ResolutionDuration:
		return nil
	case err == nil:
		oi := &openIncident{id: prev.ID, ruleID: ruleID, openedAt: prev.OpenedAt, severity: prev.Severity, title: prev.Title,
			events: prev.EventCount, bytes: prev.SizeBytes, truncated: prev.Truncated, findingKeys: map[string]bool{}}
		ts.open[ruleID] = oi
		if err := r.store.UpdateIncidentState(ctx, prev.ID, "open", nil, nil, prev.Severity, prev.Title, false); err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]any{"findings": firing})
		evs := []storage.IncidentEvent{{At: now, Kind: KindRetriggered, Payload: payload,
			Summary: fmt.Sprintf("Condition returned within the %s cooldown; recording continues in this incident.", r.cfg.Cooldown)}}
		if gap != nil {
			evs = append([]storage.IncidentEvent{*gap}, evs...)
		}
		r.publish(t.ID, prev.ID, "incident.updated")
		return r.appendEvents(ctx, oi, now, evs, true)
	case !errors.Is(err, storage.ErrNotFound):
		return err
	}

	lead := firing[0]
	for _, f := range firing {
		if severityRank(f.Severity) > severityRank(lead.Severity) {
			lead = f
		}
	}
	thRaw, _ := json.Marshal(map[string]any{"thresholds": th, "recorder": r.cfg, "rule_version": rules.Versions[ruleID]})
	in := storage.Incident{TargetID: &t.ID, TargetName: t.Name, RuleID: ruleID, RuleVersion: rules.Versions[ruleID],
		Severity: string(lead.Severity), Title: lead.Title, State: "open", OpenedAt: now, LastSeenAt: now,
		TriggerConfig: thRaw, Source: "recorder"}

	var evs []storage.IncidentEvent
	for _, p := range ts.pre[:max(len(ts.pre)-1, 0)] {
		payload, _ := json.Marshal(map[string]any{"summary": p.summary, "metrics": p.metrics, "findings": p.findings})
		evs = append(evs, storage.IncidentEvent{At: p.at, Kind: KindPreTrigger, Payload: payload, Summary: summaryLine(p.summary)})
		if p.snapshot != nil {
			evs = append(evs, storage.IncidentEvent{At: p.at, Kind: KindBlockingSnapshot, Payload: wrapSnapshot(p.snapshot),
				Summary: fmt.Sprintf("Blocking graph before the trigger: %d blocked session(s).", p.summary.BlockedSessions)})
		}
	}
	if gap != nil {
		evs = append(evs, *gap)
	}
	for _, f := range firing {
		payload, _ := json.Marshal(map[string]any{"finding": f})
		evs = append(evs, storage.IncidentEvent{At: now, Kind: KindTrigger, Payload: payload, Summary: "Triggered: " + f.Title})
	}
	if v.Activity != nil && v.Activity.Snapshot != nil && len(ts.history) > 0 && ts.history[len(ts.history)-1].BlockedSessions > 0 {
		raw, _ := json.Marshal(v.Activity.Snapshot)
		evs = append(evs, storage.IncidentEvent{At: now, Kind: KindBlockingSnapshot, Payload: wrapSnapshot(raw),
			Summary: fmt.Sprintf("Blocking graph at the trigger: %d blocked session(s).", ts.history[len(ts.history)-1].BlockedSessions)})
	}
	if v.Capabilities != nil && (!v.Capabilities.ReadAllStats || !v.Capabilities.TrackActivities) {
		payload, _ := json.Marshal(map[string]any{"read_all_stats": v.Capabilities.ReadAllStats, "track_activities": v.Capabilities.TrackActivities})
		evs = append(evs, storage.IncidentEvent{At: now, Kind: KindVisibility, Payload: payload,
			Summary: "Partial visibility: the monitoring role cannot see every session's details, so this evidence may be incomplete."})
	}
	if ruleID == rules.RuleMeanRegression {
		if ev := workloadEvent(v, th, now); ev != nil {
			evs = append(evs, *ev)
		}
	}
	evs, truncated := r.capEvents(evs, 0, 0)
	in.Truncated = truncated
	id, err := r.store.CreateIncident(ctx, in, evs)
	if err != nil {
		return err
	}
	oi := &openIncident{id: id, ruleID: ruleID, openedAt: now, severity: in.Severity, title: in.Title, events: len(evs),
		bytes: eventsSize(evs), truncated: truncated, lastSampleAt: now, findingKeys: map[string]bool{}, lastWorkload: now}
	if len(ts.history) > 0 {
		// The trigger event already stored the current graph.
		oi.lastGraphSig = graphSignature(ts.history[len(ts.history)-1])
	}
	for _, f := range firing {
		oi.findingKeys[f.Key] = true
	}
	ts.open[ruleID] = oi
	var fids []int64
	for _, f := range firing {
		if id, ok := ids[[2]string{f.RuleID, f.Key}]; ok {
			fids = append(fids, id)
		}
	}
	if err := r.store.LinkFindingsToIncident(ctx, id, fids); err != nil {
		return err
	}
	r.metrics.IncidentsOpened.Inc(ruleID)
	r.publish(t.ID, id, "incident.opened")
	r.log.Info("incident opened", "incident_id", id, "target_id", t.ID, "rule", ruleID, "title", in.Title)
	return nil
}

func wrapSnapshot(raw json.RawMessage) json.RawMessage {
	out, _ := json.Marshal(map[string]json.RawMessage{"snapshot": raw})
	return out
}

func workloadEvent(v collector.View, th rules.Thresholds, now time.Time) *storage.IncidentEvent {
	if len(v.Statements) == 0 {
		return nil
	}
	win := time.Duration(th.WorkloadWindowSeconds * float64(time.Second))
	top, covered := rules.TopQueries(v.Statements, now.Add(-win), now, 10)
	prevTop, prevCovered := rules.TopQueries(v.Statements, now.Add(-2*win), now.Add(-win), 50)
	payload, _ := json.Marshal(map[string]any{"window_seconds": win.Seconds(), "current": top, "current_covered_seconds": covered,
		"previous": prevTop, "previous_covered_seconds": prevCovered})
	return &storage.IncidentEvent{At: now, Kind: KindWorkload, Payload: payload,
		Summary: fmt.Sprintf("Top statements by total execution time in the last %s (from pg_stat_statements deltas).", win)}
}

func summaryLine(s rules.ActivitySample) string {
	ratio := "unknown"
	if r := s.UsageRatio(); r != nil {
		ratio = fmt.Sprintf("%.0f%%", *r*100)
	}
	return fmt.Sprintf("%d blocked session(s), %d root blocker(s), %d long transaction(s), connection usage %s.",
		s.BlockedSessions, len(s.Roots), s.LongTransactions, ratio)
}

func severityRank(s rules.Severity) int {
	switch s {
	case rules.Critical:
		return 2
	case rules.Warning:
		return 1
	}
	return 0
}

func (r *Recorder) continueIncident(ctx context.Context, t storage.Target, v collector.View, ts *targetState, oi *openIncident,
	firing, findings []rules.Finding, ids map[[2]string]int64, now time.Time, sample rules.ActivitySample, metrics map[string]*float64,
	refs []findingRef, snapRaw json.RawMessage, sig string, gap *storage.IncidentEvent) error {
	var evs []storage.IncidentEvent
	if gap != nil {
		evs = append(evs, *gap)
	}
	if now.Sub(oi.openedAt) >= r.cfg.MaxDuration {
		return r.resolve(ctx, t, ts, oi, now, ResolutionDuration, evs)
	}
	holds := len(firing) > 0 || conditionHolds(oi.ruleID, sample, findings, r.th.Thresholds())
	kind := KindSample
	if !holds {
		kind = KindPostTrigger
		oi.clear++
	} else {
		oi.clear = 0
	}
	if !holds || now.Sub(oi.lastSampleAt) >= r.cfg.SampleEvery || graphChanged(oi, sig, sample) {
		payload, _ := json.Marshal(map[string]any{"summary": sample, "metrics": metrics, "findings": refs})
		evs = append(evs, storage.IncidentEvent{At: now, Kind: kind, Payload: payload, Summary: summaryLine(sample)})
		oi.lastSampleAt = now
	}
	if snapRaw != nil && sig != oi.lastGraphSig {
		evs = append(evs, storage.IncidentEvent{At: now, Kind: KindBlockingSnapshot, Payload: wrapSnapshot(snapRaw),
			Summary: fmt.Sprintf("Blocking graph changed: %d blocked session(s).", sample.BlockedSessions)})
		oi.lastGraphSig = sig
	}
	var newIDs []int64
	for _, f := range firing {
		if !oi.findingKeys[f.Key] {
			oi.findingKeys[f.Key] = true
			payload, _ := json.Marshal(map[string]any{"finding": f})
			evs = append(evs, storage.IncidentEvent{At: now, Kind: KindFinding, Payload: payload, Summary: "Also observed: " + f.Title})
		}
		if id, ok := ids[[2]string{f.RuleID, f.Key}]; ok {
			newIDs = append(newIDs, id)
		}
		if severityRank(f.Severity) > severityRank(rules.Severity(oi.severity)) {
			oi.severity, oi.title = string(f.Severity), f.Title
			if err := r.store.UpdateIncidentState(ctx, oi.id, "open", nil, nil, oi.severity, oi.title, false); err != nil {
				return err
			}
		}
	}
	if oi.ruleID == rules.RuleMeanRegression && len(v.Statements) > 0 {
		last := v.Statements[len(v.Statements)-1].At
		if last.After(oi.lastWorkload) {
			if ev := workloadEvent(v, r.th.Thresholds(), now); ev != nil {
				evs = append(evs, *ev)
			}
			oi.lastWorkload = last
		}
	}
	if err := r.store.LinkFindingsToIncident(ctx, oi.id, newIDs); err != nil {
		return err
	}
	if oi.clear >= r.cfg.ResolveAfterSamples {
		return r.resolve(ctx, t, ts, oi, now, ResolutionCleared, evs)
	}
	if err := r.appendEvents(ctx, oi, now, evs, holds); err != nil {
		return err
	}
	if len(evs) > 0 {
		r.publish(t.ID, oi.id, "incident.updated")
	}
	return nil
}

func graphChanged(oi *openIncident, sig string, s rules.ActivitySample) bool {
	return s.BlockedSessions > 0 && sig != oi.lastGraphSig
}

// conditionHolds reports whether the condition behind an open incident is
// still present in the current sample even though its rule does not fire.
// Persistence-based rules need several samples again after a restart or when
// the root blocker changes; the missing finding then does not mean the
// problem cleared. A rule that reports insufficient data is not "clear"
// either: unknown is not resolved.
func conditionHolds(ruleID string, s rules.ActivitySample, findings []rules.Finding, th rules.Thresholds) bool {
	switch ruleID {
	case rules.RuleBlockingChain:
		for _, root := range s.Roots {
			if root.Transitive >= th.BlockingMinBlocked {
				return true
			}
		}
	case rules.RuleIdleBlocker:
		for _, root := range s.Roots {
			if root.State != nil && (*root.State == "idle in transaction" || *root.State == "idle in transaction (aborted)") {
				return true
			}
		}
	case rules.RuleConnections:
		if u := s.UsageRatio(); u != nil && *u >= th.ConnectionsWarnRatio {
			return true
		}
	}
	for _, f := range findings {
		if f.RuleID == ruleID && f.Status == rules.InsufficientData {
			return true
		}
	}
	return false
}

func (r *Recorder) resolve(ctx context.Context, t storage.Target, ts *targetState, oi *openIncident, now time.Time, why string, evs []storage.IncidentEvent) error {
	summary := "Resolved: the triggering condition was not observed for the last samples."
	if why == ResolutionDuration {
		summary = fmt.Sprintf("Recording stopped: the incident reached the %s duration limit. A new incident opens if the condition persists after the cooldown.", r.cfg.MaxDuration)
	}
	evs = append(evs, storage.IncidentEvent{At: now, Kind: KindResolved, Summary: summary})
	if err := r.appendEvents(ctx, oi, now, evs, false); err != nil {
		return err
	}
	res := why
	if err := r.store.UpdateIncidentState(ctx, oi.id, "resolved", &now, &res, oi.severity, oi.title, oi.truncated); err != nil {
		return err
	}
	delete(ts.open, oi.ruleID)
	r.publish(t.ID, oi.id, "incident.resolved")
	r.log.Info("incident resolved", "incident_id", oi.id, "target_id", t.ID, "resolution", why)
	return nil
}

// lifecycle events are always recorded, even after truncation.
func isLifecycle(kind string) bool {
	switch kind {
	case KindTrigger, KindRetriggered, KindResolved, KindRestarted, KindTruncated, KindGap:
		return true
	}
	return false
}

// capEvents drops non-lifecycle events beyond the limits and adds a single
// truncation marker the first time a limit is hit.
func (r *Recorder) capEvents(evs []storage.IncidentEvent, count int, bytes int64) ([]storage.IncidentEvent, bool) {
	var out []storage.IncidentEvent
	truncated := false
	for _, e := range evs {
		size := int64(len(e.Payload) + len(e.Summary))
		if !isLifecycle(e.Kind) && (count >= r.cfg.MaxEvents || bytes+size > r.cfg.MaxBytes) {
			if !truncated {
				truncated = true
				out = append(out, storage.IncidentEvent{At: e.At, Kind: KindTruncated,
					Summary: fmt.Sprintf("Incident size limit reached (%d events or %d bytes); further samples are not stored, lifecycle events still are.", r.cfg.MaxEvents, r.cfg.MaxBytes)})
				count++
			}
			continue
		}
		out = append(out, e)
		count++
		bytes += size
	}
	return out, truncated
}

func eventsSize(evs []storage.IncidentEvent) int64 {
	var n int64
	for _, e := range evs {
		n += int64(len(e.Payload) + len(e.Summary))
	}
	return n
}

func (r *Recorder) appendEvents(ctx context.Context, oi *openIncident, now time.Time, evs []storage.IncidentEvent, seen bool) error {
	if oi.truncated {
		kept := evs[:0]
		for _, e := range evs {
			if isLifecycle(e.Kind) && e.Kind != KindTruncated {
				kept = append(kept, e)
			}
		}
		evs = kept
	} else {
		var trunc bool
		evs, trunc = r.capEvents(evs, oi.events, oi.bytes)
		if trunc {
			oi.truncated = true
			if err := r.store.UpdateIncidentState(ctx, oi.id, "open", nil, nil, oi.severity, oi.title, true); err != nil {
				return err
			}
		}
	}
	var last *time.Time
	if seen {
		last = &now
	}
	if len(evs) == 0 && last == nil {
		return nil
	}
	if err := r.store.AppendIncidentEvents(ctx, oi.id, last, evs); err != nil {
		return err
	}
	oi.events += len(evs)
	oi.bytes += eventsSize(evs)
	return nil
}

func (r *Recorder) publish(targetID, incidentID int64, kind string) {
	if r.events != nil {
		r.events.Publish(kind, map[string]any{"target_id": targetID, "incident_id": incidentID})
	}
}
