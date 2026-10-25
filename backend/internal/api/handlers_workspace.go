package api

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/locklens/locklens/internal/storage"
	"github.com/locklens/locklens/internal/workspace"
	"net/http"
	"strconv"
	"time"
)

func (s *Server) workspaceError(w http.ResponseWriter, r *http.Request, err error) {
	e := workspace.SafeError(err)
	status := http.StatusBadRequest
	if e.Code == "forbidden" {
		status = 403
	}
	if e.Code == "busy" {
		status = 429
	}
	writeJSON(w, status, map[string]any{"error": e})
}
func (s *Server) workspacePolicy(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, r, 400, "invalid_id", "Invalid target ID.")
		return
	}
	p, err := s.d.Store.WorkspacePolicy(r.Context(), id)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, p)
}
func (s *Server) putWorkspacePolicy(w http.ResponseWriter, r *http.Request) {
	if s.d.Workspace == nil {
		writeError(w, r, 503, "unavailable", "Workspace service unavailable.")
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var in struct {
		storage.WorkspacePolicy
		DSN string `json:"dsn"`
	}
	if !decodeWorkspace(w, r, &in) {
		return
	}
	if err := s.d.Workspace.Configure(r.Context(), id, in.WorkspacePolicy, in.DSN); err != nil {
		s.workspaceError(w, r, err)
		return
	}
	_ = s.d.Store.AuditWorkspace(r.Context(), id, sessionOf(r).Username, "policy", "ok", 0, 0)
	s.workspacePolicy(w, r)
}
func (s *Server) workspaceRun(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.d.Workspace == nil {
		writeError(w, r, 503, "unavailable", "Workspace service unavailable.")
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var in struct {
		Action    string                `json:"action"`
		RequestID string                `json:"request_id"`
		Kind      string                `json:"kind"`
		Schema    string                `json:"schema"`
		Relation  string                `json:"relation"`
		SQL       string                `json:"sql"`
		Data      workspace.DataRequest `json:"data"`
	}
	if !decodeWorkspace(w, r, &in) {
		return
	}
	out, err := s.d.Workspace.Run(r.Context(), id, sessionOf(r).CSRFToken, in.Action, in.RequestID, func(ctx context.Context, tx pgx.Tx) (any, error) {
		switch in.Action {
		case "metadata":
			return workspace.Catalog(ctx, tx, in.Kind, in.Schema, in.Relation)
		case "data":
			return workspace.Data(ctx, tx, in.Data)
		case "sql", "explain":
			return workspace.Execute(ctx, tx, in.SQL, in.Schema, in.Action == "explain")
		}
		return nil, &workspace.Error{Code: "invalid_action", Message: "Unsupported workspace action."}
	})
	if err != nil {
		s.workspaceError(w, r, err)
		return
	}
	writeJSON(w, 200, out)
}
func (s *Server) workspaceCancel(w http.ResponseWriter, r *http.Request) {
	var in struct {
		RequestID string `json:"request_id"`
	}
	if !decodeWorkspace(w, r, &in) {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	ok := s.d.Workspace != nil && s.d.Workspace.Cancel(sessionOf(r).CSRFToken, id, in.RequestID)
	writeJSON(w, 200, map[string]bool{"cancelled": ok})
}
func (s *Server) topology(w http.ResponseWriter, r *http.Request) {
	links, err := s.d.Store.TopologyLinks(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"links": links, "source": "explicit administrator mapping; streaming observations are separate evidence"})
}
func (s *Server) putTopology(w http.ResponseWriter, r *http.Request) {
	var l storage.TopologyLink
	if !decodeWorkspace(w, r, &l) {
		return
	}
	if l.PrimaryID <= 0 || l.StandbyID <= 0 || l.PrimaryID == l.StandbyID || len(l.Application) < 1 || len(l.Application) > 63 || len(l.Provenance) < 5 || len(l.Provenance) > 300 {
		writeError(w, r, 400, "invalid_mapping", "Choose distinct targets, a replication application name and provenance (5–300 characters).")
		return
	}
	if err := s.d.Store.SetTopologyLink(r.Context(), l); err != nil {
		s.internal(w, r, err)
		return
	}
	s.topology(w, r)
}
func (s *Server) deleteTopology(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := s.d.Store.DeleteTopologyLink(r.Context(), id); err != nil {
		s.internal(w, r, err)
		return
	}
	w.WriteHeader(204)
}
func (s *Server) historyBounds(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	b, err := s.d.Store.HistoryBounds(ctx, id)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, 200, b)
}

func decodeWorkspace(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := decode(w, r, v, 65536); err != nil {
		writeError(w, r, 400, "invalid_request", "Invalid workspace request body.")
		return false
	}
	return true
}
