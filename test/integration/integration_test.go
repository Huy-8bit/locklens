//go:build integration

// Package integration runs the locklens binary against a real PostgreSQL
// server with real lock contention created by demo/scenario.
//
// Start the database with `docker compose up -d --wait`, then run
// `make test-integration`. Connection strings can be overridden with
// LOCKLENS_IT_DEMO_DSN, LOCKLENS_IT_OBSERVER_DSN, and LOCKLENS_IT_LIMITED_DSN.
//
// Assertions are made on parsed JSON keyed by the PIDs the scenario reports,
// never on full output strings, because PIDs and timestamps change per run.
package integration

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/locklens/locklens/demo/scenario"
	"github.com/locklens/locklens/internal/model"
	"github.com/locklens/locklens/internal/report"
)

var (
	demoDSN     = envOr("LOCKLENS_IT_DEMO_DSN", "postgres://demo_app:demo-app-pw@127.0.0.1:55432/locklens_demo?sslmode=disable")
	observerDSN = envOr("LOCKLENS_IT_OBSERVER_DSN", "postgres://locklens_observer:observer-pw@127.0.0.1:55432/locklens_demo?sslmode=disable")
	limitedDSN  = envOr("LOCKLENS_IT_LIMITED_DSN", "postgres://locklens_limited:limited-pw@127.0.0.1:55432/locklens_demo?sslmode=disable")
	binary      string
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "locklens-it-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	binary = filepath.Join(dir, "locklens")
	build := exec.Command("go", "build", "-o", binary, "../../cmd/locklens")
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "build locklens:", err)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// tableName gives every test its own table so a failed cleanup cannot affect
// other tests.
func tableName(t *testing.T) string {
	return "it_" + strings.ToLower(strings.ReplaceAll(t.Name(), "/", "_"))
}

func cleanup(t *testing.T, s *scenario.Scenario) {
	t.Cleanup(func() {
		if err := s.Close(context.Background()); err != nil {
			t.Errorf("scenario cleanup: %v", err)
		}
	})
}

type result struct {
	code           int
	stdout, stderr string
}

// locklens runs the binary with LOCKLENS_DSN set to dsn (empty: unset).
func locklens(t *testing.T, dsn string, args ...string) result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = append(os.Environ(), "LOCKLENS_DSN="+dsn)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	code := 0
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("run locklens: %v", err)
	}
	return result{code: code, stdout: out.String(), stderr: errOut.String()}
}

func inspectJSON(t *testing.T, dsn string, args ...string) *report.Report {
	t.Helper()
	res := locklens(t, dsn, append([]string{"inspect", "--format", "json"}, args...)...)
	if res.code != 0 {
		t.Fatalf("inspect exit %d: %s", res.code, res.stderr)
	}
	var r report.Report
	if err := json.Unmarshal([]byte(res.stdout), &r); err != nil {
		t.Fatalf("inspect JSON does not parse: %v\n%s", err, res.stdout)
	}
	return &r
}

func node(t *testing.T, r *report.Report, pid int) report.Node {
	t.Helper()
	for _, n := range r.Analysis.Nodes {
		if n.ID == pid {
			return n
		}
	}
	t.Fatalf("pid %d is not in the blocking graph; nodes: %+v", pid, r.Analysis.Nodes)
	return report.Node{}
}

func session(t *testing.T, r *report.Report, pid int) model.Session {
	t.Helper()
	for _, s := range r.Snapshot.Sessions {
		if s.PID == pid {
			return s
		}
	}
	t.Fatalf("pid %d is not in the snapshot sessions", pid)
	return model.Session{}
}

func hasEdge(r *report.Report, blocker, waiter int) bool {
	for _, e := range r.Analysis.Edges {
		if e.Blocker == blocker && e.Waiter == waiter {
			return true
		}
	}
	return false
}

func hasWarning(r *report.Report, code string) bool {
	for _, w := range append(slices.Clone(r.Snapshot.Warnings), r.Analysis.Warnings...) {
		if w.Code == code {
			return true
		}
	}
	return false
}

