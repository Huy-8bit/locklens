package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/locklens/locklens/internal/domain/auth"
)

type sessionResponse struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	// Managed is the administrator configured by the environment.
	Managed     bool              `json:"managed"`
	Permissions []auth.Permission `json:"permissions"`
	CSRFToken   string            `json:"csrf_token"`
	ExpiresAt   time.Time         `json:"expires_at"`
	Server      struct {
		Version  string    `json:"version"`
		DemoMode bool      `json:"demo_mode"`
		Time     time.Time `json:"time"`
	} `json:"server"`
}

func (s *Server) sessionPayload(sess auth.Session) sessionResponse {
	var out sessionResponse
	out.UserID, out.Username, out.Role, out.Managed = sess.UserID, sess.Username, string(sess.Role), sess.Managed
	out.Permissions = sess.Role.Permissions()
	out.CSRFToken, out.ExpiresAt = sess.CSRFToken, sess.ExpiresAt
	out.Server.Version, out.Server.DemoMode, out.Server.Time = s.d.Tool.Version, s.d.DemoMode, time.Now().UTC()
	return out
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if err := auth.CheckOrigin(r); err != nil {
		writeError(w, r, http.StatusForbidden, "csrf", err.Error())
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decode(w, r, &body, 4096); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	sess, err := s.d.Auth.Login(r.Context(), w, s.clientKey(r), body.Username, body.Password)
	switch {
	case errors.Is(err, auth.ErrInvalidCredentials):
		writeError(w, r, http.StatusUnauthorized, "invalid_credentials", err.Error())
		return
	case errors.Is(err, auth.ErrRateLimited):
		w.Header().Set("Retry-After", "60")
		writeError(w, r, http.StatusTooManyRequests, "rate_limited", err.Error())
		return
	case err != nil:
		s.internal(w, r, err)
		return
	}
	s.d.Log.Info("user logged in", "user", sess.Username, "role", sess.Role, "client", s.clientKey(r))
	writeJSON(w, http.StatusOK, s.sessionPayload(sess))
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if err := s.d.Auth.Logout(r.Context(), w, sessionOf(r)); err != nil {
		s.internal(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.sessionPayload(sessionOf(r)))
}
