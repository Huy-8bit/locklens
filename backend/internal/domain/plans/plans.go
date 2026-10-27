// Package plans parses saved execution plans, fingerprints their shape, and
// compares two plans of a statement.
//
// Plans come from EXPLAIN (FORMAT JSON) run on an explicit request, or are
// pasted by a user. Nothing here runs EXPLAIN, and nothing here ever needs
// EXPLAIN ANALYZE: estimated plans are compared by shape and cost. When a
// pasted plan includes actual timings they are shown, never required.
package plans

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// MaxPlanBytes bounds a stored plan.
const MaxPlanBytes = 512 << 10

// Node is one plan node, flattened.
type Node struct {
	Depth    int      `json:"depth"`
	Type     string   `json:"node_type"`
	Relation string   `json:"relation,omitempty"`
	Index    string   `json:"index,omitempty"`
	JoinType string   `json:"join_type,omitempty"`
	Strategy string   `json:"strategy,omitempty"`
	Cost     float64  `json:"total_cost"`
	Rows     float64  `json:"plan_rows"`
	Filter   string   `json:"filter,omitempty"`
	Cond     string   `json:"cond,omitempty"`
	Actual   *float64 `json:"actual_total_time,omitempty"`
}

// Plan is a parsed plan.
type Plan struct {
	Format      string   `json:"format"` // json or text
	Nodes       []Node   `json:"nodes"`
	TotalCost   *float64 `json:"total_cost"`
	Fingerprint string   `json:"fingerprint"`
	// Analyzed is true when the plan has actual timings (EXPLAIN ANALYZE
	// output pasted by a user).
	Analyzed bool `json:"analyzed"`
}

// Parse reads EXPLAIN output: the JSON format (an array with one object
// holding "Plan") or the text format.
func Parse(raw string) (Plan, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Plan{}, errors.New("the plan is empty")
	}
	if len(raw) > MaxPlanBytes {
		return Plan{}, fmt.Errorf("the plan is larger than %d KiB", MaxPlanBytes>>10)
	}
	if strings.HasPrefix(raw, "[") || strings.HasPrefix(raw, "{") {
		return parseJSON(raw)
	}
	return parseText(raw), nil
}

