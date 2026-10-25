package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/locklens/locklens/internal/auth"
	"github.com/locklens/locklens/internal/obs"
	"github.com/locklens/locklens/internal/storage"
)

func req(target string) *http.Request { return httptest.NewRequest(http.MethodGet, target, nil) }

func TestTimeRange(t *testing.T) {
	from, to, err := timeRange(req("/x"), time.Hour)
	if err != nil || to.Sub(from) != time.Hour {
		t.Fatalf("default: %v %v %v", from, to, err)
	}
	from, to, err = timeRange(req("/x?range=15m"), time.Hour)
	if err != nil || to.Sub(from) != 15*time.Minute {
		t.Fatalf("range=15m: %v", err)
	}
	from, to, err = timeRange(req("/x?from=2026-10-01T00:00:00Z&to=2026-10-02T00:00:00Z"), time.Hour)
	if err != nil || to.Sub(from) != 24*time.Hour || from.Location() != time.UTC {
		t.Fatalf("from/to: %v %v %v", from, to, err)
	}
	for _, q := range []string{
		"range=900h",                                     // longer than 31 days
		"range=-1h", "range=0s", "range=7d", "range=abc", // not a positive Go duration
		"from=yesterday",
		"to=2026-10-01",
		"from=2026-10-02T00:00:00Z&to=2026-10-01T00:00:00Z", // inverted
	} {
		if _, _, err := timeRange(req("/x?"+q), time.Hour); err == nil {
			t.Errorf("%s: want an error", q)
		}
	}
}

func TestPage(t *testing.T) {
	limit, offset, err := page(req("/x"))
	if err != nil || limit != 50 || offset != 0 {
		t.Fatalf("default: %d %d %v", limit, offset, err)
	}
	limit, offset, err = page(req("/x?page=3&page_size=20"))
	if err != nil || limit != 20 || offset != 40 {
		t.Fatalf("page 3: %d %d %v", limit, offset, err)
	}
	for _, q := range []string{"page_size=0", "page_size=201", "page_size=x", "page=0", "page=-1", "page=100001"} {
		if _, _, err := page(req("/x?" + q)); err == nil {
			t.Errorf("%s: want an error", q)
		}
	}
}

func TestStepForBoundsPoints(t *testing.T) {
	now := time.Now()
	for _, span := range []time.Duration{time.Minute, time.Hour, 24 * time.Hour, 7 * 24 * time.Hour, MaxRange} {
		step := stepFor(now.Add(-span), now, 10*time.Second)
		if step < 10*time.Second {
			t.Errorf("%s: step %s below the minimum", span, step)
		}
		if span/step > maxPoints {
			t.Errorf("%s: %d buckets exceed %d", span, span/step, maxPoints)
		}
	}
}

