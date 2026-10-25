package incidents

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/locklens/locklens/internal/domain/collector"
	"github.com/locklens/locklens/internal/repository"
)

// maxSessionsAtTrigger bounds the sessions stored at a trigger.
const maxSessionsAtTrigger = 25

// sessionBrief is one client session in the "sessions" event: who ran what,
// in which state, for how long, and whom it blocked. The SQL text is under
// "query", the key exports strip unless SQL text is requested, and exists
// only where the target collects it.
type sessionBrief struct {
	PID                   int      `json:"pid"`
	Database              *string  `json:"database"`
	User                  *string  `json:"user"`
	Application           *string  `json:"application_name"`
	ClientAddr            *string  `json:"client_addr"`
	State                 *string  `json:"state"`
	WaitEventType         *string  `json:"wait_event_type"`
	WaitEvent             *string  `json:"wait_event"`
	QueryAgeSeconds       *float64 `json:"query_age_seconds"`
	TransactionAgeSeconds *float64 `json:"transaction_age_seconds"`
	BlockedBy             []int    `json:"blocked_by,omitempty"`
	Blocks                int      `json:"blocks,omitempty"`
	Query                 *string  `json:"query,omitempty"`
	QueryTruncated        bool     `json:"query_truncated,omitempty"`
}

type sessionCounts struct {
	Client            int `json:"client"`
	Active            int `json:"active"`
	IdleInTransaction int `json:"idle_in_transaction"`
	WaitingLock       int `json:"waiting_lock"`
	Listed            int `json:"listed"`
}

// sessionsEvent records the client sessions that were doing something when
// an incident opened (running a query, holding a transaction, or waiting),
// ordered by how many sessions they block and then by how long they ran.
// The activity snapshot holds only sessions in a transaction or involved in
// blocking, so the counts come from pg_stat_activity's summary instead.
func sessionsEvent(v collector.View, now time.Time) *repository.IncidentEvent {
	if v.Activity == nil || v.Activity.Snapshot == nil || v.Report == nil {
		return nil
	}
	snap := v.Activity.Snapshot
	ages := map[int]int{}
	for i, a := range v.Report.Analysis.Sessions {
		ages[a.PID] = i
	}
	blocks := map[int]int{}
	blockedBy := map[int][]int{}
	for _, n := range v.Report.Analysis.Nodes {
		blocks[n.ID] = n.TransitiveBlocked
		blockedBy[n.ID] = n.BlockedBy
	}
	ac := v.Activity.Counts
	counts := sessionCounts{Client: ac.ClientTotal, Active: ac.Active, IdleInTransaction: ac.IdleInTx + ac.IdleInTxAborted, WaitingLock: ac.WaitingLock}
	var list []sessionBrief
	for _, s := range snap.Sessions {
		if s.BackendType != nil && *s.BackendType != "client backend" || s.LeaderPID != nil {
			continue
		}
		state := ""
		if s.State != nil {
			state = *s.State
		}
		waiting := len(s.WaitingLocks) > 0
		if state == "idle" && !waiting && blocks[s.PID] == 0 {
			continue
		}
		b := sessionBrief{PID: s.PID, Database: s.Database, User: s.User, Application: s.ApplicationName, ClientAddr: s.ClientAddr,
			State: s.State, WaitEventType: s.WaitEventType, WaitEvent: s.WaitEvent, BlockedBy: blockedBy[s.PID], Blocks: blocks[s.PID],
			Query: s.Query, QueryTruncated: s.QueryTruncated}
		if i, ok := ages[s.PID]; ok {
			a := v.Report.Analysis.Sessions[i]
			b.QueryAgeSeconds, b.TransactionAgeSeconds = a.QueryAgeSeconds, a.TransactionAgeSeconds
		}
		list = append(list, b)
	}
	if counts.Client == 0 && len(list) == 0 {
		return nil
	}
	age := func(p *float64) float64 {
		if p == nil {
			return -1
		}
		return *p
	}
	slices.SortStableFunc(list, func(a, b sessionBrief) int {
		if c := cmp.Compare(b.Blocks, a.Blocks); c != 0 {
			return c
		}
		return cmp.Compare(max(age(b.QueryAgeSeconds), age(b.TransactionAgeSeconds)), max(age(a.QueryAgeSeconds), age(a.TransactionAgeSeconds)))
	})
	if len(list) > maxSessionsAtTrigger {
		list = list[:maxSessionsAtTrigger]
	}
	counts.Listed = len(list)
	payload, _ := json.Marshal(map[string]any{"counts": counts, "sessions": list, "query_text_collected": snap.Collection.IncludeQuery})
	return &repository.IncidentEvent{At: now, Kind: KindSessions, Payload: payload,
		Summary: fmt.Sprintf("Sessions at the trigger: %d active, %d idle in transaction, %d waiting for a lock (of %d client sessions).",
			counts.Active, counts.IdleInTransaction, counts.WaitingLock, counts.Client)}
}