func TestRowLock(t *testing.T) {
	s, err := scenario.RowLock(testContext(t), demoDSN, tableName(t))
	if err != nil {
		t.Fatal(err)
	}
	cleanup(t, s)
	a, b := s.ByLabel("A").PID, s.ByLabel("B").PID

	r := inspectJSON(t, observerDSN)
	if !hasEdge(r, a, b) {
		t.Fatalf("expected edge %d -> %d, got %+v", a, b, r.Analysis.Edges)
	}
	na := node(t, r, a)
	if !na.Root || na.DirectBlocked != 1 || na.TransitiveBlocked != 1 || na.Kind != report.KindSession {
		t.Errorf("node A = %+v", na)
	}
	sb := session(t, r, b)
	if len(sb.WaitingLocks) == 0 || sb.WaitingLocks[0].LockType != "transactionid" {
		t.Errorf("B should wait on a transactionid lock: %+v", sb.WaitingLocks)
	}
	if sb.WaitEventType == nil || *sb.WaitEventType != "Lock" {
		t.Errorf("B wait_event_type = %v", sb.WaitEventType)
	}
	for _, ages := range r.Analysis.Sessions {
		if ages.PID == b && ages.LockWaitSeconds == nil {
			t.Error("lock wait age should be known on PostgreSQL 14+ (pg_locks.waitstart)")
		}
	}

	table := locklens(t, observerDSN, "inspect")
	if table.code != 0 || !strings.Contains(table.stdout, fmt.Sprintf("root blocker pid %d blocks 1 directly, 1 in total", a)) {
		t.Errorf("table output missing root line (exit %d):\n%s", table.code, table.stdout)
	}
}

func TestChain(t *testing.T) {
	s, err := scenario.Chain(testContext(t), demoDSN, tableName(t))
	if err != nil {
		t.Fatal(err)
	}
	cleanup(t, s)
	a, b, c := s.ByLabel("A").PID, s.ByLabel("B").PID, s.ByLabel("C").PID

	r := inspectJSON(t, observerDSN)
	if !hasEdge(r, a, b) || !hasEdge(r, b, c) {
		t.Fatalf("expected A->B->C (%d->%d->%d), got %+v", a, b, c, r.Analysis.Edges)
	}
	if hasEdge(r, a, c) {
		t.Errorf("C waits for B's row only; A->C must not be reported")
	}
	if n := node(t, r, a); !n.Root || n.DirectBlocked != 1 || n.TransitiveBlocked != 2 {
		t.Errorf("A = %+v, want root with direct 1, transitive 2", n)
	}
	if n := node(t, r, b); n.Root || n.DirectBlocked != 1 || n.TransitiveBlocked != 1 || !slices.Equal(n.BlockedBy, []int{a}) {
		t.Errorf("B = %+v", n)
	}
	if n := node(t, r, c); n.DirectBlocked != 0 || n.TransitiveBlocked != 0 {
		t.Errorf("C = %+v", n)
	}
	foundComponent := false
	for _, cp := range r.Analysis.Components {
		if slices.Contains(cp.Members, a) {
			foundComponent = true
			if !slices.Equal(cp.Roots, []int{a}) || cp.Cyclic {
				t.Errorf("component = %+v", cp)
			}
		}
	}
	if !foundComponent {
		t.Error("component of A not found")
	}
}

