package httpapi

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/locklens/locklens/internal/domain/collector"
	"github.com/locklens/locklens/internal/domain/optimizer"
	"github.com/locklens/locklens/internal/domain/workspace"
	"github.com/locklens/locklens/internal/lib/sqlparse"
	"github.com/locklens/locklens/internal/repository"
)

// activeStatuses are the recommendations shown by default.
var activeStatuses = []string{repository.RecOpen, repository.RecAccepted, repository.RecApplied, repository.RecVerified,
	repository.RecNoEffect, repository.RecRegressed}

var allStatuses = append(slices.Clone(activeStatuses), repository.RecDismissed, repository.RecResolved)

// statusFilter reads ?status=a,b (default: the active statuses).
func statusFilter(r *http.Request) ([]string, bool) {
	v := r.URL.Query().Get("status")
	if v == "" {
		return activeStatuses, true
	}
	if v == "all" {
		return nil, true
	}
	out := strings.Split(v, ",")
	for _, s := range out {
		if !slices.Contains(allStatuses, s) {
			return nil, false
		}
	}
	return out, true
}

type optimizerTarget struct {
	TargetID   int64      `json:"target_id"`
	TargetName string     `json:"target_name"`
	CatalogAt  *time.Time `json:"catalog_at"`
	Database   string     `json:"database"`
	// Databases are the databases with a catalog; CatalogAt, Database and
	// HypoPGVersion are of the connected one, where what-if checks run.
	Databases     []string `json:"databases"`
	QueryText     bool     `json:"query_text_enabled"`
	HypoPGEnabled bool     `json:"hypopg_enabled"`
	HypoPGVersion string   `json:"hypopg_installed,omitempty"`
	SQLEnabled    bool     `json:"sql_enabled"`
	Warnings      []string `json:"warnings"`
}

type recItem struct {
	repository.Recommendation
	TargetName string `json:"target_name"`
}

// optimizerOverview lists the recommendations of the fleet, the largest
// benefit first, with each target's optimizer status.
func (s *Server) optimizerOverview(w http.ResponseWriter, r *http.Request) {
	statuses, ok := statusFilter(r)
	if !ok {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "unknown status")
		return
	}
	kind := r.URL.Query().Get("kind")
	ts, err := s.fleetTargets(r)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	items := []recItem{}
	targets := []optimizerTarget{}
	counts := map[string]int{}
	for _, t := range ts {
		recs, err := s.d.Store.ListRecommendations(r.Context(), repository.RecommendationFilter{TargetID: t.ID, Statuses: statuses, Kind: kind})
		if err != nil {
			s.internal(w, r, err)
			return
		}
		for _, rec := range recs {
			items = append(items, recItem{Recommendation: rec, TargetName: t.Name})
			counts[rec.Status]++
		}
		ot := optimizerTarget{TargetID: t.ID, TargetName: t.Name, QueryText: t.QueryTextEnabled, Warnings: []string{}, Databases: []string{}}
		cats, err := s.d.Store.Catalogs(r.Context(), t.ID)
		if err != nil {
			s.internal(w, r, err)
			return
		}
		for i, cat := range cats {
			ot.Databases = append(ot.Databases, cat.DatName)
			if cat.DatName == t.Database || ot.CatalogAt == nil && i == len(cats)-1 {
				ot.CatalogAt, ot.Database, ot.HypoPGVersion = &cat.At, cat.DatName, cat.Extensions["hypopg"]
			}
			for _, warn := range cat.Warnings {
				if len(cats) > 1 {
					warn = cat.DatName + ": " + warn
				}
				ot.Warnings = append(ot.Warnings, warn)
			}
		}
		if p, err := s.d.Store.WorkspacePolicy(r.Context(), t.ID); err == nil {
			ot.HypoPGEnabled, ot.SQLEnabled = p.HypoPG, p.SQL
		}
		targets = append(targets, ot)
	}
	slices.SortStableFunc(items, func(a, b recItem) int {
		if a.Benefit != b.Benefit {
			if a.Benefit > b.Benefit {
				return -1
			}
			return 1
		}
		return int(a.ID - b.ID)
	})
	writeJSON(w, http.StatusOK, map[string]any{"recommendations": items, "targets": targets, "counts": counts,
		"parser_available": sqlparse.Available(),
		"note":             "Recommendations are derived from the catalog and the workload; LockLens never changes the database. Run the SQL yourself after review."})
}

func (s *Server) listRecommendations(w http.ResponseWriter, r *http.Request) {
	t, ok := s.targetOr404(w, r)
	if !ok {
		return
	}
	statuses, ok := statusFilter(r)
	if !ok {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "unknown status")
		return
	}
	recs, err := s.d.Store.ListRecommendations(r.Context(), repository.RecommendationFilter{TargetID: t.ID, Statuses: statuses, Kind: r.URL.Query().Get("kind")})
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"recommendations": recs, "parser_available": sqlparse.Available()})
}

