//go:build integration

package integration

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/locklens/locklens/demo/scenario"
)

// syncBuffer is a goroutine-safe buffer for subprocess stderr.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// startWatch runs `locklens watch` with stdout on a pipe (never a TTY).
func startWatch(t *testing.T, dsn string, args ...string) (*exec.Cmd, *bufio.Reader, *syncBuffer) {
	t.Helper()
	cmd := exec.Command(binary, append([]string{"watch"}, args...)...)
	cmd.Env = cleanEnv("LOCKLENS_DSN=" + dsn)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr := &syncBuffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	return cmd, bufio.NewReaderSize(stdout, 1<<20), stderr
}

// interruptAndWait sends SIGINT and requires a clean exit (status 0) within
// 10 seconds. Readers passed in drain are drained here; otherwise the caller
// must keep reading stdout.
func interruptAndWait(t *testing.T, cmd *exec.Cmd, drain ...*bufio.Reader) {
	t.Helper()
	signalAndWait(t, cmd, syscall.SIGINT, drain...)
}

func signalAndWait(t *testing.T, cmd *exec.Cmd, sig syscall.Signal, drain ...*bufio.Reader) {
	t.Helper()
	if err := cmd.Process.Signal(sig); err != nil {
		t.Fatal(err)
	}
	for _, r := range drain {
		go func() { _, _ = r.WriteTo(io.Discard) }()
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("watch did not exit cleanly after %v: %v", sig, err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("watch did not exit within 10s of %v", sig)
	}
}

// readLine reads one line with a timeout.
func readLine(t *testing.T, r *bufio.Reader, timeout time.Duration) []byte {
	t.Helper()
	type res struct {
		line []byte
		err  error
	}
	ch := make(chan res, 1)
	go func() {
		line, err := r.ReadBytes('\n')
		ch <- res{line, err}
	}()
	select {
	case got := <-ch:
		if got.err != nil {
			t.Fatalf("read watch output: %v", got.err)
		}
		return got.line
	case <-time.After(timeout):
		t.Fatalf("no watch output within %s", timeout)
		return nil
	}
}

func TestWatchNDJSONAndInterrupt(t *testing.T) {
	s, err := scenario.RowLock(testContext(t), demoDSN, tableName(t))
	if err != nil {
		t.Fatal(err)
	}
	closeScenario(t, s)
	a, b := s.ByLabel("A").PID, s.ByLabel("B").PID

	cmd, out, stderr := startWatch(t, observerDSN, "--format", "json", "--interval", "500ms")
	var prev time.Time
	for i := range 5 {
		line := readLine(t, out, 10*time.Second)
		checkOutput(t, []string{"watch"}, string(line))
		if bytes.Count(line, []byte("\n")) != 1 {
			t.Fatalf("frame %d spans several lines", i)
		}
		r := decodeReport(t, line)
		if !hasEdge(r, a, b) {
			t.Errorf("frame %d lacks edge %d -> %d", i, a, b)
		}
		if !r.Snapshot.CollectedAt.After(prev) {
			t.Errorf("frame %d is not newer than the previous one", i)
		}
		prev = r.Snapshot.CollectedAt
	}
	interruptAndWait(t, cmd, out)
	if got := stderr.String(); got != "" {
		t.Errorf("Ctrl+C must not print anything on stderr, got %q", got)
	}
}

func TestWatchRedirectedTableHasNoEscapeCodes(t *testing.T) {
	cmd, out, stderr := startWatch(t, observerDSN, "--interval", "500ms")
	var buf bytes.Buffer
	for !strings.Contains(buf.String(), "=== frame 3 ===") {
		buf.Write(readLine(t, out, 10*time.Second))
	}
	// SIGTERM (as sent by service managers) is handled like Ctrl+C.
	signalAndWait(t, cmd, syscall.SIGTERM, out)
	if strings.Contains(buf.String(), "\x1b") {
		t.Error("escape codes written to a non-terminal")
	}
	if stderr.String() != "" {
		t.Errorf("unexpected stderr %q", stderr.String())
	}
	checkOutput(t, []string{"watch"}, buf.String()+stderr.String())
}

// locklensBackends counts LockLens backends and how many of them are inside a
// statement or transaction right now.
func locklensBackends(t *testing.T, ctx context.Context, query func(context.Context, string, ...any) (int, int, error)) (int, int) {
	t.Helper()
	total, busy, err := query(ctx, `
SELECT count(*), count(*) FILTER (WHERE state <> 'idle')
FROM pg_stat_activity WHERE application_name = 'locklens'`)
	if err != nil {
		t.Fatal(err)
	}
	return total, busy
}

func TestWatchConnectionsBoundedWithoutOverlap(t *testing.T) {
	ctx := testContext(t)
	adm := admin(t)
	query := func(ctx context.Context, sql string, args ...any) (int, int, error) {
		var a, b int
		err := adm.QueryRow(ctx, sql, args...).Scan(&a, &b)
		return a, b, err
	}
	if total, _ := locklensBackends(t, ctx, query); total != 0 {
		t.Fatalf("%d LockLens connections exist before the test", total)
	}

	cmd, out, stderr := startWatch(t, observerDSN, "--format", "json", "--interval", "500ms")
	frames := make(chan struct{}, 1000)
	go func() {
		sc := bufio.NewScanner(out)
		sc.Buffer(make([]byte, 1<<20), 1<<24)
		for sc.Scan() {
			frames <- struct{}{}
		}
	}()

	maxTotal, maxBusy, samples := 0, 0, 0
	end := time.Now().Add(6 * time.Second)
	for time.Now().Before(end) {
		total, busy := locklensBackends(t, ctx, query)
		maxTotal, maxBusy = max(maxTotal, total), max(maxBusy, busy)
		samples++
		time.Sleep(20 * time.Millisecond)
	}
	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("watch exit: %v (stderr %s)", err, stderr.String())
	}
	t.Logf("sampled pg_stat_activity %d times: max LockLens connections %d, max busy %d, frames %d",
		samples, maxTotal, maxBusy, len(frames))
	if maxTotal > 2 {
		t.Errorf("watch opened %d connections; the pool allows at most 2", maxTotal)
	}
	if maxBusy > 1 {
		t.Errorf("%d LockLens connections were busy at once: collections overlapped", maxBusy)
	}
	if len(frames) < 8 {
		t.Errorf("expected about 12 frames in 6s at 500ms, got %d", len(frames))
	}
	// After exit every connection is closed.
	deadline := time.Now().Add(5 * time.Second)
	for {
		total, _ := locklensBackends(t, ctx, query)
		if total == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d LockLens connections remain after watch exited", total)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestWatchSurvivesTemporaryOutage pauses the PostgreSQL container of this
// run's isolated Compose project, then resumes it.
func TestWatchSurvivesTemporaryOutage(t *testing.T) {
	cmd, out, stderr := startWatch(t, observerDSN, "--format", "json", "--interval", "500ms",
		"--query-timeout", "2s", "--connect-timeout", "2s")
	lines := make(chan []byte, 1000)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(out)
		sc.Buffer(make([]byte, 1<<20), 1<<24)
		for sc.Scan() {
			lines <- append([]byte(nil), sc.Bytes()...)
		}
	}()
	waitFrame := func(timeout time.Duration) []byte {
		t.Helper()
		select {
		case l, ok := <-lines:
			if !ok {
				t.Fatalf("watch stdout closed; stderr:\n%s", stderr.String())
			}
			return l
		case <-time.After(timeout):
			t.Fatalf("no frame within %s; stderr:\n%s", timeout, stderr.String())
			return nil
		}
	}
	waitStderr := func(substr string, timeout time.Duration) {
		t.Helper()
		deadline := time.Now().Add(timeout)
		for !strings.Contains(stderr.String(), substr) {
			if time.Now().After(deadline) {
				t.Fatalf("stderr never contained %q:\n%s", substr, stderr.String())
			}
			time.Sleep(50 * time.Millisecond)
		}
	}

	decodeReport(t, waitFrame(10*time.Second))

	compose(t, "pause", "postgres")
	paused := true
	t.Cleanup(func() {
		if paused {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_ = exec.CommandContext(ctx, "docker", "compose", "-p", composeProject, "-f", composeFile,
				"--env-file", composeEnvFile, "unpause", "postgres").Run()
		}
	})
	// Keep the server paused long enough to observe the documented backoff:
	// interval × 2^(n-1), i.e. 500ms, 1s, 2s for attempts 1 to 3.
	waitStderr("attempt=3 ", 60*time.Second)
	for _, want := range []string{"attempt=1 retry_in=500ms", "attempt=2 retry_in=1s", "attempt=3 retry_in=2s"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr.String())
		}
	}
	// Drain frames produced before the outage was noticed.
	for len(lines) > 0 {
		<-lines
	}
	if cmd.ProcessState != nil {
		t.Fatal("watch exited during the outage")
	}
	compose(t, "unpause", "postgres")
	paused = false

	waitStderr("collection recovered", 60*time.Second)
	for range 2 {
		decodeReport(t, waitFrame(15*time.Second))
	}
	// Connections stay bounded after the outage. Startup packets queued
	// while the server was frozen may create short-lived backends, so the
	// count is sampled for a while and the last sample must be within bounds.
	adm := admin(t)
	var last, peak int
	for range 40 {
		if err := adm.QueryRow(context.Background(),
			"SELECT count(*) FROM pg_stat_activity WHERE application_name = 'locklens'").Scan(&last); err != nil {
			t.Fatal(err)
		}
		peak = max(peak, last)
		time.Sleep(50 * time.Millisecond)
	}
	t.Logf("LockLens connections after recovery: peak %d, last %d", peak, last)
	if last > 2 {
		t.Errorf("%d LockLens connections after recovery; the pool allows 2", last)
	}
	interruptAndWait(t, cmd)
	for l := range lines {
		decodeReport(t, l)
	}

	// Diagnostics went to stderr as log records; stdout held only JSON.
	for _, line := range strings.Split(strings.TrimSpace(stderr.String()), "\n") {
		if !strings.Contains(line, "level=") {
			t.Errorf("unexpected stderr line %q", line)
		}
	}
	checkOutput(t, []string{"watch"}, stderr.String())
	t.Logf("stderr during outage:\n%s", stderr.String())
}
