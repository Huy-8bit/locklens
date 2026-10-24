package graph

import (
	"slices"
	"testing"
)

func build(edges ...[2]int) Result {
	g := New()
	for _, e := range edges {
		g.AddEdge(e[0], e[1])
	}
	return g.Analyze()
}

func node(t *testing.T, r Result, id int) Node {
	t.Helper()
	n, ok := r.NodeByID(id)
	if !ok {
		t.Fatalf("node %d not found", id)
	}
	return n
}

func assertCounts(t *testing.T, r Result, id, direct, transitive int) {
	t.Helper()
	n := node(t, r, id)
	if n.DirectBlocked != direct || n.TransitiveBlocked != transitive {
		t.Errorf("node %d: direct=%d transitive=%d, want direct=%d transitive=%d",
			id, n.DirectBlocked, n.TransitiveBlocked, direct, transitive)
	}
}

func TestEmptyGraph(t *testing.T) {
	r := New().Analyze()
	if len(r.Nodes) != 0 || len(r.Edges) != 0 || len(r.Components) != 0 {
		t.Fatalf("expected empty result, got %+v", r)
	}
	if r.Nodes == nil || r.Edges == nil || r.Components == nil {
		t.Fatal("empty result must use non-nil slices so JSON renders []")
	}
}

func TestChain(t *testing.T) {
	r := build([2]int{1, 2}, [2]int{2, 3})
	assertCounts(t, r, 1, 1, 2)
	assertCounts(t, r, 2, 1, 1)
	assertCounts(t, r, 3, 0, 0)
	if len(r.Components) != 1 || !slices.Equal(r.Components[0].Roots, []int{1}) {
		t.Fatalf("expected single component rooted at 1, got %+v", r.Components)
	}
	if !node(t, r, 1).Root || node(t, r, 2).Root {
		t.Error("only node 1 should be a root")
	}
}

func TestFanOut(t *testing.T) {
	r := build([2]int{10, 11}, [2]int{10, 12}, [2]int{10, 13})
	assertCounts(t, r, 10, 3, 3)
	if got := node(t, r, 10).Blocks; !slices.Equal(got, []int{11, 12, 13}) {
		t.Errorf("blocks = %v", got)
	}
}

func TestMultipleBlockers(t *testing.T) {
	// 3 waits for both 1 and 2 (e.g. shared lock holders ahead of an
	// exclusive request).
	r := build([2]int{1, 3}, [2]int{2, 3})
	if got := node(t, r, 3).BlockedBy; !slices.Equal(got, []int{1, 2}) {
		t.Errorf("blocked_by = %v", got)
	}
	if len(r.Components) != 1 || !slices.Equal(r.Components[0].Roots, []int{1, 2}) {
		t.Fatalf("expected roots [1 2], got %+v", r.Components)
	}
}

func TestSharedDescendantsCountedOnce(t *testing.T) {
	// 1 → 2 → 4, 1 → 3 → 4, 4 → 5. Node 4 and 5 are reachable twice.
	r := build([2]int{1, 2}, [2]int{1, 3}, [2]int{2, 4}, [2]int{3, 4}, [2]int{4, 5})
	assertCounts(t, r, 1, 2, 4)
	assertCounts(t, r, 2, 1, 2)
	assertCounts(t, r, 4, 1, 1)
}

func TestDuplicateEdgesIgnored(t *testing.T) {
	r := build([2]int{1, 2}, [2]int{1, 2}, [2]int{1, 2})
	if len(r.Edges) != 1 {
		t.Fatalf("expected 1 edge, got %v", r.Edges)
	}
	assertCounts(t, r, 1, 1, 1)
}

func TestPreparedTransactionNodeZero(t *testing.T) {
	r := build([2]int{0, 7}, [2]int{7, 8})
	assertCounts(t, r, 0, 1, 2)
	if !node(t, r, 0).Root {
		t.Error("PID 0 must be treated as a normal root node by the graph")
	}
}

func TestTwoNodeCycleHasNoRoot(t *testing.T) {
	r := build([2]int{1, 2}, [2]int{2, 1})
	if len(r.Components) != 1 {
		t.Fatalf("components = %+v", r.Components)
	}
	c := r.Components[0]
	if !c.Cyclic || len(c.Roots) != 0 || !slices.Equal(c.CycleMembers, []int{1, 2}) {
		t.Fatalf("unexpected cyclic component %+v", c)
	}
	// Each node reaches the other but never counts itself.
	assertCounts(t, r, 1, 1, 1)
	assertCounts(t, r, 2, 1, 1)
}

func TestCycleWithUnblockedEntryReportsNoRoot(t *testing.T) {
	// 9 is unblocked and feeds a cycle 1 → 2 → 3 → 1.
	r := build([2]int{9, 1}, [2]int{1, 2}, [2]int{2, 3}, [2]int{3, 1})
	c := r.Components[0]
	if !c.Cyclic || len(c.Roots) != 0 {
		t.Fatalf("cyclic component must not report roots: %+v", c)
	}
	if !slices.Equal(c.Entries, []int{9}) {
		t.Errorf("entries = %v, want [9]", c.Entries)
	}
	if node(t, r, 9).InCycle || !node(t, r, 1).InCycle {
		t.Error("cycle membership wrong")
	}
	assertCounts(t, r, 9, 1, 3)
	assertCounts(t, r, 1, 1, 2)
}

func TestSelfLoopIsCycle(t *testing.T) {
	r := build([2]int{5, 5}, [2]int{5, 6})
	n := node(t, r, 5)
	if !n.InCycle {
		t.Error("self loop must be flagged as cycle")
	}
	if n.DirectBlocked != 1 || n.TransitiveBlocked != 1 {
		t.Errorf("self loop must not count itself: %+v", n)
	}
	if slices.Contains(n.BlockedBy, 5) {
		t.Error("blocked_by must not contain the node itself")
	}
}

func TestIndependentComponentsOrderedByImpact(t *testing.T) {
	r := build(
		[2]int{100, 101},                         // impact 1
		[2]int{1, 2}, [2]int{2, 3}, [2]int{2, 4}, // impact 3
	)
	if len(r.Components) != 2 {
		t.Fatalf("components = %+v", r.Components)
	}
	if r.Components[0].ID != 1 || !slices.Equal(r.Components[0].Roots, []int{1}) {
		t.Errorf("highest-impact component should be first: %+v", r.Components)
	}
	if node(t, r, 100).Component != 2 {
		t.Errorf("node 100 should be in component 2")
	}
}

func TestLongChainDoesNotOverflow(t *testing.T) {
	g := New()
	const n = 2000
	for i := 1; i < n; i++ {
		g.AddEdge(i, i+1)
	}
	g.AddEdge(n, 1) // close the cycle
	r := g.Analyze()
	if !r.Components[0].Cyclic {
		t.Fatal("expected cycle")
	}
}

func TestDeterministicOutput(t *testing.T) {
	edges := [][2]int{{3, 4}, {1, 2}, {2, 3}, {1, 5}}
	first := build(edges...)
	for range 20 {
		again := build(edges...)
		if !slices.Equal(first.Edges, again.Edges) {
			t.Fatal("edge order is not deterministic")
		}
	}
}
