//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/demo/scenario"
)

// closedPortDSN returns a DSN for a loopback port that nothing listens on.
func closedPortDSN(t *testing.T, password string) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return fmt.Sprintf("postgres://nobody:%s@127.0.0.1:%d/none?sslmode=disable", password, port)
}

// keyValueDSN rewrites a URL DSN in keyword/value form.
func keyValueDSN(t *testing.T, dsn, password string) string {
	t.Helper()
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("host=%s port=%d user=%s password='%s' dbname=%s sslmode=disable",
		cfg.Host, cfg.Port, cfg.User, password, cfg.Database)
}

func observerPassword(t *testing.T) string {
	cfg, err := pgx.ParseConfig(observerDSN)
	if err != nil {
		t.Fatal(err)
	}
	return cfg.Password
}

func TestHelpAndVersion(t *testing.T) {
	res := locklens(t, "", "--help")
	if res.code != 0 {
		t.Fatalf("--help exit %d", res.code)
	}
	for _, cmd := range []string{"doctor", "inspect", "watch", "snapshot", "version"} {
		if !strings.Contains(res.stdout, cmd) {
			t.Errorf("--help does not list %q", cmd)
		}
	}
	res = locklens(t, "", "inspect", "--help")
	for _, flag := range []string{"--format", "--min-tx-age", "--include-query", "--query-max-length", "--dsn", "--connect-timeout", "--query-timeout"} {
		if !strings.Contains(res.stdout, flag) {
			t.Errorf("inspect --help does not document %s", flag)
		}
	}
	if res := locklens(t, "", "help", "watch"); res.code != 0 || !strings.Contains(res.stdout, "--interval") {
		t.Errorf("help watch exit %d", res.code)
	}
	res = locklens(t, "", "version")
	if res.code != 0 || !strings.HasPrefix(res.stdout, "locklens ") {
		t.Errorf("version exit %d: %q", res.code, res.stdout)
	}
	res = locklens(t, "", "version", "--format", "json")
	var info map[string]string
	if err := json.Unmarshal([]byte(res.stdout), &info); err != nil || info["version"] == "" || info["go_version"] == "" {
		t.Errorf("version JSON %q: %v", res.stdout, err)
	}
}

func TestDSNPrecedence(t *testing.T) {
	refused := closedPortDSN(t, "cnryrefused")
	// The flag wins over the environment in both directions.
	if res := locklensOpts(t, runOpts{dsn: refused}, "inspect", "--format", "json", "--dsn", observerDSN); res.code != 0 {
		t.Errorf("--dsn must override LOCKLENS_DSN: exit %d %s", res.code, res.stderr)
	}
	if res := locklensOpts(t, runOpts{dsn: observerDSN}, "inspect", "--dsn", refused); res.code != 1 {
		t.Errorf("--dsn must override a working LOCKLENS_DSN: exit %d", res.code)
	}
	if res := locklens(t, observerDSN, "inspect", "--format", "json"); res.code != 0 {
		t.Errorf("LOCKLENS_DSN alone must work: exit %d", res.code)
	}
	// libpq variables are not a substitute for LockLens' own DSN settings.
	res := locklensOpts(t, runOpts{env: []string{"PGHOST=127.0.0.1"}}, "inspect")
	if res.code != 2 || !strings.Contains(res.stderr, "no connection string") || res.stdout != "" {
		t.Errorf("missing DSN: exit %d stdout %q stderr %q", res.code, res.stdout, res.stderr)
	}
}

func TestInvalidArgumentsExitTwoBeforeConnecting(t *testing.T) {
	cases := [][]string{
		{"inspect", "--interval", "1s"},
		{"inspect", "--format", "yaml"},
		{"inspect", "--min-tx-age", "5x"},
		{"inspect", "--min-tx-age", "-5s"},
		{"inspect", "--query-max-length", "abc"},
		{"inspect", "--query-max-length", "0"},
		{"watch", "--interval", "abc"},
		{"watch", "--interval", "100ms"},
		{"--connect-timeout", "soon", "inspect"},
		{"--query-timeout", "-1s", "inspect"},
		{"snapshot", "show", "a.json", "b.json"},
		{"snapshot", "save"},
		{"frobnicate"},
	}
	for _, args := range cases {
		res := locklens(t, observerDSN, args...)
		if res.code != 2 || res.stdout != "" || !strings.HasPrefix(res.stderr, "locklens: error:") {
			t.Errorf("%v: exit %d stdout %q stderr %q", args, res.code, res.stdout, res.stderr)
		}
	}
}

