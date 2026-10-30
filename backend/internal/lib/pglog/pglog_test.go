package pglog

import (
	"io"
	"strings"
	"testing"
	"time"
)

func TestStderrWithDefaultPrefix(t *testing.T) {
	p, err := CompilePrefix(DefaultPrefix)
	if err != nil {
		t.Fatal(err)
	}
	r := NewStderrReader(p)
	lines := []string{
		"2026-10-30 12:00:01.123 UTC [4242] LOG:  duration: 1520.512 ms  statement: SELECT *",
		"\tFROM orders WHERE id = 42",
		"2026-10-30 12:00:02.000 UTC [4243] ERROR:  deadlock detected",
		"2026-10-30 12:00:02.000 UTC [4243] DETAIL:  Process 4243 waits for ShareLock on transaction 9; blocked by process 4244.",
		"\tProcess 4244 waits for ShareLock on transaction 8; blocked by process 4243.",
		"2026-10-30 12:00:02.000 UTC [4243] HINT:  See server log for query details.",
		"2026-10-30 12:00:02.000 UTC [4243] STATEMENT:  UPDATE t SET v = 1 WHERE id = 2",
		"some line from another program",
	}
	var got []Record
	for _, l := range lines {
		got = append(got, r.Add(l)...)
	}
	got = append(got, r.Flush()...)
	if len(got) != 2 {
		t.Fatalf("records %+v", got)
	}
	slow := got[0]
	if slow.PID != 4242 || slow.Severity != "LOG" || !slow.Time.Equal(time.Date(2026, 10, 30, 12, 0, 1, 123e6, time.UTC)) ||
		slow.Message != "duration: 1520.512 ms  statement: SELECT *\nFROM orders WHERE id = 42" {
		t.Fatalf("slow %+v", slow)
	}
	dl := got[1]
	if dl.Severity != "ERROR" || !strings.Contains(dl.Detail, "\nProcess 4244 waits") || dl.Statement != "UPDATE t SET v = 1 WHERE id = 2" || dl.Hint == "" {
		t.Fatalf("deadlock %+v", dl)
	}
}

func TestStderrWithRichPrefix(t *testing.T) {
	p, err := CompilePrefix("%t [%p]: user=%u,db=%d,app=%a,client=%h %e ")
	if err != nil {
		t.Fatal(err)
	}
	r := NewStderrReader(p)
	r.Add("2026-10-30 12:00:01 UTC [77]: user=app,db=shop,app=api server,client=10.0.0.1 40P01 ERROR:  deadlock detected")
	rec := r.Flush()
	if len(rec) != 1 || rec[0].User != "app" || rec[0].Database != "shop" || rec[0].Application != "api server" || rec[0].SQLState != "40P01" || rec[0].PID != 77 {
		t.Fatalf("record %+v", rec)
	}
	if _, err := CompilePrefix("%Z"); err == nil {
		t.Fatal("unknown escapes are rejected")
	}
}

func TestJSONAndCSV(t *testing.T) {
	j, err := ParseJSON([]byte(`{"timestamp":"2026-10-30 12:00:01.123 UTC","user":"app","dbname":"shop","pid":12,"error_severity":"LOG","message":"duration: 5.0 ms","application_name":"api","query_id":-123}`))
	if err != nil || j.PID != 12 || j.Database != "shop" || j.QueryID != -123 || j.Time.IsZero() {
		t.Fatalf("json %+v %v", j, err)
	}
	csvlog := `2026-10-30 12:00:01.123 UTC,"app","shop",12,"10.0.0.1:5000",6721a.c,3,"SELECT",2026-10-30 11:00:00 UTC,3/7,0,LOG,00000,"duration: 5.0 ms  statement: select 1
from t",,,,,,,,,"api","client backend",,77
`
	c := NewCSVReader(strings.NewReader(csvlog))
	rec, err := c.Read()
	if err != nil || rec.PID != 12 || rec.Application != "api" || rec.QueryID != 77 || !strings.Contains(rec.Message, "\nfrom t") {
		t.Fatalf("csv %+v %v", rec, err)
	}
	if _, err := c.Read(); err != io.EOF {
		t.Fatalf("end: %v", err)
	}
}

func TestParseCSVStopsAtAnIncompleteRecord(t *testing.T) {
	full := `2026-10-30 12:00:01.123 UTC,"app","shop",12,"",6721a.c,3,"",2026-10-30 11:00:00 UTC,3/7,0,LOG,00000,"duration: 5.0 ms  statement: select 1",,,,,,,,,"api","client backend",,
`
	partial := `2026-10-30 12:00:02.000 UTC,"app","shop",13,"",6721a.d,1,"",2026-10-30 11:00:00 UTC,3/8,0,LOG,00000,"duration: 6.0 ms  statement: select
`
	recs, used := ParseCSV([]byte(full + partial))
	if len(recs) != 1 || used != len(full) || recs[0].PID != 12 {
		t.Fatalf("records %+v used %d of %d", recs, used, len(full))
	}
	recs, used = ParseCSV([]byte(partial + "2\",,,,,,,,,\"api\",\"client backend\",,\n"))
	if len(recs) != 1 || recs[0].PID != 13 || !strings.Contains(recs[0].Message, "select\n2") || used == 0 {
		t.Fatalf("completed %+v %d", recs, used)
	}
}
