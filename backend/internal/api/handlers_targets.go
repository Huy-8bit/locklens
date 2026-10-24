package api

import (
	"errors"
	"net/http"

	"github.com/locklens/locklens/internal/secret"
	"github.com/locklens/locklens/internal/storage"
	"github.com/locklens/locklens/internal/targets"
)

func (s *Server) targetError(w http.ResponseWriter, r *http.Request, err error) {
	var ve *targets.ValidationError
	switch {
	case errors.As(err, &ve):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]apiError{"error": {Code: "validation", Message: "invalid target", Fields: ve.Fields, RequestID: requestID(r)}})
	case errors.Is(err, storage.ErrNotFound):
		writeError(w, r, http.StatusNotFound, "not_found", "target not found")
	case errors.Is(err, storage.ErrNameTaken):
		writeJSON(w, http.StatusConflict, map[string]apiError{"error": {Code: "conflict", Message: err.Error(), Fields: map[string]string{"name": err.Error()}, RequestID: requestID(r)}})
	case errors.Is(err, secret.ErrDecrypt):
		writeError(w, r, http.StatusConflict, "decrypt", err.Error())
	default:
		s.internal(w, r, err)
	}
}

func (s *Server) listTargets(w http.ResponseWriter, r *http.Request) {
	ts, err := s.d.Targets.List(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"targets": ts})
}

func (s *Server) createTarget(w http.ResponseWriter, r *http.Request) {
	var in targets.Input
	if err := decode(w, r, &in, MaxBodyBytes); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	v, err := s.d.Targets.Create(r.Context(), in)
	if err != nil {
		s.targetError(w, r, err)
		return
	}
	s.d.Log.Info("target created", "target_id", v.ID, "name", v.Name)
	writeJSON(w, http.StatusCreated, v)
}

func (s *Server) getTarget(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "invalid target id")
		return
	}
	v, err := s.d.Targets.Get(r.Context(), id)
	if err != nil {
		s.targetError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) updateTarget(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "invalid target id")
		return
	}
	var in targets.Input
	if err := decode(w, r, &in, MaxBodyBytes); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	v, err := s.d.Targets.Update(r.Context(), id, in)
	if err != nil {
		s.targetError(w, r, err)
		return
	}
	s.d.Log.Info("target updated", "target_id", id)
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) deleteTarget(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "invalid target id")
		return
	}
	q := r.URL.Query()
	purge := q.Get("purge") == "true"
	keep := q.Get("keep_incidents") != "false"
	if err := s.d.Targets.Delete(r.Context(), id, purge, keep); err != nil {
		s.targetError(w, r, err)
		return
	}
	s.d.Log.Info("target deleted", "target_id", id, "purge", purge, "keep_incidents", keep)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) testNewTarget(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DSN string `json:"dsn"`
	}
	if err := decode(w, r, &body, MaxBodyBytes); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if body.DSN == "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]apiError{"error": {Code: "validation", Message: "dsn is required", Fields: map[string]string{"dsn": "required"}, RequestID: requestID(r)}})
		return
	}
	res, err := s.d.Targets.Test(r.Context(), 0, body.DSN)
	if err != nil {
		s.targetError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) testTarget(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "invalid target id")
		return
	}
	res, err := s.d.Targets.Test(r.Context(), id, "")
	if err != nil {
		s.targetError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