func TestDecodeRejectsUnknownFieldsAndLargeBodies(t *testing.T) {
	var dst struct {
		Name string `json:"name"`
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"name":"a","extra":1}`))
	if err := decode(w, r, &dst, MaxBodyBytes); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Errorf("unknown field: %v", err)
	}
	r = httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"name":"`+strings.Repeat("a", 2048)+`"}`))
	if err := decode(w, r, &dst, 1024); err == nil || err.Error() != "request body too large" {
		t.Errorf("large body: %v", err)
	}
	r = httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"name":"ok"}`))
	if err := decode(w, r, &dst, MaxBodyBytes); err != nil || dst.Name != "ok" {
		t.Errorf("valid body: %v %+v", err, dst)
	}
}

func TestPathID(t *testing.T) {
	for v, want := range map[string]bool{"1": true, "42": true, "0": false, "-3": false, "abc": false, "1e3": false, "": false} {
		r := req("/x")
		r.SetPathValue("id", v)
		if _, ok := pathID(r, "id"); ok != want {
			t.Errorf("pathID(%q) = %v, want %v", v, ok, want)
		}
	}
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	// No store: only routes that do not reach it are exercised here.
	return New(Deps{Auth: auth.NewService(nil, time.Hour, false), Metrics: obs.NewMetrics(), Log: log, MetricsEnabled: true})
}

func TestMiddlewareHeadersAndErrors(t *testing.T) {
	s := newTestServer(t)
	do := func(method, target string, hdr map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, target, nil)
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}

	w := do(http.MethodGet, "/healthz", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"ok"`) {
		t.Fatalf("healthz: %d %s", w.Code, w.Body)
	}
	for _, h := range []string{"Content-Security-Policy", "X-Content-Type-Options", "X-Frame-Options", "Referrer-Policy", "X-Request-ID"} {
		if w.Header().Get(h) == "" {
			t.Errorf("missing %s", h)
		}
	}
	if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") || strings.Contains(csp, "unsafe") {
		t.Errorf("CSP: %s", csp)
	}

	// A sane client request ID is echoed; a malformed one is replaced.
	if got := do(http.MethodGet, "/healthz", map[string]string{"X-Request-ID": "abc-123"}).Header().Get("X-Request-ID"); got != "abc-123" {
		t.Errorf("request id not echoed: %q", got)
	}
	if got := do(http.MethodGet, "/healthz", map[string]string{"X-Request-ID": strings.Repeat("x", 100)}).Header().Get("X-Request-ID"); len(got) != 16 {
		t.Errorf("overlong request id must be replaced, got %q", got)
	}

	// Unauthenticated API access.
	w = do(http.MethodGet, "/api/v1/targets", nil)
	var body struct {
		Error apiError `json:"error"`
	}
	if w.Code != 401 || json.Unmarshal(w.Body.Bytes(), &body) != nil || body.Error.Code != "unauthenticated" || body.Error.RequestID == "" {
		t.Errorf("unauthenticated: %d %s", w.Code, w.Body)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Error("API responses must not be cached")
	}

	if w := do(http.MethodGet, "/api/v1/nope", nil); w.Code != 404 || !strings.Contains(w.Body.String(), "unknown API route") {
		t.Errorf("unknown route: %d %s", w.Code, w.Body)
	}
	if w := do(http.MethodPost, "/", nil); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /: %d", w.Code)
	}
	// Without an embedded UI the root explains how to build it.
	if w := do(http.MethodGet, "/", nil); w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "make web") {
		t.Errorf("no UI: %d %s", w.Code, w.Body)
	}

	w = do(http.MethodGet, "/metrics", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "locklens_http_requests_total") {
		t.Errorf("metrics: %d", w.Code)
	}
}

func TestMetricStepBounds(t *testing.T) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(24*time.Hour + time.Second)
	for _, q := range []string{"", "?max_points=2", "?max_points=1000&step=1s", "?step=1h"} {
		r := httptest.NewRequest("GET", "/metrics"+q, nil)
		step, err := metricStep(r, from, to, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		limit := maxPoints
		if r.URL.Query().Get("max_points") == "2" {
			limit = 2
		}
		if r.URL.Query().Get("max_points") == "1000" {
			limit = 1000
		}
		if (to.Sub(from)+step-1)/step > time.Duration(limit) || step < 5*time.Second {
			t.Fatalf("unbounded resolution: %s %s", q, step)
		}
	}
	for _, q := range []string{"?max_points=1", "?max_points=1001", "?step=-1s", "?step=1ns", "?step=745h", "?step=invalid"} {
		if _, err := metricStep(httptest.NewRequest("GET", "/metrics"+q, nil), from, to, time.Second); err == nil {
			t.Errorf("accepted %s", q)
		}
	}
}

func TestFleetAggregation(t *testing.T) {
	for metric, want := range map[string]string{
		"statements.calls.per_s":           "sum",
		"connections.used":                 "sum",
		"db.total_size_bytes":              "sum",
		"cache.hit_ratio":                  "mean",
		"host.cpu_utilization_ratio":       "mean",
		"replication.max_replay_lag_bytes": "max",
		"standby.replay_delay_bytes":       "max",
		"transactions.oldest_age_seconds":  "max",
	} {
		if got := fleetAggregation(metric); got != want {
			t.Errorf("fleetAggregation(%s) = %s, want %s", metric, got, want)
		}
	}
}

