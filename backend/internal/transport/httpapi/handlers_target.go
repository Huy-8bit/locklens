package httpapi

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/locklens/locklens/internal/domain/live"
	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/domain/targets"
	"github.com/locklens/locklens/internal/repository"
)

// overviewMetrics are the latest values shown on overview pages.
var overviewMetrics = []string{
	"sessions.active", "sessions.idle", "sessions.idle_in_transaction", "sessions.waiting_lock", "sessions.unknown_state",
	"sessions.client_total", "connections.used", "connections.max", "connections.reserved", "connections.usage_ratio",
	"blocking.blocked_sessions", "blocking.root_blockers", "blocking.max_lock_wait_seconds", "transactions.open",
	"transactions.long", "transactions.oldest_age_seconds", "transactions.idle_in_transaction_oldest_age_seconds",
	"xact.commit.per_s", "xact.rollback.per_s", "cache.hit_ratio", "temp.files.per_s", "temp.bytes.per_s",
	"tuples.inserted.per_s", "tuples.updated.per_s", "tuples.deleted.per_s", "tuples.returned.per_s", "tuples.fetched.per_s", "deadlocks.delta", "wal.bytes.per_s", "wal.received_bytes.per_s", "db.total_size_bytes",
	"statements.calls.per_s", "statements.exec_ms.per_s", "replication.max_replay_lag_bytes", "slots.max_retained_bytes",
	"standby.replay_delay_bytes", "host.cpu_utilization_ratio", "host.memory_available_bytes", "host.memory_total_bytes", "host.disk_read_bytes.per_s", "host.disk_write_bytes.per_s",
}

type latestValue struct {
	Previous   *float64   `json:"previous,omitempty"`
	PreviousAt *time.Time `json:"previous_at,omitempty"`
	At         time.Time  `json:"at"`
	Value      *float64   `json:"value"`
}

func latestValues(v live.View) map[string]latestValue {
	out := map[string]latestValue{}
	for _, m := range overviewMetrics {
		if tv, ok := v.Latest(m); ok {
			lv := latestValue{At: tv.At, Value: tv.Value}
			history := v.Points[m]
			if len(history) > 1 {
				prev := history[len(history)-2]
				interval := v.Collectors[repository.MetricCollector(m)].IntervalSeconds
				if interval > 0 && tv.At.Sub(prev.At).Seconds() <= 3*interval && prev.Value != nil && tv.Value != nil {
					lv.Previous = prev.Value
					lv.PreviousAt = &prev.At
				}
			}
			out[m] = lv
		}
	}
	return out
}

func (s *Server) view(r *http.Request, id int64) (live.View, bool) {
	return s.d.Manager.View(r.Context(), id)
}

// targetOr404 loads a target view or writes an error.
func (s *Server) targetOr404(w http.ResponseWriter, r *http.Request) (targets.View, bool) {
	id, ok := pathID(r, "id")
	if !ok {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "invalid target id")
		return targets.View{}, false
	}
	t, err := s.d.Targets.Get(r.Context(), id)
	if err != nil {
		s.targetError(w, r, err)
		return targets.View{}, false
	}
	return t, true
}

