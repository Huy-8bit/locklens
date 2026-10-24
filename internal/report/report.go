// Package report turns a raw snapshot into the analysis LockLens displays:
// the blocking graph with counts, node kinds, session ages, long-running
// transactions, and interpretation warnings.
//
// Analysis is a pure function of the snapshot (plus the long-transaction
// threshold), so live inspection and offline `snapshot show` produce the same
// report from the same data.
package report

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/locklens/locklens/internal/graph"
	"github.com/locklens/locklens/internal/model"
)

// Node kinds.
const (
	// KindSession is a backend found in pg_stat_activity.
	KindSession = "session"
	// KindPreparedTransaction is PID 0: a prepared transaction holds the
	// conflicting lock. PostgreSQL does not say which one.
	KindPreparedTransaction = "prepared_transaction"
	// KindMissing is a PID that appears in a blocking relation but has no
	// pg_stat_activity row, typically because it ended between reads.
	KindMissing = "missing"
)

// Report is what inspect, watch, and snapshot show emit.
type Report struct {
	Snapshot *model.Snapshot `json:"snapshot"`
	Analysis Analysis        `json:"analysis"`
}

// Analysis holds derived data. Ages are seconds measured against the
// snapshot's server_time; nil means unknown.
type Analysis struct {
	MinTxAgeSeconds  float64           `json:"min_tx_age_seconds"`
	Summary          Summary           `json:"summary"`
	Nodes            []Node            `json:"nodes"`
	Edges            []graph.Edge      `json:"edges"`
	Components       []graph.Component `json:"components"`
	Sessions         []SessionAges     `json:"sessions"`
	LongTransactions []LongTransaction `json:"long_transactions"`
	Warnings         []model.Warning   `json:"warnings"`
}

// Summary holds headline counts.
type Summary struct {
	BlockedSessions   int `json:"blocked_sessions"`
	BlockingSessions  int `json:"blocking_sessions"`
	RootBlockers      int `json:"root_blockers"`
	CyclicComponents  int `json:"cyclic_components"`
	LongTransactions  int `json:"long_transactions"`
	IdleInTransaction int `json:"idle_in_transaction"`
}

// Node is a graph node with its kind.
type Node struct {
	graph.Node
	Kind string `json:"kind"`
}

// SessionAges separates the three different durations PostgreSQL exposes.
// They must not be confused: query_start is not when a lock wait began, and
// neither is state_change.
type SessionAges struct {
	PID int `json:"pid"`
	// TransactionAgeSeconds is server_time - xact_start.
	TransactionAgeSeconds *float64 `json:"transaction_age_seconds"`
	// QueryAgeSeconds is server_time - query_start: the running query's age
	// if active, otherwise time since the last query started.
	QueryAgeSeconds *float64 `json:"query_age_seconds"`
	// StateAgeSeconds is server_time - state_change.
	StateAgeSeconds *float64 `json:"state_age_seconds"`
	// LockWaitSeconds is server_time - the earliest pg_locks.waitstart of the
	// session's ungranted locks. Unknown if no waitstart was recorded.
	LockWaitSeconds *float64 `json:"lock_wait_seconds"`
}

// LongTransaction is a client backend whose transaction is older than the
// threshold.
type LongTransaction struct {
	PID                   int     `json:"pid"`
	State                 *string `json:"state"`
	TransactionAgeSeconds float64 `json:"transaction_age_seconds"`
	IdleInTransaction     bool    `json:"idle_in_transaction"`
	// DirectBlocked is how many sessions this transaction blocks directly.
	DirectBlocked int `json:"direct_blocked_count"`
}

