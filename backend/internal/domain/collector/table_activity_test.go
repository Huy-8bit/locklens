package collector

import (
	"testing"
	"time"

	"github.com/locklens/locklens/internal/domain/observe"
)

func TestTableRates(t *testing.T) {
	t0 := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	prev := &tableActivityState{at: t0, tables: map[uint32]observe.TableCounters{
		1: {Schema: "shop", Name: "orders", SeqScan: 10, SeqRead: 1000, Writes: 50},
		2: {Schema: "shop", Name: "items", SeqScan: 5, SeqRead: 500, IdxScan: 100, IdxFetch: 100},
		3: {Schema: "shop", Name: "reset", SeqScan: 100, SeqRead: 100000},
		4: {Schema: "shop", Name: "idle", SeqScan: 1},
	}}
	cur := tableActivityState{at: t0.Add(time.Minute), tables: map[uint32]observe.TableCounters{
		1: {Schema: "shop", Name: "orders", SeqScan: 16, SeqRead: 61000, Writes: 110, Dead: 7},
		2: {Schema: "shop", Name: "items", SeqScan: 5, SeqRead: 500, IdxScan: 160, IdxFetch: 220},
		3: {Schema: "shop", Name: "reset", SeqScan: 2, SeqRead: 10}, // pg_stat_reset: unknown, skipped
		4: {Schema: "shop", Name: "idle", SeqScan: 1},               // no activity: not hot
		5: {Schema: "shop", Name: "new", Writes: 600},               // created after the previous collection
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
	many := tableActivityState{at: t0, tables: map[uint32]observe.TableCounters{}}
	after := tableActivityState{at: t0.Add(time.Minute), tables: map[uint32]observe.TableCounters{}}
	for i := uint32(1); i <= 30; i++ {
		many.tables[i] = observe.TableCounters{Name: "t"}
		after.tables[i] = observe.TableCounters{Name: "t", Writes: int64(i) * 60}
	}
	top := tableRates(&many, after)
	if len(top) != hotTables || top[0].RelID != 30 || top[hotTables-1].RelID != 11 {
		t.Fatalf("top = %d first %d last %d", len(top), top[0].RelID, top[len(top)-1].RelID)
	}
}
