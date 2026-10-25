// Package api serves LockLens' REST API, server-sent events, health and
// metrics endpoints, and the embedded web UI.
//
// All /api/v1 routes except login and session require an authenticated
// session; mutations also require the CSRF header. Responses never contain
// connection strings, passwords, or raw internal errors.
package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/locklens/locklens/internal/auth"
	"github.com/locklens/locklens/internal/collector"
	"github.com/locklens/locklens/internal/model"
	"github.com/locklens/locklens/internal/obs"
	"github.com/locklens/locklens/internal/onboard"
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
	Onboard   *onboard.Service
	Store     *storage.Store
	Auth      *auth.Service
	Targets   *targets.Service
	Manager   *collector.Manager
	Settings  *settings.Service
	Hub       *Hub
	Metrics   *obs.Metrics
	Log       *slog.Logger
	Tool      model.Tool
	// TrustedProxies are reverse proxies whose X-Forwarded-For is believed.
	TrustedProxies []netip.Prefix
	// TrustedProxyHosts are proxy host names, resolved every 30 seconds.
	TrustedProxyHosts []string
	// LookupHost resolves TrustedProxyHosts (net.DefaultResolver when nil).
	LookupHost func(ctx context.Context, host string) ([]string, error)
	// MetricsEnabled exposes /metrics.
	MetricsEnabled bool
	// DemoMode shows the lab guide in the UI.
	DemoMode bool
}

// Server is the HTTP handler.
type Server struct {
	d   Deps
	mux *http.ServeMux

	proxyMu       sync.Mutex
	proxyAddrs    map[netip.Addr]bool
	proxyResolved time.Time
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
	m.HandleFunc("PUT /api/v1/session/password", s.authed(s.changeOwnPassword))

	// Users (admin only).
	m.HandleFunc("GET /api/v1/users", s.allow(auth.PermManageUsers, s.listUsers))
	m.HandleFunc("POST /api/v1/users", s.allow(auth.PermManageUsers, s.createUser))
	m.HandleFunc("PUT /api/v1/users/{id}", s.allow(auth.PermManageUsers, s.updateUser))
	m.HandleFunc("DELETE /api/v1/users/{id}", s.allow(auth.PermManageUsers, s.deleteUser))

	// Reads need any role (every role can view); changes name their permission.

	m.HandleFunc("GET /api/v1/fleet", s.authed(s.fleet))
	m.HandleFunc("GET /api/v1/fleet/metrics", s.authed(s.fleetMetrics))
	m.HandleFunc("GET /api/v1/fleet/queries", s.authed(s.fleetQueries))
	m.HandleFunc("GET /api/v1/fleet/live", s.authed(s.fleetLive))
	m.HandleFunc("GET /api/v1/system", s.authed(s.system))
	m.HandleFunc("GET /api/v1/topology", s.authed(s.topology))
	m.HandleFunc("PUT /api/v1/topology", s.allow(auth.PermManageSettings, s.putTopology))
	m.HandleFunc("DELETE /api/v1/topology/{id}", s.allow(auth.PermManageSettings, s.deleteTopology))
	m.HandleFunc("GET /api/v1/targets/{id}/history-bounds", s.authed(s.historyBounds))
	m.HandleFunc("GET /api/v1/targets/{id}/workspace/policy", s.authed(s.workspacePolicy))
	m.HandleFunc("PUT /api/v1/targets/{id}/workspace/policy", s.allow(auth.PermManageTargets, s.putWorkspacePolicy))
	m.HandleFunc("POST /api/v1/targets/{id}/workspace/run", s.authed(s.workspaceRun))
	m.HandleFunc("POST /api/v1/targets/{id}/workspace/cancel", s.authed(s.workspaceCancel))
	m.HandleFunc("GET /api/v1/targets", s.authed(s.listTargets))
	m.HandleFunc("POST /api/v1/targets", s.allow(auth.PermManageTargets, s.createTarget))
	m.HandleFunc("POST /api/v1/targets/test", s.allow(auth.PermManageTargets, s.testNewTarget))
	m.HandleFunc("POST /api/v1/targets/inspect", s.allow(auth.PermManageTargets, s.inspectConnection))
	m.HandleFunc("POST /api/v1/targets/quick", s.allow(auth.PermManageTargets, s.quickAddTarget))
	m.HandleFunc("GET /api/v1/targets/{id}", s.authed(s.getTarget))
	m.HandleFunc("PUT /api/v1/targets/{id}", s.allow(auth.PermManageTargets, s.updateTarget))
	m.HandleFunc("DELETE /api/v1/targets/{id}", s.allow(auth.PermManageTargets, s.deleteTarget))
	m.HandleFunc("POST /api/v1/targets/{id}/test", s.allow(auth.PermManageTargets, s.testTarget))
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
	m.HandleFunc("POST /api/v1/incidents/import", s.allow(auth.PermManageSettings, s.importIncident))
	m.HandleFunc("GET /api/v1/incidents/{id}", s.authed(s.getIncident))
	m.HandleFunc("GET /api/v1/incidents/{id}/events", s.authed(s.incidentEvents))
	m.HandleFunc("POST /api/v1/incidents/{id}/acknowledge", s.allow(auth.PermAcknowledge, s.acknowledgeIncident))
	m.HandleFunc("GET /api/v1/incidents/{id}/export", s.authed(s.exportIncident))

	m.HandleFunc("POST /api/v1/snapshots/analyze", s.authed(s.analyzeSnapshot))
	m.HandleFunc("GET /api/v1/settings/rules", s.authed(s.getRuleSettings))
	m.HandleFunc("PUT /api/v1/settings/rules", s.allow(auth.PermManageSettings, s.putRuleSettings))
	m.HandleFunc("GET /api/v1/storage", s.authed(s.storageStats))
	m.HandleFunc("GET /api/v1/events", s.authed(func(w http.ResponseWriter, r *http.Request) { s.d.Hub.ServeHTTP(w, r) }))

	m.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, http.StatusNotFound, "not_found", "unknown API route")
	})
	m.HandleFunc("/", s.notAPI)
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

