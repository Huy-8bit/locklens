package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/locklens/locklens/internal/collector"
	"github.com/locklens/locklens/internal/model"
	"github.com/locklens/locklens/internal/observe"
	"github.com/locklens/locklens/internal/report"
	"github.com/locklens/locklens/internal/snapshot"
	"github.com/locklens/locklens/internal/storage"
	"github.com/locklens/locklens/internal/targets"
)

// overviewMetrics are the latest values shown on overview pages.
var overviewMetrics = []string{
	"sessions.active", "sessions.idle", "sessions.idle_in_transaction", "sessions.waiting_lock", "sessions.unknown_state",
	"sessions.client_total", "connections.used", "connections.max", "connections.reserved", "connections.usage_ratio",
	"blocking.blocked_sessions", "blocking.root_blockers", "blocking.max_lock_wait_seconds", "transactions.open",
	"transactions.long", "transactions.oldest_age_seconds", "transactions.idle_in_transaction_oldest_age_seconds",
	"xact.commit.per_s", "xact.rollback.per_s", "cache.hit_ratio", "temp.files.per_s", "temp.bytes.per_s",
	"deadlocks.delta", "wal.bytes.per_s", "wal.received_bytes.per_s", "db.total_size_bytes",
	"statements.calls.per_s", "statements.exec_ms.per_s", "replication.max_replay_lag_bytes", "slots.max_retained_bytes",
	"standby.replay_delay_bytes", "host.cpu_utilization_ratio", "host.memory_available_bytes", "host.memory_total_bytes",
}

type latestValue struct {
	At    time.Time `json:"at"`
	Value *float64  `json:"value"`
}

func latestValues(v collector.View) map[string]latestValue {
	out := map[string]latestValue{}
	for _, m := range overviewMetrics {
		if tv, ok := v.Latest(m); ok {
			out[m] = latestValue{At: tv.At, Value: tv.Value}
		}
	}
	return out
}

func (s *Server) view(id int64) (collector.View, bool) {
	st, ok := s.d.Manager.State(id)
	if !ok {
		return collector.View{}, false
	}
	return st.Snapshot(), true
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

type fleetEntry struct {
	Target targets.View           `json:"target"`
	Latest map[string]latestValue `json:"latest"`
	Counts storage.TargetCounts   `json:"counts"`
}

func (s *Server) fleet(w http.ResponseWriter, r *http.Request) {
	ts, err := s.d.Targets.List(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	counts, err := s.d.Store.CountsByTarget(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := make([]fleetEntry, 0, len(ts))
	for _, t := range ts {
		e := fleetEntry{Target: t, Latest: map[string]latestValue{}, Counts: counts[t.ID]}
		if v, ok := s.view(t.ID); ok {
			e.Latest = latestValues(v)
		}
		out = append(out, e)
	}
	writeJSON(w, http.StatusOK, map[string]any{"targets": out, "generated_at": time.Now().UTC()})
}

func (s *Server) targetOverview(w http.ResponseWriter, r *http.Request) {
	t, ok := s.targetOr404(w, r)
	if !ok {
		return
	}
	resp := map[string]any{"target": t, "latest": map[string]latestValue{}, "connected": false}
	if v, ok := s.view(t.ID); ok {
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
	findings, err := s.d.Store.ListFindings(r.Context(), storage.FindingFilter{TargetID: t.ID, Status: "current", Limit: 100})
	if err != nil {
		s.internal(w, r, err)
		return
	}
	resp["findings"] = findings
	incs, total, err := s.d.Store.ListIncidents(r.Context(), storage.IncidentFilter{TargetID: t.ID, State: "open", Limit: 10})
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
	step := stepFor(from, to, time.Duration(t.IntervalSeconds)*time.Second)
	series, err := s.d.Store.QuerySeries(r.Context(), t.ID, metrics, from, to, step)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	gaps, err := s.d.Store.ListGaps(r.Context(), t.ID, from, to, 200)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"from": from, "to": to, "step_seconds": step.Seconds(), "series": series, "gaps": gaps})
}

func (s *Server) targetActivity(w http.ResponseWriter, r *http.Request) {
	t, ok := s.targetOr404(w, r)
	if !ok {
		return
	}
	if v, ok := s.view(t.ID); ok && v.Activity != nil && v.Report != nil {
		writeJSON(w, http.StatusOK, map[string]any{"source": "live", "observed_at": v.Activity.At, "report": v.Report,
			"counts": v.Activity.Counts, "vacuum_progress": v.Activity.Vacuum, "connected": v.Connected,
			"query_text_enabled": t.QueryTextEnabled})
		return
	}
	meta, raw, err := s.d.Store.LatestActivitySnapshot(r.Context(), t.ID)
	if errors.Is(err, storage.ErrNotFound) {
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

func (s *Server) reportFrom(raw json.RawMessage) (*report.Report, error) {
	var snap model.Snapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return nil, err
	}
	return report.Build(&snap, s.d.Settings.LongTransactionThreshold()), nil
}

func (s *Server) listSnapshots(w http.ResponseWriter, r *http.Request) {
	t, ok := s.targetOr404(w, r)
	if !ok {
		return
	}
	from, to, err := timeRange(r, 24*time.Hour)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}
	list, err := s.d.Store.ListActivitySnapshots(r.Context(), t.ID, from, to, r.URL.Query().Get("blocking") == "true", limit)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"snapshots": list, "from": from, "to": to})
}

