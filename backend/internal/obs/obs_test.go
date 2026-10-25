package obs

import (
	"strings"
	"testing"
)

func TestTextExposition(t *testing.T) {
	r := NewRegistry()
	c := r.NewCounter("x_total", "Things.", "kind")
	c.Inc("a")
	c.Add(2, "a")
	c.Inc(`we"ird`)
	h := r.NewHistogram("d_seconds", "Durations.", []float64{1, 0.1}, "op")
	h.Observe(0.05, "q")
	h.Observe(0.5, "q")
	g := r.NewGauge("g", "Gauge.")
	g.Set(4)
	var b strings.Builder
	if err := r.WriteText(&b); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{
		"# TYPE x_total counter", `x_total{kind="a"} 3`, `x_total{kind="we\"ird"} 1`,
		`d_seconds_bucket{op="q",le="0.1"} 1`, `d_seconds_bucket{op="q",le="1"} 2`, `d_seconds_bucket{op="q",le="+Inf"} 2`,
		`d_seconds_count{op="q"} 2`, "g 4", "go_goroutines ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestCardinalityGuard(t *testing.T) {
	r := NewRegistry()
	c := r.NewCounter("c", "C.", "l")
	for i := range 1500 {
		c.Inc(strings.Repeat("x", i%2000))
	}
	if n := len(c.f.series); n > 1000 {
		t.Fatalf("series = %d, want <= 1000", n)
	}
}

func TestTotals(t *testing.T) {
	r := NewRegistry()
	c := r.NewCounter("c_total", "C.", "kind")
	c.Inc("a")
	c.Add(2, "b")
	g := r.NewGauge("g", "G.")
	g.Set(7)
	if c.Total() != 3 || g.Total() != 7 {
		t.Fatalf("totals = %v, %v", c.Total(), g.Total())
	}
}
