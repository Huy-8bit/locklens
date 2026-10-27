// Package baseline describes how each statement normally behaves, from its
// own history, so rules compare it with itself instead of only with the
// window just before.
//
// History is aggregated into points (one hour by default) per statement.
// Every measure is summarised with the median and the median absolute
// deviation (MAD), which a few extreme hours cannot move, and a robust
// z-score compares a current value with them. When enough days are known,
// the points of the same hour of the day form their own profile, so a nightly
// batch is compared with other nights rather than with quiet afternoons.
//
// Everything here is deterministic arithmetic over stored aggregates; there
// is no model to train and every number can be shown to the user.
package baseline

import (
	"math"
	"slices"
	"time"
)

// Measure names a per-statement measure.
type Measure string

// Measures kept for each statement.
const (
	MeanMS      Measure = "mean_ms"         // execution time per call
	CallsPerSec Measure = "calls_per_s"     // executions per second
	LoadMSPerS  Measure = "load_ms_per_s"   // execution time per second (load)
	RowsPerCall Measure = "rows_per_call"   // rows returned or affected per call
	ReadPerCall Measure = "reads_per_call"  // shared blocks read from disk or OS cache per call
	TempPerCall Measure = "temp_per_call"   // temporary blocks written per call
	HitRatio    Measure = "cache_hit_ratio" // shared hits / (hits + reads)
)

// Point is one aggregated period of one statement. Covered is the
// statements collection time inside the period; a point without coverage is
// unknown and never counts as zero calls.
type Point struct {
	At       time.Time
	Covered  float64
	Calls    int64
	TotalMS  float64
	Rows     int64
	BlkRead  int64
	BlkHit   int64
	TempW    int64
	HadReset bool
}

// Values returns the point's measures. Per-call measures need minCalls
// calls; rates need coverage.
func (p Point) Values(minCalls int64) map[Measure]float64 {
	out := map[Measure]float64{}
	if p.Covered > 0 {
		out[CallsPerSec] = float64(p.Calls) / p.Covered
		out[LoadMSPerS] = p.TotalMS / p.Covered
	}
	if p.Calls >= minCalls && p.Calls > 0 {
		n := float64(p.Calls)
		out[MeanMS] = p.TotalMS / n
		out[RowsPerCall] = float64(p.Rows) / n
		out[ReadPerCall] = float64(p.BlkRead) / n
		out[TempPerCall] = float64(p.TempW) / n
		if p.BlkRead+p.BlkHit > 0 {
			out[HitRatio] = float64(p.BlkHit) / float64(p.BlkRead+p.BlkHit)
		}
	}
	return out
}

// Stat is a robust summary of one measure.
type Stat struct {
	Median float64 `json:"median"`
	MAD    float64 `json:"mad"`
	N      int     `json:"n"`
}

// madScale makes the MAD comparable with a standard deviation for normally
// distributed data.
const madScale = 1.4826

// Robust summarises values; N = 0 when there are none.
func Robust(values []float64) Stat {
	if len(values) == 0 {
		return Stat{}
	}
	v := slices.Clone(values)
	slices.Sort(v)
	med := median(v)
	dev := make([]float64, len(v))
	for i, x := range v {
		dev[i] = math.Abs(x - med)
	}
	slices.Sort(dev)
	return Stat{Median: med, MAD: median(dev), N: len(v)}
}

func median(sorted []float64) float64 {
	n := len(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

// Spread is the robust standard deviation, never below floor: with a
// constant history (MAD = 0) any tiny change would otherwise be infinitely
// unusual.
func (s Stat) Spread(floor float64) float64 {
	return math.Max(madScale*s.MAD, floor)
}

// Z is how many robust deviations v lies above the median.
func (s Stat) Z(v, floor float64) float64 {
	sp := s.Spread(floor)
	if sp <= 0 {
		return 0
	}
	return (v - s.Median) / sp
}

// Profile summarises the measures of a set of points.
type Profile struct {
	// Points is the number of covered points (periods with collections).
	Points int              `json:"points"`
	Stats  map[Measure]Stat `json:"stats"`
}

// Basis says which points a comparison used.
type Basis string

// Bases of a comparison.
const (
	BasisHourOfDay Basis = "same hour of day"
	BasisAllHours  Basis = "all hours"
)

// Baseline of one statement.
type Baseline struct {
	From         time.Time `json:"from"`
	To           time.Time `json:"to"`
	PointSeconds float64   `json:"point_seconds"`
	All          Profile   `json:"all"`
	// ByHour holds profiles per UTC hour of the day, for hours with enough
	// points of their own.
	ByHour map[int]Profile `json:"by_hour,omitempty"`
	// Days is how many distinct days the points span.
	Days int `json:"days"`
}

// MinProfilePoints is how many points of the same hour a time-of-day profile
// needs (that many days with this hour covered).
const MinProfilePoints = 5

// Build summarises points. coverage lists every covered period of the
// target, so periods in which the statement did not run count as zero calls
// instead of being ignored; periods with a statistics reset are skipped.
func Build(points []Point, coverage map[time.Time]float64, resets map[time.Time]bool, from, to time.Time, pointSeconds float64, minCalls int64) Baseline {
	byTime := map[time.Time]Point{}
	for _, p := range points {
		byTime[p.At] = p
	}
	all := map[Measure][]float64{}
	hours := map[int]map[Measure][]float64{}
	hourCount := map[int]int{}
	days := map[string]bool{}
	covered := 0
	for at, cov := range coverage {
		if cov <= 0 || resets[at] {
			continue
		}
		p, ok := byTime[at]
		if !ok {
			p = Point{At: at}
		}
		p.Covered = cov
		covered++
		days[at.UTC().Format("2006-01-02")] = true
		h := at.UTC().Hour()
		if hours[h] == nil {
			hours[h] = map[Measure][]float64{}
		}
		hourCount[h]++
		for m, v := range p.Values(minCalls) {
			all[m] = append(all[m], v)
			hours[h][m] = append(hours[h][m], v)
		}
	}
	b := Baseline{From: from, To: to, PointSeconds: pointSeconds, All: profile(all, covered), Days: len(days)}
	for h, vals := range hours {
		if hourCount[h] >= MinProfilePoints {
			if b.ByHour == nil {
				b.ByHour = map[int]Profile{}
			}
			b.ByHour[h] = profile(vals, hourCount[h])
		}
	}
	return b
}

func profile(vals map[Measure][]float64, points int) Profile {
	p := Profile{Points: points, Stats: map[Measure]Stat{}}
	for m, v := range vals {
		p.Stats[m] = Robust(v)
	}
	return p
}

// For returns the profile to compare a value observed at t with: the same
// hour of the day when that profile exists, otherwise all hours.
func (b Baseline) For(t time.Time) (Profile, Basis) {
	if p, ok := b.ByHour[t.UTC().Hour()]; ok {
		return p, BasisHourOfDay
	}
	return b.All, BasisAllHours
}
