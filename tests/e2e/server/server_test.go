//go:build integration

// Package server tests `locklens server` end to end over HTTP against real
// PostgreSQL: target management, live collection, incidents, exports,
// multi-target isolation, restricted roles, pg_stat_statements availability,
// primary/standby monitoring, outages, and restart recovery.
//
// Run through scripts/integration-test.sh, which provides the DSNs.
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/demo/scenario"
	"github.com/locklens/locklens/internal/server"
)

var (
	adminDSN, demoDSN, observerDSN, limitedDSN    string
	pgssObserverDSN, pgssDemoDSN, standbyObserver string
	composeProject, composeFile, composeEnvFile   string
	secrets                                       []string
)

const adminPassword = "integration-admin-pw-123"

func TestMain(m *testing.M) {
	env := map[string]*string{
		"LOCKLENS_IT_ADMIN_DSN": &adminDSN, "LOCKLENS_IT_DEMO_DSN": &demoDSN, "LOCKLENS_IT_OBSERVER_DSN": &observerDSN,
		"LOCKLENS_IT_LIMITED_DSN": &limitedDSN, "LOCKLENS_IT_PGSS_OBSERVER_DSN": &pgssObserverDSN, "LOCKLENS_IT_PGSS_DEMO_DSN": &pgssDemoDSN,
		"LOCKLENS_IT_STANDBY_OBSERVER_DSN": &standbyObserver, "LOCKLENS_IT_COMPOSE_PROJECT": &composeProject,
		"LOCKLENS_IT_COMPOSE_FILE": &composeFile, "LOCKLENS_IT_COMPOSE_ENV_FILE": &composeEnvFile,
	}
	for k, p := range env {
		*p = os.Getenv(k)
		if *p == "" {
			fmt.Fprintf(os.Stderr, "%s is required; run scripts/integration-test.sh\n", k)
			os.Exit(2)
		}
	}
	for _, d := range []string{adminDSN, demoDSN, observerDSN, limitedDSN, pgssObserverDSN, standbyObserver} {
		u, _ := url.Parse(d)
		pw, _ := u.User.Password()
		secrets = append(secrets, d, pw)
	}
	secrets = append(secrets, adminPassword)
	os.Exit(m.Run())
}

// instance is one running server.
type instance struct {
	base   string
	cancel context.CancelFunc
	done   chan error
	client *http.Client
	csrf   string
}

// storeDSN creates a fresh database for the monitoring store.
func storeDSN(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	c, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(ctx)
	name := fmt.Sprintf("store_srv_%d", time.Now().UnixNano())
	if _, err := c.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), adminDSN)
		if err == nil {
			_, _ = c.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
			c.Close(context.Background())
		}
	})
	u, _ := url.Parse(adminDSN)
	u.Path = "/" + name
	return u.String()
}

