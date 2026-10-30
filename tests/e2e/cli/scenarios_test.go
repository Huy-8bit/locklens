//go:build integration

package integration

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/demo/scenario"
	"github.com/locklens/locklens/internal/config"
	"github.com/locklens/locklens/internal/domain/model"
	"github.com/locklens/locklens/internal/domain/report"
	"github.com/locklens/locklens/internal/targetdb"
)

// A. Healthy database.

func TestHealthyDatabase(t *testing.T) {
	doc := locklens(t, observerDSN, "doctor")
	if doc.code != 0 || !strings.Contains(doc.stdout, "Result: LockLens can observe this server") {
		t.Fatalf("doctor exit %d:\n%s%s", doc.code, doc.stdout, doc.stderr)
	}
	for _, line := range strings.Split(doc.stdout, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "WARN") || strings.HasPrefix(strings.TrimSpace(line), "FAIL") {
			t.Errorf("observer role should pass every check: %q", line)
		}
	}

	table := locklens(t, observerDSN, "inspect")
	if table.code != 0 || !strings.Contains(table.stdout, "No blocking detected") || table.stderr != "" {
		t.Fatalf("inspect exit %d, stderr %q:\n%s", table.code, table.stderr, table.stdout)
	}

	r := inspectJSON(t, observerDSN)
	if len(r.Analysis.Edges) != 0 || len(r.Analysis.Nodes) != 0 || r.Analysis.Summary != (report.Summary{}) {
		t.Errorf("an idle, isolated server must yield an empty graph: %+v", r.Analysis.Summary)
	}
	if !r.Snapshot.Server.ReadAllStats || len(r.Snapshot.Warnings) != 0 {
		t.Errorf("observer should see everything without warnings: %+v", r.Snapshot.Warnings)
	}
}

func TestOwnConnectionsExcluded(t *testing.T) {
	// The collecting connection is inside a transaction while it reads
	// pg_stat_activity, so with --min-tx-age 0s it would be listed if it were
	// not excluded.
	r := inspectJSON(t, observerDSN, "--min-tx-age", "0s")
	for _, s := range r.Snapshot.Sessions {
		if s.ApplicationName != nil && *s.ApplicationName == "locklens" {
			t.Errorf("LockLens' own session %d is in the report", s.PID)
		}
	}
}

// B. Row blocking A → B.

