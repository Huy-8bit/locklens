package main

import (
	"math"
	"strings"
	"testing"
)

const exposition = `# HELP x_seconds h
# TYPE x_seconds histogram
x_seconds_bucket{collector="a",le="0.1"} 50
x_seconds_bucket{collector="a",le="1"} 90
x_seconds_bucket{collector="a",le="+Inf"} 100
x_seconds_sum{collector="a"} 20
x_seconds_count{collector="a"} 100
x_seconds_bucket{collector="b",le="0.1"} 10
x_seconds_bucket{collector="b",le="1"} 10
x_seconds_bucket{collector="b",le="+Inf"} 10
x_seconds_sum{collector="b"} 0.5
x_seconds_count{collector="b"} 10
c_total{reason="queue \"full\""} 3
g 7
`

func TestParseAndAggregate(t *testing.T) {
	sc, err := parseMetrics(strings.NewReader(exposition))
	if err != nil {
		t.Fatal(err)
	}
	if got := sc.sum("c_total", map[string]string{"reason": `queue "full"`}); got != 3 {
		t.Fatalf("escaped label: got %v", got)
	}
	if got := sc.sum("g", nil); got != 7 {
		t.Fatalf("unlabelled gauge: got %v", got)
	}
	h := sc.histogram("x_seconds", nil)
	if h.count != 110 || len(h.bounds) != 3 {
		t.Fatalf("merged histogram: %+v", h)
	}
	// 110 samples: rank 55 lies in the first bucket (60 samples ≤ 0.1) → 0.1 × 55/60.
	if q := h.quantile(0.5); math.Abs(q-0.0917) > 0.001 {
		t.Fatalf("p50 = %v", q)
	}
	// rank 108.9 lies beyond the last finite bucket (100) → answers its bound.
	if q := h.quantile(0.99); q != 1 {
		t.Fatalf("p99 = %v", q)
	}
}

func TestHistogramDelta(t *testing.T) {
	a := hist{bounds: []float64{0.1, math.Inf(1)}, counts: []float64{10, 20}, count: 20, sum: 4}
	b := hist{bounds: []float64{0.1, math.Inf(1)}, counts: []float64{30, 40}, count: 40, sum: 5}
	d := b.minus(a)
	if d.count != 20 || d.counts[0] != 20 || d.counts[1] != 20 || d.sum != 1 {
		t.Fatalf("delta: %+v", d)
	}
	if q := d.quantile(0.99); math.Abs(q-0.099) > 1e-9 {
		t.Fatalf("p99 of the delta = %v", q)
	}
}
