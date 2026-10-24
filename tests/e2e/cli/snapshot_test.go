//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/locklens/locklens/demo/scenario"
	"github.com/locklens/locklens/internal/model"
)

// assertFileHasNoSecrets checks a file for every known password and DSN.
func assertFileHasNoSecrets(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range secrets {
		if s != "" && bytes.Contains(raw, []byte(s)) {
			t.Errorf("%s contains a password or DSN", filepath.Base(path))
		}
	}
	for _, marker := range []string{"postgres://", "password"} {
		if bytes.Contains(bytes.ToLower(raw), []byte(marker)) {
			t.Errorf("%s contains %q", filepath.Base(path), marker)
		}
	}
	return raw
}

func TestSnapshotSaveAndShowOffline(t *testing.T) {
	s, err := scenario.Chain(testContext(t), demoDSN, tableName(t))
	if err != nil {
		t.Fatal(err)
	}
	a, b, c := s.ByLabel("A").PID, s.ByLabel("B").PID, s.ByLabel("C").PID
	path := filepath.Join(t.TempDir(), "incident.json")

	res := locklens(t, observerDSN, "snapshot", "save", "--out", path, "--min-tx-age", "1s")
	if res.code != 0 || !strings.Contains(res.stdout, "Snapshot saved to") {
		t.Fatalf("save exit %d: %s", res.code, res.stderr)
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	raw := assertFileHasNoSecrets(t, path)
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var snap model.Snapshot
	if err := dec.Decode(&snap); err != nil {
		t.Fatalf("snapshot file does not match the documented schema: %v", err)
	}
	if snap.SchemaVersion != 1 || snap.CollectedAt.Location() != time.UTC || snap.Collection.Atomic ||
		snap.Collection.IncludeQuery || snap.Collection.MinTxAgeSeconds != 1 {
		t.Errorf("unexpected snapshot header: %+v %+v", snap.Collection, snap.CollectedAt)
	}
	if !strings.Contains(string(raw), `"collected_at": "`) || !strings.Contains(string(raw), `Z"`) {
		t.Error("timestamps must be RFC 3339 UTC")
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("snapshot mode %v, want 0600", fi.Mode().Perm())
	}

	// No overwrite without --force; the file is left untouched.
	before, _ := os.ReadFile(path)
	if res := locklens(t, observerDSN, "snapshot", "save", "--out", path); res.code != 2 || !strings.Contains(res.stderr, "--force") {
		t.Errorf("overwrite without --force: exit %d %s", res.code, res.stderr)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
		t.Error("existing snapshot was modified without --force")
	}

	// Offline: the environment points at a closed port, and nothing connects.
	offline := closedPortDSN(t, "cnryoffline")
	show := locklensOpts(t, runOpts{dsn: offline}, "snapshot", "show", path)
	if show.code != 0 || !strings.Contains(show.stdout, fmt.Sprintf("root blocker pid %d blocks 1 directly, 2 in total", a)) {
		t.Errorf("snapshot show exit %d:\n%s\n%s", show.code, show.stdout, show.stderr)
	}
	showJSON := locklensOpts(t, runOpts{dsn: offline}, "snapshot", "show", path, "--format", "json")
	r := decodeReport(t, []byte(showJSON.stdout))
	if !hasEdge(r, a, b) || !hasEdge(r, b, c) || node(t, r, a).TransitiveBlocked != 2 {
		t.Errorf("offline analysis differs: %+v", r.Analysis.Edges)
	}
	if r.Analysis.MinTxAgeSeconds != 1 {
		t.Errorf("stored threshold not used: %v", r.Analysis.MinTxAgeSeconds)
	}

	if res := locklens(t, observerDSN, "snapshot", "save", "--out", path, "--force"); res.code != 0 {
		t.Errorf("save --force: exit %d %s", res.code, res.stderr)
	}
}

func TestSnapshotRejectsBadFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cases := map[string]string{
		write("truncated.json", "{\n  \"schema_version\": 1,\n  \"sessions\": ["):                               "not a valid snapshot file",
		write("future.json", `{"schema_version": 2, "server_time": "2026-01-01T00:00:00Z"}`):                    "unsupported snapshot schema_version 2",
		write("other.json", `{"hello": "world"}`):                                                               "schema_version is missing",
		write("wrongtype.json", `{"schema_version": 1, "server_time": "2026-01-01T00:00:00Z", "sessions": {}}`): "wrong type",
		filepath.Join(dir, "absent.json"):                                                                       "does not exist",
		dir:                                                                                                     "is a directory, not a snapshot file",
	}
	for path, want := range cases {
		res := locklens(t, "", "snapshot", "show", path)
		if res.code != 2 || res.stdout != "" || !strings.Contains(res.stderr, want) {
			t.Errorf("%s: exit %d stdout %q stderr %q (want %q)", filepath.Base(path), res.code, res.stdout, res.stderr, want)
		}
	}
}

func TestSnapshotFailedWriteLeavesNothing(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission checks do not apply to root")
	}
	dir := filepath.Join(t.TempDir(), "readonly")
	if err := os.Mkdir(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	dest := filepath.Join(dir, "snap.json")
	res := locklens(t, observerDSN, "snapshot", "save", "--out", dest)
	if res.code != 1 || !strings.Contains(res.stderr, "save snapshot") || !strings.Contains(res.stderr, "permission denied") {
		t.Errorf("read-only directory: exit %d stderr %q", res.code, res.stderr)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("failed write left files behind: %v", entries)
	}

	missing := filepath.Join(t.TempDir(), "no-such-dir", "snap.json")
	res = locklens(t, observerDSN, "snapshot", "save", "--out", missing)
	if res.code != 1 || !strings.Contains(res.stderr, "no such file or directory") {
		t.Errorf("missing directory: exit %d stderr %q", res.code, res.stderr)
	}
	if _, err := os.Stat(missing); err == nil {
		t.Error("destination exists after a failed write")
	}
}
