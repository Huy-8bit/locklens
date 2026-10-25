// Package api serves LockLens' REST API, server-sent events, health and
// metrics endpoints, and the embedded web UI.
//
// All /api/v1 routes except login and session require an authenticated
// session; mutations also require the CSRF header. Responses never contain
// connection strings, passwords, or raw internal errors.
package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/locklens/locklens/internal/auth"
	"github.com/locklens/locklens/internal/collector"
	"github.com/locklens/locklens/internal/model"
	"github.com/locklens/locklens/internal/obs"
	"github.com/locklens/locklens/internal/settings"
	"github.com/locklens/locklens/internal/storage"
	"github.com/locklens/locklens/internal/targets"
	"github.com/locklens/locklens/internal/workspace"
)

// Limits on requests.
const (
	MaxBodyBytes   = 1 << 20
	MaxImportBytes = 10 << 20
	MaxRange       = 31 * 24 * time.Hour
	MaxPageSize    = 200
	maxPoints      = 400
)

// Deps are the server's collaborators.
type Deps struct {
	Workspace *workspace.Service
	Store     *storage.Store
	Auth      *auth.Service
	Targets   *targets.Service
	Manager   *collector.Manager
	Settings  *settings.Service
	Hub       *Hub
	Metrics   *obs.Metrics
	Log       *slog.Logger
	Tool      model.Tool
	// UI is the built web UI (nil when not embedded).
	UI fs.FS
	// MetricsEnabled exposes /metrics.
	MetricsEnabled bool
	// DemoMode shows the lab guide in the UI.
	DemoMode bool
}

// Server is the HTTP handler.
type Server struct {
	d   Deps
	mux *http.ServeMux
}

// New builds the server and its routes.
func New(d Deps) *Server {
	s := &Server{d: d, mux: http.NewServeMux()}
	s.routes()
	return s
}

type ctxKey int

const (
	keySession ctxKey = iota
	keyRequestID
)

func (s *Server) routes() {
	m := s.mux
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	m.HandleFunc("GET /readyz", s.ready)
	if s.d.MetricsEnabled {
		m.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
			_ = s.d.Metrics.Registry.WriteText(w)
		})
	}

	m.HandleFunc("POST /api/v1/auth/login", s.login)
	m.HandleFunc("POST /api/v1/auth/logout", s.authed(s.logout))
	m.HandleFunc("GET /api/v1/session", s.authed(s.session))

	m.HandleFunc("GET /api/v1/fleet", s.authed(s.fleet))
	m.HandleFunc("GET /api/v1/topology", s.authed(s.topology))
	m.HandleFunc("PUT /api/v1/topology", s.authed(s.putTopology))
	m.HandleFunc("DELETE /api/v1/topology/{id}", s.authed(s.deleteTopology))
	m.HandleFunc("GET /api/v1/targets/{id}/history-bounds", s.authed(s.historyBounds))
	m.HandleFunc("GET /api/v1/targets/{id}/workspace/policy", s.authed(s.workspacePolicy))
	m.HandleFunc("PUT /api/v1/targets/{id}/workspace/policy", s.authed(s.putWorkspacePolicy))
	m.HandleFunc("POST /api/v1/targets/{id}/workspace/run", s.authed(s.workspaceRun))
	m.HandleFunc("POST /api/v1/targets/{id}/workspace/cancel", s.authed(s.workspaceCancel))
	m.HandleFunc("GET /api/v1/targets", s.authed(s.listTargets))
	m.HandleFunc("POST /api/v1/targets", s.authed(s.createTarget))
	m.HandleFunc("POST /api/v1/targets/test", s.authed(s.testNewTarget))
	m.HandleFunc("GET /api/v1/targets/{id}", s.authed(s.getTarget))
	m.HandleFunc("PUT /api/v1/targets/{id}", s.authed(s.updateTarget))
	m.HandleFunc("DELETE /api/v1/targets/{id}", s.authed(s.deleteTarget))
	m.HandleFunc("POST /api/v1/targets/{id}/test", s.authed(s.testTarget))
	m.HandleFunc("GET /api/v1/targets/{id}/overview", s.authed(s.targetOverview))
	m.HandleFunc("GET /api/v1/targets/{id}/metrics", s.authed(s.targetMetrics))
	m.HandleFunc("GET /api/v1/targets/{id}/activity", s.authed(s.targetActivity))
	m.HandleFunc("GET /api/v1/targets/{id}/blocking/snapshots", s.authed(s.listSnapshots))
	m.HandleFunc("GET /api/v1/targets/{id}/blocking/snapshots/{sid}", s.authed(s.getSnapshot))
	m.HandleFunc("GET /api/v1/targets/{id}/queries", s.authed(s.listQueries))
	m.HandleFunc("GET /api/v1/targets/{id}/queries/commands", s.authed(s.queryCommands))
	m.HandleFunc("GET /api/v1/targets/{id}/queries/{qid}", s.authed(s.getQuery))
	m.HandleFunc("GET /api/v1/targets/{id}/tables", s.authed(s.targetTables))
	m.HandleFunc("GET /api/v1/targets/{id}/replication", s.authed(s.targetReplication))
	m.HandleFunc("GET /api/v1/targets/{id}/host", s.authed(s.targetHost))
	m.HandleFunc("GET /api/v1/targets/{id}/findings", s.authed(s.targetFindings))
	m.HandleFunc("GET /api/v1/targets/{id}/gaps", s.authed(s.targetGaps))

	m.HandleFunc("GET /api/v1/incidents", s.authed(s.listIncidents))
	m.HandleFunc("POST /api/v1/incidents/import", s.authed(s.importIncident))
	m.HandleFunc("GET /api/v1/incidents/{id}", s.authed(s.getIncident))
	m.HandleFunc("GET /api/v1/incidents/{id}/events", s.authed(s.incidentEvents))
	m.HandleFunc("POST /api/v1/incidents/{id}/acknowledge", s.authed(s.acknowledgeIncident))
	m.HandleFunc("GET /api/v1/incidents/{id}/export", s.authed(s.exportIncident))

	m.HandleFunc("POST /api/v1/snapshots/analyze", s.authed(s.analyzeSnapshot))
	m.HandleFunc("GET /api/v1/settings/rules", s.authed(s.getRuleSettings))
	m.HandleFunc("PUT /api/v1/settings/rules", s.authed(s.putRuleSettings))
	m.HandleFunc("GET /api/v1/storage", s.authed(s.storageStats))
	m.HandleFunc("GET /api/v1/events", s.authed(func(w http.ResponseWriter, r *http.Request) { s.d.Hub.ServeHTTP(w, r) }))

	m.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, http.StatusNotFound, "not_found", "unknown API route")
	})
	m.HandleFunc("/", s.ui)
}

