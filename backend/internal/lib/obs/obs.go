// Package obs is LockLens' own instrumentation: a small Prometheus-compatible
// registry of counters, gauges, and histograms with bounded label sets.
//
// Labels must come from a finite set (collector names, outcomes, rule IDs,
// target IDs). PIDs, SQL text, DSNs, and incident IDs are never labels.
package obs

import (
	"fmt"
	"io"
	"math"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Registry holds metric families.
type Registry struct {
	mu       sync.Mutex
	families map[string]*family
	start    time.Time
}

type kind int

const (
	kindCounter kind = iota
	kindGauge
	kindHistogram
)

type family struct {
	name, help string
	kind       kind
	labels     []string
	buckets    []float64
	series     map[string]*series
	// maxSeries bounds the number of label combinations.
	maxSeries int
}

type series struct {
	labelValues []string
	value       float64
	counts      []uint64
	sum         float64
	count       uint64
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{families: map[string]*family{}, start: time.Now()}
}

func (r *Registry) register(name, help string, k kind, labels []string, buckets []float64) *family {
	r.mu.Lock()
	defer r.mu.Unlock()
	if f, ok := r.families[name]; ok {
		return f
	}
	f := &family{name: name, help: help, kind: k, labels: labels, buckets: buckets, series: map[string]*series{}, maxSeries: 1000}
	r.families[name] = f
	return f
}

// Counter is a monotonically increasing metric.
type Counter struct {
	r *Registry
	f *family
}

// Gauge is a metric that can go up and down.
type Gauge struct {
	r *Registry
	f *family
}

// Histogram observes value distributions.
type Histogram struct {
	r *Registry
	f *family
}

// NewCounter registers a counter.
func (r *Registry) NewCounter(name, help string, labels ...string) *Counter {
	return &Counter{r: r, f: r.register(name, help, kindCounter, labels, nil)}
}

// NewGauge registers a gauge.
func (r *Registry) NewGauge(name, help string, labels ...string) *Gauge {
	return &Gauge{r: r, f: r.register(name, help, kindGauge, labels, nil)}
}

// NewHistogram registers a histogram with the given upper bounds.
func (r *Registry) NewHistogram(name, help string, buckets []float64, labels ...string) *Histogram {
	b := slices.Clone(buckets)
	slices.Sort(b)
	return &Histogram{r: r, f: r.register(name, help, kindHistogram, labels, b)}
}

func (r *Registry) get(f *family, values []string) *series {
	if len(values) != len(f.labels) {
		panic(fmt.Sprintf("metric %s: %d label values for %d labels", f.name, len(values), len(f.labels)))
	}
	key := strings.Join(values, "\x00")
	s, ok := f.series[key]
	if !ok {
		if len(f.series) >= f.maxSeries {
			return nil // cardinality guard: drop new series
		}
		s = &series{labelValues: slices.Clone(values)}
		if f.kind == kindHistogram {
			s.counts = make([]uint64, len(f.buckets))
		}
		f.series[key] = s
	}
	return s
}

// Add increases the counter.
func (c *Counter) Add(v float64, labels ...string) {
	c.r.mu.Lock()
	defer c.r.mu.Unlock()
	if s := c.r.get(c.f, labels); s != nil {
		s.value += v
	}
}

// Inc adds one.
func (c *Counter) Inc(labels ...string) { c.Add(1, labels...) }

// Set sets the gauge.
func (g *Gauge) Set(v float64, labels ...string) {
	g.r.mu.Lock()
	defer g.r.mu.Unlock()
	if s := g.r.get(g.f, labels); s != nil {
		s.value = v
	}
}

// Delete removes one series (for example when a target is deleted).
func (g *Gauge) Delete(labels ...string) {
	g.r.mu.Lock()
	defer g.r.mu.Unlock()
	delete(g.f.series, strings.Join(labels, "\x00"))
}

// Total returns the sum over all series of a counter.
func (c *Counter) Total() float64 { return c.r.total(c.f) }

// Total returns the sum over all series of a gauge (its value when it has no
// labels).
func (g *Gauge) Total() float64 { return g.r.total(g.f) }

func (r *Registry) total(f *family) float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	var t float64
	for _, s := range f.series {
		t += s.value
	}
	return t
}

