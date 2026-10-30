package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/locklens/locklens/internal/domain/auth"
	"github.com/locklens/locklens/internal/domain/changes"
	"github.com/locklens/locklens/internal/domain/tokens"
	"github.com/locklens/locklens/internal/repository"
)

// listChanges returns the changes of a period: GET /api/v1/changes with
// from/to/range, target_id (repeatable or comma-separated), and kind.
func (s *Server) listChanges(w http.ResponseWriter, r *http.Request) {
	from, to, err := timeRange(r, 24*time.Hour)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	f := repository.ChangeFilter{From: from, To: to}
	q := r.URL.Query()
	for _, v := range q["target_id"] {
		for _, p := range strings.Split(v, ",") {
			id, err := strconv.ParseInt(strings.TrimSpace(p), 10, 64)
			if err != nil || id <= 0 {
				writeError(w, r, http.StatusBadRequest, "invalid_request", "target_id must be a positive integer")
				return
			}
			f.TargetIDs = append(f.TargetIDs, id)
		}
	}
	for _, v := range q["kind"] {
		f.Kinds = append(f.Kinds, strings.Split(v, ",")...)
	}
	if v := q.Get("limit"); v != "" {
		if f.Limit, err = strconv.Atoi(v); err != nil || f.Limit < 1 || f.Limit > 1000 {
			writeError(w, r, http.StatusBadRequest, "invalid_request", "limit must be 1 to 1000")
			return
		}
	}
	out, err := s.d.Changes.List(r.Context(), f)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"from": from, "to": to, "changes": out})
}

// reportChange records a deploy, migration, or other change. A person
// reports with a session (and the CSRF header); a pipeline with an API token
// of scope "events" in the Authorization header (Bearer). A token limited to
// one instance reports changes of that instance only.
func (s *Server) reportChange(w http.ResponseWriter, r *http.Request) {
	var in changes.Report
	if err := decode(w, r, &in, 32<<10); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	now := time.Now().UTC()
	if errs := in.Validate(now); len(errs) > 0 {
		writeFieldErrors(w, r, errs)
		return
	}
	source, by := changes.SourceUser, sessionOf(r).Username
	if t, ok := tokenOf(r); ok {
		source, by = changes.SourceAPI, "token:"+t.Name
		if t.TargetID != nil {
			if in.TargetID != nil && *in.TargetID != *t.TargetID {
				writeError(w, r, http.StatusForbidden, "forbidden", "this API token reports changes of another instance only")
				return
			}
			in.TargetID = t.TargetID
		}
	}
	if in.TargetID != nil {
		if _, err := s.d.Targets.Get(r.Context(), *in.TargetID); err != nil {
			writeFieldErrors(w, r, map[string]string{"target_id": "unknown instance"})
			return
		}
	}
	e, err := s.d.Changes.Record(r.Context(), in, source, by, now)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if s.d.Events != nil {
		s.d.Events.Publish("change.created", map[string]any{"id": e.ID, "target_id": e.TargetID})
	}
	writeJSON(w, http.StatusCreated, e)
}

func (s *Server) deleteChange(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "invalid change id")
		return
	}
	if _, err := s.d.Changes.Delete(r.Context(), id); errors.Is(err, repository.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "not_found", "no reported change with this id (detected changes cannot be deleted)")
		return
	} else if err != nil {
		s.internal(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listTokens(w http.ResponseWriter, r *http.Request) {
	out, err := s.d.Tokens.List(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": out})
}

// createToken issues a token; its secret is in this response only.
func (s *Server) createToken(w http.ResponseWriter, r *http.Request) {
	var in tokens.Input
	if err := decode(w, r, &in, 4096); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if errs := in.Validate(); len(errs) > 0 {
		writeFieldErrors(w, r, errs)
		return
	}
	if in.TargetID != nil {
		if _, err := s.d.Targets.Get(r.Context(), *in.TargetID); err != nil {
			writeFieldErrors(w, r, map[string]string{"target_id": "unknown instance"})
			return
		}
	}
	t, secret, err := s.d.Tokens.Create(r.Context(), in, sessionOf(r).Username)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"token": t, "secret": secret,
		"note": "Copy the secret now: LockLens keeps only a hash of it and cannot show it again."})
}

func (s *Server) revokeToken(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "invalid token id")
		return
	}
	if err := s.d.Tokens.Revoke(r.Context(), id); errors.Is(err, repository.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "not_found", "no live token with this id")
		return
	} else if err != nil {
		s.internal(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// bearer returns the token in the Authorization header.
func bearer(r *http.Request) (string, bool) {
	v, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return strings.TrimSpace(v), ok && strings.TrimSpace(v) != ""
}

// tokenOr authenticates a request by an API token of a scope when it
// carries one (Authorization: Bearer), and otherwise as a person with a
// permission. Token requests carry no cookies, so they need no CSRF header.
func (s *Server) tokenOr(scope string, p auth.Permission, h http.HandlerFunc) http.HandlerFunc {
	session := s.allow(p, h)
	return func(w http.ResponseWriter, r *http.Request) {
		secret, ok := bearer(r)
		if !ok {
			session(w, r)
			return
		}
		t, err := s.d.Tokens.Authenticate(r.Context(), secret, scope, 0)
		switch {
		case errors.Is(err, tokens.ErrInvalid):
			writeError(w, r, http.StatusUnauthorized, "unauthenticated", err.Error())
			return
		case errors.Is(err, tokens.ErrScope):
			writeError(w, r, http.StatusForbidden, "forbidden", err.Error())
			return
		case err != nil:
			s.internal(w, r, err)
			return
		}
		h(w, r.WithContext(context.WithValue(r.Context(), keyToken, t)))
	}
}

func tokenOf(r *http.Request) (repository.APIToken, bool) {
	t, ok := r.Context().Value(keyToken).(repository.APIToken)
	return t, ok
}
