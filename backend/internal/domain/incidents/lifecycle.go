package incidents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/locklens/locklens/internal/domain/live"
	"github.com/locklens/locklens/internal/domain/rules"
	"github.com/locklens/locklens/internal/repository"
)

func (r *recorder) startIncident(ctx context.Context, t repository.Target, v live.View, ts *targetState, ruleID string,
	firing []rules.Finding, ids map[[2]string]int64, now time.Time, th rules.Thresholds, gap *repository.IncidentEvent) error {
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
		evs := []repository.IncidentEvent{{At: now, Kind: KindRetriggered, Payload: payload,
			Summary: fmt.Sprintf("Condition returned within the %s cooldown; recording continues in this incident.", r.cfg.Cooldown)}}
		if gap != nil {
			evs = append([]repository.IncidentEvent{*gap}, evs...)
		}
		r.publish(t.ID, prev.ID, "incident.updated")
		return r.appendEvents(ctx, oi, now, evs, true)
	case !errors.Is(err, repository.ErrNotFound):
		return err
	}

	lead := firing[0]
	for _, f := range firing {
		if severityRank(f.Severity) > severityRank(lead.Severity) {
			lead = f
		}
	}
	thRaw, _ := json.Marshal(map[string]any{"thresholds": th, "recorder": r.cfg, "rule_version": rules.Versions[ruleID]})
	in := repository.Incident{TargetID: &t.ID, TargetName: t.Name, RuleID: ruleID, RuleVersion: rules.Versions[ruleID],
		Severity: string(lead.Severity), Title: lead.Title, State: "open", OpenedAt: now, LastSeenAt: now,
		TriggerConfig: thRaw, Source: "recorder"}

	var evs []repository.IncidentEvent
	for _, p := range ts.pre[:max(len(ts.pre)-1, 0)] {
		payload, _ := json.Marshal(map[string]any{"summary": p.summary, "metrics": p.metrics, "findings": p.findings})
		evs = append(evs, repository.IncidentEvent{At: p.at, Kind: KindPreTrigger, Payload: payload, Summary: summaryLine(p.summary)})
		if p.snapshot != nil {
			evs = append(evs, repository.IncidentEvent{At: p.at, Kind: KindBlockingSnapshot, Payload: wrapSnapshot(p.snapshot),
				Summary: fmt.Sprintf("Blocking graph before the trigger: %d blocked session(s).", p.summary.BlockedSessions)})
		}
	}
	if gap != nil {
		evs = append(evs, *gap)
	}
	for _, f := range firing {
		payload, _ := json.Marshal(map[string]any{"finding": f})
		evs = append(evs, repository.IncidentEvent{At: now, Kind: KindTrigger, Payload: payload, Summary: "Triggered: " + f.Title})
	}
	if v.Activity != nil && v.Activity.Snapshot != nil && len(ts.history) > 0 && ts.history[len(ts.history)-1].BlockedSessions > 0 {
		raw, _ := json.Marshal(v.Activity.Snapshot)
		evs = append(evs, repository.IncidentEvent{At: now, Kind: KindBlockingSnapshot, Payload: wrapSnapshot(raw),
			Summary: fmt.Sprintf("Blocking graph at the trigger: %d blocked session(s).", ts.history[len(ts.history)-1].BlockedSessions)})
	}
	if ev := sessionsEvent(v, now); ev != nil {
		evs = append(evs, *ev)
	}
	if ev := loadEvent(v, now); ev != nil {
		evs = append(evs, *ev)
	}
	if v.Capabilities != nil && (!v.Capabilities.ReadAllStats || !v.Capabilities.TrackActivities) {
		payload, _ := json.Marshal(map[string]any{"read_all_stats": v.Capabilities.ReadAllStats, "track_activities": v.Capabilities.TrackActivities})
		evs = append(evs, repository.IncidentEvent{At: now, Kind: KindVisibility, Payload: payload,
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

func severityRank(s rules.Severity) int {
	switch s {
	case rules.Critical:
		return 2
	case rules.Warning:
		return 1
	}
	return 0
}

func (r *recorder) continueIncident(ctx context.Context, t repository.Target, v live.View, ts *targetState, oi *openIncident,
	firing, findings []rules.Finding, ids map[[2]string]int64, now time.Time, sample rules.ActivitySample, metrics map[string]*float64,
	refs []findingRef, snapRaw json.RawMessage, sig string, gap *repository.IncidentEvent) error {
	var evs []repository.IncidentEvent
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
		evs = append(evs, repository.IncidentEvent{At: now, Kind: kind, Payload: payload, Summary: summaryLine(sample)})
		oi.lastSampleAt = now
	}
	if snapRaw != nil && sig != oi.lastGraphSig {
		evs = append(evs, repository.IncidentEvent{At: now, Kind: KindBlockingSnapshot, Payload: wrapSnapshot(snapRaw),
			Summary: fmt.Sprintf("Blocking graph changed: %d blocked session(s).", sample.BlockedSessions)})
		oi.lastGraphSig = sig
	}
	var newIDs []int64
	for _, f := range firing {
		if !oi.findingKeys[f.Key] {
			oi.findingKeys[f.Key] = true
			payload, _ := json.Marshal(map[string]any{"finding": f})
			evs = append(evs, repository.IncidentEvent{At: now, Kind: KindFinding, Payload: payload, Summary: "Also observed: " + f.Title})
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

func (r *recorder) resolve(ctx context.Context, t repository.Target, ts *targetState, oi *openIncident, now time.Time, why string, evs []repository.IncidentEvent) error {
	summary := "Resolved: the triggering condition was not observed for the last samples."
	if why == ResolutionDuration {
		summary = fmt.Sprintf("Recording stopped: the incident reached the %s duration limit. A new incident opens if the condition persists after the cooldown.", r.cfg.MaxDuration)
	}
	evs = append(evs, repository.IncidentEvent{At: now, Kind: KindResolved, Summary: summary})
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
