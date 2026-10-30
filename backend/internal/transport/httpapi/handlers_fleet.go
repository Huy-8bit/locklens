package httpapi

import (
	"cmp"
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/locklens/locklens/internal/domain/collector"
	"github.com/locklens/locklens/internal/domain/model"
	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/domain/report"
	"github.com/locklens/locklens/internal/domain/targets"
	"github.com/locklens/locklens/internal/repository"
)

// Fleet endpoints combine all enabled targets of one environment (or all
// environments). They are bounded to maxFleetTargets targets.
const maxFleetTargets = 50

func (s *Server) fleetTargets(r *http.Request) ([]targets.View, error) {
	all, err := s.d.Targets.List(r.Context())
	if err != nil {
		return nil, err
	}
	env := strings.TrimSpace(r.URL.Query().Get("environment"))
	out := make([]targets.View, 0, len(all))
	for _, t := range all {
		if t.Enabled && (env == "" || t.Environment == env) {
			out = append(out, t)
		}
	}
	if len(out) > maxFleetTargets {
		out = out[:maxFleetTargets]
	}
	return out, nil
}

// fleetAggregation says how one metric combines across targets: rates,
// counts, and sizes add up; ratios are averaged over the targets that report
// them; lag and peaks keep the maximum.
func fleetAggregation(metric string) string {
	switch {
	case strings.HasSuffix(metric, "_ratio"):
		return "mean"
	case strings.Contains(metric, "lag"), strings.Contains(metric, "delay"), strings.Contains(metric, "max_"),
		strings.Contains(metric, "oldest"), strings.HasSuffix(metric, "_seconds"):
		return "max"
	default:
		return "sum"
	}
}

type fleetPoint struct {
	T       time.Time `json:"t"`
	V       *float64  `json:"v"`
	Targets int       `json:"targets"`
}

type fleetSeries struct {
	Metric      string       `json:"metric"`
	Aggregation string       `json:"aggregation"`
	Points      []fleetPoint `json:"points"`
}

// fleetMetrics returns metric series combined across targets. A bucket is
// unknown (null) when no target has a known value in it; `targets` says how
// many contributed.
func (s *Server) fleetMetrics(w http.ResponseWriter, r *http.Request) {
	from, to, err := timeRange(r, time.Hour)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	var metrics []string
	for _, n := range strings.Split(r.URL.Query().Get("names"), ",") {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if len(n) > 80 || strings.ContainsAny(n, " {}'\"") {
			writeError(w, r, http.StatusBadRequest, "invalid_request", "invalid metric name")
			return
		}
		metrics = append(metrics, n)
	}
	if len(metrics) == 0 || len(metrics) > 12 {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "names must list 1 to 12 metrics")
		return
	}
	ts, err := s.fleetTargets(r)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	// Buckets hold a sample of every target: the slowest collection decides.
	minStep := 10 * time.Second
	for _, t := range ts {
		minStep = max(minStep, s.collectionStep(r, t, metrics))
	}
	step := s.d.Store.SeriesStep(from, stepFor(from, to, minStep))
	type acc struct {
		sum, max float64
		n        int
	}
	buckets := map[string]map[time.Time]*acc{}
	for _, t := range ts {
		series, err := s.d.Store.QuerySeries(r.Context(), t.ID, metrics, from, to, step)
		if err != nil {
			s.internal(w, r, err)
			return
		}
		// Labelled series (per database, mount point, …) are folded per
		// target first, using the metric's own fleet aggregation.
		perTarget := map[string]map[time.Time]*acc{}
		for _, se := range series {
			m := perTarget[se.Metric]
			if m == nil {
				m = map[time.Time]*acc{}
				perTarget[se.Metric] = m
			}
			for _, p := range se.Points {
				if p.V == nil || p.Gap {
					continue
				}
				a := m[p.T]
				if a == nil {
					a = &acc{max: *p.V}
					m[p.T] = a
				}
				a.sum += *p.V
				a.max = max(a.max, *p.V)
				a.n++
			}
		}
		for metric, m := range perTarget {
			agg := fleetAggregation(metric)
			out := buckets[metric]
			if out == nil {
				out = map[time.Time]*acc{}
				buckets[metric] = out
			}
			for at, a := range m {
				v := a.sum
				switch agg {
				case "mean":
					v = a.sum / float64(a.n)
				case "max":
					v = a.max
				}
				b := out[at]
				if b == nil {
					b = &acc{max: v}
					out[at] = b
				}
				b.sum += v
				b.max = max(b.max, v)
				b.n++
			}
		}
	}
	result := make([]fleetSeries, 0, len(metrics))
	for _, metric := range metrics {
		agg := fleetAggregation(metric)
		fs := fleetSeries{Metric: metric, Aggregation: agg, Points: []fleetPoint{}}
		times := make([]time.Time, 0, len(buckets[metric]))
		for at := range buckets[metric] {
			times = append(times, at)
		}
		slices.SortFunc(times, func(a, b time.Time) int { return a.Compare(b) })
		for _, at := range times {
			b := buckets[metric][at]
			v := b.sum
			switch agg {
			case "mean":
				v = b.sum / float64(b.n)
			case "max":
				v = b.max
			}
			fs.Points = append(fs.Points, fleetPoint{T: at, V: &v, Targets: b.n})
		}
		result = append(result, fs)
	}
	writeJSON(w, http.StatusOK, map[string]any{"from": from, "to": to, "step_seconds": step.Seconds(), "targets": len(ts), "series": result})
}

