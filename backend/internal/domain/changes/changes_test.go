package changes

import (
	"strings"
	"testing"
	"time"

	"github.com/locklens/locklens/internal/domain/observe"
)

func TestDiffServer(t *testing.T) {
	t0 := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)
	prev := observe.ServerState{At: t0, Facts: observe.ServerFacts{StartedAt: t0.Add(-time.Hour), ConfLoadedAt: t0.Add(-time.Hour), VersionNum: 170004, Version: "17.4"},
		Settings: map[string]observe.Setting{"work_mem": {Value: "4096", Unit: "kB"}, "jit": {Value: "on"}}}
	if ev := DiffServer(prev, prev); len(ev) != 0 {
		t.Fatalf("no change: %+v", ev)
	}

	// A reload that changed one setting: the event is at the reload.
	cur := prev
	cur.At = t0.Add(time.Minute)
	cur.Facts.ConfLoadedAt = t0.Add(30 * time.Second)
	cur.Settings = map[string]observe.Setting{"work_mem": {Value: "65536", Unit: "kB", Source: "configuration file"}, "jit": {Value: "on"}}
	ev := DiffServer(prev, cur)
	if len(ev) != 1 || ev[0].Kind != KindSettings || !ev[0].At.Equal(cur.Facts.ConfLoadedAt) ||
		ev[0].Title != "Setting work_mem changed: 4096 kB → 65536 kB" {
		t.Fatalf("settings: %+v", ev)
	}

	// A restart into a new version as a primary (it was a standby).
	prev.Facts.InRecovery = true
	cur = prev
	cur.At = t0.Add(time.Minute)
	cur.Facts.StartedAt, cur.Facts.InRecovery, cur.Facts.VersionNum, cur.Facts.Version = t0.Add(20*time.Second), false, 180000, "18.0"
	kinds := []string{}
	for _, e := range DiffServer(prev, cur) {
		kinds = append(kinds, e.Kind)
		if e.Kind == KindRestart && !e.At.Equal(cur.Facts.StartedAt) {
			t.Fatalf("restart time %v", e.At)
		}
		if e.Details["detected_after"] != prev.At.UTC() {
			t.Fatalf("window %+v", e.Details)
		}
	}
	if strings.Join(kinds, ",") != "restart,promotion,upgrade" {
		t.Fatalf("kinds %v", kinds)
	}
}

func TestDiffSchema(t *testing.T) {
	t0 := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)
	cat := observe.Catalog{At: t0, DatName: "app",
		Tables:  []observe.CatalogTable{{Schema: "public", Name: "orders"}, {Schema: "public", Name: "old"}},
		Indexes: []observe.CatalogIndex{{Schema: "public", Name: "orders_pkey", Valid: true, Definition: "CREATE UNIQUE INDEX orders_pkey ON public.orders USING btree (id)"}}}
	prev := SchemaOf(cat)
	cat.At = t0.Add(15 * time.Minute)
	cat.Tables = cat.Tables[:1]
	cat.Indexes = append(cat.Indexes, observe.CatalogIndex{Schema: "public", Name: "orders_customer", Valid: false, Definition: "CREATE INDEX orders_customer ON public.orders USING btree (customer_id)"})
	ev := DiffSchema(prev, SchemaOf(cat))
	if len(ev) != 1 || ev[0].Title != "app: schema changed (1 index created, 1 table dropped)" {
		t.Fatalf("schema: %+v", ev)
	}
	ch := ev[0].Details["changes"].([]ObjectChange)
	if ch[0].Name != "public.orders_customer" || !strings.HasSuffix(ch[0].Definition, invalidMark) {
		t.Fatalf("changes %+v", ch)
	}

	// The index finished building: it became valid.
	prev = SchemaOf(cat)
	cat.Indexes[1].Valid = true
	if ev := DiffSchema(prev, SchemaOf(cat)); len(ev) != 1 || ev[0].Title != "app: index public.orders_customer became valid" {
		t.Fatalf("valid: %+v", ev)
	}

	// A truncated catalog is not compared.
	cur := SchemaOf(cat)
	cur.Complete = false
	cur.Tables = nil
	if ev := DiffSchema(prev, cur); ev != nil {
		t.Fatalf("truncated: %+v", ev)
	}
}

func TestReportValidate(t *testing.T) {
	now := time.Now()
	old := now.Add(-40 * 24 * time.Hour)
	errs := Report{Kind: "restart", Title: "", At: &old}.Validate(now)
	if errs["kind"] == "" || errs["title"] == "" || errs["at"] == "" {
		t.Fatalf("errors %v", errs)
	}
	if errs := (Report{Kind: KindDeploy, Title: "api v2.3.1", Details: map[string]any{"commit": "abc"}}).Validate(now); len(errs) != 0 {
		t.Fatalf("valid report: %v", errs)
	}
}
