// Package graph models lock blocking relations as a directed graph and
// computes the figures LockLens reports.
//
// Edge direction is always blocker → waiter. Nodes are identified by PID;
// PID 0 is an ordinary node here (PostgreSQL uses it for prepared
// transactions) and its meaning is assigned by the caller.
//
// The package is independent of PostgreSQL so every edge case (duplicates,
// shared descendants, cycles, self loops) can be unit tested with plain data.
// See docs/blocking-graph.md for the model and worked examples.
package graph

import (
	"cmp"
	"slices"
)

// Graph is a directed blocker → waiter graph. The zero value is not usable;
// call New.
type Graph struct {
	out map[int]map[int]struct{}
	in  map[int]map[int]struct{}
}

// New returns an empty graph.
func New() *Graph {
	return &Graph{
		out: make(map[int]map[int]struct{}),
		in:  make(map[int]map[int]struct{}),
	}
}

// AddEdge records that blocker blocks waiter. Duplicate edges are ignored,
// which absorbs the duplicate PIDs pg_blocking_pids returns for parallel
// queries.
func (g *Graph) AddEdge(blocker, waiter int) {
	g.ensure(blocker)
	g.ensure(waiter)
	g.out[blocker][waiter] = struct{}{}
	g.in[waiter][blocker] = struct{}{}
}

func (g *Graph) ensure(id int) {
	if _, ok := g.out[id]; !ok {
		g.out[id] = make(map[int]struct{})
		g.in[id] = make(map[int]struct{})
	}
}

// Edge is a deduplicated blocker → waiter edge.
type Edge struct {
	Blocker int `json:"blocker"`
	Waiter  int `json:"waiter"`
}

// Node holds the computed figures for one node.
type Node struct {
	ID int `json:"id"`
	// BlockedBy and Blocks are the sorted direct neighbours, excluding the
	// node itself.
	BlockedBy []int `json:"blocked_by"`
	Blocks    []int `json:"blocks"`
	// DirectBlocked is len(Blocks).
	DirectBlocked int `json:"direct_blocked_count"`
	// TransitiveBlocked counts the unique nodes reachable from this node,
	// excluding the node itself even when it lies on a cycle.
	TransitiveBlocked int `json:"transitive_blocked_count"`
	// Root is true for a node that blocks others, is blocked by nobody, and
	// belongs to an acyclic component.
	Root bool `json:"root"`
	// InCycle is true when the node lies on a directed cycle (including a
	// self loop).
	InCycle   bool `json:"in_cycle"`
	Component int  `json:"component"`
}

// Component is a weakly connected component of the graph, i.e. one
// independent blocking incident.
type Component struct {
	ID      int   `json:"id"`
	Members []int `json:"members"`
	// Roots lists the root blockers. It is empty when the component contains a
	// cycle: a cyclic component has no trustworthy starting point.
	Roots []int `json:"roots"`
	// Cyclic is true when the component contains at least one directed cycle.
	Cyclic bool `json:"cyclic"`
	// CycleMembers lists nodes that lie on a cycle, sorted.
	CycleMembers []int `json:"cycle_members"`
	// Entries lists nodes in the component that nobody blocks. For an acyclic
	// component it equals Roots; in a cyclic component it may be empty.
	Entries []int `json:"entries"`
}

// Result is the full analysis. Slices are sorted deterministically so output
// is stable across runs.
type Result struct {
	Nodes      []Node      `json:"nodes"`
	Edges      []Edge      `json:"edges"`
	Components []Component `json:"components"`
}

// NodeByID returns the node with the given ID.
func (r *Result) NodeByID(id int) (Node, bool) {
	i, ok := slices.BinarySearchFunc(r.Nodes, id, func(n Node, id int) int { return cmp.Compare(n.ID, id) })
	if !ok {
		return Node{}, false
	}
	return r.Nodes[i], true
}