func freePort(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

func start(t *testing.T, store, listen string) *instance {
	t.Helper()
	vars := map[string]string{
		"LOCKLENS_LISTEN": listen, "LOCKLENS_STORE_DSN": store, "LOCKLENS_MASTER_KEY": "dGVzdC1rZXktdGVzdC1rZXktdGVzdC1rZXktdGVzdDA=",
		"LOCKLENS_ADMIN_PASSWORD": adminPassword, "LOCKLENS_DEMO_MODE": "true", "LOCKLENS_LOG_LEVEL": "warn",
	}
	cfg, err := server.LoadConfig(func(k string) string { return vars[k] })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	inst := &instance{base: "http://" + listen, cancel: cancel, done: make(chan error, 1)}
	logw := &bytes.Buffer{}
	log := slog.New(slog.NewTextHandler(logw, &slog.HandlerOptions{Level: slog.LevelInfo}))
	go func() { inst.done <- server.Run(ctx, cfg, "it", log) }()
	t.Cleanup(func() {
		if t.Failed() {
			inst.stop(t)
			t.Logf("server log:\n%s", logw.String())
		}
	})
	deadline := time.Now().Add(30 * time.Second)
	for {
		resp, err := http.Get(inst.base + "/readyz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("server not ready: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	jar, _ := cookiejar.New(nil)
	inst.client = &http.Client{Jar: jar, Timeout: 30 * time.Second}
	var sess struct {
		CSRF string `json:"csrf_token"`
	}
	inst.do(t, "POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": adminPassword}, 200, &sess)
	inst.csrf = sess.CSRF
	t.Cleanup(func() { inst.stop(t) })
	return inst
}

func (i *instance) stop(t *testing.T) {
	if i.cancel == nil {
		return
	}
	i.cancel()
	select {
	case err := <-i.done:
		if err != nil {
			t.Errorf("server exited with %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Error("server did not shut down within 20s")
	}
	i.cancel = nil
}

// do performs a request and checks the status; every body is checked for
// secrets.
func (i *instance) do(t *testing.T, method, path string, body any, want int, out any) []byte {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, i.base+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != "GET" {
		req.Header.Set("X-CSRF-Token", i.csrf)
	}
	resp, err := i.client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	for _, s := range secrets {
		if s != "" && bytes.Contains(data, []byte(s)) {
			t.Errorf("%s %s response contains a credential", method, path)
		}
	}
	if resp.StatusCode != want {
		t.Fatalf("%s %s: status %d, want %d: %s", method, path, resp.StatusCode, want, data)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatalf("%s %s: decode: %v\n%s", method, path, err, data)
		}
	}
	return data
}

type targetView struct {
	ID     int64 `json:"id"`
	Status struct {
		State        string `json:"state"`
		Capabilities *struct {
			IsStandby      bool   `json:"is_standby"`
			ReadAllStats   bool   `json:"read_all_stats"`
			StatStatements bool   `json:"pg_stat_statements"`
			Reason         string `json:"pg_stat_statements_reason"`
		} `json:"capabilities"`
		Collectors map[string]struct {
			Enabled bool    `json:"enabled"`
			Error   *string `json:"last_error"`
			Reason  string  `json:"disabled_reason"`
		} `json:"collectors"`
		LastError *string `json:"last_error"`
	} `json:"status"`
}

func (i *instance) addTarget(t *testing.T, name, dsn string, extra map[string]any) targetView {
	t.Helper()
	body := map[string]any{"name": name, "dsn": dsn, "interval_seconds": 5}
	for k, v := range extra {
		body[k] = v
	}
	var tv targetView
	i.do(t, "POST", "/api/v1/targets", body, 201, &tv)
	return tv
}

// waitFor polls fn until it returns true or the timeout expires.
func waitFor(t *testing.T, timeout time.Duration, what string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !fn() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s", timeout, what)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func (i *instance) target(t *testing.T, id int64) targetView {
	var tv targetView
	i.do(t, "GET", fmt.Sprintf("/api/v1/targets/%d", id), nil, 200, &tv)
	return tv
}

type incident struct {
	ID         int64  `json:"id"`
	State      string `json:"state"`
	RuleID     string `json:"rule_id"`
	TargetID   *int64 `json:"target_id"`
	EventCount int    `json:"event_count"`
}

func (i *instance) incidents(t *testing.T, target int64) []incident {
	var out struct {
		Incidents []incident `json:"incidents"`
	}
	i.do(t, "GET", fmt.Sprintf("/api/v1/incidents?target=%d", target), nil, 200, &out)
	return out.Incidents
}

func TestAuthAndSecurityHeaders(t *testing.T) {
	inst := start(t, storeDSN(t), freePort(t))
	resp, err := http.Get(inst.base + "/api/v1/targets")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("unauthenticated request: %d", resp.StatusCode)
	}
	// A mutation without the CSRF header is rejected even with a session.
	req, _ := http.NewRequest("POST", inst.base+"/api/v1/targets", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err = inst.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Errorf("mutation without CSRF: %d", resp.StatusCode)
	}
	// Cross-origin mutation with a valid token is rejected too.
	req, _ = http.NewRequest("POST", inst.base+"/api/v1/targets", strings.NewReader(`{}`))
	req.Header.Set("X-CSRF-Token", inst.csrf)
	req.Header.Set("Origin", "http://evil.example")
	resp, _ = inst.client.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Errorf("cross-origin mutation: %d", resp.StatusCode)
	}
	resp, _ = inst.client.Get(inst.base + "/")
	resp.Body.Close()
	for _, h := range []string{"Content-Security-Policy", "X-Content-Type-Options", "X-Request-ID"} {
		if resp.Header.Get(h) == "" {
			t.Errorf("missing header %s", h)
		}
	}
	// Oversized bodies are refused.
	big := map[string]string{"name": strings.Repeat("x", 2<<20)}
	inst.do(t, "POST", "/api/v1/targets", big, 400, nil)
	// Too long time ranges are refused.
	tv := inst.addTarget(t, "range", observerDSN, nil)
	inst.do(t, "GET", fmt.Sprintf("/api/v1/targets/%d/metrics?names=sessions.active&range=900h", tv.ID), nil, 400, nil)
	// Metrics endpoint exposes bounded labels only.
	resp, _ = http.Get(inst.base + "/metrics")
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !bytes.Contains(data, []byte("locklens_collect_total")) || bytes.Contains(data, []byte("postgres://")) {
		t.Errorf("unexpected /metrics output")
	}
}

func TestTargetLifecycleBlockingIncidentAndExport(t *testing.T) {
	ctx := context.Background()
	store := storeDSN(t)
	listen := freePort(t)
	inst := start(t, store, listen)

	// Validation never echoes the DSN.
	var verr struct {
		Error struct {
			Fields map[string]string `json:"fields"`
		} `json:"error"`
	}
	inst.do(t, "POST", "/api/v1/targets", map[string]any{"name": "bad", "dsn": "postgres://u:cnrybadpw@h:notaport/db"}, 422, &verr)
	if verr.Error.Fields["dsn"] == "" || strings.Contains(verr.Error.Fields["dsn"], "cnrybadpw") {
		t.Errorf("dsn validation: %+v", verr)
	}

	tv := inst.addTarget(t, "primary", observerDSN, nil)
	broken := inst.addTarget(t, "broken", "postgres://x:cnryx@127.0.0.1:1/none?sslmode=disable", nil)
	waitFor(t, 60*time.Second, "target healthy", func() bool { return inst.target(t, tv.ID).Status.State == "healthy" })
	waitFor(t, 60*time.Second, "broken target unreachable", func() bool { return inst.target(t, broken.ID).Status.State == "unreachable" })
	// Isolation: the healthy target keeps collecting while the other fails.
	got := inst.target(t, tv.ID)
	if got.Status.Capabilities == nil || got.Status.Capabilities.StatStatements {
		t.Errorf("locklens_it has no pg_stat_statements extension: %+v", got.Status.Capabilities)
	}
	if c := got.Status.Collectors["statements"]; c.Enabled || !strings.Contains(c.Reason, "not installed") {
		t.Errorf("statements collector must be unavailable with a reason: %+v", c)
	}
	// Test connection of a stored target.
	var test struct {
		OK bool `json:"ok"`
	}
	inst.do(t, "POST", fmt.Sprintf("/api/v1/targets/%d/test", tv.ID), nil, 200, &test)
	if !test.OK {
		t.Error("connection test failed")
	}

	s, err := scenario.Chain(ctx, demoDSN, "srv_chain")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	a := s.ByLabel("A").PID
	var act struct {
		Report struct {
			Analysis struct {
				Nodes []struct {
					ID         int `json:"id"`
					Transitive int `json:"transitive_blocked_count"`
				} `json:"nodes"`
			} `json:"analysis"`
			Snapshot struct {
				Sessions []map[string]any `json:"sessions"`
			} `json:"snapshot"`
		} `json:"report"`
	}
	waitFor(t, 30*time.Second, "chain visible in live activity", func() bool {
		inst.do(t, "GET", fmt.Sprintf("/api/v1/targets/%d/activity", tv.ID), nil, 200, &act)
		for _, n := range act.Report.Analysis.Nodes {
			if n.ID == a && n.Transitive == 2 {
				return true
			}
		}
		return false
	})
	for _, sess := range act.Report.Snapshot.Sessions {
		if _, ok := sess["query"]; ok {
			t.Error("query text must not be collected by default")
		}
	}
	var open []incident
	waitFor(t, 90*time.Second, "blocking incident opened", func() bool {
		open = nil
		for _, in := range inst.incidents(t, tv.ID) {
			if in.State == "open" && strings.HasPrefix(in.RuleID, "blocking.") {
				open = append(open, in)
			}
		}
		return len(open) > 0
	})

	// Restart while the incident is open: metadata survives and the
	// interruption is recorded.
	inst.stop(t)
	inst = start(t, store, listen)
	var events struct {
		Events []struct {
			Kind string `json:"kind"`
		} `json:"events"`
	}
	inst.do(t, "GET", fmt.Sprintf("/api/v1/incidents/%d/events", open[0].ID), nil, 200, &events)
	kinds := map[string]bool{}
	for _, e := range events.Events {
		kinds[e.Kind] = true
	}
	for _, k := range []string{"pre_trigger", "trigger", "blocking_snapshot", "recorder_restarted"} {
		if !kinds[k] {
			t.Errorf("event kind %s missing after restart: %v", k, kinds)
		}
	}
	var series struct {
		Series []struct {
			Points []any `json:"points"`
		} `json:"series"`
	}
	inst.do(t, "GET", fmt.Sprintf("/api/v1/targets/%d/metrics?names=blocking.blocked_sessions&range=1h", tv.ID), nil, 200, &series)
	if len(series.Series) == 0 || len(series.Series[0].Points) == 0 {
		t.Error("history must survive a restart")
	}

	// Releasing the chain resolves the incident.
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 90*time.Second, "incident resolved", func() bool {
		var d struct {
			Incident incident `json:"incident"`
		}
		inst.do(t, "GET", fmt.Sprintf("/api/v1/incidents/%d", open[0].ID), nil, 200, &d)
		return d.Incident.State == "resolved"
	})

	// Acknowledge is metadata only.
	before := inst.do(t, "GET", fmt.Sprintf("/api/v1/incidents/%d/events", open[0].ID), nil, 200, nil)
	inst.do(t, "POST", fmt.Sprintf("/api/v1/incidents/%d/acknowledge", open[0].ID), map[string]string{"note": "checked"}, 200, nil)
	after := inst.do(t, "GET", fmt.Sprintf("/api/v1/incidents/%d/events", open[0].ID), nil, 200, nil)
	if !bytes.Equal(before, after) {
		t.Error("acknowledging must not change evidence")
	}

	// Exports.
	js := inst.do(t, "GET", fmt.Sprintf("/api/v1/incidents/%d/export?format=json", open[0].ID), nil, 200, nil)
	if !bytes.Contains(js, []byte(`"format": "locklens-incident"`)) || bytes.Contains(js, []byte(`"query":`)) {
		t.Error("JSON export malformed or contains query text")
	}
	html := inst.do(t, "GET", fmt.Sprintf("/api/v1/incidents/%d/export?format=html", open[0].ID), nil, 200, nil)
	if !bytes.Contains(html, []byte("Blocking graphs")) || bytes.Contains(html, []byte("<script")) || bytes.Contains(html, []byte("https://")) {
		t.Error("HTML export must be standalone without scripts")
	}
	// Import round trip.
	req, _ := http.NewRequest("POST", inst.base+"/api/v1/incidents/import", bytes.NewReader(js))
	req.Header.Set("X-CSRF-Token", inst.csrf)
	resp, err := inst.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Errorf("import status %d", resp.StatusCode)
	}

	// Soft delete keeps incidents; the target disappears from the list.
	inst.do(t, "DELETE", fmt.Sprintf("/api/v1/targets/%d", tv.ID), nil, 204, nil)
	inst.do(t, "GET", fmt.Sprintf("/api/v1/targets/%d", tv.ID), nil, 404, nil)
	inst.do(t, "GET", fmt.Sprintf("/api/v1/incidents/%d", open[0].ID), nil, 200, nil)
}

