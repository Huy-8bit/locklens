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
	"math"
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
	// SQL text is collected by default (it can be switched off per target).
	withQuery := 0
	for _, sess := range act.Report.Snapshot.Sessions {
		if _, ok := sess["query"]; ok {
			withQuery++
		}
	}
	if withQuery == 0 {
		t.Error("query text should be collected by default")
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
			Command string  `json:"command"`
			Text    *string `json:"query_text"`
		} `json:"items"`
	}
	found := false
	for time.Now().Before(stop) && !found {
		for i := 0; i < 50; i++ {
			var v int
			_ = conn.QueryRow(ctx, "SELECT v FROM srv_pgss WHERE id = $1", i+1).Scan(&v)
		}
		inst.do(t, "GET", fmt.Sprintf("/api/v1/targets/%d/queries?range=15m", tv.ID), nil, 200, &list)
		for _, it := range list.Items {
			if it.User == "demo_app" && it.Calls >= 50 {
				found = true
				// SQL text is collected and returned by default.
				if it.Text == nil || !strings.Contains(*it.Text, "srv_pgss") {
					t.Errorf("normalised SQL text expected by default, got %v", it.Text)
				}
				if it.Command != "select" {
					t.Errorf("command = %q, want select", it.Command)
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
	// include_text=false omits the text.
	inst.do(t, "GET", fmt.Sprintf("/api/v1/targets/%d/queries?range=15m&include_text=false", tv.ID), nil, 200, &list)
	for _, it := range list.Items {
		if it.Text != nil {
			t.Error("include_text=false must omit SQL text")
		}
	}
	// Workload by command class.
	var cmds struct {
		Totals []struct {
			Command string `json:"command"`
			Calls   int64  `json:"calls"`
		} `json:"totals"`
		Coverage []struct {
			Covered float64 `json:"covered_seconds"`
		} `json:"coverage_buckets"`
	}
	inst.do(t, "GET", fmt.Sprintf("/api/v1/targets/%d/queries/commands?range=15m", tv.ID), nil, 200, &cmds)
	var selects int64
	for _, c := range cmds.Totals {
		if c.Command == "select" {
			selects = c.Calls
		}
	}
	if selects < 50 || len(cmds.Coverage) == 0 {
		t.Errorf("command breakdown: %+v", cmds)
	}
	inst.do(t, "GET", fmt.Sprintf("/api/v1/targets/%d/queries?command=bogus", tv.ID), nil, 400, nil)
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

func TestMetricsTimeRangeAPI(t *testing.T) {
	dsn := storeDSN(t)
	inst := start(t, dsn, freePort(t))
	target := inst.addTarget(t, "series-api", observerDSN, map[string]any{"enabled": false})
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	for _, sample := range []struct {
		seconds       int
		value, weight float64
	}{{10, 10, 10}, {30, 30, 20}, {90, 0, 10}} {
		_, err = conn.Exec(ctx, `INSERT INTO metric_points(target_id,metric,label,ts,value,interval_seconds) VALUES($1,'xact.commit.per_s','',$2,$3,$4)`, target.ID, from.Add(time.Duration(sample.seconds)*time.Second), sample.value, sample.weight)
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err = conn.Exec(ctx, `INSERT INTO incidents(target_id,target_name,rule_id,rule_version,severity,title,state,opened_at,last_seen_at,source,trigger_config) VALUES($1,'series-api','test',1,'warning','Time-range annotation','open',$2,$2,'recorder','{}')`, target.ID, from.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/targets/%d/metrics?names=xact.commit.per_s&from=%s&to=%s&step=1m&max_points=60", target.ID, from.Format(time.RFC3339), to.Format(time.RFC3339))
	var data struct {
		From, To time.Time
		Step     float64 `json:"step_seconds"`
		Series   []struct {
			Agg    string `json:"aggregation"`
			Points []struct {
				T time.Time `json:"t"`
				V *float64  `json:"v"`
			}
		}
		Annotations []struct{ Title string }
	}
	inst.do(t, "GET", path, nil, 200, &data)
	if data.Step != 60 || !data.From.Equal(from) || !data.To.Equal(to) || len(data.Series) != 1 || len(data.Series[0].Points) != 2 || data.Series[0].Agg != "time_weighted_mean" {
		t.Fatalf("wrong bounds/aggregation: %+v", data)
	}
	if math.Abs(*data.Series[0].Points[0].V-70.0/3) > 1e-9 || *data.Series[0].Points[1].V != 0 {
		t.Fatal("rate/zero semantics lost")
	}
	if len(data.Annotations) != 1 || data.Annotations[0].Title != "Time-range annotation" {
		t.Fatal("missing incident annotation")
	}
	for _, suffix := range []string{"&max_points=0", "&step=0s", "&from=invalid", "&from=2025-01-01T00:00:00Z"} {
		// Set replaces existing parameters rather than relying on duplicate-key parsing.
		u, _ := url.Parse(path)
		bad, _ := url.ParseQuery(strings.TrimPrefix(suffix, "&"))
		q := u.Query()
		for k, v := range bad {
			q.Set(k, v[0])
		}
		u.RawQuery = q.Encode()
		inst.do(t, "GET", u.String(), nil, 400, nil)
	}
}
