package logs

import (
	"strings"
	"testing"
	"time"

	"github.com/locklens/locklens/internal/lib/pglog"
)

func rec(sev, msg string) pglog.Record {
	return pglog.Record{Time: time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC), PID: 10, Database: "shop", User: "app", Severity: sev, Message: msg}
}

func TestSlowStatementIsNormalised(t *testing.T) {
	e, ok := Classify(rec("LOG", "duration: 1520.5 ms  statement: SELECT * FROM users WHERE email = 'alice@example.com'"))
	if !ok || e.Kind != KindSlowQuery || *e.DurationMS != 1520.5 || e.Fingerprint == "" || e.ID == "" {
		t.Fatalf("%+v", e)
	}
	if strings.Contains(e.Query, "alice") || strings.Contains(e.Message, "alice") {
		t.Fatalf("constants must not be kept: %+v", e)
	}
	e2, _ := Classify(rec("LOG", "duration: 3.0 ms  execute S_1: SELECT * FROM users WHERE email = $1"))
	if e2.Details["phase"] != "execute" || e2.Kind != KindSlowQuery {
		t.Fatalf("%+v", e2)
	}
}

func TestAutoExplainPlans(t *testing.T) {
	text := "duration: 812.1 ms  plan:\nQuery Text: SELECT id\n  FROM orders WHERE customer_id = 42\nSeq Scan on orders  (cost=0.00..1.00 rows=1 width=4)\n  Filter: (customer_id = 42)"
	e, ok := Classify(rec("LOG", text))
	if !ok || e.Kind != KindPlan || !strings.Contains(e.Query, "FROM orders") || strings.Contains(e.Query, "42") {
		t.Fatalf("text plan %+v", e)
	}
	if p := e.Details["plan"].(string); !strings.HasPrefix(p, "Seq Scan on orders") || strings.Contains(p, "42") {
		t.Fatalf("plan %q", p)
	}
	js := `duration: 5.0 ms  plan:
{"Query Text": "select * from t where name = 'bob'", "Plan": {"Node Type": "Seq Scan", "Relation Name": "t", "Filter": "(name = 'bob'::text)", "Plans": []}}`
	e, ok = Classify(rec("LOG", js))
	plan := e.Details["plan"].(map[string]any)["Plan"].(map[string]any)
	if !ok || strings.Contains(e.Query, "bob") || plan["Filter"] != "(name = '?'::text)" || plan["Relation Name"] != "t" {
		t.Fatalf("json plan %+v %+v", e, plan)
	}
}

func TestLocksAndDeadlocks(t *testing.T) {
	r := rec("LOG", "process 4242 still waiting for ShareLock on transaction 731 after 1000.072 ms")
	r.Detail = "Process holding the lock: 4243. Wait queue: 4242, 4250."
	r.Statement = "UPDATE accounts SET balance = 0 WHERE id = 7"
	e, ok := Classify(r)
	if !ok || e.Kind != KindLockWait || *e.DurationMS != 1000.072 || e.Details["acquired"] != false ||
		len(e.Details["holders"].([]int)) != 1 || len(e.Details["waiters"].([]int)) != 2 || strings.Contains(e.Query, "= 7") {
		t.Fatalf("lock wait %+v", e)
	}
	d := rec("ERROR", "deadlock detected")
	d.SQLState = "40P01"
	d.Detail = "Process 1 waits for ShareLock on transaction 9; blocked by process 2.\nProcess 2 waits for ShareLock on transaction 8; blocked by process 1.\nProcess 1: UPDATE t SET v = 'x' WHERE id = 1\nProcess 2: UPDATE t SET v = 'y' WHERE id = 2"
	e, ok = Classify(d)
	if !ok || e.Kind != KindDeadlock || len(e.Details["cycle"].([]map[string]any)) != 2 {
		t.Fatalf("deadlock %+v", e)
	}
	for _, s := range e.Details["statements"].(map[string]string) {
		if strings.Contains(s, "'x'") || strings.Contains(s, "'y'") {
			t.Fatalf("deadlock statements keep constants: %v", e.Details["statements"])
		}
	}
}

func TestMaintenanceAndErrors(t *testing.T) {
	v, ok := Classify(rec("LOG", "automatic vacuum of table \"shop.public.orders\": index scans: 1\npages: 0 removed, 10 remain\nsystem usage: CPU: user: 0.01 s, system: 0.00 s, elapsed: 2.50 s"))
	if !ok || v.Kind != KindAutovacuum || v.Details["table"] != "shop.public.orders" || *v.DurationMS != 2500 {
		t.Fatalf("vacuum %+v", v)
	}
	tf, ok := Classify(rec("LOG", `temporary file: path "base/pgsql_tmp/pgsql_tmp10.0", size 104857600`))
	if !ok || tf.Kind != KindTempFile || tf.Details["bytes"] != int64(104857600) {
		t.Fatalf("temp %+v", tf)
	}
	ck, ok := Classify(rec("LOG", "checkpoint complete: wrote 120 buffers (0.7%); 0 WAL file(s) added, 0 removed, 1 recycled; write=11.9 s, sync=0.01 s, total=12.0 s; sync files=5"))
	if !ok || ck.Kind != KindCheckpoint || *ck.DurationMS != 12000 {
		t.Fatalf("checkpoint %+v", ck)
	}
	er, ok := Classify(rec("ERROR", `duplicate key value violates unique constraint "users_email_key"`))
	if !ok || er.Kind != KindError || er.Message != `duplicate key value violates unique constraint "users_email_key"` {
		t.Fatalf("error %+v", er)
	}
	er, _ = Classify(rec("ERROR", `invalid input syntax for type integer: "4111-1111"`))
	if strings.Contains(er.Message, "4111") {
		t.Fatalf("values are redacted: %q", er.Message)
	}
	if _, ok := Classify(rec("LOG", "connection received: host=10.0.0.1")); ok {
		t.Fatal("connection messages are not kept")
	}
}

func TestEventIDIsStable(t *testing.T) {
	a, _ := Classify(rec("LOG", "duration: 1 ms  statement: select 1"))
	b, _ := Classify(rec("LOG", "duration: 1 ms  statement: select 1"))
	c, _ := Classify(rec("LOG", "duration: 2 ms  statement: select 1"))
	if a.ID != b.ID || a.ID == c.ID {
		t.Fatalf("ids %s %s %s", a.ID, b.ID, c.ID)
	}
}