func TestRestrictedRoleAndQueryTextOptIn(t *testing.T) {
	ctx := context.Background()
	inst := start(t, storeDSN(t), freePort(t))
	limited := inst.addTarget(t, "limited", limitedDSN, nil)
	withText := inst.addTarget(t, "with-text", observerDSN, map[string]any{"query_text_enabled": true})
	s, err := scenario.RowLock(ctx, demoDSN, "srv_rowlock")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	waitFor(t, 60*time.Second, "limited target degraded", func() bool { return inst.target(t, limited.ID).Status.State == "degraded" })
	var ov struct {
		Warnings []struct {
			Code string `json:"code"`
		} `json:"visibility_warnings"`
	}
	inst.do(t, "GET", fmt.Sprintf("/api/v1/targets/%d/overview", limited.ID), nil, 200, &ov)
	if len(ov.Warnings) == 0 || ov.Warnings[0].Code != "restricted_visibility" {
		t.Errorf("restricted visibility must be reported: %+v", ov)
	}
	var act struct {
		Report struct {
			Snapshot struct {
				Sessions []struct {
					PID   int     `json:"pid"`
					Query *string `json:"query"`
				} `json:"sessions"`
			} `json:"snapshot"`
		} `json:"report"`
	}
	waitFor(t, 30*time.Second, "query text collected for the opted-in target", func() bool {
		inst.do(t, "GET", fmt.Sprintf("/api/v1/targets/%d/activity", withText.ID), nil, 200, &act)
		for _, ss := range act.Report.Snapshot.Sessions {
			if ss.Query != nil && strings.Contains(*ss.Query, "UPDATE") {
				return true
			}
		}
		return false
	})
	// Switching it off stops collection.
	inst.do(t, "PUT", fmt.Sprintf("/api/v1/targets/%d", withText.ID), map[string]any{"name": "with-text", "interval_seconds": 5, "query_text_enabled": false, "enabled": true}, 200, nil)
	waitFor(t, 30*time.Second, "query text no longer collected", func() bool {
		act.Report.Snapshot.Sessions = nil // json.Unmarshal would keep stale pointers
		inst.do(t, "GET", fmt.Sprintf("/api/v1/targets/%d/activity", withText.ID), nil, 200, &act)
		for _, ss := range act.Report.Snapshot.Sessions {
			if ss.Query != nil {
				return false
			}
		}
		return true
	})
}

