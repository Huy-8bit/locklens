package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/locklens/locklens/internal/domain/auth"
	"github.com/locklens/locklens/internal/domain/changes"
	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/domain/targets"
	"github.com/locklens/locklens/internal/domain/tokens"
	"github.com/locklens/locklens/internal/lib/obs"
	"github.com/locklens/locklens/internal/repository"
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
	// The backend serves no UI; other paths point to the frontend.
	for _, m := range []string{http.MethodGet, http.MethodPost} {
		if w := do(m, "/", nil); w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "frontend") {
			t.Errorf("%s /: %d %s", m, w.Code, w.Body)
		}
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

func (roleStore) UserByUsername(context.Context, string) (repository.User, error) {
	return repository.User{}, repository.ErrNotFound
}
func (roleStore) TouchLogin(context.Context, int64) error { return nil }
func (roleStore) CreateSession(context.Context, []byte, int64, string, time.Time) error {
	return nil
}
func (r roleStore) SessionByHash(context.Context, []byte, time.Duration) (repository.AuthSession, error) {
	return repository.AuthSession{UserID: 7, Username: "someone", Role: r.role, CSRFToken: "csrf", ExpiresAt: time.Now().Add(time.Hour)}, nil
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
		{"POST", "/api/v1/changes", "{}", auth.PermAcknowledge},
		{"DELETE", "/api/v1/changes/1", "", auth.PermManageSettings},
		{"GET", "/api/v1/tokens", "", auth.PermManageSettings},
		{"POST", "/api/v1/tokens", "{}", auth.PermManageSettings},
		{"DELETE", "/api/v1/tokens/1", "", auth.PermManageSettings},
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

func TestClientKeyBehindTrustedProxy(t *testing.T) {
	proxies := []netip.Prefix{netip.MustParsePrefix("172.16.0.0/12"), netip.MustParsePrefix("127.0.0.1/32")}
	s := New(Deps{Auth: auth.NewService(nil, time.Hour, false), Metrics: obs.NewMetrics(), Log: slog.New(slog.NewTextHandler(io.Discard, nil)), TrustedProxies: proxies})
	direct := New(Deps{Auth: auth.NewService(nil, time.Hour, false), Metrics: obs.NewMetrics(), Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	cases := []struct {
		srv        *Server
		remote, xf string
		want       string
	}{
		// No trusted proxy: the header is ignored.
		{direct, "172.18.0.5:4000", "203.0.113.9", "172.18.0.5"},
		// From the frontend: the client it saw.
		{s, "172.18.0.5:4000", "203.0.113.9", "203.0.113.9"},
		// A spoofed left-most value is ignored; the right-most untrusted hop counts.
		{s, "172.18.0.5:4000", "1.2.3.4, 203.0.113.9", "203.0.113.9"},
		// Chained trusted proxies are skipped.
		{s, "172.18.0.5:4000", "198.51.100.7, 127.0.0.1", "198.51.100.7"},
		// A client that is not a trusted proxy cannot pick its key.
		{s, "198.51.100.20:5000", "10.9.9.9", "198.51.100.20"},
		// Garbage stops the walk at the proxy itself.
		{s, "172.18.0.5:4000", "not-an-ip", "172.18.0.5"},
	}
	// A trusted host name: only the addresses it resolves to are proxies.
	byName := New(Deps{Auth: auth.NewService(nil, time.Hour, false), Metrics: obs.NewMetrics(), Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		TrustedProxyHosts: []string{"frontend"},
		LookupHost: func(_ context.Context, h string) ([]string, error) {
			if h == "frontend" {
				return []string{"172.18.0.7"}, nil
			}
			return nil, errors.New("unknown host")
		}})
	cases = append(cases, cases[0], cases[0])
	cases[len(cases)-2].srv, cases[len(cases)-2].remote, cases[len(cases)-2].xf, cases[len(cases)-2].want = byName, "172.18.0.7:4000", "192.168.1.20", "192.168.1.20"
	// A client on a private network is not a proxy and cannot spoof its key.
	cases[len(cases)-1].srv, cases[len(cases)-1].remote, cases[len(cases)-1].xf, cases[len(cases)-1].want = byName, "192.168.1.20:4000", "10.0.0.1", "192.168.1.20"
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = c.remote
		r.Header.Set("X-Forwarded-For", c.xf)
		if got := c.srv.clientKey(r); got != c.want {
			t.Errorf("remote %s xff %q: got %s, want %s", c.remote, c.xf, got, c.want)
		}
	}
}

// fakeTokens accepts one secret with a fixed token.
type fakeTokens struct {
	TokenService
	secret string
	token  repository.APIToken
}

func (f fakeTokens) Authenticate(_ context.Context, secret, scope string, _ int64) (repository.APIToken, error) {
	if secret != f.secret {
		return repository.APIToken{}, tokens.ErrInvalid
	}
	if scope != f.token.Scope {
		return repository.APIToken{}, tokens.ErrScope
	}
	return f.token, nil
}

// fakeChanges records reported changes.
type fakeChanges struct {
	ChangeService
	got *[]observe.ChangeEvent
}

func (f fakeChanges) Record(_ context.Context, r changes.Report, source, by string, now time.Time) (observe.ChangeEvent, error) {
	e := observe.ChangeEvent{ID: 1, TargetID: r.TargetID, At: now, Kind: r.Kind, Source: source, Title: r.Title, CreatedBy: by}
	*f.got = append(*f.got, e)
	return e, nil
}

func TestReportChangeWithToken(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	var got []observe.ChangeEvent
	seven := int64(7)
	s := New(Deps{Auth: auth.NewService(roleStore{"viewer"}, time.Hour, false), Metrics: obs.NewMetrics(), Log: log,
		Tokens:  fakeTokens{secret: "llk_good", token: repository.APIToken{Name: "ci", Scope: repository.TokenEvents, TargetID: &seven}},
		Changes: fakeChanges{got: &got}, Targets: fakeTargets{}})
	post := func(auth, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/changes", strings.NewReader(body))
		if auth != "" {
			r.Header.Set("Authorization", auth)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	deploy := `{"kind":"deploy","title":"api v2.3.1"}`
	// A token needs no session or CSRF header; a token limited to an
	// instance reports for that instance.
	if w := post("Bearer llk_good", deploy); w.Code != http.StatusCreated || len(got) != 1 ||
		got[0].Source != changes.SourceAPI || got[0].CreatedBy != "token:ci" || got[0].TargetID == nil || *got[0].TargetID != 7 {
		t.Fatalf("token report: %d %s %+v", w.Code, w.Body, got)
	}
	if w := post("Bearer llk_good", `{"kind":"deploy","title":"x","target_id":8}`); w.Code != http.StatusForbidden {
		t.Fatalf("other instance: %d", w.Code)
	}
	if w := post("Bearer llk_bad", deploy); w.Code != http.StatusUnauthorized {
		t.Fatalf("bad token: %d", w.Code)
	}
	// Without a token it is a session request: a viewer may not report.
	if w := post("", deploy); w.Code != http.StatusUnauthorized && w.Code != http.StatusForbidden {
		t.Fatalf("no token: %d", w.Code)
	}
	if w := post("Bearer llk_good", `{"kind":"restart","title":"x"}`); w.Code != http.StatusBadRequest && w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("detected kinds cannot be reported: %d", w.Code)
	}
}

type fakeTargets struct{ TargetService }

func (fakeTargets) Get(_ context.Context, id int64) (targets.View, error) {
	return targets.View{ID: id}, nil
}
