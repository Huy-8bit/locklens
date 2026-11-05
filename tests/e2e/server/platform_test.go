//go:build integration

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// bearer sends a request with an API token instead of a session.
func (i *instance) bearer(t *testing.T, method, path, token string, body any, want int, out any) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(method, i.base+path, bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		t.Fatalf("%s %s: status %d, want %d: %s", method, path, resp.StatusCode, want, data)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatal(err)
		}
	}
}

// TestChangesTokensAndLogIngest: a pipeline reports a deploy with an API
// token, a log agent ships events with an instance's token, and both are
// read back; tokens are limited to their scope and instance.
func TestChangesTokensAndLogIngest(t *testing.T) {
	inst := start(t, storeDSN(t), freePort(t))
	tv := inst.addTarget(t, "logs", observerDSN, nil)
	other := inst.addTarget(t, "other", limitedDSN, nil)

	type created struct {
		Secret string `json:"secret"`
	}
	var events, ingest created
	inst.do(t, "POST", "/api/v1/tokens", map[string]any{"name": "ci", "scope": "events"}, 201, &events)
	inst.do(t, "POST", "/api/v1/tokens", map[string]any{"name": "agent", "scope": "ingest", "target_id": tv.ID}, 201, &ingest)
	if !strings.HasPrefix(events.Secret, "llk_") {
		t.Fatalf("secret %q", events.Secret)
	}
	if list := string(inst.do(t, "GET", "/api/v1/tokens", nil, 200, nil)); strings.Contains(list, events.Secret) {
		t.Fatal("token secrets are never listed")
	}

	inst.bearer(t, "POST", "/api/v1/changes", events.Secret, map[string]any{"kind": "deploy", "title": "orders-api v2.3.1", "target_id": tv.ID}, 201, nil)
	inst.bearer(t, "POST", "/api/v1/changes", ingest.Secret, map[string]any{"kind": "deploy", "title": "x"}, 403, nil)
	var ch struct {
		Changes []struct {
			Title     string `json:"title"`
			Source    string `json:"source"`
			CreatedBy string `json:"created_by"`
		} `json:"changes"`
	}
	inst.do(t, "GET", fmt.Sprintf("/api/v1/changes?target_id=%d", tv.ID), nil, 200, &ch)
	if len(ch.Changes) != 1 || ch.Changes[0].Source != "api" || ch.Changes[0].CreatedBy != "token:ci" {
		t.Fatalf("changes %+v", ch.Changes)
	}

	now := time.Now().UTC()
	batch := map[string]any{"agent": map[string]any{"hostname": "db1", "version": "it", "sources": []string{"/log/postgresql.json"}},
		"events": []map[string]any{
			{"id": "e1", "at": now.Add(-time.Minute), "kind": "slow_query", "severity": "LOG", "duration_ms": 1520.5, "fingerprint": "fp", "query": "SELECT $1", "message": "duration"},
			{"id": "e2", "at": now.Add(-time.Minute), "kind": "deadlock", "severity": "ERROR", "sqlstate": "40P01", "message": "deadlock detected"},
		}}
	var res struct {
		Stored int `json:"stored"`
	}
	inst.bearer(t, "POST", "/api/v1/ingest/logs", ingest.Secret, batch, 200, &res)
	if res.Stored != 2 {
		t.Fatalf("stored %d", res.Stored)
	}
	inst.bearer(t, "POST", "/api/v1/ingest/logs", ingest.Secret, batch, 200, &res)
	if res.Stored != 0 {
		t.Fatalf("a batch sent twice is stored once: %d", res.Stored)
	}
	inst.bearer(t, "POST", "/api/v1/ingest/logs", events.Secret, batch, 403, nil)
	inst.bearer(t, "POST", "/api/v1/ingest/logs", "llk_wrong", batch, 401, nil)
	bad := map[string]any{"events": []map[string]any{{"id": "x", "at": now, "kind": "shell", "severity": "LOG", "message": "x"}}}
	inst.bearer(t, "POST", "/api/v1/ingest/logs", ingest.Secret, bad, 400, nil)

	var sum struct {
		Statements []struct {
			Fingerprint string  `json:"fingerprint"`
			P95         float64 `json:"p95_ms"`
		} `json:"statements"`
		Agents []struct {
			Hostname string `json:"hostname"`
		} `json:"agents"`
	}
	inst.do(t, "GET", fmt.Sprintf("/api/v1/targets/%d/logs?range=1h", tv.ID), nil, 200, &sum)
	if len(sum.Statements) != 1 || sum.Statements[0].P95 != 1520.5 || len(sum.Agents) != 1 || sum.Agents[0].Hostname != "db1" {
		t.Fatalf("summary %+v", sum)
	}
	// The other instance has none of them.
	inst.do(t, "GET", fmt.Sprintf("/api/v1/targets/%d/logs?range=1h", other.ID), nil, 200, &sum)
	if len(sum.Statements) != 0 || len(sum.Agents) != 0 {
		t.Fatalf("other instance %+v", sum)
	}
}

