//go:build integration

package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/lib/secret"
	"github.com/locklens/locklens/internal/repository"
	"github.com/locklens/locklens/internal/targetdb/investigate"
)

func TestWorkspaceReadOnlyAndBounds(t *testing.T) {
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, os.Getenv("LOCKLENS_IT_ADMIN_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	name := fmt.Sprintf("workspace_%d", time.Now().UnixNano())
	table := pgx.Identifier{"public", name}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE TABLE "+table+" (id bigint PRIMARY KEY, label text, payload jsonb); INSERT INTO "+table+" SELECT g, CASE WHEN g=1 THEN NULL WHEN g=2 THEN '' ELSE 'item '||g END, jsonb_build_object('n',g) FROM generate_series(1,600) g; GRANT SELECT ON "+table+" TO locklens_limited"); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, "DROP TABLE "+table)
	cfg, err := pgx.ParseConfig(os.Getenv("LOCKLENS_IT_LIMITED_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	run := func(fn func(pgx.Tx)) {
		t.Helper()
		conn, err := connect(ctx, cfg.Copy())
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close(ctx)
		tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		fn(tx)
	}
	for _, sql := range []string{"INSERT INTO " + table + " VALUES(601,'x',null)", "SELECT 1; COMMIT; SELECT 2", "WITH x AS (DELETE FROM " + table + " RETURNING *) SELECT * FROM x", "SELECT * FROM " + table + " FOR UPDATE", "COPY " + table + " TO STDOUT", "EXPLAIN ANALYZE SELECT 1"} {
		t.Run(sql[:min(20, len(sql))], func(t *testing.T) {
			run(func(tx pgx.Tx) {
				if _, err := investigate.Execute(ctx, tx, sql, "public", false); err == nil {
					t.Fatal("write or multi-statement accepted")
				}
			})
		})
	}
	run(func(tx pgx.Tx) {
		r, err := investigate.Execute(ctx, tx, "SELECT * FROM "+table+" ORDER BY id", "public", false)
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Rows) != 500 || !r.Truncated || r.Rows[0][1] != nil || *r.Rows[1][1] != "" {
			t.Fatalf("bad bounded result: %d %v", len(r.Rows), r.Truncated)
		}
	})
	run(func(tx pgx.Tx) {
		r, err := investigate.Execute(ctx, tx, "SELECT repeat('&',8000) FROM generate_series(1,500)", "public", false)
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(r)
		if len(encoded) > investigate.MaxBytes || !r.Truncated || len(r.Rows) >= 500 {
			t.Fatalf("escaped JSON result unbounded: %d", len(encoded))
		}
	})
	run(func(tx pgx.Tx) {
		r, err := investigate.Execute(ctx, tx, "SELECT repeat('x',9000)", "public", false)
		if err != nil || !r.CellTruncated || len(*r.Rows[0][0]) != investigate.MaxCell {
			t.Fatalf("cell limit: %+v %v", r, err)
		}
	})
	run(func(tx pgx.Tx) {
		r, err := investigate.Execute(ctx, tx, "SELECT * FROM "+table, "public", true)
		if err != nil || len(r.Rows) != 1 {
			t.Fatalf("explain: %v", err)
		}
	})
	run(func(tx pgx.Tx) {
		r, err := investigate.Data(ctx, tx, investigate.DataRequest{Schema: "public", Relation: name, Limit: 25})
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Rows) != 25 || r.Next == nil || *r.Next != "25" {
			t.Fatalf("keyset: %+v", r)
		}
		r, err = investigate.Data(ctx, tx, investigate.DataRequest{Schema: "public", Relation: name, Limit: 25, After: r.Next})
		if err != nil || *r.Rows[0][0] != "26" {
			t.Fatalf("next page %v", err)
		}
	})
	run(func(tx pgx.Tx) {
		r, err := investigate.Data(ctx, tx, investigate.DataRequest{Schema: "public", Relation: name, FilterColumn: "label", FilterOp: "eq", FilterValue: "' OR true --"})
		if err != nil || len(r.Rows) != 0 {
			t.Fatalf("filter injection: %v", err)
		}
		if _, err = investigate.Data(ctx, tx, investigate.DataRequest{Schema: "public", Relation: name, Sort: "id; DROP TABLE x"}); err == nil {
			t.Fatal("sort identifier accepted")
		}
	})
	run(func(tx pgx.Tx) {
		c, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
		defer cancel()
		if _, err := investigate.Execute(c, tx, "SELECT pg_sleep(10)", "public", false); err == nil {
			t.Fatal("deadline not enforced")
		}
	})
	run(func(tx pgx.Tx) {
		_, err := investigate.Execute(ctx, tx, "SELECT repeat('x',2000000)", "public", false)
		if err == nil {
			t.Fatal("oversized protocol value accepted")
		}
	})
	run(func(tx pgx.Tx) {
		for _, kind := range []string{"databases", "schemas", "relations", "detail"} {
			if _, err := investigate.Catalog(ctx, tx, kind, "public", name); err != nil {
				t.Fatalf("catalog %s: %v", kind, err)
			}
		}
	})
}

