// Package finding defines the result type of LockLens' rule engine, shared by
// the rules, the store, the incident recorder, and the API.
package finding

import "time"

// Severity of a finding.
type Severity string

// Severities.
const (
	Info     Severity = "info"
	Warning  Severity = "warning"
	Critical Severity = "critical"
)

// Status of a finding.
type Status string

// Statuses. Resolved is assigned by the store when a finding stops firing.
const (
	Active           Status = "active"
	InsufficientData Status = "insufficient_data"
	Resolved         Status = "resolved"
)

// Evidence is one observed value with its source.
type Evidence struct {
	Label  string     `json:"label"`
	Value  any        `json:"value"`
	Unit   string     `json:"unit,omitempty"`
	Source string     `json:"source"`
	At     *time.Time `json:"at,omitempty"`
}

// Finding is the output of a rule.
type Finding struct {
	RuleID      string `json:"rule_id"`
	RuleVersion int    `json:"rule_version"`
	// Key distinguishes findings of one rule (for example one per root
	// blocker); "_" when the rule has one finding per target.
	Key                  string     `json:"key"`
	Severity             Severity   `json:"severity"`
	Title                string     `json:"title"`
	Status               Status     `json:"status"`
	ObservedFrom         time.Time  `json:"observed_from"`
	ObservedTo           time.Time  `json:"observed_to"`
	Observed             []string   `json:"observed"`
	Evidence             []Evidence `json:"evidence"`
	Explanation          string     `json:"explanation"`
	PossibleExplanations []string   `json:"possible_explanations"`
	NextChecks           []string   `json:"next_checks"`
	Limitations          string     `json:"limitations"`
	// Trigger marks findings that may open an incident.
	Trigger bool `json:"trigger"`
}