// ServeHTTP applies middleware: request ID, panic recovery, security
// headers, access logging, and request metrics.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	id := r.Header.Get("X-Request-ID")
	if id == "" || len(id) > 64 || strings.ContainsAny(id, "\r\n") {
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		id = hex.EncodeToString(b)
	}
	w.Header().Set("X-Request-ID", id)
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
	r = r.WithContext(context.WithValue(r.Context(), keyRequestID, id))
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	defer func() {
		if p := recover(); p != nil {
			s.d.Log.Error("panic in handler", "request_id", id, "panic", p, "stack", string(debug.Stack()))
			writeError(rec, r, http.StatusInternalServerError, "internal", "internal error")
		}
		route := "ui"
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/"):
			route = "api"
		case r.URL.Path == "/metrics" || r.URL.Path == "/healthz" || r.URL.Path == "/readyz":
			route = "ops"
		}
		s.d.Metrics.HTTPRequests.Inc(route, strconv.Itoa(rec.status/100)+"xx")
		if route != "ops" && !strings.HasSuffix(r.URL.Path, "/events") {
			s.d.Log.Debug("request", "request_id", id, "method", r.Method, "path", r.URL.Path, "status", rec.status,
				"duration_ms", time.Since(start).Milliseconds())
		}
	}()
	s.mux.ServeHTTP(rec, r)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// authed requires a session and, for mutations, a valid CSRF token.
func (s *Server) authed(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, err := s.d.Auth.Authenticate(r)
		if err != nil {
			if !errors.Is(err, storage.ErrNotFound) {
				s.internal(w, r, err)
				return
			}
			writeError(w, r, http.StatusUnauthorized, "unauthenticated", "login required")
			return
		}
		if err := auth.CheckCSRF(r, sess); err != nil {
			writeError(w, r, http.StatusForbidden, "csrf", err.Error())
			return
		}
		h(w, r.WithContext(context.WithValue(r.Context(), keySession, sess)))
	}
}

func sessionOf(r *http.Request) auth.Session {
	s, _ := r.Context().Value(keySession).(auth.Session)
	return s
}

func requestID(r *http.Request) string {
	id, _ := r.Context().Value(keyRequestID).(string)
	return id
}

