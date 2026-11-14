package cluster

import (
	"cmp"
	"slices"
	"time"
)

// Planning. A collector more than 15% above its share of the cluster's
// collection cost gives targets to a collector below its share, and a
// collector more than 15% below its share takes from one above it; inside
// the band nothing moves. A target stays at least minHold with its
// collector, and a move never leaves the receiver relatively busier than the
// giver, so the assignment settles instead of moving targets back and forth.
const (
	bandHigh = 1.15
	bandLow  = 0.85
	minHold  = 10 * time.Minute
	// bandUrgent: a collector above this multiple of its share (a server that
	// collected alone before others joined) gives targets regardless of
	// minHold.
	bandUrgent = 1.5
	// costFloor is added to every target's weight, as a fraction of the
	// average, so that targets that cost (almost) nothing still spread.
	costFloor = 0.05
	// maxMovesShare bounds the targets moved per plan, as a share of all.
	maxMovesShare = 0.05
)

// Member is a live collector the plan gives targets to.
type Member struct {
	ID string
	// Capacity is how many collections it runs at once; its share of the
	// cost is proportional (at least a tiny positive value).
	Capacity float64
	// MaxTargets is its target limit (0: none).
	MaxTargets int
}

// Holding is the collector currently holding a target's lease.
type Holding struct {
	Holder string
	// Since is when the holder took the lease.
	Since time.Time
}

// planned is the outcome of a plan.
type planned struct {
	// owner is the collector each target should have; targets absent from it
	// have none (every collector is at its target limit).
	owner map[int64]string
	// share and load are the cost each member should carry and the cost it
	// carries with the plan.
	share, load map[string]float64
}

// plan assigns every target to a collector. It is a pure function of its
// inputs, so the servers, which compute it each from the same leases and
// weights, agree.
//
//   - current holds the targets with a live lease held by one of members
//     (other targets are free).
//   - weights is each target's collection cost; a target without one costs
//     the average.
//   - members are the live collectors.
//
// A target stays with its holder unless the holder is over its target limit,
// or it is over its share beyond the band (or another is under it beyond the
// band), the target was held for minHold (unless the holder carries more
// than bandUrgent times its share), and moving it to a collector under its
// share keeps that collector at most as busy, relative to its share, as the
// giver. At most maxMovesShare of the targets move per plan. Free targets go, heaviest first, to the collector
// relatively least busy.
func plan(targets []int64, current map[int64]Holding, weights map[int64]float64, members []Member, now time.Time) planned {
	out := planned{owner: map[int64]string{}, share: map[string]float64{}, load: map[string]float64{}}
	if len(members) == 0 || len(targets) == 0 {
		return out
	}
	members = slices.Clone(members)
	slices.SortFunc(members, func(a, b Member) int { return cmp.Compare(a.ID, b.ID) })
	byID := map[string]Member{}
	var capSum float64
	for i, m := range members {
		if m.Capacity <= 0 {
			members[i].Capacity = 1e-6
		}
		byID[m.ID] = members[i]
		capSum += members[i].Capacity
	}

	cost := costs(targets, weights)
	var total float64
	for _, id := range targets {
		total += cost[id]
	}
	for _, m := range members {
		out.share[m.ID] = total * m.Capacity / capSum
	}
	count := map[string]int{}
	held := map[string][]int64{}
	assign := func(id int64, m string) {
		out.owner[id] = m
		out.load[m] += cost[id]
		count[m]++
	}
	unassign := func(id int64) {
		m := out.owner[id]
		delete(out.owner, id)
		out.load[m] -= cost[id]
		count[m]--
	}
	room := func(m string) bool { mx := byID[m].MaxTargets; return mx <= 0 || count[m] < mx }
	ratio := func(m string, extra float64) float64 { return (out.load[m] + extra) / out.share[m] }

	sorted := slices.Clone(targets)
	slices.Sort(sorted)
	var free []int64
	for _, id := range sorted {
		h, ok := current[id]
		if _, member := byID[h.Holder]; ok && member {
			assign(id, h.Holder)
			held[h.Holder] = append(held[h.Holder], id)
		} else {
			free = append(free, id)
		}
	}
	// Over its target limit: the most recently taken targets go first.
	for _, m := range members {
		if m.MaxTargets <= 0 || count[m.ID] <= m.MaxTargets {
			continue
		}
		ids := held[m.ID]
		slices.SortStableFunc(ids, func(a, b int64) int { return current[b].Since.Compare(current[a].Since) })
		excess := count[m.ID] - m.MaxTargets
		for _, id := range ids[:excess] {
			unassign(id)
			free = append(free, id)
		}
		held[m.ID] = ids[excess:]
	}

	// Free targets, heaviest first, to the relatively least busy collector.
	slices.SortStableFunc(free, func(a, b int64) int {
		if c := cmp.Compare(cost[b], cost[a]); c != 0 {
			return c
		}
		return cmp.Compare(a, b)
	})
	for _, id := range free {
		best := ""
		for _, m := range members {
			if room(m.ID) && (best == "" || ratio(m.ID, cost[id]) < ratio(best, cost[id])) {
				best = m.ID
			}
		}
		if best != "" {
			assign(id, best)
		}
	}

	// Rebalance held targets.
	moves := max(1, int(float64(len(targets))*maxMovesShare))
	for moves > 0 {
		from, to, id := move(members, held, current, cost, out, room, ratio, now)
		if from == "" {
			break
		}
		unassign(id)
		assign(id, to)
		held[from] = slices.DeleteFunc(held[from], func(x int64) bool { return x == id })
		moves--
	}
	return out
}

