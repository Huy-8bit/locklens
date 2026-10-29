//go:build cgo

package optimizer

import (
	"strings"
	"testing"
	"time"

	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/repository"
)

var now = time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

func ptr[T any](v T) *T { return &v }

func catalog() observe.Catalog {
	reset := now.Add(-30 * 24 * time.Hour)
	return observe.Catalog{At: now, DatName: "app", StatsReset: &reset, Settings: map[string]string{"work_mem": "4096"},
		Tables: []observe.CatalogTable{
			{RelID: 1, Schema: "shop", Name: "orders", RelTuples: 2e6, SeqScan: 900, SeqTupRead: 9e8, IdxScan: 100, LiveTuples: 2e6},
			{RelID: 2, Schema: "shop", Name: "customers", RelTuples: 5e4, Writes: 300, LiveTuples: 5e4},
			{RelID: 3, Schema: "shop", Name: "items", RelTuples: 5e6, LiveTuples: 5e6, ModSinceAnalyze: 2e6, LastAutoAnalyze: ptr(now.Add(-10 * 24 * time.Hour))},
			{RelID: 4, Schema: "shop", Name: "tiny", RelTuples: 50},
		},
		Indexes: []observe.CatalogIndex{
			{RelID: 10, TableRelID: 1, Schema: "shop", Table: "orders", Name: "orders_pkey", Method: "btree", Columns: []string{"id"}, KeyColumns: 1, Unique: true, Primary: true, Valid: true, Constraint: true, Scans: ptr(int64(5))},
			{RelID: 11, TableRelID: 1, Schema: "shop", Table: "orders", Name: "orders_status_idx", Method: "btree", Columns: []string{"status"}, KeyColumns: 1, Valid: true, SizeBytes: 50 << 20, Scans: ptr(int64(0))},
			{RelID: 12, TableRelID: 1, Schema: "shop", Table: "orders", Name: "orders_status_created_idx", Method: "btree", Columns: []string{"status", "created_at"}, KeyColumns: 2, Valid: true, SizeBytes: 80 << 20, Scans: ptr(int64(40))},
			{RelID: 13, TableRelID: 3, Schema: "shop", Table: "items", Name: "items_broken", Method: "btree", Columns: []string{"sku"}, KeyColumns: 1, Valid: false, Scans: ptr(int64(0))},
		},
		ForeignKeys: []observe.CatalogFK{
			{Name: "orders_customer_fk", Schema: "shop", Table: "orders", Columns: []string{"customer_id"}, RefSchema: "shop", RefTable: "customers", OnDelete: "a"},
			{Name: "items_order_fk", Schema: "shop", Table: "items", Columns: []string{"order_id"}, RefSchema: "shop", RefTable: "orders", OnDelete: "c"},
		},
		Columns: []observe.ColumnStats{
			{Schema: "shop", Table: "orders", Column: "customer_id", NDistinct: -0.02},
			{Schema: "shop", Table: "orders", Column: "region", NDistinct: 12},
		},
	}
}

func stmt(id int64, text string, share float64) repository.WorkloadStatement {
	return repository.WorkloadStatement{IdentityID: id, Key: observe.QueryKey{QueryID: id * 100}, DatName: "app", UserName: "web", Command: "select",
		Text: &text, Calls: 1000, TotalMS: 5000, Rows: 1000, Share: share}
}

func byKind(recs []repository.RecommendationInput, kind string) []repository.RecommendationInput {
	var out []repository.RecommendationInput
	for _, r := range recs {
		if r.Kind == kind {
			out = append(out, r)
		}
	}
	return out
}

func TestMissingIndexFromStatements(t *testing.T) {
	in := Input{Now: now, Catalog: catalog(), Parser: true, Statements: []repository.WorkloadStatement{
		stmt(1, "SELECT id FROM shop.orders WHERE region = $1 AND customer_id = $2 ORDER BY created_at DESC LIMIT $3", 0.3),
		// The same index serves this one too: one recommendation, two statements.
		stmt(2, "SELECT total FROM shop.orders o WHERE o.customer_id = $1 AND o.region = $2 AND o.placed_at > $3", 0.1),
		// Served by orders_status_created_idx: nothing to propose.
		stmt(3, "SELECT id FROM shop.orders WHERE status = $1", 0.2),
		// A tiny table never needs an index.
		stmt(4, "SELECT id FROM shop.tiny WHERE name = $1", 0.2),
		// Another database: not in this catalog.
		{IdentityID: 5, DatName: "other", Text: ptr("SELECT 1 FROM shop.orders WHERE note = $1"), Calls: 10, Share: 0.1},
	}}
	recs := byKind(Analyze(in), KindMissingIndex)
	if len(recs) != 2 {
		t.Fatalf("missing indexes = %+v", recs)
	}
	// customer_id (2% distinct of 2M = 40k values) is more selective than
	// region (12 values), so it comes first.
	a := recs[0]
	if a.Key != "shop.orders(customer_id,region,created_at)" || *a.DDL != "CREATE INDEX CONCURRENTLY ON shop.orders (customer_id, region, created_at);" || a.Severity != "warning" {
		t.Fatalf("first = %s %s %s", a.Key, *a.DDL, a.Severity)
	}
	if c := a.Confidence.(Confidence); c.Level != "medium" || !strings.Contains(strings.Join(c.Reasons, " "), "read sequentially 900 times") {
		t.Fatalf("confidence = %+v", c)
	}
	if b := recs[1]; b.Key != "shop.orders(customer_id,region,placed_at)" {
		t.Fatalf("second = %s", b.Key)
	}
	// Without the parser no statement-based recommendation is made.
	in.Parser = false
	if n := len(byKind(Analyze(in), KindMissingIndex)); n != 0 {
		t.Fatalf("without parser: %d", n)
	}
}