func TestWorkspacePolicyIsolation(t *testing.T) {
	ctx := context.Background()
	store, err := repository.Open(ctx, os.Getenv("LOCKLENS_IT_ADMIN_DSN"), repository.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	box, _ := secret.New(make([]byte, 32))
	svc := New(store, box)
	cfg, err := pgx.ParseConfig(os.Getenv("LOCKLENS_IT_LIMITED_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateTarget(ctx, repository.Target{Name: fmt.Sprintf("workspace-policy-%d", time.Now().UnixNano()), DSNCiphertext: []byte("collector-not-used"), DisplayHost: cfg.Host, DisplayPort: int(cfg.Port), DisplayDatabase: cfg.Database, DisplayUser: "locklens_observer", Enabled: true, IntervalSeconds: 10, MaxConnections: 1})
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.WorkspacePolicy(ctx, target.ID)
	if err != nil || p.Metadata || p.Data || p.SQL || p.History || p.Configured {
		t.Fatalf("default policy %+v %v", p, err)
	}
	fn := func(c context.Context, tx pgx.Tx) (any, error) {
		return investigate.Execute(c, tx, "SELECT 1 AS safe", "public", false)
	}
	if _, err = svc.Run(ctx, target.ID, "session", "sql", "denied-request", fn); SafeError(err).Code != "forbidden" {
		t.Fatal("default allowed")
	}
	p.Metadata = true
	if err = svc.Configure(ctx, target.ID, p, os.Getenv("LOCKLENS_IT_LIMITED_DSN")); err != nil {
		t.Fatal(err)
	}
	p, _ = store.WorkspacePolicy(ctx, target.ID)
	out, _ := json.Marshal(p)
	if strings.Contains(string(out), cfg.Password) || strings.Contains(string(out), "ciphertext") {
		t.Fatal("secret exposed")
	}
	if _, err = svc.Run(ctx, target.ID, "session", "data", "denied-data-request", fn); SafeError(err).Code != "forbidden" {
		t.Fatal("metadata granted data")
	}
	p.SQL = true
	if err = svc.Configure(ctx, target.ID, p, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Run(ctx, target.ID, "session", "sql", "allowed-request", fn); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 2)
	for _, id := range []string{"slow-request-1", "slow-request-2"} {
		go func() {
			_, e := svc.Run(ctx, target.ID, "session", "sql", id, func(c context.Context, tx pgx.Tx) (any, error) {
				return investigate.Execute(c, tx, "SELECT pg_sleep(10)", "public", false)
			})
			done <- e
		}()
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		svc.mu.Lock()
		n := svc.counts[target.ID]
		svc.mu.Unlock()
		if n == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("operations not registered")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err = svc.Run(ctx, target.ID, "session", "sql", "third-request", fn); SafeError(err).Code != "busy" {
		t.Fatalf("concurrency unbounded: %v", err)
	}
	if svc.Cancel("other-session", target.ID, "slow-request-1") {
		t.Fatal("foreign session cancelled")
	}
	p.SQL = false
	if err = svc.Configure(ctx, target.ID, p, ""); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		select {
		case e := <-done:
			if e == nil {
				t.Fatal("revocation did not cancel")
			}
		case <-time.After(4 * time.Second):
			t.Fatal("revocation timeout")
		}
	}
}

func TestExplainGenericPlan(t *testing.T) {
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, os.Getenv("LOCKLENS_IT_ADMIN_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	name := fmt.Sprintf("generic_%d", time.Now().UnixNano())
	table := pgx.Identifier{"public", name}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE TABLE "+table+" (id bigint PRIMARY KEY, v int); INSERT INTO "+table+" SELECT g, g FROM generate_series(1,100) g; GRANT SELECT, DELETE ON "+table+" TO locklens_limited"); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, "DROP TABLE "+table)
	var version int
	if err := admin.QueryRow(ctx, "SELECT current_setting('server_version_num')::int").Scan(&version); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgx.ParseConfig(os.Getenv("LOCKLENS_IT_LIMITED_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	explain := func(sql string) (string, error) {
		conn, err := connect(ctx, cfg.Copy())
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close(ctx)
		tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		return investigate.ExplainGeneric(ctx, tx, sql)
	}
	if _, err := explain("SELECT 1; SELECT 2"); err == nil {
		t.Fatal("several statements must be refused")
	}
	plan, err := explain("DELETE FROM " + table + " WHERE id = $1")
	if version < 160000 {
		if err == nil {
			t.Fatal("GENERIC_PLAN needs PostgreSQL 16")
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	var doc []map[string]any
	if err := json.Unmarshal([]byte(plan), &doc); err != nil || len(doc) != 1 || doc[0]["Plan"] == nil {
		t.Fatalf("plan = %s (%v)", plan, err)
	}
	// Planned, never executed.
	var n int
	if err := admin.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != 100 {
		t.Fatalf("rows = %d %v", n, err)
	}
}
