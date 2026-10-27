package httpapi

import (
	"cmp"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/locklens/locklens/internal/domain/baseline"
	"github.com/locklens/locklens/internal/domain/finding"
	"github.com/locklens/locklens/internal/domain/insight"
	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/domain/rules"
	"github.com/locklens/locklens/internal/repository"
)

// perfItem is one current statement finding ranked by impact.
type perfItem struct {
	TargetID   int64                `json:"target_id"`
	TargetName string               `json:"target_name"`
	FindingID  int64                `json:"finding_id"`
	FirstSeen  time.Time            `json:"first_seen"`
	LastSeen   time.Time            `json:"last_seen"`
	Finding    finding.Finding      `json:"finding"`
	Impact     insight.Impact       `json:"impact"`
	Statement  *repository.QueryRef `json:"statement"`
}

// percentilesNote explains why LockLens shows no latency percentiles.
var percentilesNote = map[string]any{"available": false,
	"reason": "pg_stat_statements keeps totals per statement, not individual executions, so latency percentiles cannot be derived from it. LockLens shows means over periods and never estimates percentiles."}

// performance lists the current statement anomalies and slow running
// statements of the fleet, ranked by impact.
func (s *Server) performance(w http.ResponseWriter, r *http.Request) {
	ts, err := s.fleetTargets(r)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	now := time.Now().UTC()
	anomalies, running := []perfItem{}, []perfItem{}
	unavailable := []map[string]any{}
	insufficient := []map[string]any{}
	baselines := []map[string]any{}
	th := s.d.Settings.Thresholds()
	for _, t := range ts {
		rows, err := s.d.Store.ListFindings(r.Context(), repository.FindingFilter{TargetID: t.ID, Status: "current", Limit: 500})
		if err != nil {
			s.internal(w, r, err)
			return
		}
		var keys []observe.QueryKey
		var items []perfItem
		for _, row := range rows {
			if !insight.IsStatementRule(row.Finding.RuleID) {
				continue
			}
			if row.Finding.Status != finding.Active {
				insufficient = append(insufficient, map[string]any{"target_id": t.ID, "target_name": t.Name, "rule_id": row.Finding.RuleID,
					"reason": row.Finding.Explanation})
				continue
			}
			it := perfItem{TargetID: t.ID, TargetName: t.Name, FindingID: row.ID, FirstSeen: row.FirstSeen, LastSeen: row.LastSeen,
				Finding: row.Finding, Impact: insight.Score(row.Finding, now.Sub(row.FirstSeen))}
			if k, ok := insight.StatementKey(row.Finding.Key); ok {
				keys = append(keys, k)
			}
			items = append(items, it)
		}
		refs, err := s.d.Store.QueryRefs(r.Context(), t.ID, keys, t.QueryTextEnabled && r.URL.Query().Get("include_text") != "false")
		if err != nil {
			s.internal(w, r, err)
			return
		}
		byKey := map[observe.QueryKey]repository.QueryRef{}
		for _, ref := range refs {
			byKey[ref.Key] = ref
		}
		for _, it := range items {
			if k, ok := insight.StatementKey(it.Finding.Key); ok {
				if ref, ok := byKey[k]; ok {
					it.Statement = &ref
				}
			}
			if it.Finding.RuleID == rules.RuleLongRunning {
				running = append(running, it)
			} else {
				anomalies = append(anomalies, it)
			}
		}
		if c := t.Status.Capabilities; c != nil && !c.StatStatements {
			unavailable = append(unavailable, map[string]any{"target_id": t.ID, "target_name": t.Name, "reason": c.StatementsReason})
		}
		bs := s.d.Baselines.Get(t.ID)
		warm := 0
		for _, b := range bs {
			if b.All.Points >= th.BaselineWarmupPoints {
				warm++
			}
		}
		baselines = append(baselines, map[string]any{"target_id": t.ID, "target_name": t.Name, "statements": len(bs), "warm": warm})
	}
	byImpact := func(a, b perfItem) int {
		if c := cmp.Compare(b.Impact.Score, a.Impact.Score); c != 0 {
			return c
		}
		return cmp.Compare(a.FindingID, b.FindingID)
	}
	slices.SortStableFunc(anomalies, byImpact)
	slices.SortStableFunc(running, byImpact)
	writeJSON(w, http.StatusOK, map[string]any{"generated_at": now, "anomalies": anomalies, "running": running,
		"unavailable": unavailable, "insufficient": insufficient, "baselines": baselines,
		"baseline_config": map[string]any{"days": th.BaselineDays, "point_seconds": th.BaselinePointSeconds, "warmup_points": th.BaselineWarmupPoints,
			"sensitivity": th.AnomalySensitivity},
		"percentiles": percentilesNote})
}

