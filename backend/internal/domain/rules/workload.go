package rules

import (
	"cmp"
	"slices"
	"time"

	"github.com/locklens/locklens/internal/domain/observe"
)

// WindowAgg aggregates pg_stat_statements deltas of one query identity over
// a time window. Mean execution time is TotalMS / Calls within the window,
// computed from deltas; LockLens never reports percentiles because
// pg_stat_statements has no per-execution samples.
type WindowAgg struct {
	Calls   int64   `json:"calls"`
	TotalMS float64 `json:"total_exec_ms"`
	Rows    int64   `json:"rows"`
	BlkRead int64   `json:"shared_blks_read"`
	BlkHit  int64   `json:"shared_blks_hit"`
	TempW   int64   `json:"temp_blks_written"`
}

// MeanMS returns the window mean, or nil without calls.
func (w WindowAgg) MeanMS() *float64 {
	if w.Calls <= 0 {
		return nil
	}
	m := w.TotalMS / float64(w.Calls)
	return &m
}

// Comparison of a query identity between two equal-length windows.
type Comparison struct {
	MeanRatio  *float64 `json:"mean_ratio"`
	CallsRatio *float64 `json:"calls_ratio"`
	LoadRatio  *float64 `json:"total_time_ratio"`
	// MeanRegression: each call got slower.
	MeanRegression bool `json:"mean_regression"`
	// VolumeIncrease: many more calls.
	VolumeIncrease bool `json:"volume_increase"`
	// LoadIncrease: more total execution time (from either cause).
	LoadIncrease bool `json:"load_increase"`
	// TooFewCalls is true when either window is below the minimum and no
	// flag can be raised from such a small sample.
	TooFewCalls bool `json:"too_few_calls"`
}

func ratio(a, b float64) *float64 {
	if b <= 0 {
		return nil
	}
	r := a / b
	return &r
}

// Compare classifies the change from prev to cur.
func Compare(prev, cur WindowAgg, th Thresholds) Comparison {
	var c Comparison
	c.CallsRatio = ratio(float64(cur.Calls), float64(prev.Calls))
	c.LoadRatio = ratio(cur.TotalMS, prev.TotalMS)
	pm, cm := prev.MeanMS(), cur.MeanMS()
	if pm != nil && cm != nil {
		c.MeanRatio = ratio(*cm, *pm)
	}
	if prev.Calls < th.RegressionMinCalls || cur.Calls < th.RegressionMinCalls {
		c.TooFewCalls = true
		return c
	}
	if c.MeanRatio != nil && *c.MeanRatio >= th.RegressionMeanRatio && *cm-*pm >= th.RegressionMinDeltaMS {
		c.MeanRegression = true
	}
	if c.CallsRatio != nil && *c.CallsRatio >= th.VolumeCallsRatio && cur.Calls >= th.VolumeMinCalls {
		c.VolumeIncrease = true
	}
	if c.LoadRatio != nil && *c.LoadRatio >= th.VolumeCallsRatio {
		c.LoadIncrease = true
	}
	return c
}

type windowStats struct {
	aggs      map[observe.QueryKey]WindowAgg
	meta      map[observe.QueryKey]observe.QueryDelta
	covered   float64
	resets    int
	dealloc   int64
	truncated int
}

// aggregate sums in-memory statement collections with At in (from, to].
func aggregate(colls []observe.StatementsCollection, from, to time.Time) windowStats {
	w := windowStats{aggs: map[observe.QueryKey]WindowAgg{}, meta: map[observe.QueryKey]observe.QueryDelta{}}
	for _, c := range colls {
		if !c.At.After(from) || c.At.After(to) || c.Interval == nil {
			continue
		}
		w.covered += *c.Interval
		if c.Reset {
			w.resets++
		}
		if c.Dealloc != nil {
			w.dealloc += *c.Dealloc
		}
		w.truncated += c.Truncated
		for _, d := range c.Deltas {
			a := w.aggs[d.Key]
			a.Calls += d.Calls
			a.TotalMS += d.TotalMS
			a.Rows += d.Rows
			a.BlkRead += d.BlkRead
			a.BlkHit += d.BlkHit
			a.TempW += d.TempW
			w.aggs[d.Key] = a
			w.meta[d.Key] = d
		}
	}
	return w
}

// TopQuery is one entry of a workload summary.
type TopQuery struct {
	Key      observe.QueryKey `json:"key"`
	DatName  string           `json:"datname"`
	UserName string           `json:"username"`
	Text     *string          `json:"text,omitempty"`
	WindowAgg
	MeanMS *float64 `json:"mean_exec_ms"`
	Share  *float64 `json:"share_of_total_time"`
}

// TopQueries returns the n identities with the highest total execution time
// in (from, to], with their share of the observed total, and the coverage in
// seconds.
func TopQueries(colls []observe.StatementsCollection, from, to time.Time, n int) ([]TopQuery, float64) {
	w := aggregate(colls, from, to)
	var total float64
	for _, a := range w.aggs {
		total += a.TotalMS
	}
	out := make([]TopQuery, 0, len(w.aggs))
	for k, a := range w.aggs {
		m := w.meta[k]
		q := TopQuery{Key: k, DatName: m.DatName, UserName: m.UserName, Text: m.Text, WindowAgg: a, MeanMS: a.MeanMS()}
		if total > 0 {
			s := a.TotalMS / total
			q.Share = &s
		}
		out = append(out, q)
	}
	slices.SortFunc(out, func(a, b TopQuery) int { return cmp.Compare(b.TotalMS, a.TotalMS) })
	if len(out) > n {
		out = out[:n]
	}
	return out, w.covered
}