func (s *Server) targetOverview(w http.ResponseWriter, r *http.Request) {
	t, ok := s.targetOr404(w, r)
	if !ok {
		return
	}
	resp := map[string]any{"target": t, "latest": map[string]latestValue{}, "connected": false}
	if v, ok := s.view(r, t.ID); ok {
		resp["latest"] = latestValues(v)
		resp["connected"] = v.Connected
		resp["connect_error"] = v.ConnectError
		if v.Activity != nil {
			resp["activity_at"] = v.Activity.At
			resp["counts"] = v.Activity.Counts
		}
		if v.Report != nil {
			resp["summary"] = v.Report.Analysis.Summary
			resp["visibility_warnings"] = v.Report.Snapshot.Warnings
		}
		resp["host_configured"] = t.NodeExporterURL != nil
		resp["host_error"] = v.HostError
	}
	findings, err := s.d.Store.ListFindings(r.Context(), repository.FindingFilter{TargetID: t.ID, Status: "current", Limit: 100})
	if err != nil {
		s.internal(w, r, err)
		return
	}
	resp["findings"] = findings
	incs, total, err := s.d.Store.ListIncidents(r.Context(), repository.IncidentFilter{TargetID: t.ID, State: "open", Limit: 10})
	if err != nil {
		s.internal(w, r, err)
		return
	}
	resp["open_incidents"] = incs
	resp["open_incident_count"] = total
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) targetMetrics(w http.ResponseWriter, r *http.Request) {
	t, ok := s.targetOr404(w, r)
	if !ok {
		return
	}
	from, to, err := timeRange(r, time.Hour)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	names := strings.Split(r.URL.Query().Get("names"), ",")
	var metrics []string
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if len(n) > 80 || strings.ContainsAny(n, " {}'\"") {
			writeError(w, r, http.StatusBadRequest, "invalid_request", "invalid metric name")
			return
		}
		metrics = append(metrics, n)
	}
	if len(metrics) == 0 || len(metrics) > 20 {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "names must list 1 to 20 metrics")
		return
	}
	collectors := t.Status.Collectors
	if v, ok := s.view(r, t.ID); ok {
		collectors = v.Collectors
	}
	step, err := metricStep(r, from, to, s.collectionStep(r, t, metrics))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	// Coarse steps and ranges beyond raw history are read from rollups.
	step = s.d.Store.SeriesStep(from, step)
	series, err := s.d.Store.QuerySeries(r.Context(), t.ID, metrics, from, to, step)
	if errors.Is(err, repository.ErrSeriesLimit) {
		writeError(w, r, http.StatusUnprocessableEntity, "series_limit", err.Error())
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	gaps, err := s.d.Store.ListGaps(r.Context(), t.ID, from, to, 200)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	incs, total, err := s.d.Store.ListIncidents(r.Context(), repository.IncidentFilter{TargetID: t.ID, From: &from, To: &to, Limit: 200})
	if err != nil {
		s.internal(w, r, err)
		return
	}
	annotations := make([]map[string]any, 0, len(incs))
	for _, i := range incs {
		annotations = append(annotations, map[string]any{"id": i.ID, "title": i.Title, "severity": i.Severity, "opened_at": i.OpenedAt, "resolved_at": i.ResolvedAt})
	}
	writeJSON(w, http.StatusOK, map[string]any{"from": from, "to": to, "step_seconds": step.Seconds(), "series": series, "gaps": gaps,
		"collection_interval_seconds": t.IntervalSeconds, "collectors": collectors, "annotations": annotations, "annotations_truncated": total > len(incs), "gaps_truncated": len(gaps) == 200})
}

func (s *Server) targetActivity(w http.ResponseWriter, r *http.Request) {
	t, ok := s.targetOr404(w, r)
	if !ok {
		return
	}
	if v, ok := s.view(r, t.ID); ok && v.Activity != nil && v.Report != nil {
		writeJSON(w, http.StatusOK, map[string]any{"source": "live", "observed_at": v.Activity.At, "report": v.Report,
			"counts": v.Activity.Counts, "vacuum_progress": v.Activity.Vacuum, "connected": v.Connected,
			"query_text_enabled": t.QueryTextEnabled})
		return
	}
	meta, raw, err := s.d.Store.LatestActivitySnapshot(r.Context(), t.ID)
	if errors.Is(err, repository.ErrNotFound) {
		writeJSON(w, http.StatusOK, map[string]any{"source": "none", "report": nil})
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	rep, err := s.reportFrom(raw)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"source": "stored", "observed_at": meta.At, "report": rep, "snapshot_id": meta.ID,
		"query_text_enabled": t.QueryTextEnabled})
}

