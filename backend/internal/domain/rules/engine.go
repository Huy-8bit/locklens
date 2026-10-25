package rules

import (
	"cmp"
	"slices"
)

// Evaluate runs every rule and returns findings sorted by severity, rule, key.
func Evaluate(in Input, th Thresholds) []Finding {
	var out []Finding
	out = append(out, blockingRules(in, th)...)
	out = append(out, connectionRule(in, th)...)
	out = append(out, longTransactionRule(in, th)...)
	out = append(out, workloadRules(in, th)...)
	out = append(out, tempFilesRule(in, th)...)
	out = append(out, deadlocksRule(in, th)...)
	out = append(out, vacuumRules(in, th)...)
	out = append(out, replicationRules(in, th)...)
	out = append(out, archiverRule(in, th)...)
	rank := map[Severity]int{Critical: 0, Warning: 1, Info: 2}
	slices.SortStableFunc(out, func(a, b Finding) int {
		if a.Status != b.Status {
			return cmp.Compare(statusRank(a.Status), statusRank(b.Status))
		}
		if c := cmp.Compare(rank[a.Severity], rank[b.Severity]); c != 0 {
			return c
		}
		if c := cmp.Compare(a.RuleID, b.RuleID); c != 0 {
			return c
		}
		return cmp.Compare(a.Key, b.Key)
	})
	return out
}

func statusRank(s Status) int {
	if s == Active {
		return 0
	}
	return 1
}
