package rules

import (
	"fmt"
	"strings"
	"time"
)

func newFinding(id, key string, sev Severity, title string, from, to time.Time) Finding {
	return Finding{RuleID: id, RuleVersion: Versions[id], Key: key, Severity: sev, Title: title, Status: Active,
		ObservedFrom: from.UTC(), ObservedTo: to.UTC(), Observed: []string{}, Evidence: []Evidence{},
		PossibleExplanations: []string{}, NextChecks: []string{}}
}

// insufficient reports that a rule could not be evaluated. It is never a pass.
func insufficient(id string, now time.Time, why string) Finding {
	f := newFinding(id, "_", Info, "Not evaluated: insufficient data", now, now)
	f.Status = InsufficientData
	f.Explanation = why
	f.Limitations = "The rule could not run, so the absence of this finding does not mean the condition is absent."
	return f
}

func ev(label string, value any, unit, source string) Evidence {
	return Evidence{Label: label, Value: value, Unit: unit, Source: source}
}

func fmtDuration(sec float64) string {
	d := time.Duration(sec * float64(time.Second)).Round(time.Second)
	if d < time.Minute {
		return d.String()
	}
	return strings.TrimSuffix(d.String(), "0s")
}

func fmtBytes(b float64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	i := 0
	for b >= 1024 && i < len(units)-1 {
		b /= 1024
		i++
	}
	return fmt.Sprintf("%.1f %s", b, units[i])
}

func deref[T any](p *T, def T) T {
	if p == nil {
		return def
	}
	return *p
}

func strOr(p *string, def string) string {
	if p == nil {
		return def
	}
	return *p
}
