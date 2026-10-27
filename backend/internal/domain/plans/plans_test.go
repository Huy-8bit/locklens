package plans

import (
	"strings"
	"testing"
)

const seqJSON = `[{"Plan": {"Node Type": "Seq Scan", "Relation Name": "orders", "Schema": "public", "Total Cost": 18334.0, "Plan Rows": 5,
 "Filter": "(customer_id = $1)"}}]`

const idxJSON = `[{"Plan": {"Node Type": "Index Scan", "Relation Name": "orders", "Schema": "public", "Index Name": "orders_customer_idx",
 "Total Cost": 8.44, "Plan Rows": 5, "Index Cond": "(customer_id = $1)"}}]`

const joinJSON = `[{"Plan": {"Node Type": "Hash Join", "Join Type": "Inner", "Total Cost": 500, "Plan Rows": 10, "Hash Cond": "(o.id = i.order_id)",
 "Plans": [{"Node Type": "Seq Scan", "Relation Name": "items", "Schema": "public", "Total Cost": 200, "Plan Rows": 1000},
           {"Node Type": "Hash", "Total Cost": 100, "Plan Rows": 10, "Plans": [{"Node Type": "Index Scan", "Relation Name": "orders", "Schema": "public",
            "Index Name": "orders_pkey", "Total Cost": 90, "Plan Rows": 10, "Actual Total Time": 0.5}]}]}}]`

func TestParseAndFingerprint(t *testing.T) {
	a, err := Parse(seqJSON)
	if err != nil || a.Format != "json" || len(a.Nodes) != 1 || *a.TotalCost != 18334 || a.Nodes[0].Relation != "public.orders" {
		t.Fatalf("parse = %+v %v", a, err)
	}
	// Different costs, same shape.
	b, _ := Parse(strings.Replace(seqJSON, "18334.0", "99.0", 1))
	if a.Fingerprint != b.Fingerprint {
		t.Fatal("costs must not change the fingerprint")
	}
	c, _ := Parse(idxJSON)
	if a.Fingerprint == c.Fingerprint {
		t.Fatal("a different scan must change the fingerprint")
	}
	j, _ := Parse(joinJSON)
	if len(j.Nodes) != 4 || j.Nodes[3].Depth != 2 || !j.Analyzed {
		t.Fatalf("nested = %+v", j.Nodes)
	}
	if _, err := Parse(`{"nope": 1}`); err == nil {
		t.Fatal("JSON without a plan must be rejected")
	}
	if _, err := Parse("   "); err == nil {
		t.Fatal("empty plan must be rejected")
	}
}

func TestParseText(t *testing.T) {
	txt := `Hash Join  (cost=10.00..500.00 rows=10 width=8)
  Hash Cond: (o.id = i.order_id)
  ->  Seq Scan on items i  (cost=0.00..200.00 rows=1000 width=8)
        Filter: (qty > 5)
  ->  Hash  (cost=90.00..100.00 rows=10 width=8)
        ->  Index Scan using orders_pkey on orders o  (cost=0.29..90.00 rows=10 width=8)`
	p, err := Parse(txt)
	if err != nil || p.Format != "text" || len(p.Nodes) != 4 || *p.TotalCost != 500 {
		t.Fatalf("text = %+v %v", p, err)
	}
	if p.Nodes[1].Relation != "items" || p.Nodes[1].Filter != "(qty > 5)" || p.Nodes[3].Index != "orders_pkey" {
		t.Fatalf("nodes = %+v", p.Nodes)
	}
}

func TestCompareAndHints(t *testing.T) {
	idx, _ := Parse(idxJSON)
	seq, _ := Parse(seqJSON)
	c := Compare(idx, seq)
	if c.SameShape || c.CostRatio == nil || *c.CostRatio < 1000 {
		t.Fatalf("compare = %+v", c)
	}
	var kinds []string
	for _, ch := range c.Changes {
		kinds = append(kinds, ch.Kind)
	}
	if strings.Join(kinds, ",") != "cost,scan_changed" || !strings.Contains(c.Changes[1].Detail, "Index Scan using orders_customer_idx → Seq Scan") {
		t.Fatalf("changes = %+v", c.Changes)
	}
	if same := Compare(seq, seq); !same.SameShape || len(same.Changes) != 0 {
		t.Fatalf("same = %+v", same)
	}
	h := IndexHints(seq)
	if len(h) != 1 || h[0].Relation != "public.orders" || strings.Join(h[0].Columns, ",") != "customer_id" {
		t.Fatalf("hints = %+v", h)
	}
	if len(IndexHints(idx)) != 0 {
		t.Fatal("an index scan needs no hint")
	}
}