func TestIdleInTransaction(t *testing.T) {
	const age = 2 * time.Second
	s, err := scenario.IdleInTransaction(testContext(t), demoDSN, tableName(t), age)
	if err != nil {
		t.Fatal(err)
	}
	cleanup(t, s)
	a := s.ByLabel("A")

	r := inspectJSON(t, observerDSN, "--min-tx-age", "1s")
	var found *report.LongTransaction
	for i, lt := range r.Analysis.LongTransactions {
		if lt.PID == a.PID {
			found = &r.Analysis.LongTransactions[i]
		}
	}
	if found == nil {
		t.Fatalf("idle session %d not reported: %+v", a.PID, r.Analysis.LongTransactions)
	}
	if !found.IdleInTransaction {
		t.Error("session should be flagged idle in transaction")
	}
	// xact_start must equal now() inside the transaction, to the microsecond,
	// and the reported age must match server_time - xact_start.
	sa := session(t, r, a.PID)
	if sa.XactStart == nil || !sa.XactStart.Equal(a.TxStart) {
		t.Fatalf("xact_start = %v, want %v", sa.XactStart, a.TxStart)
	}
	want := r.Snapshot.ServerTime.Sub(a.TxStart).Seconds()
	if diff := found.TransactionAgeSeconds - want; diff < -0.001 || diff > 0.001 || found.TransactionAgeSeconds < age.Seconds() {
		t.Errorf("transaction age %.3fs, want %.3fs (>= %s)", found.TransactionAgeSeconds, want, age)
	}

	// The same session must not be reported under a higher threshold.
	r = inspectJSON(t, observerDSN, "--min-tx-age", "1h")
	for _, lt := range r.Analysis.LongTransactions {
		if lt.PID == a.PID {
			t.Error("threshold not applied")
		}
	}
}

func TestNoBlocking(t *testing.T) {
	r := inspectJSON(t, observerDSN)
	if r.Analysis.Summary.BlockedSessions != 0 || len(r.Analysis.Edges) != 0 {
		t.Skipf("other activity is blocking on this server; skipping: %+v", r.Analysis.Edges)
	}
	res := locklens(t, observerDSN, "inspect")
	if res.code != 0 || !strings.Contains(res.stdout, "No blocking detected") {
		t.Errorf("exit %d, output:\n%s", res.code, res.stdout)
	}
}

func TestOwnConnectionsExcluded(t *testing.T) {
	// The collecting connection is inside a transaction while it reads
	// pg_stat_activity, so it would be listed if it were not excluded.
	r := inspectJSON(t, observerDSN, "--min-tx-age", "0s")
	for _, s := range r.Snapshot.Sessions {
		if s.ApplicationName != nil && *s.ApplicationName == "locklens" {
			t.Errorf("LockLens' own session %d is in the report", s.PID)
		}
	}
}

func TestQueryTextOptIn(t *testing.T) {
	s, err := scenario.RowLock(testContext(t), demoDSN, tableName(t))
	if err != nil {
		t.Fatal(err)
	}
	cleanup(t, s)
	b := s.ByLabel("B").PID

	for _, format := range []string{"json", "table"} {
		res := locklens(t, observerDSN, "inspect", "--format", format)
		if strings.Contains(res.stdout, "UPDATE") || strings.Contains(res.stdout, "balance") {
			t.Errorf("%s output contains query text without --include-query", format)
		}
	}
	res := locklens(t, observerDSN, "snapshot", "save", "--out", filepath.Join(t.TempDir(), "s.json"))
	if res.code != 0 {
		t.Fatalf("snapshot save: %s", res.stderr)
	}

	r := inspectJSON(t, observerDSN, "--include-query", "--query-max-length", "10")
	sb := session(t, r, b)
	if sb.Query == nil || *sb.Query != "UPDATE \"it" || !sb.QueryTruncated {
		t.Errorf("query = %v truncated=%v, want first 10 characters", sb.Query, sb.QueryTruncated)
	}
}

