//go:build integration

// Package integration runs the locklens binary against real, disposable
// PostgreSQL servers with real lock contention created by demo/scenario.
//
// Run it through scripts/integration-test.sh (or `make test-integration`),
// which provisions an isolated Docker Compose project per PostgreSQL version
// and passes explicit DSNs. The tests refuse to run without those DSNs, so
// they can never fall back to a database configured elsewhere.
//
// Conventions:
//   - every actor uses its own connection; PIDs come from those connections;
//   - waits poll PostgreSQL state with a timeout, never fixed sleeps;
//   - every subprocess has a timeout and a scrubbed environment;
//   - every subprocess output is checked for passwords and DSNs;
//   - assertions use parsed JSON and actor PIDs, not whole output strings.
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/demo/scenario"
	"github.com/locklens/locklens/internal/model"
	"github.com/locklens/locklens/internal/report"
)

// Connection strings and harness metadata, all required.
var (
	adminDSN    string // superuser of the disposable server (test setup only)
	demoDSN     string // role that creates contention
	observerDSN string // LockLens role with pg_read_all_stats
	limitedDSN  string // LockLens role without monitoring privileges

	composeProject string
	composeFile    string
	composeEnvFile string
	artifactsDir   string
	pgVersion      string

	binary string
	// secrets holds every password and full DSN known to the suite. No
	// LockLens output may contain any of them.
	secrets []string
)

func TestMain(m *testing.M) {
	required := map[string]*string{
		"LOCKLENS_IT_ADMIN_DSN":        &adminDSN,
		"LOCKLENS_IT_DEMO_DSN":         &demoDSN,
		"LOCKLENS_IT_OBSERVER_DSN":     &observerDSN,
		"LOCKLENS_IT_LIMITED_DSN":      &limitedDSN,
		"LOCKLENS_IT_COMPOSE_PROJECT":  &composeProject,
		"LOCKLENS_IT_COMPOSE_FILE":     &composeFile,
		"LOCKLENS_IT_COMPOSE_ENV_FILE": &composeEnvFile,
	}
	var missing []string
	for name, dst := range required {
		*dst = os.Getenv(name)
		if *dst == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		fmt.Fprintf(os.Stderr, "integration tests need explicit settings (%s).\n"+
			"Run scripts/integration-test.sh or `make test-integration`; it provisions an isolated database.\n",
			strings.Join(missing, ", "))
		os.Exit(2)
	}
	artifactsDir = os.Getenv("LOCKLENS_IT_ARTIFACTS")
	pgVersion = os.Getenv("LOCKLENS_IT_PG_VERSION")

	for _, dsn := range []string{adminDSN, demoDSN, observerDSN, limitedDSN} {
		cfg, err := pgx.ParseConfig(dsn)
		if err != nil {
			fmt.Fprintln(os.Stderr, "invalid test DSN")
			os.Exit(2)
		}
		secrets = append(secrets, dsn, cfg.Password)
	}

	dir, err := os.MkdirTemp("", "locklens-it-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	binary = filepath.Join(dir, "locklens")
	// The binary is built with the race detector so end-to-end runs (watch
	// goroutines, signal handling) are race-checked too.
	build := exec.Command("go", "-C", "../../../backend", "build", "-race", "-o", binary, "./cmd/locklens")
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
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// tableName gives every test its own synthetic table.
func tableName(t *testing.T) string {
	return "it_" + strings.ToLower(strings.ReplaceAll(t.Name(), "/", "_"))
}

func closeScenario(t *testing.T, s *scenario.Scenario) {
	t.Cleanup(func() {
		if err := s.Close(context.Background()); err != nil {
			t.Errorf("scenario cleanup: %v", err)
		}
	})
}

// cleanEnv returns the parent environment without LOCKLENS_* and PG*
// variables, so subprocesses only see the settings a test passes explicitly.
func cleanEnv(extra ...string) []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "LOCKLENS_") || strings.HasPrefix(name, "PG") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, extra...)
}

type result struct {
	code           int
	stdout, stderr string
	elapsed        time.Duration
}

type runOpts struct {
	dsn     string        // LOCKLENS_DSN for the subprocess; empty leaves it unset
	timeout time.Duration // default 30s
	env     []string      // additional environment entries
}

// locklens runs the binary with LOCKLENS_DSN=dsn (empty: unset).
func locklens(t *testing.T, dsn string, args ...string) result {
	t.Helper()
	return locklensOpts(t, runOpts{dsn: dsn}, args...)
}

