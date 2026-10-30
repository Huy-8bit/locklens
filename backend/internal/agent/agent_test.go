package agent

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/locklens/locklens/internal/domain/logs"
)

// fakeServer records the events it receives; down makes it fail.
type fakeServer struct {
	mu     sync.Mutex
	events []logs.Event
	down   atomic.Bool
	auth   string
}

func (f *fakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if f.down.Load() {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	var b logs.Batch
	body, _ := io.ReadAll(r.Body)
	if err := json.Unmarshal(body, &b); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.auth = r.Header.Get("Authorization")
	f.events = append(f.events, b.Events...)
	f.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

func (f *fakeServer) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.events)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func appendLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, l := range lines {
		if _, err := f.WriteString(l + "\n"); err != nil {
			t.Fatal(err)
		}
	}
}

func line(n int, msg string) string {
	return time.Now().UTC().Format("2006-01-02 15:04:05.000") + " UTC [" + string(rune('0'+n)) + "] " + msg
}

func TestAgentShipsSpoolsAndResumes(t *testing.T) {
	srv := &fakeServer{}
	ts := httptest.NewServer(srv)
	defer ts.Close()
	dir, state := t.TempDir(), t.TempDir()
	logFile := filepath.Join(dir, "postgresql.log")
	appendLines(t, logFile, line(1, "LOG:  duration: 99.0 ms  statement: select 'old'")) // before the first start: skipped

	cfg := Config{Server: ts.URL, Token: "llk_x", Paths: []string{dir}, StateDir: state, FlushEvery: 50 * time.Millisecond,
		Hostname: "db1", Version: "test", Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	run := func() (context.CancelFunc, chan error) {
		a, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		a.every = 20 * time.Millisecond
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- a.Run(ctx) }()
		return cancel, done
	}
	cancel, done := run()
	time.Sleep(100 * time.Millisecond)
	appendLines(t, logFile,
		line(2, "LOG:  duration: 1520.5 ms  statement: SELECT * FROM users WHERE email = 'alice@example.com'"),
		line(3, "LOG:  connection received: host=10.0.0.1"),
		line(4, "ERROR:  deadlock detected"))
	waitFor(t, "two events", func() bool { return srv.count() == 2 })
	if srv.auth != "Bearer llk_x" {
		t.Fatalf("auth header %q", srv.auth)
	}
	for _, e := range srv.events {
		if strings.Contains(e.Query, "alice") || strings.Contains(e.Query, "old") {
			t.Fatalf("event %+v", e)
		}
	}

	// The server is down: batches are spooled, then sent when it is back.
	srv.down.Store(true)
	appendLines(t, logFile, line(5, "LOG:  duration: 2.0 ms  statement: select 2"))
	waitFor(t, "a spooled batch", func() bool { m, _ := filepath.Glob(filepath.Join(state, "spool", "*.json")); return len(m) == 1 })
	srv.down.Store(false)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	// After a restart: the spool is sent and reading continues after the
	// last position (nothing is sent twice).
	cancel, done = run()
	defer func() { cancel(); <-done }()
	appendLines(t, logFile, line(6, "LOG:  duration: 3.0 ms  statement: select 3"))
	waitFor(t, "four events", func() bool { return srv.count() == 4 })
	time.Sleep(150 * time.Millisecond)
	if n := srv.count(); n != 4 {
		t.Fatalf("events sent again: %d", n)
	}
}

func TestConfigChecks(t *testing.T) {
	base := Config{Server: "https://ll.example.com", Token: "t", Paths: []string{"/x"}, StateDir: t.TempDir()}
	for name, mod := range map[string]func(*Config){
		"plain http to a remote server": func(c *Config) { c.Server = "http://ll.example.com" },
		"no token":                      func(c *Config) { c.Token = "" },
		"unknown format":                func(c *Config) { c.Format = "xml" },
		"bad prefix":                    func(c *Config) { c.Format, c.Prefix = "stderr", "%Z" },
	} {
		c := base
		mod(&c)
		if _, err := New(c); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	local := base
	local.Server = "http://127.0.0.1:8080"
	if _, err := New(local); err != nil {
		t.Fatalf("http to a local server is allowed: %v", err)
	}
}
