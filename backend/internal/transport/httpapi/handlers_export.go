package httpapi

import (
	"errors"
	"net/http"

	"github.com/locklens/locklens/internal/domain/telemetry"
	"github.com/locklens/locklens/internal/domain/tokens"
	"github.com/locklens/locklens/internal/repository"
)

// metricsToken checks the API token of scope "metrics" a scrape carries;
// false when the request was answered.
func (s *Server) metricsToken(w http.ResponseWriter, r *http.Request) (repository.APIToken, bool) {
	secret, ok := bearer(r)
	if !ok {
		w.Header().Set("WWW-Authenticate", `Bearer realm="locklens"`)
		writeError(w, r, http.StatusUnauthorized, "unauthenticated", "an API token of scope metrics is required (Authorization: Bearer)")
		return repository.APIToken{}, false
	}
	t, err := s.d.Tokens.Authenticate(r.Context(), secret, repository.TokenMetrics, 0)
	switch {
	case errors.Is(err, tokens.ErrInvalid):
		writeError(w, r, http.StatusUnauthorized, "unauthenticated", err.Error())
		return t, false
	case errors.Is(err, tokens.ErrScope):
		writeError(w, r, http.StatusForbidden, "forbidden", err.Error())
		return t, false
	case err != nil:
		s.internal(w, r, err)
		return t, false
	}
	return t, true
}

// exportTargets serves the monitored instances' metrics in the Prometheus
// text format: those this server collects (each server of a cluster serves
// its own), or only the token's instance when the token is limited to one.
func (s *Server) exportTargets(w http.ResponseWriter, r *http.Request) {
	t, ok := s.metricsToken(w, r)
	if !ok {
		return
	}
	var only int64
	if t.TargetID != nil {
		only = *t.TargetID
	}
	fams, err := s.d.Telemetry.Collect(r.Context(), only)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	w.Header().Set("Content-Type", telemetry.ContentType)
	_ = telemetry.WritePrometheus(w, fams)
}

// selfMetrics serves LockLens' own metrics; with MetricsRequireToken a
// token of scope "metrics" is needed.
func (s *Server) selfMetrics(w http.ResponseWriter, r *http.Request) {
	if s.d.MetricsRequireToken {
		if _, ok := s.metricsToken(w, r); !ok {
			return
		}
	}
	w.Header().Set("Content-Type", telemetry.ContentType)
	_ = s.d.Metrics.Registry.WriteText(w)
}