func TestSnapshotSaveAndShowOffline(t *testing.T) {
	s, err := scenario.Chain(testContext(t), demoDSN, tableName(t))
	if err != nil {
		t.Fatal(err)
	}
	a := s.ByLabel("A").PID
	path := filepath.Join(t.TempDir(), "incident.json")

	res := locklens(t, observerDSN, "snapshot", "save", "--out", path)
	if res.code != 0 {
		t.Fatalf("save exit %d: %s", res.code, res.stderr)
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"observer-pw", "postgres://", "password"} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("snapshot file contains %q", secret)
		}
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("snapshot mode %v", fi.Mode().Perm())
	}

	// Saving again without --force must fail and keep the file.
	if res := locklens(t, observerDSN, "snapshot", "save", "--out", path); res.code != 2 {
		t.Errorf("overwrite without --force: exit %d", res.code)
	}

	// Show with an unreachable DSN in the environment: no connection is made.
	offline := "postgres://nobody:x@127.0.0.1:1/none?sslmode=disable"
	show := locklens(t, offline, "snapshot", "show", path)
	if show.code != 0 || !strings.Contains(show.stdout, fmt.Sprintf("root blocker pid %d blocks 1 directly, 2 in total", a)) {
		t.Errorf("snapshot show exit %d:\n%s\n%s", show.code, show.stdout, show.stderr)
	}
	showJSON := locklens(t, offline, "snapshot", "show", path, "--format", "json")
	var r report.Report
	if err := json.Unmarshal([]byte(showJSON.stdout), &r); err != nil {
		t.Fatalf("snapshot show JSON: %v", err)
	}
	if n := node(t, &r, a); n.TransitiveBlocked != 2 {
		t.Errorf("offline transitive count = %d", n.TransitiveBlocked)
	}

	if res := locklens(t, observerDSN, "snapshot", "save", "--out", path, "--force"); res.code != 0 {
		t.Errorf("save --force: exit %d %s", res.code, res.stderr)
	}
}

func TestRestrictedRoleDegradesExplicitly(t *testing.T) {
	s, err := scenario.RowLock(testContext(t), demoDSN, tableName(t))
	if err != nil {
		t.Fatal(err)
	}
	cleanup(t, s)
	a, b := s.ByLabel("A").PID, s.ByLabel("B").PID

	r := inspectJSON(t, limitedDSN, "--include-query")
	if r.Snapshot.Server.ReadAllStats {
		t.Fatal("locklens_limited must not have pg_read_all_stats")
	}
	// pg_locks and pg_blocking_pids() are readable by every role, so the
	// relation itself is still observed...
	if !hasEdge(r, a, b) {
		t.Fatalf("edge %d -> %d should be visible to a plain role: %+v", a, b, r.Analysis.Edges)
	}
	// ...but session details are hidden and the report says so.
	sa := session(t, r, a)
	if sa.DetailsVisible || sa.State != nil || sa.XactStart != nil || sa.QueryStart != nil ||
		sa.WaitEventType != nil || sa.ClientAddr != nil || sa.BackendType != nil || sa.Query != nil {
		t.Errorf("hidden session exposes details: %+v", sa)
	}
	// Identifying columns stay visible to every role (documented in
	// docs/postgresql-observation.md).
	if sa.User == nil || *sa.User != "demo_app" || sa.Database == nil || sa.ApplicationName == nil {
		t.Errorf("identifying columns should remain visible: %+v", sa)
	}
	// Lock wait age comes from pg_locks, which is not restricted.
	for _, ages := range r.Analysis.Sessions {
		if ages.PID == b && ages.LockWaitSeconds == nil {
			t.Error("lock wait age should be known even for a hidden session")
		}
	}
	if !hasWarning(r, model.WarnRestrictedVisibility) {
		t.Error("missing restricted_visibility warning")
	}

	table := locklens(t, limitedDSN, "inspect")
	if !strings.Contains(table.stdout, "[details hidden by permissions]") || !strings.Contains(table.stdout, "restricted_visibility") {
		t.Errorf("table does not flag hidden data:\n%s", table.stdout)
	}

	doc := locklens(t, limitedDSN, "doctor", "--format", "json")
	if doc.code != 0 {
		t.Fatalf("doctor exit %d: %s", doc.code, doc.stdout)
	}
	if !strings.Contains(doc.stdout, `"status": "warn"`) || !strings.Contains(doc.stdout, "pg_read_all_stats") {
		t.Errorf("doctor should warn about visibility:\n%s", doc.stdout)
	}
}

func TestPreparedTransactionBlocker(t *testing.T) {
	gid := "locklens-it-" + fmt.Sprint(time.Now().UnixNano())
	s, err := scenario.PreparedBlocker(testContext(t), demoDSN, tableName(t), gid)
	if err != nil {
		t.Fatal(err)
	}
	cleanup(t, s)
	b := s.ByLabel("B").PID

	r := inspectJSON(t, observerDSN)
	if !hasEdge(r, 0, b) {
		t.Fatalf("expected edge 0 -> %d, got %+v", b, r.Analysis.Edges)
	}
	if n := node(t, r, 0); n.Kind != report.KindPreparedTransaction || !n.Root {
		t.Errorf("PID 0 node = %+v", n)
	}
	found := false
	for _, p := range r.Snapshot.PreparedTransactions {
		found = found || p.GID == gid
	}
	if !found || !hasWarning(r, model.WarnPreparedBlocker) {
		t.Errorf("prepared transaction %q not reported (warning present: %v)", gid, hasWarning(r, model.WarnPreparedBlocker))
	}
}