// queryInsight returns a statement's baseline, its usual range over a time
// range, its current anomalies with their impact, and the correlated
// investigation when it has an active anomaly.
func (s *Server) queryInsight(w http.ResponseWriter, r *http.Request) {
	t, ok := s.targetOr404(w, r)
	if !ok {
		return
	}
	qid, ok := pathID(r, "qid")
	if !ok {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "invalid query identity")
		return
	}
	from, to, err := timeRange(r, time.Hour)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	step, err := metricStep(r, from, to, time.Minute)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	ident, _, _, err := s.d.Store.QueryIdentity(r.Context(), t.ID, qid, false)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "not_found", "query identity not found")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	now := time.Now().UTC()
	th := s.d.Settings.Thresholds()
	p := insight.BaselineParams(th)
	bfrom, bto := baseline.Window(now, p)
	byID, err := s.d.Baselines.Load(r.Context(), t.ID, []int64{qid}, bfrom, bto, p)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	b := byID[qid]
	var times []time.Time
	// The same bucket starts as the statement's chart (origin from).
	for at := from; !at.After(to); at = at.Add(step) {
		times = append(times, at)
	}
	cur, basis := b.For(now)
	resp := map[string]any{
		"baseline": map[string]any{"from": b.From, "to": b.To, "point_seconds": b.PointSeconds, "points": b.All.Points, "days": b.Days,
			"warm": b.All.Points >= th.BaselineWarmupPoints, "warmup_points": th.BaselineWarmupPoints,
			"all": b.All, "current": cur, "current_basis": basis, "sensitivity": th.AnomalySensitivity},
		"bands": map[string]any{
			"mean_ms":       insight.Band(b, baseline.MeanMS, times, th.AnomalySensitivity),
			"calls_per_s":   insight.Band(b, baseline.CallsPerSec, times, th.AnomalySensitivity),
			"load_ms_per_s": insight.Band(b, baseline.LoadMSPerS, times, th.AnomalySensitivity),
		},
		"step_seconds": step.Seconds(),
		"percentiles":  percentilesNote,
	}
	key, err := s.d.Store.IdentityKey(r.Context(), t.ID, qid)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	rows, err := s.d.Store.ListFindings(r.Context(), repository.FindingFilter{TargetID: t.ID, Limit: 500})
	if err != nil {
		s.internal(w, r, err)
		return
	}
	anomalies := []perfItem{}
	history := []perfItem{}
	subject := ""
	for _, row := range rows {
		if k, ok := insight.StatementKey(row.Finding.Key); !ok || k != key {
			continue
		}
		it := perfItem{TargetID: t.ID, TargetName: t.Name, FindingID: row.ID, FirstSeen: row.FirstSeen, LastSeen: row.LastSeen, Finding: row.Finding}
		end := now
		if row.ResolvedAt != nil {
			end = *row.ResolvedAt
		}
		it.Impact = insight.Score(row.Finding, end.Sub(row.FirstSeen))
		switch {
		case row.Finding.Status == finding.Active:
			anomalies = append(anomalies, it)
			subject = row.Finding.Key
		case row.Finding.Status == finding.Resolved:
			history = append(history, it)
		}
	}
	resp["anomalies"], resp["history"] = anomalies, history
	if subject != "" {
		inv, err := s.d.Insight.Investigate(r.Context(), t.ID, subject, &ident.QueryID, now)
		if err != nil {
			s.internal(w, r, err)
			return
		}
		resp["investigation"] = inv
	}
	n, err := s.d.Store.CountPlans(r.Context(), t.ID, ident.QueryID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	resp["plans"] = n
	writeJSON(w, http.StatusOK, resp)
}

// incidentInvestigation correlates the evidence around a statement
// incident as of its trigger.
func (s *Server) incidentInvestigation(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "invalid incident id")
		return
	}
	in, err := s.d.Store.GetIncident(r.Context(), id)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "not_found", "incident not found")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if in.TargetID == nil || !insight.IsStatementRule(in.RuleID) {
		writeJSON(w, http.StatusOK, map[string]any{"available": false,
			"reason": "Correlation is available for statement incidents (slower statements and long-running queries)."})
		return
	}
	fs, err := s.d.Store.ListFindings(r.Context(), repository.FindingFilter{IncidentID: id, Limit: 50})
	if err != nil {
		s.internal(w, r, err)
		return
	}
	var subject string
	var queryID *int64
	for _, f := range fs {
		if f.Finding.RuleID == in.RuleID {
			subject = f.Finding.Key
			if k, ok := insight.StatementKey(subject); ok {
				queryID = &k.QueryID
			}
			break
		}
	}
	if subject == "" {
		writeJSON(w, http.StatusOK, map[string]any{"available": false, "reason": "The incident's findings are no longer stored (finding retention)."})
		return
	}
	inv, err := s.d.Insight.Investigate(r.Context(), *in.TargetID, subject, queryID, in.OpenedAt.Add(time.Second))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"available": true, "investigation": inv})
}