func (s *Server) targetTables(w http.ResponseWriter, r *http.Request) {
	t, ok := s.targetOr404(w, r)
	if !ok {
		return
	}
	resp := map[string]any{"source": "none", "tables": nil}
	var h *observe.TableHealth
	if v, ok := s.view(r, t.ID); ok {
		h = v.Tables
		resp["activity"] = v.TableActivity
		if v.Activity != nil {
			resp["vacuum_progress"] = v.Activity.Vacuum
			resp["vacuum_progress_at"] = v.Activity.At
		}
		if v.Report != nil {
			resp["long_transactions"] = v.Report.Analysis.LongTransactions
		}
		if h != nil {
			resp["source"] = "live"
		}
	}
	if h == nil {
		stored, err := s.d.Store.LatestTableHealth(r.Context(), t.ID)
		if err != nil && !errors.Is(err, repository.ErrNotFound) {
			s.internal(w, r, err)
			return
		}
		if stored != nil {
			h = stored
			resp["source"] = "stored"
		}
	}
	resp["tables"] = h
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) targetReplication(w http.ResponseWriter, r *http.Request) {
	t, ok := s.targetOr404(w, r)
	if !ok {
		return
	}
	resp := map[string]any{"source": "none", "replication": nil}
	if v, ok := s.view(r, t.ID); ok && v.Replication != nil {
		resp["source"], resp["replication"] = "live", v.Replication
	} else {
		ts, raw, err := s.d.Store.LatestState(r.Context(), t.ID, "replication")
		if err != nil && !errors.Is(err, repository.ErrNotFound) {
			s.internal(w, r, err)
			return
		}
		if raw != nil {
			resp["source"], resp["replication"], resp["observed_at"] = "stored", raw, ts
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) targetHost(w http.ResponseWriter, r *http.Request) {
	t, ok := s.targetOr404(w, r)
	if !ok {
		return
	}
	resp := map[string]any{"configured": t.NodeExporterURL != nil, "available": false, "host": nil}
	if v, ok := s.view(r, t.ID); ok {
		if v.Host != nil {
			resp["available"], resp["host"] = true, v.Host
		}
		resp["error"] = v.HostError
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) targetFindings(w http.ResponseWriter, r *http.Request) {
	t, ok := s.targetOr404(w, r)
	if !ok {
		return
	}
	status := r.URL.Query().Get("status")
	if !slices.Contains([]string{"", "current", "resolved"}, status) {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "status must be current or resolved")
		return
	}
	fs, err := s.d.Store.ListFindings(r.Context(), repository.FindingFilter{TargetID: t.ID, Status: status, Limit: 200})
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"findings": fs})
}

func (s *Server) targetGaps(w http.ResponseWriter, r *http.Request) {
	t, ok := s.targetOr404(w, r)
	if !ok {
		return
	}
	from, to, err := timeRange(r, 24*time.Hour)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	gaps, err := s.d.Store.ListGaps(r.Context(), t.ID, from, to, 500)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"gaps": gaps})
}

// collectionStep is the shortest bucket that holds at least one sample of
// each metric: the target's interval, or the slower interval of the
// collector behind a metric. A shorter bucket would alternate between a
// value and a gap.
// statementsStep is the finest step of statement charts: the statements
// collection interval.
func (s *Server) statementsStep(r *http.Request, t targets.View) time.Duration {
	return s.collectionStep(r, t, []string{"statements.calls.per_s"})
}

func (s *Server) collectionStep(r *http.Request, t targets.View, metrics []string) time.Duration {
	collectors := t.Status.Collectors
	if v, ok := s.view(r, t.ID); ok {
		collectors = v.Collectors
	}
	step := time.Duration(t.IntervalSeconds) * time.Second
	for _, m := range metrics {
		if status, ok := collectors[repository.MetricCollector(m)]; ok {
			// A burst shortens the interval for a few minutes only; the
			// configured interval decides the step (a slowed-down collector
			// the longer one).
			step = max(step, time.Duration(max(status.IntervalSeconds, status.BaseIntervalSeconds)*float64(time.Second)))
		}
	}
	return step
}