// TestTwoServersShareTheStore: two servers split the instances, both answer
// for every instance, and when one stops the other takes its instances over.
func TestTwoServersShareTheStore(t *testing.T) {
	store := storeDSN(t)
	a := start(t, store, freePort(t))
	b := start(t, store, freePort(t))
	var ids []int64
	for i, dsn := range []string{observerDSN, limitedDSN} {
		ids = append(ids, a.addTarget(t, fmt.Sprintf("srv%d", i), dsn, nil).ID)
	}
	holders := func() map[string]string {
		ctx := context.Background()
		c, err := pgx.Connect(ctx, store)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close(ctx)
		rows, _ := c.Query(ctx, "SELECT name, holder FROM leases WHERE name LIKE 'target:%' AND expires_at > now()")
		out := map[string]string{}
		for rows.Next() {
			var n, h string
			_ = rows.Scan(&n, &h)
			out[n] = h
		}
		return out
	}
	var servers struct {
		Cluster struct {
			Servers []struct {
				ID    string `json:"id"`
				Alive bool   `json:"alive"`
			} `json:"servers"`
		} `json:"cluster"`
	}
	waitFor(t, 60*time.Second, "two live servers", func() bool {
		a.do(t, "GET", "/api/v1/system", nil, 200, &servers)
		return len(servers.Cluster.Servers) == 2
	})
	waitFor(t, 90*time.Second, "each server collecting one instance", func() bool {
		h := holders()
		return len(h) == 2 && h[fmt.Sprintf("target:%d", ids[0])] != h[fmt.Sprintf("target:%d", ids[1])]
	})
	// Both servers answer with live data for both instances.
	for _, inst := range []*instance{a, b} {
		for _, id := range ids {
			waitFor(t, 60*time.Second, "live activity on every server", func() bool {
				var act struct {
					Source string `json:"source"`
				}
				inst.do(t, "GET", fmt.Sprintf("/api/v1/targets/%d/activity", id), nil, 200, &act)
				return act.Source == "live"
			})
		}
	}
	// a stops (releasing its leases): b collects both.
	a.stop(t)
	waitFor(t, 90*time.Second, "takeover", func() bool {
		h := holders()
		return len(h) == 2 && h[fmt.Sprintf("target:%d", ids[0])] == h[fmt.Sprintf("target:%d", ids[1])]
	})
}

// scrape fetches /metrics/targets with a token.
func (i *instance) scrape(t *testing.T, token string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", i.base+"/metrics/targets", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(data)
}

// docker runs docker and returns its trimmed output.
func docker(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("docker", args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// TestPrometheusExportAndAlertmanager: Prometheus scrapes the monitored
// instances with a metrics token (the output passes promtool's checks), and
// an unreachable instance opens an incident that reaches a real Alertmanager.
func TestPrometheusExportAndAlertmanager(t *testing.T) {
	inst := start(t, storeDSN(t), freePort(t))
	tv := inst.addTarget(t, "export", observerDSN, nil)
	var tok struct {
		Secret string `json:"secret"`
	}
	inst.do(t, "POST", "/api/v1/tokens", map[string]any{"name": "prometheus", "scope": "metrics"}, 201, &tok)
	if code, _ := inst.scrape(t, ""); code != 401 {
		t.Fatalf("without a token: %d", code)
	}
	var out string
	waitFor(t, 90*time.Second, "the instance in the export", func() bool {
		var code int
		code, out = inst.scrape(t, tok.Secret)
		return code == 200 && strings.Contains(out, `locklens_target_up{target="export",environment="default"} 1`) &&
			strings.Contains(out, "locklens_pg_xact_commit_total{")
	})
	for _, want := range []string{"# TYPE locklens_pg_xact_commit_total counter", `locklens_pg_sessions{target="export",environment="default",state="active"}`,
		"locklens_pg_connections_usage_ratio{", "locklens_target_info{"} {
		if !strings.Contains(out, want) {
			t.Errorf("export lacks %s", want)
		}
	}
	for _, s := range secrets {
		if s != "" && strings.Contains(out, s) {
			t.Fatal("the export contains a credential")
		}
	}
	if res, err := docker(t, out, "run", "--rm", "-i", "--entrypoint", "promtool", "prom/prometheus:v3.5.0", "check", "metrics"); err != nil {
		t.Fatalf("promtool check metrics: %v\n%s", err, res)
	}
	_ = tv

	// Alertmanager receives an incident of an unreachable instance.
	id, err := docker(t, "", "run", "-d", "--rm", "-p", "127.0.0.1::9093", "prom/alertmanager:v0.28.1")
	if err != nil {
		t.Fatalf("start alertmanager: %v %s", err, id)
	}
	t.Cleanup(func() { _, _ = docker(t, "", "rm", "-f", id) })
	addr, err := docker(t, "", "port", id, "9093/tcp")
	if err != nil {
		t.Fatal(err)
	}
	am := "http://" + strings.Split(addr, "\n")[0]
	waitFor(t, 30*time.Second, "alertmanager ready", func() bool {
		resp, err := http.Get(am + "/-/ready")
		if err == nil {
			resp.Body.Close()
		}
		return err == nil && resp.StatusCode == 200
	})
	inst.do(t, "POST", "/api/v1/notifications/channels", map[string]any{"name": "am", "kind": "alertmanager", "enabled": true,
		"min_severity": "warning", "destination": map[string]any{"url": am}}, 201, nil)
	down := inst.addTarget(t, "gone", "postgres://nobody:secret@127.0.0.1:1/none?connect_timeout=1", nil)
	waitFor(t, 150*time.Second, "the unreachable incident in Alertmanager", func() bool {
		resp, err := http.Get(am + "/api/v2/alerts")
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		var alerts []struct {
			Labels map[string]string `json:"labels"`
		}
		if json.NewDecoder(resp.Body).Decode(&alerts) != nil {
			return false
		}
		for _, a := range alerts {
			if a.Labels["alertname"] == "target.unreachable" && a.Labels["instance"] == "gone" && a.Labels["severity"] == "critical" {
				return true
			}
		}
		return false
	})
	if is := inst.incidents(t, down.ID); len(is) != 1 || is[0].RuleID != "target.unreachable" || is[0].State != "open" {
		t.Fatalf("incidents %+v", is)
	}
}
