// Package telemetry exports what LockLens knows about the monitored
// instances to other monitoring systems: the Prometheus text format (scraped
// from /metrics/targets) and OTLP (pushed to an OpenTelemetry collector).
//
// Names follow Prometheus conventions: a locklens_pg_ (or locklens_host_)
// prefix, base units (seconds, bytes, ratios 0-1), and _total for counters.
// Rates LockLens computes are exported as counters (running sums of what it
// observed since it started collecting the instance), so the receiving
// system computes rates itself. Labels are bounded: the instance, its
// environment, and a fixed per-metric label (database, replica, slot,
// mount point, state). Statement metrics are off unless asked for, limited to
// the top statements, labelled by queryid, and never carry SQL text.
package telemetry

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/locklens/locklens/internal/domain/live"
	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/repository"
)

// Metric types.
const (
	Gauge   = "gauge"
	Counter = "counter"
)

// Label is one label of a sample.
type Label struct{ Name, Value string }

// Sample is one value of a family.
type Sample struct {
	Labels []Label
	Value  float64
}

// Family is a metric family.
type Family struct {
	Name, Help, Type string
	Samples          []Sample
}

// Target is one instance with what is known about it.
type Target struct {
	ID              int64
	Name            string
	Environment     string
	IntervalSeconds int
	View            live.View
	Findings        []repository.RuleCount
	Incidents       []repository.RuleCount
}

// Input is what an export is built from.
type Input struct {
	Targets []Target
	Now     time.Time
	// TopStatements is how many statements per instance get statement
	// metrics (0: none).
	TopStatements int
}

// Build returns the metric families of the instances, sorted by name.
func Build(in Input) []Family {
	fams := map[string]*Family{}
	add := func(name, help, typ string, labels []Label, v float64) {
		f, ok := fams[name]
		if !ok {
			f = &Family{Name: name, Help: help, Type: typ}
			fams[name] = f
		}
		f.Samples = append(f.Samples, Sample{Labels: labels, Value: v})
	}
	for _, t := range in.Targets {
		base := []Label{{"target", t.Name}, {"environment", t.Environment}}
		with := func(extra ...Label) []Label { return append(slices.Clone(base), extra...) }

		add("locklens_target_up", "1 when LockLens collected the instance's activity recently, 0 when it cannot.", Gauge, with(), up(t, in.Now))
		if v := t.View; v.Capabilities != nil {
			role := "primary"
			if v.Capabilities.IsStandby {
				role = "standby"
			}
			add("locklens_target_info", "The instance's server version and role (value 1).", Gauge,
				with(Label{"server_version", v.Capabilities.ServerVersion}, Label{"role", role}), 1)
		}
		if a := t.View.Activity; a != nil {
			add("locklens_target_last_collection_timestamp_seconds", "Unix time of the last activity collection.", Gauge, with(), float64(a.At.Unix()))
		}
		for key, h := range t.View.Points {
			metric, label := splitKey(key)
			d, ok := gauges[metric]
			if !ok || len(h) == 0 || h[len(h)-1].Value == nil {
				continue
			}
			add(d.name, d.help, Gauge, with(d.labels(label)...), *h[len(h)-1].Value*d.scale())
		}
		for key, total := range t.View.Totals {
			metric, label := splitKey(key)
			if d, ok := counters[metric]; ok {
				add(d.name, d.help, Counter, with(d.labels(label)...), total*d.scale())
			}
		}
		for _, c := range t.Findings {
			add("locklens_findings_active", "Active findings by rule and severity.", Gauge,
				with(Label{"rule", c.RuleID}, Label{"severity", c.Severity}), float64(c.Count))
		}
		for _, c := range t.Incidents {
			add("locklens_incidents_open", "Open incidents by rule and severity.", Gauge,
				with(Label{"rule", c.RuleID}, Label{"severity", c.Severity}), float64(c.Count))
		}
		statements(t, in.TopStatements, with, add)
	}
	out := make([]Family, 0, len(fams))
	for _, f := range fams {
		slices.SortFunc(f.Samples, func(a, b Sample) int { return strings.Compare(labelKey(a.Labels), labelKey(b.Labels)) })
		out = append(out, *f)
	}
	slices.SortFunc(out, func(a, b Family) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// up is 1 when the instance is connected and its activity was collected
// within three intervals (and half a minute of slack).
func up(t Target, now time.Time) float64 {
	a := t.View.Activity
	iv := time.Duration(max(t.IntervalSeconds, 5)) * time.Second
	if !t.View.Connected || a == nil || now.Sub(a.At) > 3*iv+30*time.Second {
		return 0
	}
	return 1
}

// statements exports the statements with the most execution time in the
// latest statements collection.
func statements(t Target, top int, with func(...Label) []Label, add func(string, string, string, []Label, float64)) {
	ss := t.View.Statements
	if top <= 0 || len(ss) == 0 {
		return
	}
	last := ss[len(ss)-1]
	if last.Interval == nil || *last.Interval <= 0 {
		return
	}
	ds := slices.Clone(last.Deltas)
	slices.SortStableFunc(ds, func(a, b observe.QueryDelta) int { return cmp.Compare(b.TotalMS, a.TotalMS) })
	for _, d := range ds[:min(top, len(ds))] {
		if d.Calls <= 0 {
			continue
		}
		ls := with(Label{"queryid", strconv.FormatInt(d.Key.QueryID, 10)}, Label{"database", d.DatName}, Label{"user", d.UserName})
		add("locklens_pg_statement_calls_per_second", "Calls per second of the busiest statements in the latest collection.", Gauge, ls,
			float64(d.Calls) / *last.Interval)
		add("locklens_pg_statement_mean_seconds", "Mean execution time per call of the busiest statements in the latest collection.", Gauge, ls,
			d.TotalMS/float64(d.Calls)/1000)
	}
}

// splitKey splits a point key "metric{label}" into its metric and label.
func splitKey(key string) (string, string) {
	if i := strings.IndexByte(key, '{'); i >= 0 && strings.HasSuffix(key, "}") {
		return key[:i], key[i+1 : len(key)-1]
	}
	return key, ""
}

func labelKey(ls []Label) string {
	var b strings.Builder
	for _, l := range ls {
		b.WriteString(l.Name)
		b.WriteByte(0)
		b.WriteString(l.Value)
		b.WriteByte(0)
	}
	return b.String()
}
