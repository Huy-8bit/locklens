package render

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/locklens/locklens/internal/model"
	"github.com/locklens/locklens/internal/report"
)

var now = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func ptr[T any](v T) *T { return &v }

func sess(pid int, state string) model.Session {
	return model.Session{
		PID: pid, State: ptr(state), BackendType: ptr("client backend"), DetailsVisible: true,
		User: ptr("app"), ApplicationName: ptr("svc"), XactStart: ptr(now.Add(-time.Minute)),
		WaitingLocks: []model.WaitingLock{},
	}
}

func snap(sessions []model.Session, edges ...model.Edge) *model.Snapshot {
	return &model.Snapshot{
		SchemaVersion: model.SchemaVersion, ServerTime: now, CollectedAt: now,
		Server:   model.Server{Host: "localhost", Port: 5432, Database: "db", User: "observer", Version: "17.0"},
		Sessions: sessions, Edges: edges,
		Collection: model.Collection{QueryMaxLength: 50},
	}
}

func table(t *testing.T, s *model.Snapshot) string {
	t.Helper()
	var buf bytes.Buffer
	if err := Table(&buf, report.Build(s, time.Minute)); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestTableNoBlocking(t *testing.T) {
	out := table(t, snap([]model.Session{}))
	if !strings.Contains(out, "No blocking detected") {
		t.Errorf("missing no-blocking message:\n%s", out)
	}
}

func TestTableSanitisesServerStrings(t *testing.T) {
	evil := sess(1, "idle in transaction")
	evil.ApplicationName = ptr("x\x1b]0;owned\x07\x1b[2J")
	evil.User = ptr("u\u202Eser")
	waiter := sess(2, "active")
	s := snap([]model.Session{evil, waiter}, model.Edge{Blocker: 1, Waiter: 2})
	s.Collection.IncludeQuery = true
	evil.Query = ptr("select '\x1b[31mred'")
	s.Sessions[0].Query = evil.Query
	out := table(t, s)
	for _, bad := range []string{"\x1b", "\x07", "\u202E"} {
		if strings.Contains(out, bad) {
			t.Fatalf("output contains raw control character %q:\n%q", bad, out)
		}
	}
}

func TestTableSharedDescendantPrintedOnce(t *testing.T) {
	// 1 → 2 → 4 and 1 → 3 → 4: node 4 is shared.
	s := snap([]model.Session{sess(1, "idle in transaction"), sess(2, "active"), sess(3, "active"), sess(4, "active")},
		model.Edge{Blocker: 1, Waiter: 2}, model.Edge{Blocker: 1, Waiter: 3},
		model.Edge{Blocker: 2, Waiter: 4}, model.Edge{Blocker: 3, Waiter: 4})
	out := table(t, s)
	if !strings.Contains(out, "pid 4 (shown above)") {
		t.Errorf("shared node not referenced:\n%s", out)
	}
	if !strings.Contains(out, "blocks 2 directly, 3 in total") {
		t.Errorf("root counts missing:\n%s", out)
	}
}

func TestTableCycleTerminates(t *testing.T) {
	s := snap([]model.Session{sess(1, "active"), sess(2, "active"), sess(3, "active")},
		model.Edge{Blocker: 1, Waiter: 2}, model.Edge{Blocker: 2, Waiter: 3}, model.Edge{Blocker: 3, Waiter: 1})
	out := table(t, s)
	if !strings.Contains(out, "CYCLE among PIDs 1,2,3") || !strings.Contains(out, "cycle back to an ancestor") {
		t.Errorf("cycle not rendered:\n%s", out)
	}
	if strings.Contains(out, ": root blocker") {
		t.Errorf("a cycle must not get a root blocker:\n%s", out)
	}
}

func TestTablePlaceholderAndPreparedNodes(t *testing.T) {
	s := snap([]model.Session{sess(10, "active")}, model.Edge{Blocker: 0, Waiter: 10}, model.Edge{Blocker: 77, Waiter: 10})
	out := table(t, s)
	if !strings.Contains(out, "prepared transaction (PID 0)") || !strings.Contains(out, "pid 77  [no pg_stat_activity row") {
		t.Errorf("special nodes not rendered:\n%s", out)
	}
}

func TestTableUnknownValues(t *testing.T) {
	hidden := model.Session{PID: 5, DetailsVisible: false, WaitingLocks: []model.WaitingLock{}}
	s := snap([]model.Session{sess(1, "active"), hidden}, model.Edge{Blocker: 1, Waiter: 5})
	out := table(t, s)
	if !strings.Contains(out, "[details hidden by permissions]") || !strings.Contains(out, "unknown") {
		t.Errorf("hidden session not marked:\n%s", out)
	}
}

func TestQueryTextOnlyWhenIncluded(t *testing.T) {
	a := sess(1, "idle in transaction")
	a.Query = ptr("UPDATE accounts SET secret = 'x'")
	s := snap([]model.Session{a, sess(2, "active")}, model.Edge{Blocker: 1, Waiter: 2})
	if out := table(t, s); strings.Contains(out, "Query text") || strings.Contains(out, "UPDATE accounts") {
		t.Errorf("query text shown without opt-in:\n%s", out)
	}
	s.Collection.IncludeQuery = true
	if out := table(t, s); !strings.Contains(out, "pid 1: UPDATE accounts SET secret = 'x'") {
		t.Errorf("query text missing with opt-in:\n%s", out)
	}
}

func TestDescribeLock(t *testing.T) {
	tests := []struct {
		lock model.WaitingLock
		want string
	}{
		{model.WaitingLock{LockType: "relation", Mode: "AccessExclusiveLock", Relation: ptr("public.t")},
			"AccessExclusiveLock on relation public.t"},
		{model.WaitingLock{LockType: "relation", Mode: "RowExclusiveLock", RelationOID: ptr(uint32(16384)), Database: ptr("other")},
			"RowExclusiveLock on relation relation OID 16384 in database other"},
		{model.WaitingLock{LockType: "tuple", Mode: "ExclusiveLock", Page: ptr(int32(0)), Tuple: ptr(int16(3)), Relation: ptr("t")},
			"ExclusiveLock on tuple (0,3) of t"},
		{model.WaitingLock{LockType: "transactionid", Mode: "ShareLock", TransactionID: ptr("123")},
			"ShareLock on transaction 123 (typically a row lock held by that transaction)"},
		{model.WaitingLock{LockType: "advisory", Mode: "ExclusiveLock", ClassID: ptr(uint32(0)), ObjID: ptr(uint32(42)), ObjSubID: ptr(int16(1))},
			"ExclusiveLock on advisory lock (0, 42, 1)"},
	}
	for _, tt := range tests {
		if got := DescribeLock(tt.lock); got != tt.want {
			t.Errorf("got %q, want %q", got, tt.want)
		}
	}
}

func TestDuration(t *testing.T) {
	tests := map[float64]string{0.25: "250ms", 42: "42s", 187: "3m07s", 7500: "2h05m", 3*86400 + 4*3600: "3d04h"}
	for in, want := range tests {
		if got := Duration(in); got != want {
			t.Errorf("Duration(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestNDJSONLineIsSingleLine(t *testing.T) {
	a := sess(1, "active")
	a.Query = ptr("select 1;\nselect 2;")
	r := report.Build(snap([]model.Session{a}), time.Minute)
	var buf bytes.Buffer
	if err := NDJSONLine(&buf, r); err != nil {
		t.Fatal(err)
	}
	if strings.Count(buf.String(), "\n") != 1 {
		t.Fatalf("expected exactly one line, got %q", buf.String())
	}
	var decoded map[string]any
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
}