func (s *Server) getSnapshot(w http.ResponseWriter, r *http.Request) {
	t, ok := s.targetOr404(w, r)
	if !ok {
		return
	}
	sid, ok := pathID(r, "sid")
	if !ok {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "invalid snapshot id")
		return
	}
	meta, raw, err := s.d.Store.GetActivitySnapshot(r.Context(), t.ID, sid)
	if errors.Is(err, storage.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "not_found", "snapshot not found (it may have expired)")
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
	writeJSON(w, http.StatusOK, map[string]any{"source": "stored", "snapshot_id": meta.ID, "observed_at": meta.At, "report": rep})
}

// analyzeSnapshot renders an uploaded CLI snapshot file (offline analysis).
func (s *Server) analyzeSnapshot(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, MaxImportBytes)
	snap, err := snapshot.Decode(r.Body, "uploaded snapshot")
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeError(w, r, http.StatusRequestEntityTooLarge, "too_large", "snapshot exceeds 10 MiB")
			return
		}
		writeError(w, r, http.StatusUnprocessableEntity, "invalid_snapshot", err.Error())
		return
	}
	th := time.Duration(snap.Collection.MinTxAgeSeconds * float64(time.Second))
	writeJSON(w, http.StatusOK, map[string]any{"source": "upload", "observed_at": snap.ServerTime, "report": report.Build(snap, th)})
}

func (s *Server) targetTables(w http.ResponseWriter, r *http.Request) {
	t, ok := s.targetOr404(w, r)
	if !ok {
		return
	}
	resp := map[string]any{"source": "none", "tables": nil}
	var h *observe.TableHealth
	if v, ok := s.view(t.ID); ok {
		h = v.Tables
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
		if err != nil && !errors.Is(err, storage.ErrNotFound) {
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
	if v, ok := s.view(t.ID); ok && v.Replication != nil {
		resp["source"], resp["replication"] = "live", v.Replication
	} else {
		ts, raw, err := s.d.Store.LatestState(r.Context(), t.ID, "replication")
		if err != nil && !errors.Is(err, storage.ErrNotFound) {
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
	if v, ok := s.view(t.ID); ok {
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
	fs, err := s.d.Store.ListFindings(r.Context(), storage.FindingFilter{TargetID: t.ID, Status: status, Limit: 200})
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

func (s *Server) storageStats(w http.ResponseWriter, r *http.Request) {
	st, err := s.d.Store.TableSizes(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tables": st})
}