func TestCatalogRecommendations(t *testing.T) {
	recs := Analyze(Input{Now: now, Catalog: catalog()})
	fk := byKind(recs, KindFKIndex)
	if len(fk) != 2 || fk[0].Severity != "warning" {
		t.Fatalf("fk = %+v", fk)
	}
	for _, r := range fk {
		if !strings.HasPrefix(*r.DDL, "CREATE INDEX CONCURRENTLY ON shop.") {
			t.Errorf("fk ddl %s", *r.DDL)
		}
	}
	// orders_status_idx: unused and a prefix of orders_status_created_idx.
	if u := byKind(recs, KindUnusedIndex); len(u) != 1 || u[0].Key != "shop.orders_status_idx" || u[0].Confidence.(Confidence).Level != "medium" {
		t.Fatalf("unused = %+v", u)
	}
	if d := byKind(recs, KindDuplicateIndex); len(d) != 1 || d[0].Key != "shop.orders_status_idx" || !strings.Contains(d[0].Summary, "leading prefix") {
		t.Fatalf("duplicate = %+v", d)
	}
	if v := byKind(recs, KindInvalidIndex); len(v) != 1 || v[0].Key != "shop.items_broken" {
		t.Fatalf("invalid = %+v", v)
	}
	if s := byKind(recs, KindStaleStats); len(s) != 1 || s[0].Key != "shop.items" || *s[0].DDL != "ANALYZE shop.items;" {
		t.Fatalf("stale = %+v", s)
	}
	// Recently reset statistics lower the confidence of "unused".
	c := catalog()
	c.StatsReset = ptr(now.Add(-time.Hour))
	if u := byKind(Analyze(Input{Now: now, Catalog: c}), KindUnusedIndex); u[0].Confidence.(Confidence).Level != "low" {
		t.Fatal("recent reset must be low confidence")
	}
	// Indexing the foreign key removes the recommendation.
	c = catalog()
	c.Indexes = append(c.Indexes, observe.CatalogIndex{RelID: 20, Schema: "shop", Table: "items", Name: "items_order_idx", Method: "btree",
		Columns: []string{"order_id", "sku"}, KeyColumns: 2, Valid: true})
	if fk := byKind(Analyze(Input{Now: now, Catalog: c}), KindFKIndex); len(fk) != 1 || fk[0].Key != "shop.orders:orders_customer_fk" {
		t.Fatalf("fk after index = %+v", fk)
	}
}

func TestShapesSpillsAndMisestimates(t *testing.T) {
	spill := stmt(7, `SELECT * FROM shop.items ORDER BY lower(name)`, 0.25)
	spill.TempW = 4096 * spill.Calls // 32 MiB per call
	spill.UserName = "Reporting"
	in := Input{Now: now, Catalog: catalog(), Parser: true, Statements: []repository.WorkloadStatement{
		spill,
		stmt(8, "SELECT id FROM shop.orders WHERE customer_id = $1 AND region = $2", 0.05),
	}, Plans: map[int64]ParsedPlan{8: {TopRows: 50000, Relations: []string{"shop.orders"}}}}
	recs := Analyze(in)
	shapes := byKind(recs, KindQueryShape)
	if len(shapes) != 1 || shapes[0].Key != "7:select_star" {
		t.Fatalf("shapes = %+v", shapes)
	}
	sp := byKind(recs, KindTempSpill)
	if len(sp) != 1 || !strings.Contains(*sp[0].DDL, "SET LOCAL work_mem = '64MB'") || !strings.Contains(*sp[0].DDL, `ALTER ROLE "Reporting"`) {
		t.Fatalf("spill = %+v", sp)
	}
	m := byKind(recs, KindMisestimate)
	if len(m) != 1 || !strings.Contains(*m[0].DDL, "ANALYZE shop.orders;") || !strings.Contains(*m[0].DDL, "CREATE STATISTICS ON customer_id, region FROM shop.orders") {
		t.Fatalf("misestimate = %+v", m)
	}
	// Ordered by benefit, largest first.
	for i := 1; i < len(recs); i++ {
		if recs[i].Benefit > recs[i-1].Benefit {
			t.Fatalf("not ordered at %d", i)
		}
	}
}

func TestStandbyGetsStatementRecommendationsOnly(t *testing.T) {
	c := catalog()
	c.InRecovery = true
	recs := Analyze(Input{Now: now, Catalog: c, Parser: true, Statements: []repository.WorkloadStatement{
		stmt(1, "SELECT id FROM shop.orders WHERE region = $1 AND customer_id = $2", 0.3)}})
	for _, r := range recs {
		switch r.Kind {
		case KindFKIndex, KindUnusedIndex, KindDuplicateIndex, KindInvalidIndex, KindStaleStats:
			t.Fatalf("structural recommendation on a standby: %+v", r)
		}
	}
	m := byKind(recs, KindMissingIndex)
	if len(m) != 1 || !strings.Contains(m[0].Summary, "apply the change on its primary") {
		t.Fatalf("standby missing index = %+v", m)
	}
}

func TestQuoteIdent(t *testing.T) {
	for in, want := range map[string]string{"orders": "orders", "Orders": `"Orders"`, "user": `"user"`, `a"b`: `"a""b"`, "x1_y": "x1_y"} {
		if got := quoteIdent(in); got != want {
			t.Errorf("quoteIdent(%q) = %s, want %s", in, got, want)
		}
	}
}
