package rules

import (
	"fmt"
	"slices"
	"time"

	"github.com/locklens/locklens/internal/domain/baseline"
	"github.com/locklens/locklens/internal/domain/live"
	"github.com/locklens/locklens/internal/domain/model"
	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/domain/report"
)

// RootSample describes one root blocker (or cycle) in one activity sample.
type RootSample struct {
	Key          string     `json:"key"`
	PID          int        `json:"pid"`
	Kind         string     `json:"kind"`
	BackendStart *time.Time `json:"backend_start,omitempty"`
	State        *string    `json:"state"`
	Direct       int        `json:"direct_blocked"`
	Transitive   int        `json:"transitive_blocked"`
	Members      []int      `json:"members"`
	Cyclic       bool       `json:"cyclic"`
	MaxLockWait  *float64   `json:"max_lock_wait_seconds"`
	TxAge        *float64   `json:"transaction_age_seconds"`
}

// ActivitySample is the compact per-collection summary kept in history.
type ActivitySample struct {
	At               time.Time    `json:"at"`
	Roots            []RootSample `json:"roots"`
	BlockedSessions  int          `json:"blocked_sessions"`
	UsedConnections  int          `json:"used_connections"`
	MaxConnections   int          `json:"max_connections"`
	Reserved         int          `json:"reserved_connections"`
	HiddenSessions   int          `json:"hidden_sessions"`
	LongTransactions int          `json:"long_transactions"`
}

// UsageRatio is used / (max_connections - reserved slots), or nil.
func (s ActivitySample) UsageRatio() *float64 {
	avail := s.MaxConnections - s.Reserved
	if avail <= 0 {
		return nil
	}
	r := float64(s.UsedConnections) / float64(avail)
	return &r
}

// Summarize builds an ActivitySample from an activity collection.
func Summarize(a *observe.Activity, rep *report.Report) ActivitySample {
	s := ActivitySample{At: a.At, UsedConnections: a.Counts.UsedConnections, MaxConnections: a.Counts.MaxConnections,
		Reserved: a.Counts.ReservedConnections, Roots: []RootSample{}}
	if rep == nil {
		return s
	}
	s.BlockedSessions = rep.Analysis.Summary.BlockedSessions
	s.LongTransactions = rep.Analysis.Summary.LongTransactions
	sessions := map[int]model.Session{}
	for _, x := range rep.Snapshot.Sessions {
		sessions[x.PID] = x
		if !x.DetailsVisible {
			s.HiddenSessions++
		}
	}
	ages := map[int]report.SessionAges{}
	for _, x := range rep.Analysis.Sessions {
		ages[x.PID] = x
	}
	nodes := map[int]report.Node{}
	for _, n := range rep.Analysis.Nodes {
		nodes[n.ID] = n
	}
	for _, c := range rep.Analysis.Components {
		var maxWait *float64
		for _, m := range c.Members {
			if w := ages[m].LockWaitSeconds; w != nil && (maxWait == nil || *w > *maxWait) {
				maxWait = w
			}
		}
		starts := c.Roots
		if c.Cyclic {
			starts = c.CycleMembers[:1]
		}
		for _, pid := range starts {
			n := nodes[pid]
			r := RootSample{PID: pid, Kind: n.Kind, Direct: n.DirectBlocked, Transitive: n.TransitiveBlocked,
				Members: slices.Clone(c.Members), Cyclic: c.Cyclic, MaxLockWait: maxWait}
			if sess, ok := sessions[pid]; ok {
				r.BackendStart, r.State = sess.BackendStart, sess.State
				r.TxAge = ages[pid].TransactionAgeSeconds
			}
			if c.Cyclic {
				r.Key = fmt.Sprintf("cycle:%v", c.CycleMembers)
				r.Transitive = len(c.Members) - 1
			} else {
				r.Key = rootKey(pid, r.BackendStart)
			}
			s.Roots = append(s.Roots, r)
		}
	}
	return s
}

func rootKey(pid int, start *time.Time) string {
	if start == nil {
		return fmt.Sprintf("pid:%d", pid)
	}
	return fmt.Sprintf("pid:%d@%d", pid, start.Unix())
}

// Input is everything the rules evaluate.
type Input struct {
	Now time.Time
	// Interval is the activity collection interval of the target.
	Interval time.Duration
	View     live.View
	// History holds activity samples, oldest first; the last is the current one.
	History []ActivitySample
	// Baselines of the target's recently active statements (may be empty:
	// rules then compare with the previous window, or report nothing).
	Baselines map[observe.QueryKey]baseline.Baseline
}

// latest returns the current sample if it is fresh enough to evaluate.
func (in Input) latest() (ActivitySample, bool) {
	if len(in.History) == 0 || in.View.Report == nil {
		return ActivitySample{}, false
	}
	last := in.History[len(in.History)-1]
	if in.Now.Sub(last.At) > 3*in.Interval+15*time.Second {
		return ActivitySample{}, false
	}
	return last, true
}

// streak walks back from the newest sample while match holds and samples are
// contiguous (no gap larger than three intervals). It returns the number of
// samples and the time of the first one.
func (in Input) streak(match func(ActivitySample) bool) (int, time.Time) {
	n := 0
	var first time.Time
	var next time.Time
	for i := len(in.History) - 1; i >= 0; i-- {
		s := in.History[i]
		if !match(s) {
			break
		}
		if !next.IsZero() && next.Sub(s.At) > 3*in.Interval+15*time.Second {
			break
		}
		n++
		first, next = s.At, s.At
	}
	return n, first
}