// move finds one target worth moving: from a collector above its share to
// one below it, where either is outside the band, the target held for
// minHold, and the move does not make the receiver relatively busier than
// the giver. The heaviest such target of the most loaded giver and least
// loaded receiver is chosen.
func move(members []Member, held map[string][]int64, current map[int64]Holding, cost map[int64]float64, out planned,
	room func(string) bool, ratio func(string, float64) float64, now time.Time) (string, string, int64) {
	givers := slices.Clone(members)
	slices.SortStableFunc(givers, func(a, b Member) int { return cmp.Compare(ratio(b.ID, 0), ratio(a.ID, 0)) })
	takers := slices.Clone(members)
	slices.SortStableFunc(takers, func(a, b Member) int { return cmp.Compare(ratio(a.ID, 0), ratio(b.ID, 0)) })
	for _, g := range givers {
		rg := ratio(g.ID, 0)
		if rg <= 1 {
			break
		}
		for _, t := range takers {
			rt := ratio(t.ID, 0)
			if rt >= 1 {
				break
			}
			if (rg <= bandHigh && rt >= bandLow) || !room(t.ID) {
				continue
			}
			var best int64
			for _, id := range held[g.ID] {
				if now.Sub(current[id].Since) < minHold && rg <= bandUrgent {
					continue
				}
				w := cost[id]
				if (out.load[g.ID]-w)/out.share[g.ID] < ratio(t.ID, w) {
					continue // would overshoot: the receiver ends busier
				}
				if best == 0 || w > cost[best] || (w == cost[best] && id < best) {
					best = id
				}
			}
			if best != 0 {
				return g.ID, t.ID, best
			}
		}
	}
	return "", "", 0
}

// costs returns each target's planning cost: its weight (the average for a
// target without one) plus a small floor.
func costs(targets []int64, weights map[int64]float64) map[int64]float64 {
	var sum float64
	n := 0
	for _, id := range targets {
		if w, ok := weights[id]; ok && w >= 0 {
			sum += w
			n++
		}
	}
	avg := 1.0
	if n > 0 && sum > 0 {
		avg = sum / float64(n)
	}
	out := make(map[int64]float64, len(targets))
	for _, id := range targets {
		w, ok := weights[id]
		if !ok || w < 0 {
			w = avg
		}
		out[id] = w + costFloor*avg
	}
	return out
}