// Build analyses a snapshot. minTxAge is the long-transaction threshold.
func Build(snap *model.Snapshot, minTxAge time.Duration) *Report {
	byPID := make(map[int]*model.Session, len(snap.Sessions))
	for i := range snap.Sessions {
		byPID[snap.Sessions[i].PID] = &snap.Sessions[i]
	}

	g := graph.New()
	for _, e := range snap.Edges {
		g.AddEdge(e.Blocker, e.Waiter)
	}
	res := g.Analyze()

	a := Analysis{
		MinTxAgeSeconds:  minTxAge.Seconds(),
		Edges:            res.Edges,
		Components:       res.Components,
		Nodes:            make([]Node, 0, len(res.Nodes)),
		Sessions:         make([]SessionAges, 0, len(snap.Sessions)),
		LongTransactions: []LongTransaction{},
		Warnings:         []model.Warning{},
	}

	var missing []int
	hasPrepared := false
	for _, n := range res.Nodes {
		kind := KindSession
		switch _, ok := byPID[n.ID]; {
		case n.ID == 0:
			kind = KindPreparedTransaction
			hasPrepared = true
		case !ok:
			kind = KindMissing
			missing = append(missing, n.ID)
		}
		a.Nodes = append(a.Nodes, Node{Node: n, Kind: kind})
		if len(n.BlockedBy) > 0 {
			a.Summary.BlockedSessions++
		}
		if n.DirectBlocked > 0 {
			a.Summary.BlockingSessions++
		}
		if n.Root {
			a.Summary.RootBlockers++
		}
	}

	var cyclic []string
	for _, c := range res.Components {
		if c.Cyclic {
			a.Summary.CyclicComponents++
			cyclic = append(cyclic, joinInts(c.CycleMembers))
		}
	}

	for i := range snap.Sessions {
		s := &snap.Sessions[i]
		ages := SessionAges{
			PID:                   s.PID,
			TransactionAgeSeconds: ageSince(snap.ServerTime, s.XactStart),
			QueryAgeSeconds:       ageSince(snap.ServerTime, s.QueryStart),
			StateAgeSeconds:       ageSince(snap.ServerTime, s.StateChange),
			LockWaitSeconds:       ageSince(snap.ServerTime, EarliestWaitStart(s.WaitingLocks)),
		}
		a.Sessions = append(a.Sessions, ages)

		if ages.TransactionAgeSeconds == nil || *ages.TransactionAgeSeconds < minTxAge.Seconds() {
			continue
		}
		if s.BackendType == nil || *s.BackendType != "client backend" {
			continue
		}
		lt := LongTransaction{
			PID:                   s.PID,
			State:                 s.State,
			TransactionAgeSeconds: *ages.TransactionAgeSeconds,
			IdleInTransaction:     IsIdleInTransaction(s.State),
		}
		if n, ok := res.NodeByID(s.PID); ok {
			lt.DirectBlocked = n.DirectBlocked
		}
		a.LongTransactions = append(a.LongTransactions, lt)
		a.Summary.LongTransactions++
		if lt.IdleInTransaction {
			a.Summary.IdleInTransaction++
		}
	}
	slices.SortStableFunc(a.LongTransactions, func(x, y LongTransaction) int {
		if c := cmp.Compare(y.TransactionAgeSeconds, x.TransactionAgeSeconds); c != 0 {
			return c
		}
		return cmp.Compare(x.PID, y.PID)
	})

	if len(missing) > 0 {
		a.Warnings = append(a.Warnings, model.Warning{
			Code: model.WarnMissingSession,
			Message: fmt.Sprintf("PID(s) %s appear in blocking relations but not in pg_stat_activity; they probably ended between catalog reads "+
				"and are shown as placeholders", joinInts(missing)),
		})
	}
	if hasPrepared {
		a.Warnings = append(a.Warnings, model.Warning{
			Code: model.WarnPreparedBlocker,
			Message: fmt.Sprintf("a prepared transaction (PID 0) holds a conflicting lock; PostgreSQL does not identify which one. "+
				"%d prepared transaction(s) exist: resolve with COMMIT PREPARED or ROLLBACK PREPARED after review", len(snap.PreparedTransactions)),
		})
	}
	if len(cyclic) > 0 {
		a.Warnings = append(a.Warnings, model.Warning{
			Code: model.WarnCycle,
			Message: fmt.Sprintf("cyclic blocking observed among PIDs [%s]; no root blocker is reported for it. "+
				"This is not a confirmed deadlock: the snapshot is not atomic, and PostgreSQL's deadlock detector resolves real deadlocks after deadlock_timeout",
				strings.Join(cyclic, "], [")),
		})
	}
	return &Report{Snapshot: snap, Analysis: a}
}

// EarliestWaitStart returns the earliest non-NULL waitstart, or nil.
func EarliestWaitStart(locks []model.WaitingLock) *time.Time {
	var earliest *time.Time
	for _, l := range locks {
		if l.WaitStart != nil && (earliest == nil || l.WaitStart.Before(*earliest)) {
			earliest = l.WaitStart
		}
	}
	return earliest
}

// IsIdleInTransaction reports the "idle in transaction" and
// "idle in transaction (aborted)" states.
func IsIdleInTransaction(state *string) bool {
	return state != nil && strings.HasPrefix(*state, "idle in transaction")
}

// ageSince returns now - t in seconds, clamped at zero, or nil if t is nil.
func ageSince(now time.Time, t *time.Time) *float64 {
	if t == nil {
		return nil
	}
	d := max(now.Sub(*t).Seconds(), 0)
	return &d
}

func joinInts(ids []int) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = fmt.Sprint(id)
	}
	return strings.Join(parts, ", ")
}