// Observe records a value.
func (h *Histogram) Observe(v float64, labels ...string) {
	h.r.mu.Lock()
	defer h.r.mu.Unlock()
	s := h.r.get(h.f, labels)
	if s == nil {
		return
	}
	for i, b := range h.f.buckets {
		if v <= b {
			s.counts[i]++
		}
	}
	s.sum += v
	s.count++
}

// WriteText writes the Prometheus text exposition format (version 0.0.4),
// including Go process metrics.
func (r *Registry) WriteText(w io.Writer) error {
	r.mu.Lock()
	names := make([]string, 0, len(r.families))
	for n := range r.families {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		f := r.families[n]
		typ := map[kind]string{kindCounter: "counter", kindGauge: "gauge", kindHistogram: "histogram"}[f.kind]
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s %s\n", f.name, escapeHelp(f.help), f.name, typ)
		keys := make([]string, 0, len(f.series))
		for k := range f.series {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			s := f.series[k]
			switch f.kind {
			case kindHistogram:
				for i, bound := range f.buckets {
					fmt.Fprintf(&b, "%s_bucket%s %d\n", f.name, labelString(f.labels, s.labelValues, "le", formatFloat(bound)), s.counts[i])
				}
				fmt.Fprintf(&b, "%s_bucket%s %d\n", f.name, labelString(f.labels, s.labelValues, "le", "+Inf"), s.count)
				fmt.Fprintf(&b, "%s_sum%s %s\n", f.name, labelString(f.labels, s.labelValues, "", ""), formatFloat(s.sum))
				fmt.Fprintf(&b, "%s_count%s %d\n", f.name, labelString(f.labels, s.labelValues, "", ""), s.count)
			default:
				fmt.Fprintf(&b, "%s%s %s\n", f.name, labelString(f.labels, s.labelValues, "", ""), formatFloat(s.value))
			}
		}
	}
	r.mu.Unlock()

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	fmt.Fprintf(&b, "# HELP go_goroutines Number of goroutines.\n# TYPE go_goroutines gauge\ngo_goroutines %d\n", runtime.NumGoroutine())
	fmt.Fprintf(&b, "# HELP go_memstats_heap_alloc_bytes Heap bytes allocated and in use.\n# TYPE go_memstats_heap_alloc_bytes gauge\ngo_memstats_heap_alloc_bytes %d\n", ms.HeapAlloc)
	fmt.Fprintf(&b, "# HELP go_memstats_sys_bytes Bytes obtained from the OS.\n# TYPE go_memstats_sys_bytes gauge\ngo_memstats_sys_bytes %d\n", ms.Sys)
	fmt.Fprintf(&b, "# HELP process_uptime_seconds Seconds since the process started.\n# TYPE process_uptime_seconds gauge\nprocess_uptime_seconds %s\n", formatFloat(time.Since(r.start).Seconds()))
	_, err := io.WriteString(w, b.String())
	return err
}

func labelString(names, values []string, extraName, extraValue string) string {
	if len(names) == 0 && extraName == "" {
		return ""
	}
	parts := make([]string, 0, len(names)+1)
	for i, n := range names {
		parts = append(parts, n+`="`+escapeLabel(values[i])+`"`)
	}
	if extraName != "" {
		parts = append(parts, extraName+`="`+extraValue+`"`)
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func escapeLabel(s string) string {
	return strings.NewReplacer(`\`, `\\`, "\n", `\n`, `"`, `\"`).Replace(s)
}

func escapeHelp(s string) string {
	return strings.NewReplacer(`\`, `\\`, "\n", `\n`).Replace(s)
}

func formatFloat(v float64) string {
	switch {
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	case math.IsNaN(v):
		return "NaN"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}
