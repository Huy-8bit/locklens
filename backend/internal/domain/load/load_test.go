package load

import (
	"math"
	"testing"
	"time"

	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/repository"
)

func near(t *testing.T, what string, got *float64, want float64) {
	t.Helper()
	if got == nil || math.Abs(*got-want) > 1e-9 {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}

func TestWaitClass(t *testing.T) {
	s := func(v string) *string { return &v }
	cases := []struct {
		state, wait *string
		want        string
		ok          bool
	}{
		{s("active"), nil, observe.WaitCPU, true},
		{s("active"), s("Lock"), "Lock", true},
		{s("active"), s(""), observe.WaitCPU, true},
		{s("idle in transaction"), s("Client"), observe.WaitIdleTx, true},
		{s("idle in transaction (aborted)"), nil, observe.WaitIdleTx, true},
		{s("idle"), s("Client"), "", false},
		{nil, nil, observe.WaitUnknown, true},
	}
	for _, c := range cases {
		got, ok := observe.WaitClass(c.state, c.wait)
		if got != c.want || ok != c.ok {
			t.Errorf("WaitClass(%v, %v) = %q, %v; want %q, %v", c.state, c.wait, got, ok, c.want, c.ok)
		}
	}
}

func TestBuildByClass(t *testing.T) {
	t0 := time.Date(2026, 10, 25, 13, 0, 0, 0, time.UTC)
	t1 := t0.Add(30 * time.Second)
	ticks := []repository.LoadTicks{{T: t0, Ticks: 10}, {T: t1, Ticks: 5}}
	rows := []repository.LoadRow{
		{T: t0, WaitClass: "CPU", Samples: 20},
		{T: t0, WaitClass: "Lock", Samples: 10},
		{T: t1, WaitClass: "CPU", Samples: 5},
		{T: t1, WaitClass: observe.WaitIdleTx, Samples: 15},
	}
	r := Build(repository.LoadByClass, rows, ticks, 8, false)
	if r.Ticks != 15 {
		t.Fatalf("ticks = %d", r.Ticks)
	}
	near(t, "active AAS", r.ActiveAAS, 35.0/15)
	near(t, "idle AAS", r.IdleTxAAS, 1)
	if r.Top[0].Key != "CPU" || r.Top[0].AAS != 25.0/15 {
		t.Fatalf("top = %+v", r.Top[0])
	}
	// Per bucket: samples / that bucket's ticks; a group absent from a sampled bucket is zero.
	for _, s := range r.Series {
		if s.Key == "Lock" {
			near(t, "lock t0", s.Points[0].V, 1)
			near(t, "lock t1", s.Points[1].V, 0)
		}
	}
}

func TestBuildLeavesIdleTransactionsOutOfStatements(t *testing.T) {
	t0 := time.Date(2026, 10, 25, 13, 0, 0, 0, time.UTC)
	rows := []repository.LoadRow{
		{T: t0, WaitClass: "IO", Key: []string{"16384", "10", "42", "shop", "app"}, Samples: 4},
		{T: t0, WaitClass: "CPU", Key: []string{"16384", "10", "42", "shop", "app"}, Samples: 4},
		{T: t0, WaitClass: observe.WaitIdleTx, Key: []string{"16384", "10", "7", "shop", "app"}, Samples: 8},
		{T: t0, WaitClass: "CPU", Key: []string{"16384", "10", "0", "shop", "app"}, Samples: 2},
	}
	r := Build(repository.LoadByQuery, rows, []repository.LoadTicks{{T: t0, Ticks: 2}}, 8, false)
	if len(r.Top) != 2 || r.Top[0].Key != "16384/10/42" || r.Top[0].AAS != 4 {
		t.Fatalf("top = %+v", r.Top)
	}
	if r.Top[0].Classes["IO"] != 2 || r.Top[0].Classes["CPU"] != 2 {
		t.Fatalf("classes = %v", r.Top[0].Classes)
	}
	near(t, "share", r.Top[0].Share, 0.8)
	if r.Top[1].Label != "Statements without a query ID" {
		t.Fatalf("label = %q", r.Top[1].Label)
	}
}

func TestBuildGroupsTheRestAsOther(t *testing.T) {
	t0 := time.Date(2026, 10, 25, 13, 0, 0, 0, time.UTC)
	var rows []repository.LoadRow
	for i, app := range []string{"a", "b", "c", "d"} {
		rows = append(rows, repository.LoadRow{T: t0, WaitClass: "CPU", Key: []string{app}, Samples: int64(10 - i)})
	}
	r := Build(repository.LoadByApplication, rows, []repository.LoadTicks{{T: t0, Ticks: 1}}, 2, false)
	if len(r.Series) != 3 || r.Series[2].Key != "_other" {
		t.Fatalf("series = %+v", r.Series)
	}
	near(t, "other", r.Series[2].Points[0].V, 8+7)
}

func TestBuildWithoutSamplesIsEmptyNotZero(t *testing.T) {
	r := Build(repository.LoadByClass, nil, nil, 8, false)
	if r.ActiveAAS != nil || len(r.Series) != 0 {
		t.Fatalf("result = %+v", r)
	}
}