type fleetQuery struct {
	repository.QueryRow
	TargetID    int64    `json:"target_id"`
	TargetName  string   `json:"target_name"`
	MeanMS      *float64 `json:"mean_exec_ms"`
	CallsPerSec *float64 `json:"calls_per_second"`
}

// fleetQueries returns the top statements across targets.
func (s *Server) fleetQueries(w http.ResponseWriter, r *http.Request) {
	from, to, err := timeRange(r, time.Hour)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	q := r.URL.Query()
	sort := q.Get("sort")
	if sort == "" {
		sort = "total_time"
	}
	if !slices.Contains([]string{"total_time", "calls", "mean_time", "rows"}, sort) {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "sort must be total_time, calls, mean_time, or rows")
		return
	}
	limit := 10
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 50 {
			writeError(w, r, http.StatusBadRequest, "invalid_request", "limit must be between 1 and 50")
			return
		}
		limit = n
	}
	command := q.Get("command")
	if command != "" && !slices.Contains(observe.Commands, command) {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "unknown command class")
		return
	}
	filter := q.Get("q")
	if len(filter) > 200 {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "filter is too long")
		return
	}
	ts, err := s.fleetTargets(r)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	win := to.Sub(from).Seconds()
	items := []fleetQuery{}
	for _, t := range ts {
		rows, _, err := s.d.Store.ListQueries(r.Context(), repository.QueryListParams{TargetID: t.ID, From: from, To: to, Sort: sort,
			Desc: true, Limit: limit, IncludeText: t.QueryTextEnabled && q.Get("include_text") != "false", ExcludeUser: t.User, Command: command, Filter: filter})
		if err != nil {
			s.internal(w, r, err)
			return
		}
		for _, row := range rows {
			it := fleetQuery{QueryRow: row, TargetID: t.ID, TargetName: t.Name}
			if row.Calls > 0 {
				m := row.TotalMS / float64(row.Calls)
				it.MeanMS = &m
			}
			cps := float64(row.Calls) / win
			it.CallsPerSec = &cps
			items = append(items, it)
		}
	}
	key := func(it fleetQuery) float64 {
		switch sort {
		case "calls":
			return float64(it.Calls)
		case "mean_time":
			if it.MeanMS == nil {
				return 0
			}
			return *it.MeanMS
		case "rows":
			return float64(it.Rows)
		}
		return it.TotalMS
	}
	slices.SortStableFunc(items, func(a, b fleetQuery) int { return cmp.Compare(key(b), key(a)) })
	if len(items) > limit {
		items = items[:limit]
	}
	writeJSON(w, http.StatusOK, map[string]any{"from": from, "to": to, "sort": sort, "items": items, "targets": len(ts)})
}

type fleetLiveTarget struct {
	Target           targets.View              `json:"target"`
	Connected        bool                      `json:"connected"`
	ObservedAt       *time.Time                `json:"observed_at"`
	Latest           map[string]latestValue    `json:"latest"`
	Counts           *observe.ActivityCounts   `json:"counts"`
	Report           *report.Report            `json:"report"`
	Replication      *observe.Replication      `json:"replication"`
	ConnectionGroups []observe.ConnectionGroup `json:"connection_groups"`
	Host             *observe.HostMetrics      `json:"host"`
}

// maxLiveSessions bounds the sessions returned per target.
const maxLiveSessions = 200