func TestRowBlocking(t *testing.T) {
	ctx := testContext(t)
	s, err := scenario.RowLock(ctx, demoDSN, tableName(t))
	if err != nil {
		t.Fatal(err)
	}
	closeScenario(t, s)
	a, b := s.ByLabel("A"), s.ByLabel("B")
	adm := admin(t)

	// Ground truth from PostgreSQL before asserting LockLens output.
	if got := blockingPIDs(t, adm, b.PID); !slices.Equal(got, []int{a.PID}) {
		t.Fatalf("pg_blocking_pids(B) = %v, want [%d]", got, a.PID)
	}

	r := inspectJSON(t, observerDSN)
	if got := edgesAmong(r, a.PID, b.PID); !slices.Equal(got, [][2]int{{a.PID, b.PID}}) {
		t.Fatalf("edges = %v, want only %d -> %d", got, a.PID, b.PID)
	}
	if n := node(t, r, a.PID); !n.Root || n.DirectBlocked != 1 || n.TransitiveBlocked != 1 || n.Kind != report.KindSession {
		t.Errorf("A = %+v", n)
	}
	if n := node(t, r, b.PID); n.Root || !slices.Equal(n.BlockedBy, []int{a.PID}) || n.DirectBlocked != 0 {
		t.Errorf("B = %+v", n)
	}
	sb := session(t, r, b.PID)
	if len(sb.WaitingLocks) == 0 || sb.WaitingLocks[0].LockType != "transactionid" ||
		sb.WaitEventType == nil || *sb.WaitEventType != "Lock" {
		t.Errorf("B should wait on a transactionid lock: %+v / %v", sb.WaitingLocks, sb.WaitEventType)
	}
	if ages(t, r, b.PID).LockWaitSeconds == nil {
		t.Error("lock wait age should be known (pg_locks.waitstart)")
	}

	table := locklens(t, observerDSN, "inspect")
	if !strings.Contains(table.stdout, fmt.Sprintf("root blocker pid %d blocks 1 directly, 1 in total", a.PID)) {
		t.Errorf("table output lacks the root line:\n%s", table.stdout)
	}

	// Releasing A lets B's UPDATE finish; the blocking must disappear.
	if err := s.Release(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := s.WaitDone(ctx, b); err != nil {
		t.Fatalf("B's update should succeed once A is released: %v", err)
	}
	r = inspectJSON(t, observerDSN)
	if len(edgesAmong(r, a.PID, b.PID)) != 0 || r.Analysis.Summary.BlockedSessions != 0 {
		t.Errorf("blocking should be gone after release: %+v", r.Analysis.Edges)
	}
}

// C. Chain A → B → C.

func TestBlockingChain(t *testing.T) {
	s, err := scenario.Chain(testContext(t), demoDSN, tableName(t))
	if err != nil {
		t.Fatal(err)
	}
	closeScenario(t, s)
	a, b, c := s.ByLabel("A").PID, s.ByLabel("B").PID, s.ByLabel("C").PID
	adm := admin(t)
	if got := blockingPIDs(t, adm, b); !slices.Equal(got, []int{a}) {
		t.Fatalf("pg_blocking_pids(B) = %v, want [%d]", got, a)
	}
	if got := blockingPIDs(t, adm, c); !slices.Equal(got, []int{b}) {
		t.Fatalf("pg_blocking_pids(C) = %v, want [%d]", got, b)
	}

	r := inspectJSON(t, observerDSN)
	want := [][2]int{{a, b}, {b, c}}
	if got := edgesAmong(r, a, b, c); !slices.Equal(got, want) {
		t.Fatalf("edges = %v, want %v", got, want)
	}
	for _, tc := range []struct {
		pid, direct, transitive int
		root                    bool
	}{{a, 1, 2, true}, {b, 1, 1, false}, {c, 0, 0, false}} {
		n := node(t, r, tc.pid)
		if n.DirectBlocked != tc.direct || n.TransitiveBlocked != tc.transitive || n.Root != tc.root {
			t.Errorf("pid %d = %+v, want direct %d transitive %d root %v", tc.pid, n, tc.direct, tc.transitive, tc.root)
		}
	}
	if len(r.Analysis.Components) != 1 || !slices.Equal(r.Analysis.Components[0].Roots, []int{a}) {
		t.Errorf("components = %+v", r.Analysis.Components)
	}
}

// D. Fan-out: one ACCESS EXCLUSIVE holder, several waiters. Relation locks are
// used because row-lock waiters queue behind each other (the second waiter
// waits for the first waiter's tuple lock), which would not be a fan-out.

func TestFanOut(t *testing.T) {
	ctx := testContext(t)
	s, err := scenario.New(ctx, "fan-out", demoDSN, tableName(t))
	if err != nil {
		t.Fatal(err)
	}
	closeScenario(t, s)
	a, err := s.Begin(ctx, "A")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Exec(ctx, a, "LOCK TABLE "+s.Ident()+" IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	var waiters []int
	for i := 1; i <= 4; i++ {
		w, err := s.Begin(ctx, fmt.Sprintf("W%d", i))
		if err != nil {
			t.Fatal(err)
		}
		s.ExecAsync(w, "SELECT count(*) FROM "+s.Ident())
		if err := s.WaitBlocked(ctx, w.PID, a.PID); err != nil {
			t.Fatal(err)
		}
		waiters = append(waiters, w.PID)
	}
	adm := admin(t)
	for _, w := range waiters {
		if got := blockingPIDs(t, adm, w); !slices.Equal(got, []int{a.PID}) {
			t.Fatalf("pg_blocking_pids(%d) = %v, want [%d]", w, got, a.PID)
		}
	}

	r := inspectJSON(t, observerDSN)
	all := append([]int{a.PID}, waiters...)
	if got := edgesAmong(r, all...); len(got) != 4 {
		t.Fatalf("expected 4 edges, got %v", got)
	}
	for _, w := range waiters {
		if !hasEdge(r, a.PID, w) || !slices.Equal(node(t, r, w).BlockedBy, []int{a.PID}) {
			t.Errorf("waiter %d not correctly attached", w)
		}
	}
	if n := node(t, r, a.PID); n.DirectBlocked != 4 || n.TransitiveBlocked != 4 || !n.Root {
		t.Errorf("A = %+v, want direct 4 transitive 4", n)
	}
	seen := map[int]bool{}
	for _, n := range r.Analysis.Nodes {
		if seen[n.ID] {
			t.Errorf("node %d listed twice", n.ID)
		}
		seen[n.ID] = true
	}
	if r.Analysis.Summary.BlockedSessions != 4 || r.Analysis.Summary.RootBlockers != 1 {
		t.Errorf("summary = %+v", r.Analysis.Summary)
	}
}

// D2. One waiter, two blockers: two compatible SHARE locks block a
// ROW EXCLUSIVE request.

func TestMultipleBlockers(t *testing.T) {
	ctx := testContext(t)
	s, err := scenario.New(ctx, "multiple-blockers", demoDSN, tableName(t))
	if err != nil {
		t.Fatal(err)
	}
	closeScenario(t, s)
	var holders []int
	for _, label := range []string{"A", "B"} {
		h, err := s.Begin(ctx, label)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Exec(ctx, h, "LOCK TABLE "+s.Ident()+" IN SHARE MODE"); err != nil {
			t.Fatal(err)
		}
		holders = append(holders, h.PID)
	}
	c, err := s.Begin(ctx, "C")
	if err != nil {
		t.Fatal(err)
	}
	s.ExecAsync(c, "UPDATE "+s.Ident()+" SET balance = balance + 1 WHERE id = 1")
	if err := s.WaitBlocked(ctx, c.PID, holders...); err != nil {
		t.Fatal(err)
	}
	slices.Sort(holders)
	if got := blockingPIDs(t, admin(t), c.PID); !slices.Equal(got, holders) {
		t.Fatalf("pg_blocking_pids(C) = %v, want %v", got, holders)
	}

	r := inspectJSON(t, observerDSN)
	if got := node(t, r, c.PID).BlockedBy; !slices.Equal(got, holders) {
		t.Errorf("C blocked_by = %v, want %v", got, holders)
	}
	var roots []int
	for _, cp := range r.Analysis.Components {
		if slices.Contains(cp.Members, c.PID) {
			roots = cp.Roots
		}
	}
	if !slices.Equal(roots, holders) {
		t.Errorf("roots = %v, want %v", roots, holders)
	}
	for _, h := range holders {
		if n := node(t, r, h); n.DirectBlocked != 1 || n.TransitiveBlocked != 1 {
			t.Errorf("holder %d = %+v", h, n)
		}
	}
}

// D3. Lock queue: A holds ACCESS SHARE, B waits for ACCESS EXCLUSIVE, and C's
// ACCESS SHARE request queues behind B (a "soft block": C does not conflict
// with A, only with B's pending request).

func TestLockQueueSoftBlock(t *testing.T) {
	ctx := testContext(t)
	s, err := scenario.New(ctx, "lock-queue", demoDSN, tableName(t))
	if err != nil {
		t.Fatal(err)
	}
	closeScenario(t, s)
	a, err := s.Begin(ctx, "A")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Exec(ctx, a, "SELECT count(*) FROM "+s.Ident()); err != nil {
		t.Fatal(err)
	}
	b, err := s.Begin(ctx, "B")
	if err != nil {
		t.Fatal(err)
	}
	s.ExecAsync(b, "LOCK TABLE "+s.Ident()+" IN ACCESS EXCLUSIVE MODE")
	if err := s.WaitBlocked(ctx, b.PID, a.PID); err != nil {
		t.Fatal(err)
	}
	c, err := s.Begin(ctx, "C")
	if err != nil {
		t.Fatal(err)
	}
	s.ExecAsync(c, "SELECT count(*) FROM "+s.Ident())
	if err := s.WaitBlocked(ctx, c.PID, b.PID); err != nil {
		t.Fatal(err)
	}
	if got := blockingPIDs(t, admin(t), c.PID); !slices.Equal(got, []int{b.PID}) {
		t.Fatalf("pg_blocking_pids(C) = %v, want [%d] (soft block only)", got, b.PID)
	}

	r := inspectJSON(t, observerDSN)
	want := [][2]int{{a.PID, b.PID}, {b.PID, c.PID}}
	if got := edgesAmong(r, a.PID, b.PID, c.PID); !slices.Equal(got, want) {
		t.Fatalf("edges = %v, want %v", got, want)
	}
	if n := node(t, r, a.PID); !n.Root || n.TransitiveBlocked != 2 {
		t.Errorf("A = %+v", n)
	}
	sc := session(t, r, c.PID)
	if len(sc.WaitingLocks) != 1 || sc.WaitingLocks[0].LockType != "relation" || sc.WaitingLocks[0].Mode != "AccessShareLock" ||
		sc.WaitingLocks[0].Relation == nil || !strings.Contains(*sc.WaitingLocks[0].Relation, s.Table) {
		t.Errorf("C waiting lock = %+v", sc.WaitingLocks)
	}
}

// E. Idle in transaction, long transactions, and the three distinct ages.

func TestIdleInTransactionAndThreshold(t *testing.T) {
	ctx := testContext(t)
	s, err := scenario.New(ctx, "idle", demoDSN, tableName(t))
	if err != nil {
		t.Fatal(err)
	}
	closeScenario(t, s)
	a, err := s.Begin(ctx, "A")
	if err != nil {
		t.Fatal(err)
	}
	// Run the statement well after BEGIN so query_start differs clearly from
	// xact_start, then let the session sit idle.
	if err := s.WaitIdleInTransaction(ctx, a.PID, 700*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err := s.Exec(ctx, a, "UPDATE "+s.Ident()+" SET balance = balance + 1 WHERE id = 3"); err != nil {
		t.Fatal(err)
	}
	if err := s.WaitIdleInTransaction(ctx, a.PID, 1500*time.Millisecond); err != nil {
		t.Fatal(err)
	}

	r := inspectJSON(t, observerDSN, "--min-tx-age", "1s")
	var lt *report.LongTransaction
	for i := range r.Analysis.LongTransactions {
		if r.Analysis.LongTransactions[i].PID == a.PID {
			lt = &r.Analysis.LongTransactions[i]
		}
	}
	if lt == nil || !lt.IdleInTransaction || lt.State == nil || *lt.State != "idle in transaction" {
		t.Fatalf("idle session not reported correctly: %+v", r.Analysis.LongTransactions)
	}
	sa := session(t, r, a.PID)
	if sa.XactStart == nil || !sa.XactStart.Equal(a.TxStart) {
		t.Fatalf("xact_start = %v, want now() of the transaction %v", sa.XactStart, a.TxStart)
	}
	ag := ages(t, r, a.PID)
	wantTx := r.Snapshot.ServerTime.Sub(a.TxStart).Seconds()
	if d := lt.TransactionAgeSeconds - wantTx; d < -0.001 || d > 0.001 || lt.TransactionAgeSeconds < 1.5 {
		t.Errorf("transaction age %.4f, want %.4f", lt.TransactionAgeSeconds, wantTx)
	}
	if ag.QueryAgeSeconds == nil || *ag.TransactionAgeSeconds-*ag.QueryAgeSeconds < 0.6 {
		t.Errorf("query age must be measured from query_start, not xact_start: %+v", ag)
	}
	if ag.LockWaitSeconds != nil {
		t.Errorf("an idle session has no lock wait, got %v", *ag.LockWaitSeconds)
	}

	table := locklens(t, observerDSN, "inspect", "--min-tx-age", "1s")
	flagged := false
	for _, line := range strings.Split(table.stdout, "\n") {
		f := strings.Fields(line)
		flagged = flagged || (len(f) > 0 && f[0] == fmt.Sprint(a.PID) && strings.Contains(line, "idle in transaction (!)"))
	}
	if !flagged {
		t.Errorf("table does not flag the idle transaction:\n%s", table.stdout)
	}

	// Threshold filtering: above the transaction age the session disappears.
	r = inspectJSON(t, observerDSN, "--min-tx-age", "1h")
	for _, lt := range r.Analysis.LongTransactions {
		if lt.PID == a.PID {
			t.Error("--min-tx-age 1h must exclude a seconds-old transaction")
		}
	}
}

func TestLockWaitAgeComesFromWaitStart(t *testing.T) {
	ctx := testContext(t)
	s, err := scenario.New(ctx, "lock-wait-age", demoDSN, tableName(t))
	if err != nil {
		t.Fatal(err)
	}
	closeScenario(t, s)
	a, err := s.Begin(ctx, "A")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Exec(ctx, a, "UPDATE "+s.Ident()+" SET balance = balance + 1 WHERE id = 1"); err != nil {
		t.Fatal(err)
	}
	b, err := s.Begin(ctx, "B")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WaitIdleInTransaction(ctx, b.PID, 500*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	// pg_sleep in the qual runs before the row lock is requested, so the
	// statement starts (query_start) about 0.6 s before the wait begins.
	s.ExecAsync(b, "UPDATE "+s.Ident()+" SET balance = balance + 1 WHERE id = 1 AND pg_sleep(0.6) IS NOT NULL")
	if err := s.WaitBlocked(ctx, b.PID, a.PID); err != nil {
		t.Fatal(err)
	}

	adm := admin(t)
	var waitStart, queryStart, stateChange time.Time
	deadline := time.Now().Add(5 * time.Second)
	for {
		var ws *time.Time
		if err := adm.QueryRow(ctx, "SELECT min(waitstart) FROM pg_locks WHERE pid = $1 AND NOT granted", b.PID).Scan(&ws); err != nil {
			t.Fatal(err)
		}
		if ws != nil {
			waitStart = *ws
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("waitstart stayed NULL")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := adm.QueryRow(ctx, "SELECT query_start, state_change FROM pg_stat_activity WHERE pid = $1", b.PID).Scan(&queryStart, &stateChange); err != nil {
		t.Fatal(err)
	}

	r := inspectJSON(t, observerDSN)
	sb := session(t, r, b.PID)
	if len(sb.WaitingLocks) == 0 || sb.WaitingLocks[0].WaitStart == nil || !sb.WaitingLocks[0].WaitStart.Equal(waitStart) {
		t.Fatalf("wait_start = %+v, want pg_locks.waitstart %v", sb.WaitingLocks, waitStart)
	}
	ag := ages(t, r, b.PID)
	want := r.Snapshot.ServerTime.Sub(waitStart).Seconds()
	if ag.LockWaitSeconds == nil || *ag.LockWaitSeconds-want > 0.001 || want-*ag.LockWaitSeconds > 0.001 {
		t.Fatalf("lock wait = %v, want %.4f (server_time - waitstart)", ag.LockWaitSeconds, want)
	}
	fromQueryStart := r.Snapshot.ServerTime.Sub(queryStart).Seconds()
	fromStateChange := r.Snapshot.ServerTime.Sub(stateChange).Seconds()
	if fromQueryStart-*ag.LockWaitSeconds < 0.5 || fromStateChange-*ag.LockWaitSeconds < 0.5 {
		t.Errorf("lock wait %.3f must not be derived from query_start (%.3f) or state_change (%.3f)",
			*ag.LockWaitSeconds, fromQueryStart, fromStateChange)
	}
	if *ag.TransactionAgeSeconds-*ag.QueryAgeSeconds < 0.4 {
		t.Errorf("transaction age and query age should differ: %+v", ag)
	}
}

// F. Permissions: a dedicated role without, then with, monitoring privileges.

func TestRestrictedPermissions(t *testing.T) {
	ctx := testContext(t)
	adm := admin(t)
	role := "locklens_it_perm"
	password := "cnryperm" + fmt.Sprint(time.Now().UnixNano())
	secrets = append(secrets, password)
	if _, err := adm.Exec(ctx, "CREATE ROLE "+role+" LOGIN PASSWORD '"+password+"'"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := adm.Exec(context.Background(), "DROP ROLE IF EXISTS "+role); err != nil {
			t.Errorf("drop role: %v", err)
		}
	})
	cfg, err := pgx.ParseConfig(observerDSN)
	if err != nil {
		t.Fatal(err)
	}
	u := url.URL{Scheme: "postgres", User: url.UserPassword(role, password),
		Host: fmt.Sprintf("%s:%d", cfg.Host, cfg.Port), Path: "/" + cfg.Database, RawQuery: "sslmode=disable"}
	roleDSN := u.String()
	secrets = append(secrets, roleDSN)

	s, err := scenario.RowLock(ctx, demoDSN, tableName(t))
	if err != nil {
		t.Fatal(err)
	}
	closeScenario(t, s)
	a, b := s.ByLabel("A").PID, s.ByLabel("B").PID
	// An idle transaction of another role that is not involved in blocking.
	other, err := scenario.IdleInTransaction(ctx, demoDSN, tableName(t)+"_idle", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	closeScenario(t, other)
	idle := other.ByLabel("A").PID

	// Without monitoring privileges: relations visible, details hidden, and
	// the report says it is partial.
	r := inspectJSON(t, roleDSN, "--include-query", "--min-tx-age", "0s")
	if r.Snapshot.Server.ReadAllStats || !hasWarning(r, model.WarnRestrictedVisibility) {
		t.Fatalf("expected restricted visibility warning, got read_all_stats=%v warnings=%+v",
			r.Snapshot.Server.ReadAllStats, r.Snapshot.Warnings)
	}
	if !hasEdge(r, a, b) {
		t.Fatalf("pg_locks and pg_blocking_pids are public: edge %d -> %d must be visible", a, b)
	}
	sa := session(t, r, a)
	if sa.DetailsVisible || sa.State != nil || sa.XactStart != nil || sa.QueryStart != nil || sa.WaitEventType != nil ||
		sa.ClientAddr != nil || sa.BackendType != nil || sa.Query != nil {
		t.Errorf("hidden session exposes details: %+v", sa)
	}
	if sa.User == nil || *sa.User != "demo_app" || sa.Database == nil || sa.ApplicationName == nil {
		t.Errorf("identifying columns should stay visible: %+v", sa)
	}
	if ages(t, r, b).LockWaitSeconds == nil {
		t.Error("lock wait age comes from pg_locks and should be known")
	}
	for _, sess := range r.Snapshot.Sessions {
		if sess.PID == idle {
			t.Errorf("idle transaction %d of another role should be invisible without pg_read_all_stats", idle)
		}
	}
	table := locklens(t, roleDSN, "inspect")
	if !strings.Contains(table.stdout, "[details hidden by permissions]") || !strings.Contains(table.stdout, "restricted_visibility") {
		t.Errorf("table does not flag partial data:\n%s", table.stdout)
	}
	doc := locklens(t, roleDSN, "doctor", "--format", "json")
	if doc.code != 0 || !strings.Contains(doc.stdout, `"status": "warn"`) || !strings.Contains(doc.stdout, "pg_read_all_stats") {
		t.Errorf("doctor should pass with warnings (exit %d):\n%s", doc.code, doc.stdout)
	}

	fullVisibility := func(label string) {
		t.Helper()
		r := inspectJSON(t, roleDSN, "--include-query", "--min-tx-age", "0s")
		if !r.Snapshot.Server.ReadAllStats || hasWarning(r, model.WarnRestrictedVisibility) {
			t.Errorf("%s: expected full visibility, warnings %+v", label, r.Snapshot.Warnings)
		}
		sa := session(t, r, a)
		if !sa.DetailsVisible || sa.State == nil || sa.XactStart == nil || sa.Query == nil {
			t.Errorf("%s: details still hidden: %+v", label, sa)
		}
		found := false
		for _, sess := range r.Snapshot.Sessions {
			found = found || sess.PID == idle
		}
		if !found {
			t.Errorf("%s: idle transaction of another role should now be visible", label)
		}
		doc := locklens(t, roleDSN, "doctor", "--format", "json")
		if doc.code != 0 || strings.Contains(doc.stdout, `"status": "warn"`) {
			t.Errorf("%s: doctor should report no warnings:\n%s", label, doc.stdout)
		}
	}

	if _, err := adm.Exec(ctx, "GRANT pg_read_all_stats TO "+role); err != nil {
		t.Fatal(err)
	}
	fullVisibility("pg_read_all_stats")

	if _, err := adm.Exec(ctx, "REVOKE pg_read_all_stats FROM "+role+"; GRANT pg_monitor TO "+role); err != nil {
		t.Fatal(err)
	}
	fullVisibility("pg_monitor")
	if _, err := adm.Exec(ctx, "REVOKE pg_monitor FROM "+role); err != nil {
		t.Fatal(err)
	}
}

// G. Prepared transaction blocker (PID 0).

func TestPreparedTransactionBlocker(t *testing.T) {
	ctx := testContext(t)
	gid := fmt.Sprintf("locklens-it-%d", time.Now().UnixNano())
	s, err := scenario.PreparedBlocker(ctx, demoDSN, tableName(t), gid)
	if err != nil {
		t.Fatal(err)
	}
	b := s.ByLabel("B").PID
	adm := admin(t)
	t.Cleanup(func() {
		// Safety net in case the test fails before the explicit rollback.
		_, _ = adm.Exec(context.Background(), "ROLLBACK PREPARED '"+gid+"'")
	})
	if got := blockingPIDs(t, adm, b); !slices.Equal(got, []int{0}) {
		t.Fatalf("pg_blocking_pids(B) = %v, want [0]", got)
	}

	r := inspectJSON(t, observerDSN)
	if !hasEdge(r, 0, b) {
		t.Fatalf("edge 0 -> %d dropped: %+v", b, r.Analysis.Edges)
	}
	if n := node(t, r, 0); n.Kind != report.KindPreparedTransaction || !n.Root || n.DirectBlocked != 1 {
		t.Errorf("PID 0 node = %+v", n)
	}
	found := false
	for _, p := range r.Snapshot.PreparedTransactions {
		found = found || p.GID == gid
	}
	if !found || !hasWarning(r, model.WarnPreparedBlocker) {
		t.Errorf("prepared transaction %q not reported", gid)
	}
	table := locklens(t, observerDSN, "inspect")
	if table.code != 0 || !strings.Contains(table.stdout, "root blocker prepared transaction (PID 0)") {
		t.Errorf("table:\n%s", table.stdout)
	}

	// Cleanup must use ROLLBACK PREPARED: the prepared transaction outlives
	// every connection.
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := adm.QueryRow(ctx, "SELECT count(*) FROM pg_prepared_xacts WHERE gid = $1", gid).Scan(&n); err != nil || n != 0 {
		t.Fatalf("prepared transaction still present after cleanup (count %d, err %v)", n, err)
	}
}

// H. Sessions that end while LockLens is collecting.

func TestSessionsDisappearingDuringCollection(t *testing.T) {
	ctx := testContext(t)
	adm := admin(t)

	cmd, out, stderr := startWatch(t, observerDSN, "--format", "json", "--interval", "500ms")
	lines := make(chan []byte, 100)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(out)
		sc.Buffer(make([]byte, 1<<20), 1<<24)
		for sc.Scan() {
			lines <- append([]byte(nil), sc.Bytes()...)
		}
	}()

	// Churn: build contention and terminate the actors abruptly, repeatedly.
	stop := make(chan struct{})
	var churnErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			s, err := scenario.Chain(ctx, demoDSN, tableName(t))
			if err != nil {
				churnErr = err
				return
			}
			for _, sess := range s.Sessions {
				_, _ = adm.Exec(ctx, "SELECT pg_terminate_backend($1)", sess.PID)
			}
			_ = s.Close(context.Background()) // errors expected: connections are gone
		}
	}()

	// The built binary, a few times...
	var inspects []*report.Report
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		res := locklens(t, observerDSN, "inspect", "--format", "json")
		if res.code != 0 {
			t.Errorf("inspect during churn exit %d: %s", res.code, res.stderr)
			continue
		}
		inspects = append(inspects, decodeReport(t, []byte(res.stdout)))
	}
	// ...and the same collector in-process, as fast as possible, to raise the
	// chance of a session ending between the catalog reads of one collection.
	cfg, red, err := config.PoolConfig(observerDSN, config.Options{ConnectTimeout: 5 * time.Second, QueryTimeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	collector, err := targetdb.Connect(ctx, cfg, red, model.Tool{Name: "locklens", Version: "it"})
	if err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		snap, err := collector.Collect(ctx, targetdb.Options{QueryMaxLength: 200, MinTxAge: time.Minute})
		if err != nil {
			t.Errorf("in-process collect during churn: %v", err)
			continue
		}
		data, err := json.Marshal(report.Build(snap, time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		inspects = append(inspects, decodeReport(t, data))
	}
	collector.Close()
	close(stop)
	wg.Wait()
	if churnErr != nil {
		t.Fatalf("churn: %v", churnErr)
	}
	interruptAndWait(t, cmd)

	frames := 0
	for line := range lines {
		decodeReport(t, line)
		frames++
	}
	if frames < 5 {
		t.Errorf("expected several watch frames, got %d", frames)
	}
	if strings.Contains(stderr.String(), "collection failed") {
		t.Logf("watch reported transient failures during churn:\n%s", stderr.String())
	}
	withEdges, placeholders := 0, 0
	for _, r := range inspects {
		if len(r.Analysis.Edges) > 0 {
			withEdges++
		}
		for _, n := range r.Analysis.Nodes {
			if n.Kind == report.KindMissing {
				placeholders++
			}
		}
	}
	t.Logf("churn: %d inspect reports (%d with edges, %d missing-session placeholders), %d watch frames",
		len(inspects), withEdges, placeholders, frames)
}

// Observing from another database: the graph is cluster-wide, but relation
// names can only be resolved in the database LockLens is connected to.
func TestCrossDatabaseObservation(t *testing.T) {
	ctx := testContext(t)
	s, err := scenario.New(ctx, "cross-db", demoDSN, tableName(t))
	if err != nil {
		t.Fatal(err)
	}
	closeScenario(t, s)
	a, err := s.Begin(ctx, "A")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Exec(ctx, a, "LOCK TABLE "+s.Ident()+" IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	b, err := s.Begin(ctx, "B")
	if err != nil {
		t.Fatal(err)
	}
	s.ExecAsync(b, "SELECT count(*) FROM "+s.Ident())
	if err := s.WaitBlocked(ctx, b.PID, a.PID); err != nil {
		t.Fatal(err)
	}

	cfg, err := pgx.ParseConfig(observerDSN)
	if err != nil {
		t.Fatal(err)
	}
	other := strings.Replace(observerDSN, "/"+cfg.Database+"?", "/postgres?", 1)
	secrets = append(secrets, other)
	r := inspectJSON(t, other)
	if r.Snapshot.Server.Database != "postgres" || !hasEdge(r, a.PID, b.PID) {
		t.Fatalf("edge %d -> %d should be visible from database postgres: %+v", a.PID, b.PID, r.Analysis.Edges)
	}
	l := session(t, r, b.PID).WaitingLocks
	if len(l) != 1 || l[0].LockType != "relation" || l[0].Relation != nil || l[0].RelationOID == nil ||
		l[0].Database == nil || *l[0].Database != cfg.Database {
		t.Errorf("relation in another database must be reported by OID only: %+v", l)
	}
	table := locklens(t, other, "inspect")
	if !strings.Contains(table.stdout, "AccessShareLock on relation OID") || !strings.Contains(table.stdout, "in database "+cfg.Database) {
		t.Errorf("table:\n%s", table.stdout)
	}
}