// Analyze computes the result.
//
// Complexity for V nodes and E edges: cycle detection (Tarjan) and component
// labelling are O(V+E); transitive counts run one BFS per node and are
// O(V·(V+E)) time and O(V) extra space per BFS. Blocking graphs are bounded by
// max_connections and are usually tiny, so the quadratic term is acceptable.
func (g *Graph) Analyze() Result {
	ids := make([]int, 0, len(g.out))
	for id := range g.out {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	out := sortedAdjacency(g.out)
	in := sortedAdjacency(g.in)

	var res Result
	for _, b := range ids {
		for _, w := range out[b] {
			res.Edges = append(res.Edges, Edge{Blocker: b, Waiter: w})
		}
	}

	inCycle := cycleNodes(ids, out)
	comp := components(ids, out, in)

	res.Nodes = make([]Node, len(ids))
	for i, id := range ids {
		res.Nodes[i] = Node{
			ID:                id,
			BlockedBy:         without(in[id], id),
			Blocks:            without(out[id], id),
			TransitiveBlocked: reachableCount(id, out),
			InCycle:           inCycle[id],
			Component:         comp[id],
		}
		res.Nodes[i].DirectBlocked = len(res.Nodes[i].Blocks)
	}

	byComp := map[int]*Component{}
	var order []int
	for _, n := range res.Nodes {
		c, ok := byComp[n.Component]
		if !ok {
			c = &Component{ID: n.Component}
			byComp[n.Component] = c
			order = append(order, n.Component)
		}
		c.Members = append(c.Members, n.ID)
		if n.InCycle {
			c.Cyclic = true
			c.CycleMembers = append(c.CycleMembers, n.ID)
		}
		if len(n.BlockedBy) == 0 && !n.InCycle && n.DirectBlocked > 0 {
			c.Entries = append(c.Entries, n.ID)
		}
	}
	for _, id := range order {
		c := byComp[id]
		if !c.Cyclic {
			c.Roots = slices.Clone(c.Entries)
		}
	}
	for i := range res.Nodes {
		c := byComp[res.Nodes[i].Component]
		res.Nodes[i].Root = !c.Cyclic && slices.Contains(c.Roots, res.Nodes[i].ID)
	}

	// Order components by impact: the largest transitive count of any member
	// first, then by smallest member ID for stability.
	impact := func(c *Component) int {
		best := 0
		for _, m := range c.Members {
			n, _ := res.NodeByID(m)
			best = max(best, n.TransitiveBlocked)
		}
		return best
	}
	comps := make([]*Component, 0, len(order))
	for _, id := range order {
		comps = append(comps, byComp[id])
	}
	slices.SortStableFunc(comps, func(a, b *Component) int {
		if c := cmp.Compare(impact(b), impact(a)); c != 0 {
			return c
		}
		return cmp.Compare(a.Members[0], b.Members[0])
	})
	// Renumber components in display order so IDs are stable and meaningful.
	renumber := map[int]int{}
	for i, c := range comps {
		renumber[c.ID] = i + 1
		c.ID = i + 1
		c.Roots = nonNil(c.Roots)
		c.CycleMembers = nonNil(c.CycleMembers)
		c.Entries = nonNil(c.Entries)
		res.Components = append(res.Components, *c)
	}
	for i := range res.Nodes {
		res.Nodes[i].Component = renumber[res.Nodes[i].Component]
	}
	res.Nodes = nonNil(res.Nodes)
	res.Edges = nonNil(res.Edges)
	res.Components = nonNil(res.Components)
	return res
}

func sortedAdjacency(m map[int]map[int]struct{}) map[int][]int {
	adj := make(map[int][]int, len(m))
	for id, set := range m {
		list := make([]int, 0, len(set))
		for n := range set {
			list = append(list, n)
		}
		slices.Sort(list)
		adj[id] = list
	}
	return adj
}

func without(list []int, id int) []int {
	res := make([]int, 0, len(list))
	for _, n := range list {
		if n != id {
			res = append(res, n)
		}
	}
	return res
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// reachableCount returns how many distinct nodes are reachable from start,
// not counting start itself. The visited set makes it terminate on cycles and
// count shared descendants once.
func reachableCount(start int, out map[int][]int) int {
	visited := map[int]bool{start: true}
	queue := []int{start}
	count := 0
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, next := range out[cur] {
			if !visited[next] {
				visited[next] = true
				count++
				queue = append(queue, next)
			}
		}
	}
	return count
}

// cycleNodes marks nodes that lie on a directed cycle using Tarjan's strongly
// connected components algorithm: a node is on a cycle if its SCC has more
// than one member or it has a self loop. The traversal is iterative so very
// deep chains cannot overflow the stack.
func cycleNodes(ids []int, out map[int][]int) map[int]bool {
	index := map[int]int{}
	low := map[int]int{}
	onStack := map[int]bool{}
	var stack []int
	next := 0
	result := map[int]bool{}

	type frame struct {
		id   int
		edge int
	}
	for _, root := range ids {
		if _, seen := index[root]; seen {
			continue
		}
		call := []frame{{id: root}}
		index[root], low[root] = next, next
		next++
		stack = append(stack, root)
		onStack[root] = true

		for len(call) > 0 {
			top := &call[len(call)-1]
			v := top.id
			if top.edge < len(out[v]) {
				w := out[v][top.edge]
				top.edge++
				if _, seen := index[w]; !seen {
					index[w], low[w] = next, next
					next++
					stack = append(stack, w)
					onStack[w] = true
					call = append(call, frame{id: w})
				} else if onStack[w] {
					low[v] = min(low[v], index[w])
				}
				continue
			}
			// All edges of v explored.
			if low[v] == index[v] {
				var scc []int
				for {
					w := stack[len(stack)-1]
					stack = stack[:len(stack)-1]
					onStack[w] = false
					scc = append(scc, w)
					if w == v {
						break
					}
				}
				if len(scc) > 1 {
					for _, w := range scc {
						result[w] = true
					}
				} else if slices.Contains(out[v], v) {
					result[v] = true
				}
			}
			call = call[:len(call)-1]
			if len(call) > 0 {
				parent := call[len(call)-1].id
				low[parent] = min(low[parent], low[v])
			}
		}
	}
	return result
}

// components labels weakly connected components. Labels are provisional and
// renumbered by Analyze.
func components(ids []int, out, in map[int][]int) map[int]int {
	comp := map[int]int{}
	label := 0
	for _, start := range ids {
		if _, ok := comp[start]; ok {
			continue
		}
		label++
		comp[start] = label
		queue := []int{start}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			for _, list := range [][]int{out[cur], in[cur]} {
				for _, n := range list {
					if _, ok := comp[n]; !ok {
						comp[n] = label
						queue = append(queue, n)
					}
				}
			}
		}
	}
	return comp
}