func TestDoctor(t *testing.T) {
	res := locklens(t, observerDSN, "doctor", "--format", "json")
	if res.code != 0 {
		t.Fatalf("doctor exit %d:\n%s%s", res.code, res.stdout, res.stderr)
	}
	var out struct {
		OK     bool `json:"ok"`
		Checks []struct {
			Name, Status string
		} `json:"checks"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatal(err)
	}
	for _, c := range out.Checks {
		if c.Status != "ok" {
			t.Errorf("check %q = %s for the observer role", c.Name, c.Status)
		}
	}
}

func TestAuthenticationFailure(t *testing.T) {
	bad := strings.Replace(observerDSN, "observer-pw", "WrongPassword123", 1)
	res := locklens(t, bad, "inspect")
	if res.code != 1 {
		t.Fatalf("exit %d", res.code)
	}
	if strings.Contains(res.stderr, "WrongPassword123") {
		t.Error("password leaked")
	}
	if !strings.Contains(res.stderr, "hint: authentication failed") {
		t.Errorf("stderr: %s", res.stderr)
	}
}

// startWatch runs `locklens watch` with stdout on a pipe (never a TTY).
func startWatch(t *testing.T, args ...string) (*exec.Cmd, *bufio.Reader, *bytes.Buffer) {
	t.Helper()
	cmd := exec.Command(binary, append([]string{"watch", "--interval", "500ms"}, args...)...)
	cmd.Env = append(os.Environ(), "LOCKLENS_DSN="+observerDSN)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	return cmd, bufio.NewReader(stdout), &stderr
}

func interruptAndWait(t *testing.T, cmd *exec.Cmd, rest *bufio.Reader) {
	t.Helper()
	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = rest.WriteTo(io.Discard) }()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("watch did not exit cleanly after SIGINT: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("watch did not exit within 10s of SIGINT")
	}
}

func TestWatchNDJSONAndInterrupt(t *testing.T) {
	s, err := scenario.RowLock(testContext(t), demoDSN, tableName(t))
	if err != nil {
		t.Fatal(err)
	}
	cleanup(t, s)
	a, b := s.ByLabel("A").PID, s.ByLabel("B").PID

	cmd, out, stderr := startWatch(t, "--format", "json")
	for i := range 3 {
		line, err := out.ReadBytes('\n')
		if err != nil {
			t.Fatalf("read line %d: %v (stderr %s)", i, err, stderr.String())
		}
		var r report.Report
		if err := json.Unmarshal(line, &r); err != nil {
			t.Fatalf("line %d is not a complete JSON report: %v", i, err)
		}
		if !hasEdge(&r, a, b) {
			t.Errorf("line %d lacks edge %d -> %d", i, a, b)
		}
	}
	interruptAndWait(t, cmd, out)
	if stderr.Len() != 0 {
		t.Errorf("Ctrl+C must not print errors, got %q", stderr.String())
	}
}

func TestWatchTableWithoutTTYHasNoEscapeCodes(t *testing.T) {
	cmd, out, stderr := startWatch(t)
	var buf bytes.Buffer
	for !strings.Contains(buf.String(), "=== frame 2 ===") {
		line, err := out.ReadString('\n')
		if err != nil {
			t.Fatalf("read: %v (stderr %s)", err, stderr.String())
		}
		buf.WriteString(line)
	}
	interruptAndWait(t, cmd, out)
	if strings.Contains(buf.String(), "\x1b") {
		t.Error("escape codes written to a non-terminal")
	}
	if stderr.Len() != 0 {
		t.Errorf("unexpected stderr %q", stderr.String())
	}
}
