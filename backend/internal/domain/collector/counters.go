package collector

import "time"

// CounterSample is one reading of cumulative counters from one source.
//
// Values are keyed first by scope (for example a database OID in
// pg_stat_database, or "" for a cluster-wide view) and then by counter name.
// Resets are tracked per scope.
type CounterSample struct {
	At time.Time
	// ServerStart is pg_postmaster_start_time(); a change means the server
	// restarted and every counter started again from zero.
	ServerStart time.Time
	Scopes      map[string]ScopeCounters
}

// ScopeCounters holds the counters of one scope.
type ScopeCounters struct {
	// StatsReset is the scope's stats_reset timestamp, if the view has one.
	StatsReset *time.Time
	Values     map[string]float64
}

// Unknown reasons. They are recorded with gaps and exposed to clients.
const (
	ReasonFirstSample = "first_sample"
	ReasonRestart     = "server_restart"
	ReasonStatsReset  = "stats_reset"
	ReasonNegative    = "counter_decreased"
	ReasonGap         = "collection_gap"
	ReasonNoScopes    = "no_common_scopes"
)

// DeltaResult is the change of one counter between two samples.
type DeltaResult struct {
	// Delta is nil when the change is unknown; Reason then says why.
	Delta   *float64
	Elapsed time.Duration
	Reason  string
}

// Rate returns Delta per second, or nil.
func (d DeltaResult) Rate() *float64 {
	if d.Delta == nil || d.Elapsed <= 0 {
		return nil
	}
	r := *d.Delta / d.Elapsed.Seconds()
	return &r
}

// Delta computes the change of counter name summed over the scopes present in
// both samples. Scopes that appear or disappear between samples (a database
// created or dropped) are ignored instead of producing a jump.
//
// The result is unknown (never zero) when:
//   - there is no previous sample;
//   - the server restarted;
//   - any common scope's stats_reset changed or a counter decreased;
//   - more than maxGap elapsed, because a single average over a long gap would
//     hide what happened in between (a gap is recorded instead);
//   - no scope is present in both samples.
func Delta(prev *CounterSample, cur CounterSample, name string, maxGap time.Duration) DeltaResult {
	if prev == nil {
		return DeltaResult{Reason: ReasonFirstSample}
	}
	res := DeltaResult{Elapsed: cur.At.Sub(prev.At)}
	if !prev.ServerStart.IsZero() && !cur.ServerStart.Equal(prev.ServerStart) {
		res.Reason = ReasonRestart
		return res
	}
	if maxGap > 0 && res.Elapsed > maxGap {
		res.Reason = ReasonGap
		return res
	}
	var sum float64
	common := 0
	for scope, c := range cur.Scopes {
		p, ok := prev.Scopes[scope]
		if !ok {
			continue
		}
		cv, okc := c.Values[name]
		pv, okp := p.Values[name]
		if !okc || !okp {
			continue
		}
		if !sameTime(p.StatsReset, c.StatsReset) {
			res.Reason = ReasonStatsReset
			return res
		}
		if cv < pv {
			res.Reason = ReasonNegative
			return res
		}
		sum += cv - pv
		common++
	}
	if common == 0 {
		res.Reason = ReasonNoScopes
		return res
	}
	res.Delta = &sum
	return res
}

// Ratio returns num/(num+other) for two deltas, or nil if unknown or the
// denominator is zero (no activity means the ratio is undefined, not 100%).
func Ratio(num, other DeltaResult) *float64 {
	if num.Delta == nil || other.Delta == nil {
		return nil
	}
	den := *num.Delta + *other.Delta
	if den <= 0 {
		return nil
	}
	r := *num.Delta / den
	return &r
}

func sameTime(a, b *time.Time) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	}
	return a.Equal(*b)
}
