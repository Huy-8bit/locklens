package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/locklens/locklens/internal/export"
	"github.com/locklens/locklens/internal/rules"
	"github.com/locklens/locklens/internal/storage"
)

func (s *Server) listIncidents(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := page(r)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	q := r.URL.Query()
	f := storage.IncidentFilter{State: q.Get("state"), RuleID: q.Get("rule"), Limit: limit, Offset: offset}
	if f.State != "" && f.State != "open" && f.State != "resolved" {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "state must be open or resolved")
		return
	}
	if v := q.Get("target"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "invalid_request", "invalid target")
			return
		}
		f.TargetID = id
	}
	if q.Get("from") != "" || q.Get("range") != "" {
		from, to, err := timeRange(r, 24*time.Hour)
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		f.From, f.To = &from, &to
	}
	list, total, err := s.d.Store.ListIncidents(r.Context(), f)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"incidents": list, "total": total, "page_size": limit, "rules": rules.TriggerRules})
}

func (s *Server) incidentOr404(w http.ResponseWriter, r *http.Request) (storage.Incident, bool) {
	id, ok := pathID(r, "id")
	if !ok {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "invalid incident id")
		return storage.Incident{}, false
	}
	in, err := s.d.Store.GetIncident(r.Context(), id)
	if errors.Is(err, storage.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "not_found", "incident not found")
		return in, false
	}
	if err != nil {
		s.internal(w, r, err)
		return in, false
	}
	return in, true
}

func (s *Server) getIncident(w http.ResponseWriter, r *http.Request) {
	in, ok := s.incidentOr404(w, r)
	if !ok {
		return
	}
	fs, err := s.d.Store.ListFindings(r.Context(), storage.FindingFilter{IncidentID: in.ID, Limit: 500})
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"incident": in, "findings": fs})
}

func (s *Server) incidentEvents(w http.ResponseWriter, r *http.Request) {
	in, ok := s.incidentOr404(w, r)
	if !ok {
		return
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	limit := 500
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 2000 {
			limit = n
		}
	}
	evs, err := s.d.Store.ListIncidentEvents(r.Context(), in.ID, after, limit)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": evs, "has_more": len(evs) == limit})
}

func (s *Server) acknowledgeIncident(w http.ResponseWriter, r *http.Request) {
	in, ok := s.incidentOr404(w, r)
	if !ok {
		return
	}
	var body struct {
		Note string `json:"note"`
	}
	if err := decode(w, r, &body, 8192); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if len([]rune(body.Note)) > 1000 {
		writeError(w, r, http.StatusUnprocessableEntity, "validation", "note must be at most 1000 characters")
		return
	}
	out, err := s.d.Store.AcknowledgeIncident(r.Context(), in.ID, sessionOf(r).Username, body.Note)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.d.Hub.Publish("incident.updated", map[string]any{"incident_id": in.ID})
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) exportIncident(w http.ResponseWriter, r *http.Request) {
	in, ok := s.incidentOr404(w, r)
	if !ok {
		return
	}
	includeQuery := r.URL.Query().Get("include_query") == "true"
	e, err := export.Build(r.Context(), s.d.Store, in.ID, includeQuery, s.d.Tool)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	switch r.URL.Query().Get("format") {
	case "", "json":
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="locklens-incident-%d.json"`, in.ID))
		_ = export.WriteJSON(w, e)
	case "html":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="locklens-incident-%d.html"`, in.ID))
		// The report is a self-contained document with its own CSP.
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src data:")
		_ = export.WriteHTML(w, e)
	default:
		writeError(w, r, http.StatusBadRequest, "invalid_request", "format must be json or html")
	}
}

func (s *Server) importIncident(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, export.MaxImportBytes+1)
	e, err := export.Decode(r.Body)
	if err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, "invalid_import", err.Error())
		return
	}
	in := e.Incident
	in.TargetID = nil
	in.Source = "import"
	in.State = "resolved"
	if in.ResolvedAt == nil {
		at := in.LastSeenAt
		in.ResolvedAt = &at
	}
	res := "imported from an export file"
	in.Resolution = &res
	in.TargetName = in.TargetName + " (imported)"
	in.EventCount, in.SizeBytes = 0, 0
	id, err := s.d.Store.CreateIncident(r.Context(), in, e.Events)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (s *Server) getRuleSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"thresholds": s.d.Settings.Thresholds(), "defaults": rules.Defaults(),
		"rule_versions": rules.Versions, "trigger_rules": rules.TriggerRules})
}

func (s *Server) putRuleSettings(w http.ResponseWriter, r *http.Request) {
	var th rules.Thresholds
	if err := decode(w, r, &th, 16384); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := th.Validate(); err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, "validation", err.Error())
		return
	}
	if err := s.d.Settings.UpdateThresholds(r.Context(), th); err != nil {
		s.internal(w, r, err)
		return
	}
	s.d.Log.Info("rule thresholds updated", "user", sessionOf(r).Username)
	writeJSON(w, http.StatusOK, map[string]any{"thresholds": th})
}