func locklensOpts(t *testing.T, o runOpts, args ...string) result {
	t.Helper()
	if o.timeout == 0 {
		o.timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), o.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.WaitDelay = 5 * time.Second
	extra := o.env
	if o.dsn != "" {
		extra = append(extra, "LOCKLENS_DSN="+o.dsn)
	}
	cmd.Env = cleanEnv(extra...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	start := time.Now()
	err := cmd.Run()
	res := result{stdout: out.String(), stderr: errOut.String(), elapsed: time.Since(start)}
	var exitErr *exec.ExitError
	switch {
	case ctx.Err() != nil:
		t.Fatalf("locklens %v did not finish within %s", args, o.timeout)
	case errors.As(err, &exitErr):
		res.code = exitErr.ExitCode()
	case err != nil:
		t.Fatalf("run locklens: %v", err)
	}
	checkOutput(t, args, res.stdout+res.stderr)
	return res
}

// checkOutput enforces suite-wide invariants on any LockLens output.
func checkOutput(t *testing.T, args []string, text string) {
	t.Helper()
	for _, s := range secrets {
		if s != "" && strings.Contains(text, s) {
			t.Errorf("locklens %v leaked a password or DSN in its output", args)
		}
	}
	for _, bad := range []string{"WARNING: DATA RACE", "panic:", "goroutine "} {
		if strings.Contains(text, bad) {
			t.Errorf("locklens %v output contains %q:\n%s", args, bad, text)
		}
	}
}

func inspectJSON(t *testing.T, dsn string, args ...string) *report.Report {
	t.Helper()
	res := locklens(t, dsn, append([]string{"inspect", "--format", "json"}, args...)...)
	if res.code != 0 {
		t.Fatalf("inspect exit %d: %s", res.code, res.stderr)
	}
	return decodeReport(t, []byte(res.stdout))
}

// decodeReport parses a report strictly: unknown fields are an error, so the
// output must match the documented types exactly.
func decodeReport(t *testing.T, data []byte) *report.Report {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var r report.Report
	if err := dec.Decode(&r); err != nil {
		t.Fatalf("report JSON does not parse strictly: %v\n%s", err, data)
	}
	if r.Snapshot == nil || r.Snapshot.SchemaVersion != model.SchemaVersion {
		t.Fatalf("report lacks a snapshot with schema_version %d", model.SchemaVersion)
	}
	return &r
}

func node(t *testing.T, r *report.Report, pid int) report.Node {
	t.Helper()
	n, ok := findNode(r, pid)
	if !ok {
		t.Fatalf("pid %d is not in the blocking graph; edges: %+v", pid, r.Analysis.Edges)
	}
	return n
}

func findNode(r *report.Report, pid int) (report.Node, bool) {
	for _, n := range r.Analysis.Nodes {
		if n.ID == pid {
			return n, true
		}
	}
	return report.Node{}, false
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

func ages(t *testing.T, r *report.Report, pid int) report.SessionAges {
	t.Helper()
	for _, a := range r.Analysis.Sessions {
		if a.PID == pid {
			return a
		}
	}
	t.Fatalf("pid %d has no ages", pid)
	return report.SessionAges{}
}

func hasEdge(r *report.Report, blocker, waiter int) bool {
	for _, e := range r.Analysis.Edges {
		if e.Blocker == blocker && e.Waiter == waiter {
			return true
		}
	}
	return false
}

// edgesAmong returns the edges whose endpoints are both in pids.
func edgesAmong(r *report.Report, pids ...int) [][2]int {
	var out [][2]int
	for _, e := range r.Analysis.Edges {
		if slices.Contains(pids, e.Blocker) && slices.Contains(pids, e.Waiter) {
			out = append(out, [2]int{e.Blocker, e.Waiter})
		}
	}
	return out
}

func hasWarning(r *report.Report, code string) bool {
	for _, w := range append(slices.Clone(r.Snapshot.Warnings), r.Analysis.Warnings...) {
		if w.Code == code {
			return true
		}
	}
	return false
}

// admin opens a superuser connection used only for setup and for checking
// PostgreSQL state independently of LockLens.
func admin(t *testing.T) *pgx.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatalf("admin connection: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

// blockingPIDs asks PostgreSQL directly who blocks pid.
func blockingPIDs(t *testing.T, conn *pgx.Conn, pid int) []int {
	t.Helper()
	var got []int32
	if err := conn.QueryRow(context.Background(), "SELECT pg_blocking_pids($1)", pid).Scan(&got); err != nil {
		t.Fatal(err)
	}
	out := make([]int, len(got))
	for i, p := range got {
		out[i] = int(p)
	}
	slices.Sort(out)
	return out
}

// writeArtifact stores a file in the harness artifacts directory, if any.
func writeArtifact(t *testing.T, name string, data []byte) {
	t.Helper()
	if artifactsDir == "" {
		return
	}
	path := filepath.Join(artifactsDir, fmt.Sprintf("pg%s-%s", pgVersion, name))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Logf("write artifact %s: %v", path, err)
	}
}

// compose runs docker compose against this run's isolated project only.
func compose(t *testing.T, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	full := append([]string{"compose", "-p", composeProject, "-f", composeFile, "--env-file", composeEnvFile}, args...)
	out, err := exec.CommandContext(ctx, "docker", full...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker compose %v: %v\n%s", args, err, out)
	}
}
