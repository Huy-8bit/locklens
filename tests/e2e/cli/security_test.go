//go:build integration

package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/locklens/locklens/demo/scenario"
)

// TestQueryTextCanary puts a unique marker into the SQL text of blocking
// sessions and checks every output channel.
func TestQueryTextCanary(t *testing.T) {
	ctx := testContext(t)
	canary := fmt.Sprintf("cnryquerytext%d", time.Now().UnixNano())
	s, err := scenario.New(ctx, "canary", demoDSN, tableName(t))
	if err != nil {
		t.Fatal(err)
	}
	closeScenario(t, s)
	update := fmt.Sprintf("UPDATE %s SET owner = '%s' WHERE id = 1", s.Ident(), canary)
	a, err := s.Begin(ctx, "A")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Exec(ctx, a, update); err != nil {
		t.Fatal(err)
	}
	b, err := s.Begin(ctx, "B")
	if err != nil {
		t.Fatal(err)
	}
	s.ExecAsync(b, update)
	if err := s.WaitBlocked(ctx, b.PID, a.PID); err != nil {
		t.Fatal(err)
	}

	absent := func(channel, text string) {
		t.Helper()
		if strings.Contains(text, canary) {
			t.Errorf("query text appeared in %s without --include-query", channel)
		}
	}
	absent("table", locklens(t, observerDSN, "inspect", "--min-tx-age", "0s").stdout)
	absent("JSON", locklens(t, observerDSN, "inspect", "--format", "json", "--min-tx-age", "0s").stdout)

	cmd, out, _ := startWatch(t, observerDSN, "--format", "json", "--interval", "500ms", "--min-tx-age", "0s")
	for range 2 {
		absent("NDJSON", string(readLine(t, out, 10*time.Second)))
	}
	interruptAndWait(t, cmd, out)

	path := filepath.Join(t.TempDir(), "s.json")
	if res := locklens(t, observerDSN, "snapshot", "save", "--out", path, "--min-tx-age", "0s"); res.code != 0 {
		t.Fatal(res.stderr)
	}
	raw, _ := os.ReadFile(path)
	absent("snapshot file", string(raw))

	// Opt-in with sufficient privileges: present.
	r := inspectJSON(t, observerDSN, "--include-query")
	for _, pid := range []int{a.PID, b.PID} {
		if q := session(t, r, pid).Query; q == nil || !strings.Contains(*q, canary) {
			t.Errorf("--include-query should expose pid %d's query, got %v", pid, q)
		}
	}
	path2 := filepath.Join(t.TempDir(), "s2.json")
	locklens(t, observerDSN, "snapshot", "save", "--out", path2, "--include-query")
	if raw, _ := os.ReadFile(path2); !strings.Contains(string(raw), canary) {
		t.Error("snapshot saved with --include-query should contain the query text")
	}

	// Opt-in without privileges: still absent, never "<insufficient privilege>".
	res := locklens(t, limitedDSN, "inspect", "--format", "json", "--include-query")
	absent("JSON of a role without pg_read_all_stats", res.stdout)
	if strings.Contains(res.stdout, "insufficient privilege") {
		t.Error("PostgreSQL's placeholder text must not be reported as a query")
	}
	table := locklens(t, limitedDSN, "inspect", "--include-query")
	absent("table of a role without pg_read_all_stats", table.stdout)
	if !strings.Contains(table.stdout, "[hidden by permissions]") {
		t.Errorf("hidden query text should be marked:\n%s", table.stdout)
	}

	if err := s.Release(context.Background(), a); err != nil {
		t.Fatal(err)
	}
}
