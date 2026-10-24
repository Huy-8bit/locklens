package snapshot

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/locklens/locklens/internal/model"
)

func sample() *model.Snapshot {
	ts := time.Date(2026, 10, 24, 8, 0, 0, 123456000, time.UTC)
	state := "active"
	return &model.Snapshot{
		SchemaVersion: model.SchemaVersion,
		CollectedAt:   ts,
		ServerTime:    ts,
		Tool:          model.Tool{Name: "locklens", Version: "test"},
		Server:        model.Server{Host: "localhost", Port: 5432, Database: "db", User: "observer", Version: "17.0", VersionNum: 170000},
		Sessions: []model.Session{{
			PID: 10, State: &state, XactStart: &ts, DetailsVisible: true,
			WaitingLocks: []model.WaitingLock{{PID: 10, LockType: "transactionid", Mode: "ShareLock", WaitStart: &ts}},
		}},
		Edges:                []model.Edge{{Blocker: 9, Waiter: 10}},
		PreparedTransactions: []model.PreparedTransaction{},
		Warnings:             []model.Warning{},
	}
}

func TestRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	want := sample()
	if err := Save(path, want, false); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip mismatch:\n got %+v\nwant %+v", got, want)
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
	}
}

func TestSaveRefusesOverwriteWithoutForce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.json")
	if err := os.WriteFile(path, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Save(path, sample(), false)
	if !errors.Is(err, ErrExists) {
		t.Fatalf("expected ErrExists, got %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != "keep me" {
		t.Error("existing file was modified")
	}
	if err := Save(path, sample(), true); err != nil {
		t.Fatalf("force: %v", err)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("forced file unreadable: %v", err)
	}
	assertNoTempFiles(t, dir)
}

func TestSaveFailureLeavesNoPartialFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing-dir")
	path := filepath.Join(dir, "s.json")
	if err := Save(path, sample(), false); err == nil {
		t.Fatal("expected error for missing directory")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Error("destination must not exist after a failed save")
	}
}

func TestSnapshotContainsNoCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	if err := Save(path, sample(), false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	for _, key := range []string{"password", "dsn", "postgres://"} {
		if strings.Contains(strings.ToLower(string(b)), key) {
			t.Errorf("snapshot contains %q", key)
		}
	}
}

func TestLoadRejectsUnsupportedVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	os.WriteFile(path, []byte(`{"schema_version": 99, "server_time": "2026-10-24T08:00:00Z"}`), 0o600)
	_, err := Load(path)
	var uv *UnsupportedVersionError
	if !errors.As(err, &uv) || uv.Found != 99 {
		t.Fatalf("expected UnsupportedVersionError(99), got %v", err)
	}
	if !strings.Contains(err.Error(), "upgrade LockLens") {
		t.Errorf("message not actionable: %v", err)
	}
}

func TestLoadRejectsMissingVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	os.WriteFile(path, []byte(`{"sessions": []}`), 0o600)
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "not a LockLens snapshot") {
		t.Fatalf("got %v", err)
	}
}

func TestLoadReportsCorruptJSONWithLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	os.WriteFile(path, []byte("{\n  \"schema_version\": 1,\n  \"sessions\": [\n}"), 0o600)
	_, err := Load(path)
	var inv *InvalidError
	if !errors.As(err, &inv) || !strings.Contains(err.Error(), "line 4") {
		t.Fatalf("expected InvalidError with line number, got %v", err)
	}
}

func TestLoadIgnoresUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	os.WriteFile(path, []byte(`{"schema_version": 1, "server_time": "2026-10-24T08:00:00Z", "future_field": {"x": 1}}`), 0o600)
	if _, err := Load(path); err != nil {
		t.Fatalf("additive fields must be accepted: %v", err)
	}
}

func assertNoTempFiles(t *testing.T, dir string) {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("temporary file left behind: %s", e.Name())
		}
	}
}