type apiError struct {
	Code      string            `json:"code"`
	Message   string            `json:"message"`
	Fields    map[string]string `json:"fields,omitempty"`
	RequestID string            `json:"request_id"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(true)
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, msg string) {
	writeJSON(w, status, map[string]apiError{"error": {Code: code, Message: msg, RequestID: requestID(r)}})
}

// internal logs an unexpected error and returns a generic message, so
// internal details (which could include data from the store) never reach
// clients.
func (s *Server) internal(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, context.Canceled) {
		return
	}
	s.d.Log.Error("request failed", "request_id", requestID(r), "path", r.URL.Path, "error", err.Error())
	writeError(w, r, http.StatusInternalServerError, "internal", "internal error (see server logs for request "+requestID(r)+")")
}

// decode reads a JSON body with a size limit and rejects unknown fields.
func decode(w http.ResponseWriter, r *http.Request, dst any, limit int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return errors.New("request body too large")
		}
		return errors.New("invalid JSON body: " + err.Error())
	}
	return nil
}

func pathID(r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	return id, err == nil && id > 0
}

// timeRange parses from/to (RFC 3339) or range (Go duration ending now),
// defaulting to the last hour, bounded by MaxRange.
func timeRange(r *http.Request, def time.Duration) (time.Time, time.Time, error) {
	q := r.URL.Query()
	to := time.Now().UTC()
	if v := q.Get("to"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return time.Time{}, time.Time{}, errors.New("to must be an RFC 3339 timestamp")
		}
		to = t.UTC()
	}
	from := to.Add(-def)
	if v := q.Get("range"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return time.Time{}, time.Time{}, errors.New("range must be a positive duration such as 1h")
		}
		from = to.Add(-d)
	}
	if v := q.Get("from"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return time.Time{}, time.Time{}, errors.New("from must be an RFC 3339 timestamp")
		}
		from = t.UTC()
	}
	if !from.Before(to) {
		return time.Time{}, time.Time{}, errors.New("from must be before to")
	}
	if to.Sub(from) > MaxRange {
		return time.Time{}, time.Time{}, errors.New("time range is limited to 31 days")
	}
	return from, to, nil
}

// niceSteps are bucket sizes for downsampling.
var niceSteps = []time.Duration{10 * time.Second, 30 * time.Second, time.Minute, 5 * time.Minute, 15 * time.Minute,
	time.Hour, 3 * time.Hour, 6 * time.Hour, 24 * time.Hour}

// stepFor returns the smallest nice step giving at most maxPoints buckets.
func stepFor(from, to time.Time, minStep time.Duration) time.Duration {
	span := to.Sub(from)
	for _, s := range niceSteps {
		if s >= minStep && span/s <= maxPoints {
			return s
		}
	}
	return niceSteps[len(niceSteps)-1]
}

func page(r *http.Request) (limit, offset int, err error) {
	q := r.URL.Query()
	limit, offset = 50, 0
	if v := q.Get("page_size"); v != "" {
		n, e := strconv.Atoi(v)
		if e != nil || n < 1 || n > MaxPageSize {
			return 0, 0, errors.New("page_size must be between 1 and 200")
		}
		limit = n
	}
	if v := q.Get("page"); v != "" {
		n, e := strconv.Atoi(v)
		if e != nil || n < 1 || n > 100000 {
			return 0, 0, errors.New("page must be a positive integer")
		}
		offset = (n - 1) * limit
	}
	return limit, offset, nil
}

func clientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := s.d.Store.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable", "reason": "monitoring store unreachable"})
		return
	}
	v, err := s.d.Store.SchemaVersion(ctx)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable", "reason": "schema version unreadable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ready", "schema_version": v})
}

// ui serves the embedded single-page app, falling back to index.html for
// client-side routes.
func (s *Server) ui(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.d.UI == nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("The LockLens web UI is not built into this binary. Build it with `make web` and rebuild, or use the API.\n"))
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path != "" && path != "index.html" {
		if f, err := s.d.UI.Open(path); err == nil {
			st, _ := f.Stat()
			f.Close()
			if st != nil && !st.IsDir() {
				if strings.HasPrefix(path, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				http.ServeFileFS(w, r, s.d.UI, path)
				return
			}
		}
		if strings.HasPrefix(path, "assets/") {
			http.NotFound(w, r)
			return
		}
	}
	html, err := fs.ReadFile(s.d.UI, "index.html")
	if err != nil {
		http.Error(w, "UI unavailable", 503)
		return
	}
	nonceBytes := make([]byte, 24)
	if _, err = rand.Read(nonceBytes); err != nil {
		http.Error(w, "UI unavailable", 503)
		return
	}
	nonce := hex.EncodeToString(nonceBytes)
	html = bytes.Replace(html, []byte("<head>"), []byte(`<head><meta name="csp-nonce" content="`+nonce+`">`), 1)
	w.Header().Set("Content-Security-Policy", strings.Replace(w.Header().Get("Content-Security-Policy"), "style-src 'self'", "style-src 'self' 'nonce-"+nonce+"'", 1))
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(html))
}

// metricStep accepts an explicit minimum resolution and a per-series point cap.
func metricStep(r *http.Request, from, to time.Time, interval time.Duration) (time.Duration, error) {
	points := maxPoints
	if raw := r.URL.Query().Get("max_points"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 2 || n > 1000 {
			return 0, errors.New("max_points must be between 2 and 1000")
		}
		points = n
	}
	step := max(interval, time.Second)
	if raw := r.URL.Query().Get("step"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d < time.Second || d > MaxRange {
			return 0, errors.New("step must be a duration between 1s and 744h")
		}
		step = max(step, d)
	}
	required := (to.Sub(from) + time.Duration(points) - 1) / time.Duration(points)
	return max(step, ((required+time.Second-1)/time.Second)*time.Second), nil
}
