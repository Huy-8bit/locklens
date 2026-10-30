package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/locklens/locklens/internal/domain/logs"
	"github.com/locklens/locklens/internal/domain/tokens"
	"github.com/locklens/locklens/internal/repository"
)

// maxIngestBytes bounds one batch from a log agent.
const maxIngestBytes = 4 << 20

// ingestLogs stores a batch of log events from a log agent. It accepts only
// an API token of scope "ingest", which belongs to one instance.
func (s *Server) ingestLogs(w http.ResponseWriter, r *http.Request) {
	secret, ok := bearer(r)
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "unauthenticated", "an API token is required (Authorization: Bearer)")
		return
	}
	t, err := s.d.Tokens.Authenticate(r.Context(), secret, repository.TokenIngest, 0)
	switch {
	case errors.Is(err, tokens.ErrInvalid):
		writeError(w, r, http.StatusUnauthorized, "unauthenticated", err.Error())
		return
	case errors.Is(err, tokens.ErrScope) || err == nil && t.TargetID == nil:
		writeError(w, r, http.StatusForbidden, "forbidden", "the API token does not allow shipping logs")
		return
	case err != nil:
		s.internal(w, r, err)
		return
	}
	var b logs.Batch
	if err := decode(w, r, &b, maxIngestBytes); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := b.Validate(time.Now().UTC()); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	n, err := s.d.Logs.Ingest(r.Context(), *t.TargetID, t.ID, b)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"received": len(b.Events), "stored": n})
}

// targetLogs summarises a target's log events in a period.
func (s *Server) targetLogs(w http.ResponseWriter, r *http.Request) {
	t, ok := s.targetOr404(w, r)
	if !ok {
		return
	}
	from, to, err := timeRange(r, 24*time.Hour)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	sum, err := s.d.Logs.Summarize(r.Context(), t.ID, from, to, stepFor(from, to, time.Minute))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sum)
}

// targetLogEvents lists a target's log events: kind (repeatable or
// comma-separated), fingerprint, limit.
func (s *Server) targetLogEvents(w http.ResponseWriter, r *http.Request) {
	t, ok := s.targetOr404(w, r)
	if !ok {
		return
	}
	from, to, err := timeRange(r, 24*time.Hour)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	q := r.URL.Query()
	f := repository.LogEventFilter{TargetID: t.ID, From: from, To: to, Fingerprint: q.Get("fingerprint"), Limit: 200}
	for _, v := range q["kind"] {
		f.Kinds = append(f.Kinds, strings.Split(v, ",")...)
	}
	if v := q.Get("limit"); v != "" {
		if f.Limit, err = strconv.Atoi(v); err != nil || f.Limit < 1 || f.Limit > 1000 {
			writeError(w, r, http.StatusBadRequest, "invalid_request", "limit must be 1 to 1000")
			return
		}
	}
	out, err := s.d.Logs.Events(r.Context(), f)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"from": from, "to": to, "events": out})
}
