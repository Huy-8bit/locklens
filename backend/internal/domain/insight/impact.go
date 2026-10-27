// Package insight ranks query performance findings by their impact and
// correlates the evidence around an anomaly into ranked hypotheses.
//
// Everything here is deterministic: the same evidence always gives the same
// score and the same hypotheses, and every number can be traced back to a
// measurement. Hypotheses are explanations to check, never confirmed causes.
package insight

import (
	"fmt"
	"math"
	"time"

	"github.com/locklens/locklens/internal/domain/finding"
)

// Component is one explained part of an impact score.
type Component struct {
	Name   string  `json:"name"`
	Points float64 `json:"points"`
	Max    float64 `json:"max"`
	Detail string  `json:"detail"`
}

// Impact is a 0-100 score of how much a finding matters now, with the parts
// it is made of. It orders findings; it is not a probability.
type Impact struct {
	Score      float64     `json:"score"`
	Components []Component `json:"components"`
	// Factor is the confidence adjustment applied to the sum of components.
	Factor float64 `json:"confidence_factor"`
}

// Component ceilings; they add up to 100.
const (
	maxShare    = 30
	maxAdded    = 20
	maxSeverity = 20
	maxBlocking = 15
	maxDuration = 15
)

// Score computes the impact of a finding that has been active for activeFor.
func Score(f finding.Finding, activeFor time.Duration) Impact {
	m := f.Metrics
	var cs []Component
	if share, ok := m["share_of_load"]; ok {
		cs = append(cs, Component{Name: "Share of execution time", Max: maxShare, Points: maxShare * clamp01(share),
			Detail: fmt.Sprintf("%.1f%% of all statement execution time in the window", 100*share)})
	}
	if added, ok := m["added_load_ms_per_s"]; ok {
		// Logarithmic: 1000 ms of execution per second (one busy core) is the
		// ceiling.
		cs = append(cs, Component{Name: "Added load", Max: maxAdded, Points: maxAdded * clamp01(math.Log10(1+added)/3),
			Detail: fmt.Sprintf("%.1f ms of execution per second above the usual level", added)})
	}
	sev := map[finding.Severity]float64{finding.Critical: maxSeverity, finding.Warning: 12, finding.Info: 4}[f.Severity]
	cs = append(cs, Component{Name: "Severity", Max: maxSeverity, Points: sev, Detail: "rule severity " + string(f.Severity)})
	if n, ok := m["blocked_sessions"]; ok && n > 0 {
		cs = append(cs, Component{Name: "Sessions blocked", Max: maxBlocking, Points: maxBlocking * clamp01(n/10),
			Detail: fmt.Sprintf("%.0f session(s) wait behind it", n)})
	}
	dur := activeFor.Seconds()
	what := "active for"
	if run, ok := m["running_seconds"]; ok && run > dur {
		dur, what = run, "running for"
	}
	if dur > 0 {
		// Logarithmic: an hour is the ceiling.
		cs = append(cs, Component{Name: "Duration", Max: maxDuration, Points: maxDuration * clamp01(math.Log10(1+dur/60)/math.Log10(61)),
			Detail: fmt.Sprintf("%s %s", what, (time.Duration(dur) * time.Second).Round(time.Second))})
	}
	sum := 0.0
	for i := range cs {
		cs[i].Points = math.Round(cs[i].Points*10) / 10
		sum += cs[i].Points
	}
	factor := 0.8 // no stated confidence
	if f.Confidence != nil {
		factor = map[string]float64{finding.ConfidenceHigh: 1, finding.ConfidenceMedium: 0.85, finding.ConfidenceLow: 0.6}[f.Confidence.Level]
	}
	return Impact{Score: math.Round(sum*factor*10) / 10, Components: cs, Factor: factor}
}

func clamp01(v float64) float64 {
	if math.IsNaN(v) || v < 0 {
		return 0
	}
	return math.Min(v, 1)
}