// allow is authed plus a permission check. Missing permission answers 403.
func (s *Server) allow(p auth.Permission, h http.HandlerFunc) http.HandlerFunc {
	return s.authed(func(w http.ResponseWriter, r *http.Request) {
		if !sessionOf(r).Can(p) {
			writeError(w, r, http.StatusForbidden, "forbidden", "your role does not allow this action")
			return
		}
		h(w, r)
	})
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

// writeFieldErrors answers 422 with one message per invalid field.
func writeFieldErrors(w http.ResponseWriter, r *http.Request, fields map[string]string) {
	writeJSON(w, http.StatusUnprocessableEntity, map[string]apiError{"error": {Code: "validation", Message: "invalid input", Fields: fields, RequestID: requestID(r)}})
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

// clientKey identifies the client for login rate limiting and logs. Behind
// a trusted reverse proxy it is the right-most X-Forwarded-For address that
// is not itself a trusted proxy; anything a client writes into the header
// further left is ignored.
func (s *Server) clientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil || !s.trusted(addr) {
		return host
	}
	hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		hop, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			break
		}
		if !s.trusted(hop) {
			return hop.Unmap().String()
		}
	}
	return host
}

func (s *Server) trusted(a netip.Addr) bool {
	a = a.Unmap()
	for _, p := range s.d.TrustedProxies {
		if p.Contains(a) {
			return true
		}
	}
	if len(s.d.TrustedProxyHosts) == 0 {
		return false
	}
	return s.proxyHostAddrs()[a]
}

// proxyHostAddrs resolves the trusted proxy host names at most every 30
// seconds. A failed lookup keeps the previous addresses.
func (s *Server) proxyHostAddrs() map[netip.Addr]bool {
	s.proxyMu.Lock()
	defer s.proxyMu.Unlock()
	if s.proxyAddrs != nil && time.Since(s.proxyResolved) < 30*time.Second {
		return s.proxyAddrs
	}
	lookup := s.d.LookupHost
	if lookup == nil {
		lookup = net.DefaultResolver.LookupHost
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	addrs := map[netip.Addr]bool{}
	ok := false
	for _, h := range s.d.TrustedProxyHosts {
		list, err := lookup(ctx, h)
		if err != nil {
			continue
		}
		ok = true
		for _, a := range list {
			if ip, err := netip.ParseAddr(a); err == nil {
				addrs[ip.Unmap()] = true
			}
		}
	}
	if ok || s.proxyAddrs == nil {
		s.proxyAddrs = addrs
	}
	s.proxyResolved = time.Now()
	return s.proxyAddrs
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
// notAPI answers every path outside /api/ and the operational endpoints. The
// web UI is a separate image (the frontend) that proxies /api/ here.
func (s *Server) notAPI(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, http.StatusNotFound, "not_found", "this is the LockLens backend: the API is under /api/; the web UI is served by the frontend")
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
