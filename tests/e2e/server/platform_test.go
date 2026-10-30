//go:build integration

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