func TestPgStatStatementsAvailable(t *testing.T) {
	ctx := context.Background()
	inst := start(t, storeDSN(t), freePort(t))
	tv := inst.addTarget(t, "pgss", pgssObserverDSN, nil)
	waitFor(t, 60*time.Second, "pg_stat_statements detected", func() bool {
		c := inst.target(t, tv.ID).Status.Capabilities
		return c != nil && c.StatStatements
	})
	conn, err := pgx.Connect(ctx, pgssDemoDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, "CREATE TABLE IF NOT EXISTS srv_pgss (id int PRIMARY KEY, v int); INSERT INTO srv_pgss SELECT g, g FROM generate_series(1, 1000) g ON CONFLICT DO NOTHING"); err != nil {
		t.Fatal(err)
	}
	stop := time.Now().Add(50 * time.Second)
	var list struct {
		Items []struct {
			QueryID string  `json:"queryid"`
			User    string  `json:"username"`
			Calls   int64   `json:"calls"`
			Text    *string `json:"query_text"`
		} `json:"items"`
	}
	found := false
	for time.Now().Before(stop) && !found {
		for i := 0; i < 50; i++ {
			var v int
			_ = conn.QueryRow(ctx, "SELECT v FROM srv_pgss WHERE id = $1", i+1).Scan(&v)
		}
		inst.do(t, "GET", fmt.Sprintf("/api/v1/targets/%d/queries?range=15m&include_text=true", tv.ID), nil, 200, &list)
		for _, it := range list.Items {
			if it.User == "demo_app" && it.Calls >= 50 {
				found = true
				if it.Text != nil {
					t.Error("text must not be returned when the target has not opted in")
				}
				if _, err := fmt.Sscan(it.QueryID, new(int64)); err != nil {
					t.Errorf("queryid %q is not a decimal string", it.QueryID)
				}
			}
		}
		time.Sleep(time.Second)
	}
	if !found {
		t.Fatalf("demo_app statements never appeared in the workload view: %+v", list.Items)
	}
}