// fleetLive returns each target's latest in-memory observation: sessions and
// blocking analysis, replication state, connection groups, and key values.
// Nothing here reads the store or contacts targets.
func (s *Server) fleetLive(w http.ResponseWriter, r *http.Request) {
	ts, err := s.fleetTargets(r)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := make([]fleetLiveTarget, 0, len(ts))
	for _, t := range ts {
		e := fleetLiveTarget{Target: t, Latest: map[string]latestValue{}, ConnectionGroups: []observe.ConnectionGroup{}}
		if v, ok := s.view(r, t.ID); ok {
			e.Connected = v.Connected
			e.Latest = latestValues(v)
			e.Replication = v.Replication
			e.Host = v.Host
			if v.Activity != nil {
				at := v.Activity.At
				e.ObservedAt = &at
				c := v.Activity.Counts
				e.Counts = &c
				if v.Activity.Groups != nil {
					e.ConnectionGroups = v.Activity.Groups
				}
			}
			e.Report = boundedReport(v)
		}
		out = append(out, e)
	}
	writeJSON(w, http.StatusOK, map[string]any{"targets": out, "generated_at": time.Now().UTC()})
}

// boundedReport returns the view's report with at most maxLiveSessions
// sessions, keeping every session that takes part in blocking.
func boundedReport(v collector.View) *report.Report {
	if v.Report == nil || len(v.Report.Snapshot.Sessions) <= maxLiveSessions {
		return v.Report
	}
	r := *v.Report
	involved := map[int]bool{}
	for _, e := range r.Analysis.Edges {
		involved[e.Blocker], involved[e.Waiter] = true, true
	}
	kept := make([]model.Session, 0, maxLiveSessions)
	for _, s := range r.Snapshot.Sessions {
		if involved[s.PID] || len(kept) < maxLiveSessions {
			kept = append(kept, s)
		}
	}
	r.Snapshot.Sessions = kept
	return &r
}

// system reports LockLens' own health for the status panel.
func (s *Server) system(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	storeOK := s.d.Store.Ping(ctx) == nil
	schema, _ := s.d.Store.SchemaVersion(ctx)
	ts, err := s.d.Targets.List(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	states := map[string]int{}
	failing, enabledCollectors := 0, 0
	for _, t := range ts {
		states[t.Status.State]++
		for _, c := range t.Status.Collectors {
			if !c.Enabled {
				continue
			}
			enabledCollectors++
			if c.LastError != nil {
				failing++
			}
		}
	}
	open, _, err := s.d.Store.ListIncidents(r.Context(), repository.IncidentFilter{State: "open", Limit: 200})
	if err != nil {
		s.internal(w, r, err)
		return
	}
	critical := 0
	for _, in := range open {
		if in.Severity == "critical" {
			critical++
		}
	}
	m := s.d.Metrics
	cluster := map[string]any{"server_id": "", "servers": []repository.Server{}}
	if s.d.Cluster != nil {
		cluster["server_id"], cluster["servers"] = s.d.Cluster.ID(), s.d.Cluster.Servers()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"cluster":   cluster,
		"version":   s.d.Tool.Version,
		"demo_mode": s.d.DemoMode,
		"time":      time.Now().UTC(),
		"store":     map[string]any{"reachable": storeOK, "schema_version": schema},
		"writer": map[string]any{"queue_length": m.WriteQueueLength.Total(), "queue_capacity": m.WriteQueueCap.Total(),
			"dropped_batches": m.StoreDropped.Total(), "write_errors": m.StoreWriteErrors.Total()},
		"targets":    map[string]any{"total": len(ts), "by_state": states},
		"collectors": map[string]any{"enabled": enabledCollectors, "failing": failing},
		"incidents":  map[string]any{"open": len(open), "critical": critical},
	})
}

type fleetEntry struct {
	Target targets.View            `json:"target"`
	Latest map[string]latestValue  `json:"latest"`
	Counts repository.TargetCounts `json:"counts"`
}

func (s *Server) fleet(w http.ResponseWriter, r *http.Request) {
	ts, err := s.d.Targets.List(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	counts, err := s.d.Store.CountsByTarget(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := make([]fleetEntry, 0, len(ts))
	for _, t := range ts {
		e := fleetEntry{Target: t, Latest: map[string]latestValue{}, Counts: counts[t.ID]}
		if v, ok := s.view(r, t.ID); ok {
			e.Latest = latestValues(v)
		}
		out = append(out, e)
	}
	writeJSON(w, http.StatusOK, map[string]any{"targets": out, "generated_at": time.Now().UTC()})
}
