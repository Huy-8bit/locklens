package collector

import (
	"testing"
	"time"
)

func TestTableRates(t *testing.T) {
	t0 := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	prev := &tableActivityState{at: t0, tables: map[uint32]tableCounters{
		1: {schema: "shop", name: "orders", seqScan: 10, seqRead: 1000, writes: 50},
		2: {schema: "shop", name: "items", seqScan: 5, seqRead: 500, idxScan: 100, idxFetch: 100},
		3: {schema: "shop", name: "reset", seqScan: 100, seqRead: 100000},
		4: {schema: "shop", name: "idle", seqScan: 1},
	}}
	cur := tableActivityState{at: t0.Add(time.Minute), tables: map[uint32]tableCounters{
		1: {schema: "shop", name: "orders", seqScan: 16, seqRead: 61000, writes: 110, dead: 7},
		2: {schema: "shop", name: "items", seqScan: 5, seqRead: 500, idxScan: 160, idxFetch: 220},
		3: {schema: "shop", name: "reset", seqScan: 2, seqRead: 10}, // pg_stat_reset: unknown, skipped
		4: {schema: "shop", name: "idle", seqScan: 1},               // no activity: not hot
		5: {schema: "shop", name: "new", writes: 600},               // created after the previous collection
	}}
	if tableRates(nil, cur) != nil {
		t.Fatal("the first collection has no rates")
	}
	got := tableRates(prev, cur)
	if len(got) != 2 {
		t.Fatalf("hot tables = %+v", got)
	}
	o := got[0]
	if o.Name != "orders" || o.SeqRowsPerSec != 1000 || o.SeqScansPerSec != 0.1 || o.WritesPerSec != 1 || o.DeadRows != 7 || o.IntervalSeconds != 60 {
		t.Fatalf("orders = %+v", o)
	}
	if i := got[1]; i.Name != "items" || i.IdxScansPerSec != 1 || i.IdxRowsPerSec != 2 {
		t.Fatalf("items = %+v", i)
	}
	// At most hotTables, the busiest first.
	many := tableActivityState{at: t0, tables: map[uint32]tableCounters{}}
	after := tableActivityState{at: t0.Add(time.Minute), tables: map[uint32]tableCounters{}}
	for i := uint32(1); i <= 30; i++ {
		many.tables[i] = tableCounters{name: "t"}
		after.tables[i] = tableCounters{name: "t", writes: int64(i) * 60}
	}
	top := tableRates(&many, after)
	if len(top) != hotTables || top[0].RelID != 30 || top[hotTables-1].RelID != 11 {
		t.Fatalf("top = %d first %d last %d", len(top), top[0].RelID, top[len(top)-1].RelID)
	}
}
