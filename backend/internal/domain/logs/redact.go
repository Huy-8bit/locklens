package logs

import (
	"regexp"
	"strings"
)

var (
	// A quoted string in a message, unless it names a database object.
	reQuoted = regexp.MustCompile(`(\b(?:relation|table|index|column|constraint|function|schema|database|role|type|sequence|view|extension|operator|trigger|policy|publication|subscription|slot|tablespace|collation|domain|server|user mapping|cursor|prepared statement|savepoint|parameter|configuration parameter)\s+)?"([^"]*)"`)
	reSingle = regexp.MustCompile(`'(?:[^']|'')*'`)
	// A literal in a plan condition: a quoted string or a number that is
	// not part of a name or a type cast.
	rePlanLit = regexp.MustCompile(`'(?:[^']|'')*'|(?:^|[^\w.$:])(-?\d+(?:\.\d+)?)\b`)
)

// Redact removes data values from an error message: quoted values are
// replaced by "?", while quoted names of database objects (relation "x")
// stay. DETAIL lines, which carry key values, are never kept.
func Redact(msg string) string {
	msg = reQuoted.ReplaceAllStringFunc(msg, func(s string) string {
		m := reQuoted.FindStringSubmatch(s)
		if m[1] != "" {
			return s
		}
		return `"?"`
	})
	return reSingle.ReplaceAllString(msg, "'?'")
}

// planConditionKeys are the plan lines and JSON keys that hold expressions
// with constants.
var planConditionKeys = []string{"Filter", "Cond", "Index Cond", "Recheck Cond", "Hash Cond", "Merge Cond", "Join Filter",
	"One-Time Filter", "Sort Key", "Group Key", "Output", "Order By", "Cache Key", "Presorted Key", "Hash Key"}

// RedactPlanText replaces the constants in the conditions of a text plan.
func RedactPlanText(plan string) string {
	lines := strings.Split(plan, "\n")
	for i, l := range lines {
		if k := strings.Index(l, ": "); k > 0 && isConditionKey(strings.TrimSpace(l[:k])) {
			lines[i] = l[:k+2] + redactExpr(l[k+2:])
		}
	}
	return strings.Join(lines, "\n")
}

func isConditionKey(k string) bool {
	for _, c := range planConditionKeys {
		if k == c || strings.HasSuffix(k, " "+c) || strings.HasSuffix(k, c) {
			return true
		}
	}
	return false
}

func redactExpr(s string) string {
	return rePlanLit.ReplaceAllStringFunc(s, func(m string) string {
		if strings.HasPrefix(m, "'") {
			return "'?'"
		}
		// Keep the character before the number.
		sub := rePlanLit.FindStringSubmatch(m)
		return strings.TrimSuffix(m, sub[1]) + "?"
	})
}

// redactPlanJSON replaces the constants in the conditions of a JSON plan.
func redactPlanJSON(v any) {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			if s, ok := val.(string); ok && isConditionKey(k) {
				x[k] = redactExpr(s)
				continue
			}
			if list, ok := val.([]any); ok && isConditionKey(k) {
				for i, item := range list {
					if s, ok := item.(string); ok {
						list[i] = redactExpr(s)
					}
				}
				continue
			}
			redactPlanJSON(val)
		}
	case []any:
		for _, item := range x {
			redactPlanJSON(item)
		}
	}
}