func TestConnectionRefused(t *testing.T) {
	res := locklensOpts(t, runOpts{dsn: closedPortDSN(t, "cnryrefusedpw")}, "inspect", "--format", "json")
	if res.code != 1 || res.stdout != "" || !strings.Contains(res.stderr, "hint: connection refused") {
		t.Errorf("exit %d stdout %q stderr %q", res.code, res.stdout, res.stderr)
	}
	if strings.Contains(res.stderr, "cnryrefusedpw") {
		t.Error("password leaked")
	}
}

func TestAuthenticationFailureURIAndKeyValue(t *testing.T) {
	const wrong = "cnrywrongpassword42"
	cfg, err := pgx.ParseConfig(observerDSN)
	if err != nil {
		t.Fatal(err)
	}
	uri := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable", cfg.User, wrong, cfg.Host, cfg.Port, cfg.Database)
	for name, dsn := range map[string]string{"uri": uri, "keyword/value": keyValueDSN(t, observerDSN, wrong)} {
		res := locklens(t, dsn, "inspect")
		if res.code != 1 || !strings.Contains(res.stderr, "hint: authentication failed") {
			t.Errorf("%s: exit %d stderr %q", name, res.code, res.stderr)
		}
		if strings.Contains(res.stdout+res.stderr, wrong) {
			t.Errorf("%s: password leaked", name)
		}
	}
	// watch must stop instead of retrying an authentication failure forever.
	res := locklensOpts(t, runOpts{dsn: uri, timeout: 20 * time.Second}, "watch", "--interval", "500ms")
	if res.code != 1 || strings.Count(res.stderr, "retrying") != 0 {
		t.Errorf("watch on auth failure: exit %d stderr %q", res.code, res.stderr)
	}
}

func TestKeyValueDSNWorks(t *testing.T) {
	kv := keyValueDSN(t, observerDSN, observerPassword(t))
	secrets = append(secrets, kv)
	r := inspectJSON(t, kv)
	if !r.Snapshot.Server.ReadAllStats {
		t.Error("keyword/value DSN should connect as the observer")
	}
}

func TestMalformedDSNIsNeverEchoed(t *testing.T) {
	for _, dsn := range []string{
		"postgres://user:cnrymalformed1@127.0.0.1:notaport/db",
		"host=127.0.0.1 user=x password='cnrymalformed2",
		"postgres://user:cnrymalformed3@[::1/db",
	} {
		res := locklens(t, dsn, "inspect")
		if res.code != 2 || !strings.Contains(res.stderr, "invalid connection string") {
			t.Errorf("exit %d stderr %q", res.code, res.stderr)
		}
		if strings.Contains(res.stdout+res.stderr, "cnrymalformed") {
			t.Errorf("malformed DSN echoed: %q", res.stderr)
		}
	}
}

func TestConnectTimeout(t *testing.T) {
	// A listener that accepts TCP connections but never speaks the protocol.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()
	dsn := fmt.Sprintf("postgres://x:cnrysilent@127.0.0.1:%d/db?sslmode=disable", l.Addr().(*net.TCPAddr).Port)
	res := locklens(t, dsn, "inspect", "--connect-timeout", "1s")
	if res.code != 1 || !strings.Contains(res.stderr, "timed out") {
		t.Errorf("exit %d stderr %q", res.code, res.stderr)
	}
	if res.elapsed > 6*time.Second {
		t.Errorf("connect timeout of 1s took %s", res.elapsed)
	}
}

// TestCollectionTimeouts blocks LockLens' own catalog read by holding an
// ACCESS EXCLUSIVE lock on the pg_locks view in a superuser transaction.
func TestCollectionTimeouts(t *testing.T) {
	ctx := testContext(t)
	adm := admin(t)
	tx, err := adm.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(ctx, "LOCK TABLE pg_catalog.pg_locks IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatalf("lock pg_locks: %v", err)
	}

	// statement_timeout / context deadline (1s) fires before lock_timeout (2s).
	res := locklens(t, observerDSN, "inspect", "--query-timeout", "1s")
	if res.code != 1 || !(strings.Contains(res.stderr, "statement_timeout") || strings.Contains(res.stderr, "timed out")) {
		t.Errorf("query timeout: exit %d stderr %q", res.code, res.stderr)
	}
	if res.elapsed > 8*time.Second {
		t.Errorf("1s query timeout took %s", res.elapsed)
	}

	// With the default 10s query timeout, LockLens' 2s lock_timeout fires.
	res = locklens(t, observerDSN, "inspect")
	if res.code != 1 || !strings.Contains(res.stderr, "lock_timeout") {
		t.Errorf("lock timeout: exit %d stderr %q", res.code, res.stderr)
	}
	if res.elapsed > 8*time.Second {
		t.Errorf("lock_timeout path took %s", res.elapsed)
	}

	// doctor reports the unreadable source as a failed check.
	res = locklens(t, observerDSN, "doctor", "--format", "json")
	if res.code != 1 || !strings.Contains(res.stdout, `"name": "read pg_locks"`) || !strings.Contains(res.stdout, `"status": "fail"`) {
		t.Errorf("doctor with pg_locks blocked: exit %d\n%s", res.code, res.stdout)
	}
}

