# Blocking graph

The graph logic lives in [`backend/internal/domain/graph`](../backend/internal/domain/graph/graph.go). It is
pure Go with no database dependency, so every case below is covered by unit
tests in [`graph_test.go`](../backend/internal/domain/graph/graph_test.go).

## Model

- **Node**: a PID. Identity is the integer PID within one snapshot.
- **Edge**: `blocker → waiter`. This direction is used everywhere: in code, in
  JSON (`{"blocker": 1, "waiter": 2}`), and in the terminal (`Blocking chains
  (blocker -> waiter)`, children are drawn below the session that blocks them).
- Edges come only from `pg_blocking_pids()`; see
  [postgresql-observation.md](postgresql-observation.md).

Node kinds are assigned by [`backend/internal/domain/report`](../backend/internal/domain/report/report.go),
not by the graph:

| Kind | Meaning |
| --- | --- |
| `session` | The PID has a `pg_stat_activity` row in the snapshot |
| `prepared_transaction` | PID 0: a prepared transaction holds the conflicting lock |
| `missing` | The PID is in an edge but has no row in the `pg_stat_activity` view LockLens read (for example a backend that started while the collection ran). It stays in the graph as a placeholder so the observed relation is not lost; see [postgresql-observation.md](postgresql-observation.md#consistency-of-a-collection) |

## Computed values

For each node:

- `blocked_by`, `blocks`: sorted direct neighbours, excluding the node itself.
- `direct_blocked_count` = `len(blocks)`.
- `transitive_blocked_count`: the number of **unique** nodes reachable by
  following edges, not counting the node itself (even when the node lies on a
  cycle). Shared descendants are counted once.
- `in_cycle`: the node lies on a directed cycle (a strongly connected
  component with more than one node, or a self loop).
- `root`: the node blocks someone, nobody blocks it, and its component is
  acyclic.
- `component`: the weakly connected component, i.e. one independent incident.

For each component:

- `members`, `cyclic`, `cycle_members`.
- `entries`: members that block someone and are blocked by nobody.
- `roots`: equal to `entries` for an acyclic component, and **empty** for a
  cyclic one. A cyclic incident has no trustworthy starting point, so LockLens
  does not invent a root blocker; it labels the incident as a cycle instead.

Components are ordered by impact (largest transitive count of any member
first, then smallest PID) and numbered from 1 in that order. All lists are
sorted, so output is deterministic for the same input.

### Duplicates

`AddEdge` ignores an edge that already exists. This absorbs the duplicate PIDs
that `pg_blocking_pids()` can return for parallel queries.

## Worked examples

### Fan-out

```text
1 → 2, 1 → 3, 1 → 4
```

Node 1: direct 3, transitive 3, root. Nodes 2–4: direct 0.

### Chain (the demo's A → B → C)

```text
A → B → C
```

| Node | direct | transitive | root |
| --- | --- | --- | --- |
| A | 1 | 2 | yes |
| B | 1 | 1 | no |
| C | 0 | 0 | no |

### Multiple blockers

```text
1 → 3, 2 → 3
```

Session 3 waits for both (for example two `SHARE` holders ahead of an
`EXCLUSIVE` request). One component with roots `[1, 2]`; node 3 has
`blocked_by = [1, 2]`.

### Shared descendants

```text
1 → 2 → 4 → 5
1 → 3 → 4
```

Node 1 reaches {2, 3, 4, 5}: transitive count 4, not 5, even though 4 and 5
are reachable along two paths. In the terminal tree, node 4 is printed in full
once and then as `pid 4 (shown above)`.

### Prepared transaction

```text
0 → 7 → 8
```

PID 0 is an ordinary root node for the graph (direct 1, transitive 2); the
report labels it `prepared_transaction`.

### Cycle

```text
9 → 1 → 2 → 3 → 1
```

`{1, 2, 3}` is a strongly connected component, so the component is cyclic:
`roots = []`, `entries = [9]`, `cycle_members = [1, 2, 3]`. Transitive counts:
9 → 3, and 1, 2, 3 → 2 each (each reaches the other two, never itself). The
terminal shows `CYCLE among PIDs 1,2,3`, draws the tree from the entry 9 (or
from the smallest cycle member if there is no entry), and prints
`(cycle back to an ancestor)` where the path loops. See
[postgresql-observation.md](postgresql-observation.md#blocking-versus-deadlocks)
for why this is not reported as a deadlock.

## Rendering without infinite recursion

The terminal tree uses two sets: `shown` (nodes already printed in this
incident, so shared descendants appear once) and `path` (the current ancestry,
so a back edge is printed as a reference instead of recursing). Either set
alone guarantees termination; together they also make the output readable.

JSON never depends on this layout: it carries the normalised `nodes`, `edges`,
and `components` lists.

## Complexity

For V nodes and E edges:

| Step | Time | Extra space |
| --- | --- | --- |
| Insert edges with deduplication | O(E) expected | O(V + E) |
| Sort adjacency for determinism | O(E log E) | O(E) |
| Cycle detection (iterative Tarjan SCC) | O(V + E) | O(V) |
| Weakly connected components (BFS) | O(V + E) | O(V) |
| Transitive counts (one BFS per node) | O(V · (V + E)) | O(V) per BFS |

The quadratic term is acceptable because a blocking graph is bounded by
`max_connections` and is usually a handful of nodes. The cycle detection is
iterative so a long chain cannot overflow the goroutine stack; a unit test
covers a 2,000-node cycle.