func TestFleetRoutesRequireAuth(t *testing.T) {
	s := newTestServer(t)
	for _, p := range []string{"/api/v1/fleet/metrics?names=x", "/api/v1/fleet/queries", "/api/v1/fleet/live", "/api/v1/system"} {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, p, nil))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s: %d", p, w.Code)
		}
	}
}

// roleStore is an auth store where every session cookie belongs to a user
// with a fixed role.
type roleStore struct{ role string }

func (roleStore) UserByUsername(context.Context, string) (storage.User, error) {
	return storage.User{}, storage.ErrNotFound
}
func (roleStore) TouchLogin(context.Context, int64) error { return nil }
func (roleStore) CreateSession(context.Context, []byte, int64, string, time.Time) error {
	return nil
}
func (r roleStore) SessionByHash(context.Context, []byte, time.Duration) (storage.AuthSession, error) {
	return storage.AuthSession{UserID: 7, Username: "someone", Role: r.role, CSRFToken: "csrf", ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (roleStore) DeleteSession(context.Context, []byte) error { return nil }

func TestRoutePermissions(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	routes := []struct {
		method, path, body string
		need               auth.Permission
	}{
		{"GET", "/api/v1/users", "", auth.PermManageUsers},
		{"POST", "/api/v1/users", "{}", auth.PermManageUsers},
		{"PUT", "/api/v1/users/1", "{}", auth.PermManageUsers},
		{"DELETE", "/api/v1/users/1", "", auth.PermManageUsers},
		{"POST", "/api/v1/targets", "{}", auth.PermManageTargets},
		{"POST", "/api/v1/targets/test", "{}", auth.PermManageTargets},
		{"POST", "/api/v1/targets/inspect", "{}", auth.PermManageTargets},
		{"POST", "/api/v1/targets/quick", "{}", auth.PermManageTargets},
		{"PUT", "/api/v1/targets/1", "{}", auth.PermManageTargets},
		{"DELETE", "/api/v1/targets/1", "", auth.PermManageTargets},
		{"POST", "/api/v1/targets/1/test", "", auth.PermManageTargets},
		{"PUT", "/api/v1/targets/1/workspace/policy", "{}", auth.PermManageTargets},
		{"PUT", "/api/v1/settings/rules", "{}", auth.PermManageSettings},
		{"PUT", "/api/v1/topology", "{}", auth.PermManageSettings},
		{"DELETE", "/api/v1/topology/1", "", auth.PermManageSettings},
		{"POST", "/api/v1/incidents/import", "{}", auth.PermManageSettings},
		{"POST", "/api/v1/incidents/1/acknowledge", "{}", auth.PermAcknowledge},
		{"POST", "/api/v1/targets/1/workspace/run", `{"action":"data","request_id":"r-123456"}`, auth.PermBrowseData},
		{"POST", "/api/v1/targets/1/workspace/run", `{"action":"sql","request_id":"r-123456"}`, auth.PermRunSQL},
		{"POST", "/api/v1/targets/1/workspace/run", `{"action":"explain","request_id":"r-123456"}`, auth.PermRunSQL},
	}
	for _, role := range auth.Roles {
		s := New(Deps{Auth: auth.NewService(roleStore{string(role)}, time.Hour, false), Metrics: obs.NewMetrics(), Log: log})
		for _, rt := range routes {
			r := httptest.NewRequest(rt.method, rt.path, strings.NewReader(rt.body))
			r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: "token"})
			r.Header.Set(auth.CSRFHeader, "csrf")
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			forbidden := w.Code == http.StatusForbidden && strings.Contains(w.Body.String(), "your role does not allow")
			if want := !role.Can(rt.need); forbidden != want {
				t.Errorf("%s %s %s as %s: status %d, forbidden=%v want %v", rt.method, rt.path, rt.body, role, w.Code, forbidden, want)
			}
		}
	}
}