func (s *Server) recommendationOr404(w http.ResponseWriter, r *http.Request, targetID int64) (repository.Recommendation, bool) {
	rid, ok := pathID(r, "rid")
	if !ok {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "invalid recommendation id")
		return repository.Recommendation{}, false
	}
	rec, err := s.d.Store.GetRecommendation(r.Context(), targetID, rid)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "not_found", "recommendation not found")
		return rec, false
	}
	if err != nil {
		s.internal(w, r, err)
		return rec, false
	}
	return rec, true
}

func (s *Server) getRecommendation(w http.ResponseWriter, r *http.Request) {
	t, ok := s.targetOr404(w, r)
	if !ok {
		return
	}
	if rec, ok := s.recommendationOr404(w, r, t.ID); ok {
		writeJSON(w, http.StatusOK, rec)
	}
}

// decideRecommendation records accepted, dismissed, or open again, with a
// note. It changes LockLens metadata only.
func (s *Server) decideRecommendation(w http.ResponseWriter, r *http.Request) {
	t, ok := s.targetOr404(w, r)
	if !ok {
		return
	}
	rec, ok := s.recommendationOr404(w, r, t.ID)
	if !ok {
		return
	}
	var in struct {
		Status string `json:"status"`
		Note   string `json:"note"`
	}
	if err := decode(w, r, &in, MaxBodyBytes); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if !slices.Contains([]string{repository.RecAccepted, repository.RecDismissed, repository.RecOpen}, in.Status) || len(in.Note) > 1000 {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "status must be accepted, dismissed, or open; the note at most 1000 characters")
		return
	}
	out, err := s.d.Store.DecideRecommendation(r.Context(), t.ID, rec.ID, in.Status, sessionOf(r).Username, in.Note)
	if errors.Is(err, repository.ErrRecommendationState) {
		writeError(w, r, http.StatusConflict, "invalid_state", "This recommendation was already applied or resolved; it can no longer be decided.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// whatIfRecommendation checks an index recommendation with a hypothetical
// index (HypoPG) through the investigation workspace.
func (s *Server) whatIfRecommendation(w http.ResponseWriter, r *http.Request) {
	t, ok := s.targetOr404(w, r)
	if !ok {
		return
	}
	rec, ok := s.recommendationOr404(w, r, t.ID)
	if !ok {
		return
	}
	var in struct {
		RequestID string `json:"request_id"`
	}
	if !decodeWorkspace(w, r, &in) {
		return
	}
	stored, err := s.d.Store.GetTarget(r.Context(), t.ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	ctx := workspace.WithActor(r.Context(), sessionOf(r).Username)
	res, err := s.d.Optimizer.WhatIf(ctx, stored, rec.ID, sessionOf(r).CSRFToken, in.RequestID)
	if errors.Is(err, optimizer.ErrNotIndex) {
		writeError(w, r, http.StatusBadRequest, "unsupported", err.Error())
		return
	}
	if err != nil {
		s.workspaceError(w, r, err)
		return
	}
	updated, err := s.d.Store.GetRecommendation(r.Context(), t.ID, rec.ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"whatif": res, "recommendation": updated})
}

// refreshOptimizer runs an optimizer pass for one target now.
func (s *Server) refreshOptimizer(w http.ResponseWriter, r *http.Request) {
	t, ok := s.targetOr404(w, r)
	if !ok {
		return
	}
	stored, err := s.d.Store.GetTarget(r.Context(), t.ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	// A fresh catalog first: the collector runs every 15 minutes, and a
	// person asking for an analysis has usually just changed something.
	start := time.Now()
	fresh := false
	if s.d.Manager != nil && s.d.Manager.RunSoon(t.ID, collector.CollectorCatalog) {
		deadline := time.NewTimer(20 * time.Second)
		defer deadline.Stop()
		tick := time.NewTicker(250 * time.Millisecond)
		defer tick.Stop()
	wait:
		for {
			select {
			case <-r.Context().Done():
				return
			case <-deadline.C:
				break wait
			case <-tick.C:
				if c, err := s.d.Store.LatestCatalog(r.Context(), t.ID); err == nil && c.At.After(start) {
					fresh = true
					break wait
				}
			}
		}
	}
	n, err := s.d.Optimizer.Refresh(r.Context(), stored, time.Now().UTC())
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, r, http.StatusConflict, "not_ready", "The schema catalog of this instance has not been collected yet (every 15 minutes, starting shortly after it connects).")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"derived": n, "fresh_catalog": fresh})
}
