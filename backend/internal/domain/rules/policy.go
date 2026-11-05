package rules

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Modes of a rule.
const (
	// ModeIncident reports findings and opens an incident (and notifies)
	// when the rule fires.
	ModeIncident = "incident"
	// ModeFinding reports findings only.
	ModeFinding = "finding"
	// ModeOff does not report the rule.
	ModeOff = "off"
)

// AllRules lists every rule ID in display order.
var AllRules = []string{RuleTargetUnreachable, RuleBlockingChain, RuleIdleBlocker, RuleConnections, RuleLongTransaction,
	RuleLongRunning, RuleMeanRegression, RuleCallVolume, RuleLoadIncrease, RuleIORegression, RulePatternChange, RuleNPlusOne,
	RuleTempFiles, RuleDeadlocks, RuleDeadTuples, RuleXIDAge, RuleReplicaLag, RuleSlotRetainedWAL, RuleArchiverFailures}

// DefaultModes are the modes of the rules unless changed: the rules that
// mean an outage or data loss risk open incidents, the others report
// findings.
func DefaultModes() map[string]string {
	m := map[string]string{}
	for _, r := range AllRules {
		m[r] = ModeFinding
	}
	for _, r := range append(slices.Clone(TriggerRules), RuleTargetUnreachable, RuleReplicaLag, RuleXIDAge, RuleArchiverFailures) {
		m[r] = ModeIncident
	}
	return m
}

// Override changes rule modes and thresholds for an environment or a
// target. Thresholds are keyed by their JSON names (for example
// "blocking_min_seconds").
type Override struct {
	Modes      map[string]string  `json:"modes,omitempty"`
	Thresholds map[string]float64 `json:"thresholds,omitempty"`
}

// Overrides are the overrides of environments (by name) and targets (by
// ID).
type Overrides struct {
	Environments map[string]Override `json:"environments"`
	Targets      map[int64]Override  `json:"targets"`
}

// Validate checks the rule IDs, modes, and threshold names of an override.
func (o Override) Validate() error {
	known := thresholdKeys()
	var errs []error
	for r, m := range o.Modes {
		if !slices.Contains(AllRules, r) {
			errs = append(errs, fmt.Errorf("unknown rule %q", r))
		}
		if m != ModeIncident && m != ModeFinding && m != ModeOff {
			errs = append(errs, fmt.Errorf("rule %s: mode must be incident, finding, or off", r))
		}
	}
	for k := range o.Thresholds {
		if !slices.Contains(known, k) {
			errs = append(errs, fmt.Errorf("unknown threshold %q", k))
		}
	}
	return errors.Join(errs...)
}

// Validate checks every override and that thresholds stay valid with them.
func (o Overrides) Validate(base Thresholds) error {
	var errs []error
	for env, ov := range o.Environments {
		if err := ov.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("environment %s: %w", env, err))
		} else if _, err := applyThresholds(base, ov.Thresholds); err != nil {
			errs = append(errs, fmt.Errorf("environment %s: %w", env, err))
		}
	}
	for id, ov := range o.Targets {
		if err := ov.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("instance %d: %w", id, err))
		} else if _, err := applyThresholds(base, ov.Thresholds); err != nil {
			errs = append(errs, fmt.Errorf("instance %d: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

// Policy is what applies to one target: thresholds and rule modes after
// the overrides of its environment and its own.
type Policy struct {
	Thresholds Thresholds        `json:"thresholds"`
	Modes      map[string]string `json:"modes"`
	// Sources names where each value that is not the global one comes
	// from ("environment" or "instance"), keyed by threshold name or
	// "mode:<rule>".
	Sources map[string]string `json:"sources"`
}

// Effective layers the global thresholds and modes, then the
// environment's override, then the target's.
func Effective(base Thresholds, modes map[string]string, env, target *Override) (Policy, error) {
	p := Policy{Thresholds: base, Modes: DefaultModes(), Sources: map[string]string{}}
	maps.Copy(p.Modes, modes)
	for _, layer := range []struct {
		name string
		o    *Override
	}{{"environment", env}, {"instance", target}} {
		if layer.o == nil {
			continue
		}
		for r, m := range layer.o.Modes {
			p.Modes[r] = m
			p.Sources["mode:"+r] = layer.name
		}
		th, err := applyThresholds(p.Thresholds, layer.o.Thresholds)
		if err != nil {
			return Policy{}, fmt.Errorf("%s override: %w", layer.name, err)
		}
		p.Thresholds = th
		for k := range layer.o.Thresholds {
			p.Sources[k] = layer.name
		}
	}
	return p, nil
}

// Mode returns a rule's mode.
func (p Policy) Mode(rule string) string {
	if m, ok := p.Modes[rule]; ok {
		return m
	}
	return ModeFinding
}

// Fires reports whether a finding of a rule in incident mode should open
// (or keep open) an incident: rules that decide it themselves set Trigger;
// for the others, an active warning or critical finding does.
func (p Policy) Fires(f Finding) bool {
	if p.Mode(f.RuleID) != ModeIncident || f.Status != Active {
		return false
	}
	if slices.Contains(TriggerRules, f.RuleID) {
		return f.Trigger
	}
	return f.Severity == Warning || f.Severity == Critical
}

// Filter drops the findings of rules that are off.
func (p Policy) Filter(fs []Finding) []Finding {
	return slices.DeleteFunc(fs, func(f Finding) bool { return p.Mode(f.RuleID) == ModeOff })
}

// applyThresholds sets thresholds by JSON name and validates the result.
func applyThresholds(base Thresholds, set map[string]float64) (Thresholds, error) {
	if len(set) == 0 {
		return base, nil
	}
	raw, _ := json.Marshal(base)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	for k, v := range set {
		m[k] = v
	}
	raw, _ = json.Marshal(m)
	var out Thresholds
	// Integer thresholds given as whole numbers decode; fractions fail.
	if err := json.Unmarshal(raw, &out); err != nil {
		return base, fmt.Errorf("thresholds: %s", strings.TrimPrefix(err.Error(), "json: "))
	}
	return out, out.Validate()
}

// thresholdKeys are the JSON names of the thresholds.
func thresholdKeys() []string {
	raw, _ := json.Marshal(Defaults())
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return slices.Sorted(maps.Keys(m))
}

// ThresholdKeys are the JSON names of the thresholds, sorted.
func ThresholdKeys() []string { return thresholdKeys() }
