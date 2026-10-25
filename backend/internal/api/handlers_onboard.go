package api

import (
	"errors"
	"net/http"

	"github.com/locklens/locklens/internal/onboard"
)

// inspectConnection shows what adding a connection string would do.
func (s *Server) inspectConnection(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DSN string `json:"dsn"`
	}
	if err := decode(w, r, &body, MaxBodyBytes); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if body.DSN == "" {
		writeFieldErrors(w, r, map[string]string{"dsn": "required"})
		return
	}
	if s.d.Onboard == nil {
		writeError(w, r, http.StatusServiceUnavailable, "unavailable", "setup service unavailable")
		return
	}
	p, err := s.d.Onboard.Inspect(r.Context(), body.DSN)
	if err != nil {
		s.targetError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// quickAddTarget adds an instance from a connection string.
func (s *Server) quickAddTarget(w http.ResponseWriter, r *http.Request) {
	var req onboard.Request
	if err := decode(w, r, &req, MaxBodyBytes); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if req.DSN == "" {
		writeFieldErrors(w, r, map[string]string{"dsn": "required"})
		return
	}
	if s.d.Onboard == nil {
		writeError(w, r, http.StatusServiceUnavailable, "unavailable", "setup service unavailable")
		return
	}
	v, p, err := s.d.Onboard.Add(r.Context(), req)
	var se *onboard.SetupError
	switch {
	case errors.Is(err, onboard.ErrNeedsConfirmation):
		writeError(w, r, http.StatusConflict, "confirmation_required", err.Error())
		return
	case errors.As(err, &se):
		writeError(w, r, http.StatusUnprocessableEntity, "setup_failed", se.Message)
		return
	case err != nil:
		s.targetError(w, r, err)
		return
	}
	s.d.Log.Info("target added from a connection string", "target", v.Name, "mode", p.Mode, "by", sessionOf(r).Username)
	writeJSON(w, http.StatusCreated, map[string]any{"target": v, "mode": p.Mode, "warnings": p.Warnings})
}