func TestPrimaryAndStandby(t *testing.T) {
	inst := start(t, storeDSN(t), freePort(t))
	p := inst.addTarget(t, "it-primary", observerDSN, nil)
	sb := inst.addTarget(t, "it-standby", standbyObserver, nil)
	waitFor(t, 60*time.Second, "standby detected", func() bool {
		c := inst.target(t, sb.ID).Status.Capabilities
		return c != nil && c.IsStandby
	})
	var rep struct {
		Replication *struct {
			Role     string `json:"role"`
			Replicas []any  `json:"replicas"`
			Slots    []struct {
				Name string `json:"slot_name"`
			} `json:"slots"`
			Standby *struct {
				Receiver *string `json:"wal_receiver_status"`
			} `json:"standby"`
		} `json:"replication"`
	}
	waitFor(t, 60*time.Second, "primary replication state", func() bool {
		inst.do(t, "GET", fmt.Sprintf("/api/v1/targets/%d/replication", p.ID), nil, 200, &rep)
		return rep.Replication != nil && rep.Replication.Role == "primary" && len(rep.Replication.Replicas) >= 1
	})
	if len(rep.Replication.Slots) == 0 {
		t.Error("standby1_slot should be listed")
	}
	waitFor(t, 60*time.Second, "standby replication state", func() bool {
		inst.do(t, "GET", fmt.Sprintf("/api/v1/targets/%d/replication", sb.ID), nil, 200, &rep)
		return rep.Replication != nil && rep.Replication.Role == "standby" && rep.Replication.Standby != nil &&
			rep.Replication.Standby.Receiver != nil && *rep.Replication.Standby.Receiver == "streaming"
	})
}

