package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/locklens/locklens/internal/domain/model"
	"github.com/locklens/locklens/internal/lib/snapshot"
)

func runCLI(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	code = Main(ctx, args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func writeSnapshot(t *testing.T) string {
	t.Helper()
	ts := time.Date(2026, 10, 24, 8, 0, 0, 0, time.UTC)
	state, idle := "active", "idle in transaction"
	backend := "client backend"
	snap := &model.Snapshot{
		SchemaVersion: model.SchemaVersion, CollectedAt: ts, ServerTime: ts,
		Server: model.Server{Host: "localhost", Port: 5432, Database: "db", User: "observer", Version: "17.0"},
		Sessions: []model.Session{
			{PID: 1, State: &idle, BackendType: &backend, XactStart: ptr(ts.Add(-10 * time.Minute)), DetailsVisible: true, WaitingLocks: []model.WaitingLock{}},
			{PID: 2, State: &state, BackendType: &backend, XactStart: ptr(ts.Add(-time.Minute)), DetailsVisible: true, WaitingLocks: []model.WaitingLock{}},
			{PID: 3, State: &state, BackendType: &backend, XactStart: ptr(ts.Add(-time.Minute)), DetailsVisible: true, WaitingLocks: []model.WaitingLock{}},
		},
		Edges:      []model.Edge{{Blocker: 1, Waiter: 2}, {Blocker: 2, Waiter: 3}},
		Collection: model.Collection{MinTxAgeSeconds: 300, QueryMaxLength: 200},
	}
	path := filepath.Join(t.TempDir(), "snap.json")
	if err := snapshot.Save(path, snap, false); err != nil {
		t.Fatal(err)
	}
	return path
}

func ptr[T any](v T) *T { return &v }

func TestUsageErrorsExitTwo(t *testing.T) {
	t.Setenv("LOCKLENS_DSN", "")
	cases := [][]string{
		{"inspect"}, // no DSN
		{"inspect", "--dsn", "postgres://x@localhost/db", "--format", "xml"},
		{"inspect", "--dsn", "postgres://x@localhost/db", "--query-max-length", "0"},
		{"inspect", "--dsn", "postgres://x@localhost/db", "--min-tx-age", "-1s"},
		{"inspect", "--no-such-flag"},
		{"inspect", "extra-arg"},
		{"watch", "--dsn", "postgres://x@localhost/db", "--interval", "10ms"},
		{"no-such-command"},
		{"snapshot", "show"},
		{"snapshot", "save", "--dsn", "postgres://x@localhost/db"}, // missing --out
		{"--log-level", "loud", "version"},
		{"--connect-timeout", "0s", "version"},
	}
	for _, args := range cases {
		code, _, stderr := runCLI(t, args...)
		if code != ExitUsage {
			t.Errorf("%v: exit %d, want %d (stderr: %s)", args, code, ExitUsage, stderr)
		}
		if !strings.HasPrefix(stderr, "locklens: error:") {
			t.Errorf("%v: stderr = %q", args, stderr)
		}
	}
}

func TestInvalidDSNIsUsageErrorAndRedacted(t *testing.T) {
	code, stdout, stderr := runCLI(t, "inspect", "--dsn", "postgres://bob:TopSecret!@host:notaport/db")
	if code != ExitUsage {
		t.Fatalf("exit %d", code)
	}
	if strings.Contains(stderr+stdout, "TopSecret") || strings.Contains(stderr, "bob") {
		t.Errorf("credentials leaked: %s", stderr)
	}
}

func TestConnectionFailureExitsOneWithoutPassword(t *testing.T) {
	// Port 1 on loopback is closed on any sane test machine.
	code, stdout, stderr := runCLI(t, "inspect", "--connect-timeout", "2s",
		"--dsn", "postgres://bob:TopSecret99@127.0.0.1:1/db?sslmode=disable")
	if code != ExitRuntime {
		t.Fatalf("exit %d, stderr %s", code, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout must stay empty on error, got %q", stdout)
	}
	if strings.Contains(stderr, "TopSecret99") {
		t.Errorf("password leaked: %s", stderr)
	}
	if !strings.Contains(stderr, "hint: connection refused") {
		t.Errorf("missing hint: %s", stderr)
	}
}

func TestDoctorConnectionFailure(t *testing.T) {
	code, stdout, _ := runCLI(t, "doctor", "--format", "json", "--connect-timeout", "2s",
		"--dsn", "postgres://bob:TopSecret99@127.0.0.1:1/db?sslmode=disable")
	if code != ExitRuntime {
		t.Fatalf("exit %d", code)
	}
	var res doctorResult
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatalf("doctor JSON invalid: %v\n%s", err, stdout)
	}
	if res.OK || len(res.Checks) != 1 || res.Checks[0].Name != "connection" {
		t.Errorf("unexpected doctor result %+v", res)
	}
	if strings.Contains(stdout, "TopSecret99") {
		t.Error("password leaked into doctor output")
	}
}

func TestSnapshotShowOffline(t *testing.T) {
	path := writeSnapshot(t)
	t.Setenv("LOCKLENS_DSN", "") // no database needed

	code, stdout, stderr := runCLI(t, "snapshot", "show", path)
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	for _, want := range []string{"root blocker pid 1 blocks 1 directly, 2 in total", "Long-running transactions (>= 5m00s)"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("missing %q in:\n%s", want, stdout)
		}
	}

	code, stdout, _ = runCLI(t, "snapshot", "show", path, "--format", "json", "--min-tx-age", "30s")
	if code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	var r struct {
		Analysis struct {
			MinTxAgeSeconds float64 `json:"min_tx_age_seconds"`
			Summary         struct {
				LongTransactions int `json:"long_transactions"`
			} `json:"summary"`
		} `json:"analysis"`
	}
	if err := json.Unmarshal([]byte(stdout), &r); err != nil {
		t.Fatal(err)
	}
	if r.Analysis.MinTxAgeSeconds != 30 || r.Analysis.Summary.LongTransactions != 3 {
		t.Errorf("threshold override not applied: %+v", r.Analysis)
	}
}

func TestSnapshotShowBadFiles(t *testing.T) {
	dir := t.TempDir()
	corrupt := filepath.Join(dir, "corrupt.json")
	os.WriteFile(corrupt, []byte(`{"schema_version": 1,`), 0o600)
	future := filepath.Join(dir, "future.json")
	os.WriteFile(future, []byte(`{"schema_version": 2}`), 0o600)

	for path, want := range map[string]string{
		corrupt:                         "not a valid snapshot file",
		future:                          "unsupported snapshot schema_version 2",
		filepath.Join(dir, "nope.json"): "does not exist",
	} {
		code, _, stderr := runCLI(t, "snapshot", "show", path)
		if code != ExitUsage || !strings.Contains(stderr, want) {
			t.Errorf("%s: exit %d, stderr %q (want %q)", filepath.Base(path), code, stderr, want)
		}
	}
}

func TestSnapshotSaveRefusesExistingFileBeforeConnecting(t *testing.T) {
	path := writeSnapshot(t)
	// The DSN points at a closed port: reaching the network would exit 1.
	code, _, stderr := runCLI(t, "snapshot", "save", "--out", path, "--dsn", "postgres://x@127.0.0.1:1/db")
	if code != ExitUsage || !strings.Contains(stderr, "already exists") {
		t.Fatalf("exit %d, stderr %s", code, stderr)
	}
}

func TestVersion(t *testing.T) {
	code, stdout, _ := runCLI(t, "version", "--format", "json")
	if code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	var info map[string]string
	if err := json.Unmarshal([]byte(stdout), &info); err != nil || info["version"] == "" {
		t.Fatalf("bad version JSON %q: %v", stdout, err)
	}
}

func TestBackoff(t *testing.T) {
	tests := []struct {
		interval time.Duration
		failures int
		want     time.Duration
	}{
		{2 * time.Second, 1, 2 * time.Second},
		{2 * time.Second, 2, 4 * time.Second},
		{2 * time.Second, 4, 16 * time.Second},
		{2 * time.Second, 5, 30 * time.Second},
		{2 * time.Second, 50, 30 * time.Second},
		{time.Minute, 3, time.Minute}, // interval above the cap is never shortened
	}
	for _, tt := range tests {
		if got := backoff(tt.interval, tt.failures); got != tt.want {
			t.Errorf("backoff(%s, %d) = %s, want %s", tt.interval, tt.failures, got, tt.want)
		}
	}
}

func TestIsTerminalFalseForBuffersAndFiles(t *testing.T) {
	if isTerminal(&bytes.Buffer{}) {
		t.Error("buffer is not a terminal")
	}
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if isTerminal(f) {
		t.Error("regular file is not a terminal")
	}
}

func TestWatchKeepsRetryingAndExitsCleanlyOnCancel(t *testing.T) {
	var out, errOut bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()
	// Closed port: every attempt fails with "connection refused", which is
	// retryable. Cancelling the context stands in for Ctrl+C.
	code := Main(ctx, []string{"watch", "--interval", "500ms", "--connect-timeout", "1s",
		"--dsn", "postgres://bob:TopSecret99@127.0.0.1:1/db?sslmode=disable"}, &out, &errOut)
	if code != ExitOK {
		t.Fatalf("exit %d, stderr %s", code, errOut.String())
	}
	if n := strings.Count(errOut.String(), "collection failed; retrying"); n < 2 {
		t.Errorf("expected at least 2 retries, got %d:\n%s", n, errOut.String())
	}
	if strings.Contains(errOut.String(), "TopSecret99") {
		t.Error("password leaked into logs")
	}
	if strings.Contains(errOut.String(), "locklens: error") {
		t.Error("cancellation must not be reported as an error")
	}
	if out.Len() != 0 {
		t.Errorf("no frames expected on stdout, got %q", out.String())
	}
}

func TestSnapshotShowDocumentedExample(t *testing.T) {
	code, stdout, stderr := runCLI(t, "snapshot", "show", "../../../../docs/examples/snapshot-chain.json")
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	for _, want := range []string{
		"Incident 1: root blocker pid 116 blocks 1 directly, 2 in total",
		"└─ pid 117",
		"└─ pid 118",
		"Long-running transactions (>= 1s)",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("missing %q in:\n%s", want, stdout)
		}
	}
}

func TestInterruptedDuringConnectIsReportedAsInterrupted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // as if Ctrl+C arrived before the connection was established
	var out, errOut bytes.Buffer
	code := Main(ctx, []string{"inspect", "--dsn", "postgres://x@127.0.0.1:1/db?sslmode=disable"}, &out, &errOut)
	if code != ExitRuntime || strings.TrimSpace(errOut.String()) != "locklens: error: interrupted" {
		t.Errorf("exit %d, stderr %q", code, errOut.String())
	}
}
