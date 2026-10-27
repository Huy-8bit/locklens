package httpapi

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/domain/plans"
	"github.com/locklens/locklens/internal/domain/workspace"
	"github.com/locklens/locklens/internal/repository"
)

// explainableCommands are the statement classes EXPLAIN accepts.
var explainableCommands = []string{"select", "insert", "update", "delete", "merge"}

func (s *Server) listPlans(w http.ResponseWriter, r *http.Request) {
	t, ok := s.targetOr404(w, r)
	if !ok {
		return
	}
	qid, ok := pathID(r, "qid")
	if !ok {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "invalid query identity")
		return
	}
	ps, err := s.d.Store.ListPlans(r.Context(), t.ID, qid)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plans": ps,
		"note": "Plans are saved only on request. EXPLAIN runs without ANALYZE through the read-only investigation workspace; LockLens never executes the statement to capture a plan."})
}

// savePlan stores EXPLAIN output a user ran or pasted.
func (s *Server) savePlan(w http.ResponseWriter, r *http.Request) {
	t, ok := s.targetOr404(w, r)
	if !ok {
		return
	}
	qid, ok := pathID(r, "qid")
	if !ok {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "invalid query identity")
		return
	}
	var in struct {
		Plan   string `json:"plan"`
		Note   string `json:"note"`
		Source string `json:"source"`
	}
	if err := decode(w, r, &in, plans.MaxPlanBytes+4096); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if in.Source == "" {
		in.Source = "manual"
	}
	if !slices.Contains([]string{"manual", "workspace_explain"}, in.Source) || len(in.Note) > 500 {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "source must be manual or workspace_explain, and the note at most 500 characters")
		return
	}
	s.storePlan(w, r, t.ID, qid, in.Plan, in.Source, in.Note)
}

func (s *Server) storePlan(w http.ResponseWriter, r *http.Request, targetID, qid int64, raw, source, note string) {
	p, err := plans.Parse(raw)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_plan", err.Error())
		return
	}
	row, err := s.d.Store.SavePlan(r.Context(), targetID, repository.PlanRow{IdentityID: qid, CapturedBy: sessionOf(r).Username, Source: source,
		Format: p.Format, Plan: strings.TrimSpace(raw), Fingerprint: p.Fingerprint, TotalCost: p.TotalCost, Note: note})
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "not_found", "query identity not found")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	row.Plan = ""
	writeJSON(w, http.StatusCreated, map[string]any{"plan": row, "parsed": p})
}

// capturePlan asks the target for the generic plan of a statement's
// normalised text (PostgreSQL 16+) through the investigation workspace, on an
// explicit request only.
func (s *Server) capturePlan(w http.ResponseWriter, r *http.Request) {
	t, ok := s.targetOr404(w, r)
	if !ok {
		return
	}
	qid, ok := pathID(r, "qid")
	if !ok {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "invalid query identity")
		return
	}
	var in struct {
		RequestID string `json:"request_id"`
		Note      string `json:"note"`
	}
	if !decodeWorkspace(w, r, &in) {
		return
	}
	ident, _, _, err := s.d.Store.QueryIdentity(r.Context(), t.ID, qid, true)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "not_found", "query identity not found")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	switch {
	case ident.ServerMajor < 16:
		writeError(w, r, http.StatusBadRequest, "unsupported", "Generic plans need PostgreSQL 16 or later. Run EXPLAIN in the SQL workspace with real values and save the result instead.")
		return
	case ident.Text == nil:
		writeError(w, r, http.StatusBadRequest, "unsupported", "The statement text is not collected for this target (query text collection is off).")
		return
	case !slices.Contains(explainableCommands, ident.Command):
		writeError(w, r, http.StatusBadRequest, "unsupported", "Only SELECT, INSERT, UPDATE, DELETE, and MERGE statements can be explained.")
		return
	case ident.DatName != t.Database:
		writeError(w, r, http.StatusBadRequest, "unsupported", "The statement runs in database "+strconv.Quote(ident.DatName)+", but the workspace connects to "+strconv.Quote(t.Database)+".")
		return
	}
	if s.d.Workspace == nil {
		writeError(w, r, http.StatusServiceUnavailable, "unavailable", "Workspace service unavailable.")
		return
	}
	ctx := workspace.WithActor(r.Context(), sessionOf(r).Username)
	out, err := s.d.Workspace.Run(ctx, t.ID, sessionOf(r).CSRFToken, "explain", in.RequestID, func(ctx context.Context, tx pgx.Tx) (any, error) {
		return workspace.ExplainGeneric(ctx, tx, *ident.Text)
	})
	if err != nil {
		s.workspaceError(w, r, err)
		return
	}
	note := in.Note
	if len(note) > 500 {
		note = note[:500]
	}
	s.storePlan(w, r, t.ID, qid, out.(string), "generic_plan", note)
}

func (s *Server) getPlan(w http.ResponseWriter, r *http.Request) {
	t, ok := s.targetOr404(w, r)
	if !ok {
		return
	}
	pid, ok := pathID(r, "pid")
	if !ok {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "invalid plan id")
		return
	}
	row, err := s.d.Store.GetPlan(r.Context(), t.ID, pid)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "not_found", "plan not found")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	p, err := plans.Parse(row.Plan)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plan": row, "parsed": p, "index_hints": plans.IndexHints(p),
		"index_hints_note": "Index hints are hypotheses from the plan shape only. An index costs writes and space; test it on a copy first. LockLens never creates indexes."})
}

func (s *Server) comparePlans(w http.ResponseWriter, r *http.Request) {
	t, ok := s.targetOr404(w, r)
	if !ok {
		return
	}
	a, errA := strconv.ParseInt(r.URL.Query().Get("a"), 10, 64)
	b, errB := strconv.ParseInt(r.URL.Query().Get("b"), 10, 64)
	if errA != nil || errB != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "a and b must be plan ids")
		return
	}
	var parsed [2]plans.Plan
	var rows [2]repository.PlanRow
	for i, id := range []int64{a, b} {
		row, err := s.d.Store.GetPlan(r.Context(), t.ID, id)
		if errors.Is(err, repository.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "not_found", "plan not found")
			return
		}
		if err != nil {
			s.internal(w, r, err)
			return
		}
		p, err := plans.Parse(row.Plan)
		if err != nil {
			s.internal(w, r, err)
			return
		}
		rows[i], parsed[i] = row, p
	}
	writeJSON(w, http.StatusOK, map[string]any{"a": rows[0], "b": rows[1], "parsed_a": parsed[0], "parsed_b": parsed[1],
		"comparison": plans.Compare(parsed[0], parsed[1])})
}

func (s *Server) deletePlan(w http.ResponseWriter, r *http.Request) {
	t, ok := s.targetOr404(w, r)
	if !ok {
		return
	}
	pid, ok := pathID(r, "pid")
	if !ok {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "invalid plan id")
		return
	}
	err := s.d.Store.DeletePlan(r.Context(), t.ID, pid)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "not_found", "plan not found")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