func compose(t *testing.T, args ...string) {
	t.Helper()
	full := append([]string{"compose", "-p", composeProject, "-f", composeFile, "--env-file", composeEnvFile}, args...)
	if out, err := exec.Command("docker", full...).CombinedOutput(); err != nil {
		t.Fatalf("docker %v: %v\n%s", full, err, out)
	}
}

// TestOutageAndRecovery pauses the database container, which holds both the
// monitored database and the monitoring store, so collection and storage
// fail together, then resumes it.
func TestOutageAndRecovery(t *testing.T) {
	inst := start(t, storeDSN(t), freePort(t))
	tv := inst.addTarget(t, "outage", observerDSN, nil)
	waitFor(t, 60*time.Second, "healthy", func() bool { return inst.target(t, tv.ID).Status.State == "healthy" })
	compose(t, "pause", "postgres")
	paused := true
	defer func() {
		if paused {
			compose(t, "unpause", "postgres")
		}
	}()
	// The API itself needs the store; wait long enough for failed collections.
	time.Sleep(25 * time.Second)
	compose(t, "unpause", "postgres")
	paused = false
	waitFor(t, 120*time.Second, "healthy again", func() bool { return inst.target(t, tv.ID).Status.State == "healthy" })
	var gaps struct {
		Gaps []struct {
			Collector string `json:"collector"`
			Reason    string `json:"reason"`
		} `json:"gaps"`
	}
	waitFor(t, 60*time.Second, "collection gap recorded", func() bool {
		inst.do(t, "GET", fmt.Sprintf("/api/v1/targets/%d/gaps?range=1h", tv.ID), nil, 200, &gaps)
		return len(gaps.Gaps) > 0
	})
}