func parseJSON(raw string) (Plan, error) {
	var doc []map[string]json.RawMessage
	if strings.HasPrefix(raw, "{") {
		raw = "[" + raw + "]"
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil || len(doc) == 0 {
		return Plan{}, errors.New("not EXPLAIN (FORMAT JSON) output")
	}
	var root map[string]any
	if err := json.Unmarshal(doc[0]["Plan"], &root); err != nil || root == nil {
		return Plan{}, errors.New(`EXPLAIN JSON output must contain a "Plan" object`)
	}
	p := Plan{Format: "json"}
	var walk func(n map[string]any, depth int)
	walk = func(n map[string]any, depth int) {
		node := Node{Depth: depth, Type: str(n["Node Type"]), Relation: qualified(n), Index: str(n["Index Name"]),
			JoinType: str(n["Join Type"]), Strategy: str(n["Strategy"]), Cost: num(n["Total Cost"]), Rows: num(n["Plan Rows"]),
			Filter: str(n["Filter"])}
		for _, k := range []string{"Index Cond", "Hash Cond", "Merge Cond", "Join Filter", "Recheck Cond"} {
			if v := str(n[k]); v != "" {
				node.Cond = v
				break
			}
		}
		if v, ok := n["Actual Total Time"].(float64); ok {
			node.Actual = &v
			p.Analyzed = true
		}
		p.Nodes = append(p.Nodes, node)
		if kids, ok := n["Plans"].([]any); ok {
			for _, k := range kids {
				if m, ok := k.(map[string]any); ok {
					walk(m, depth+1)
				}
			}
		}
	}
	walk(root, 0)
	if len(p.Nodes) > 0 {
		c := p.Nodes[0].Cost
		p.TotalCost = &c
	}
	p.Fingerprint = fingerprint(p.Nodes)
	return p, nil
}

var (
	textNode = regexp.MustCompile(`^(\s*)(?:->\s+)?([A-Z][A-Za-z ]+?)(?: using (\S+))?(?: on (\S+))?(?: \S+)?\s+\(cost=[\d.]+\.\.([\d.]+) rows=(\d+)`)
	textCond = regexp.MustCompile(`^\s*(Filter|Index Cond|Hash Cond|Merge Cond|Join Filter|Recheck Cond):\s*(.*)$`)
)

// parseText reads the text format well enough to fingerprint and compare.
func parseText(raw string) Plan {
	p := Plan{Format: "text"}
	for _, line := range strings.Split(raw, "\n") {
		if m := textNode.FindStringSubmatch(line); m != nil {
			n := Node{Depth: len(m[1]) / 2, Type: strings.TrimSpace(m[2]), Index: m[3], Relation: m[4]}
			fmt.Sscan(m[5], &n.Cost)
			fmt.Sscan(m[6], &n.Rows)
			if strings.Contains(line, "actual time=") {
				p.Analyzed = true
			}
			p.Nodes = append(p.Nodes, n)
			continue
		}
		if m := textCond.FindStringSubmatch(line); m != nil && len(p.Nodes) > 0 {
			last := &p.Nodes[len(p.Nodes)-1]
			if m[1] == "Filter" {
				last.Filter = m[2]
			} else if last.Cond == "" {
				last.Cond = m[2]
			}
		}
	}
	if len(p.Nodes) > 0 {
		c := p.Nodes[0].Cost
		p.TotalCost = &c
	}
	p.Fingerprint = fingerprint(p.Nodes)
	return p
}

// fingerprint hashes the plan shape: node types, relations, indexes, and
// join types by depth. Costs and estimates do not change it.
func fingerprint(nodes []Node) string {
	h := sha256.New()
	for _, n := range nodes {
		fmt.Fprintf(h, "%d|%s|%s|%s|%s|%s\n", n.Depth, n.Type, n.Relation, n.Index, n.JoinType, n.Strategy)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func num(v any) float64 {
	f, _ := v.(float64)
	return f
}

func qualified(n map[string]any) string {
	rel := str(n["Relation Name"])
	if rel == "" {
		return ""
	}
	if s := str(n["Schema"]); s != "" {
		return s + "." + rel
	}
	return rel
}

// Change is one difference between two plans.
type Change struct {
	Kind   string `json:"kind"` // scan_changed, node_added, node_removed, join_changed, cost
	Detail string `json:"detail"`
}

// Comparison is the difference between an older and a newer plan.
type Comparison struct {
	SameShape bool     `json:"same_shape"`
	CostRatio *float64 `json:"cost_ratio"`
	Changes   []Change `json:"changes"`
}

// Compare lists how plan b differs from plan a.
func Compare(a, b Plan) Comparison {
	c := Comparison{SameShape: a.Fingerprint == b.Fingerprint, Changes: []Change{}}
	if a.TotalCost != nil && b.TotalCost != nil && *a.TotalCost > 0 {
		r := *b.TotalCost / *a.TotalCost
		c.CostRatio = &r
		if r >= 2 || r <= 0.5 {
			c.Changes = append(c.Changes, Change{Kind: "cost", Detail: fmt.Sprintf("Estimated total cost changed from %.0f to %.0f (%.1fx).", *a.TotalCost, *b.TotalCost, r)})
		}
	}
	// Access to each relation: how it is scanned in each plan.
	access := func(p Plan) map[string][]string {
		out := map[string][]string{}
		for _, n := range p.Nodes {
			if n.Relation != "" {
				d := n.Type
				if n.Index != "" {
					d += " using " + n.Index
				}
				out[n.Relation] = append(out[n.Relation], d)
			}
		}
		return out
	}
	aa, ba := access(a), access(b)
	rels := make([]string, 0, len(aa)+len(ba))
	for r := range aa {
		rels = append(rels, r)
	}
	for r := range ba {
		if _, ok := aa[r]; !ok {
			rels = append(rels, r)
		}
	}
	slices.Sort(rels)
	for _, r := range rels {
		x, y := strings.Join(aa[r], ", "), strings.Join(ba[r], ", ")
		switch {
		case x == "":
			c.Changes = append(c.Changes, Change{Kind: "node_added", Detail: fmt.Sprintf("%s is now read (%s).", r, y)})
		case y == "":
			c.Changes = append(c.Changes, Change{Kind: "node_removed", Detail: fmt.Sprintf("%s is no longer read (was %s).", r, x)})
		case x != y:
			c.Changes = append(c.Changes, Change{Kind: "scan_changed", Detail: fmt.Sprintf("%s: %s → %s.", r, x, y)})
		}
	}
	joins := func(p Plan) []string {
		var out []string
		for _, n := range p.Nodes {
			if strings.Contains(n.Type, "Join") || n.Type == "Nested Loop" {
				out = append(out, n.Type)
			}
		}
		return out
	}
	if ja, jb := joins(a), joins(b); !slices.Equal(ja, jb) {
		c.Changes = append(c.Changes, Change{Kind: "join_changed", Detail: fmt.Sprintf("Join methods changed: %s → %s.", orNone(ja), orNone(jb))})
	}
	return c
}

func orNone(v []string) string {
	if len(v) == 0 {
		return "none"
	}
	return strings.Join(v, ", ")
}

// IndexHint is a possible index for a sequential scan with a filter.
type IndexHint struct {
	Relation string   `json:"relation"`
	Columns  []string `json:"columns"`
	Reason   string   `json:"reason"`
}

var filterColumn = regexp.MustCompile(`\(?\(?([a-z_][a-z0-9_]*)\)?(?:::[a-z ]+)?\s*(=|<|>|<=|>=|~~|IN|ANY)\s`)

// IndexHints lists sequential scans that filter rows, with the columns
// compared in the filter. They are hypotheses for a person to evaluate: an
// index costs writes and space, and a sequential scan is often the right
// plan for small tables or unselective filters. LockLens never creates
// indexes.
func IndexHints(p Plan) []IndexHint {
	out := []IndexHint{}
	for _, n := range p.Nodes {
		if n.Type != "Seq Scan" || n.Filter == "" || n.Relation == "" {
			continue
		}
		var cols []string
		for _, m := range filterColumn.FindAllStringSubmatch(n.Filter, -1) {
			if !slices.Contains(cols, m[1]) && m[1] != "and" && m[1] != "or" {
				cols = append(cols, m[1])
			}
		}
		if len(cols) == 0 {
			continue
		}
		out = append(out, IndexHint{Relation: n.Relation, Columns: cols,
			Reason: fmt.Sprintf("Sequential scan of %s filtering on %s (estimated %0.f rows kept). If the filter is selective and the table is large, an index on these columns may help.",
				n.Relation, strings.Join(cols, ", "), n.Rows)})
	}
	return out
}