func TestStdoutCarriesOnlyJSON(t *testing.T) {
	res := locklens(t, observerDSN, "inspect", "--format", "json", "--log-level", "debug")
	if res.code != 0 {
		t.Fatal(res.stderr)
	}
	dec := json.NewDecoder(strings.NewReader(res.stdout))
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("stdout is not JSON: %v", err)
	}
	if dec.More() {
		t.Error("stdout holds more than one JSON document")
	}
	doc := locklens(t, observerDSN, "doctor", "--format", "json")
	if err := json.Unmarshal([]byte(doc.stdout), &v); err != nil {
		t.Errorf("doctor stdout is not JSON: %v", err)
	}
}

// TestUnicodeAndControlCharacters stores hostile query text in
// pg_stat_activity and checks every output format.
func TestUnicodeAndControlCharacters(t *testing.T) {
	ctx := testContext(t)
	s, err := scenario.New(ctx, "unicode", demoDSN, tableName(t))
	if err != nil {
		t.Fatal(err)
	}
	closeScenario(t, s)
	a, err := s.Begin(ctx, "A")
	if err != nil {
		t.Fatal(err)
	}
	canary := fmt.Sprintf("cnryq%d", time.Now().UnixNano())
	payload := canary + " \x1b[2J\x1b]0;owned\x07 \u202Eevil naïve café 日本語 👍"
	query := "SELECT $q$" + payload + "$q$"
	if err := s.Exec(ctx, a, query); err != nil {
		t.Fatal(err)
	}
	if err := s.WaitIdleInTransaction(ctx, a.PID, 0); err != nil {
		t.Fatal(err)
	}

	table := locklens(t, observerDSN, "inspect", "--include-query", "--min-tx-age", "0s", "--query-max-length", "500")
	for _, raw := range []string{"\x1b", "\x07", "\u202E"} {
		if strings.Contains(table.stdout, raw) {
			t.Errorf("table output contains raw %q", raw)
		}
	}
	for _, want := range []string{canary, `\x1b[2J`, `\x07`, `\u202e`, "naïve café 日本語 👍"} {
		if !strings.Contains(table.stdout, want) {
			t.Errorf("table output lacks %q:\n%s", want, table.stdout)
		}
	}

	r := inspectJSON(t, observerDSN, "--include-query", "--min-tx-age", "0s", "--query-max-length", "500")
	sa := session(t, r, a.PID)
	if sa.Query == nil || *sa.Query != query || sa.QueryTruncated {
		t.Errorf("JSON must carry the exact query text, got %q", deref(sa.Query))
	}

	// Rune-based truncation of multi-byte text.
	r = inspectJSON(t, observerDSN, "--include-query", "--min-tx-age", "0s", "--query-max-length", "31")
	sa = session(t, r, a.PID)
	want := string([]rune(query)[:31])
	if sa.Query == nil || *sa.Query != want || !sa.QueryTruncated || !utf8.ValidString(*sa.Query) {
		t.Errorf("truncated query = %q (truncated=%v), want %q", deref(sa.Query), sa.QueryTruncated, want)
	}
	// Cut right after the first CJK character (three bytes in UTF-8).
	n := strings.Index(query, "日本語")
	limit := utf8.RuneCountInString(query[:n]) + 1
	cut := inspectJSON(t, observerDSN, "--include-query", "--min-tx-age", "0s", "--query-max-length", fmt.Sprint(limit))
	if q := deref(session(t, cut, a.PID).Query); !strings.HasSuffix(q, "日") || utf8.RuneCountInString(q) != limit || !utf8.ValidString(q) {
		t.Errorf("cut inside multi-byte text gave %q", q)
	}
}

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}
