package incidents

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/locklens/locklens/internal/domain/live"
	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/repository"
)

// loadWindow is how much active session sampling an incident keeps from
// before its trigger.
const loadWindow = 5 * time.Minute

type loadStatement struct {
	DBID     uint32             `json:"dbid"`
	UserID   uint32             `json:"userid"`
	QueryID  int64              `json:"queryid,string"`
	DatName  string             `json:"datname"`
	UserName string             `json:"username"`
	AAS      float64            `json:"aas"`
	Classes  map[string]float64 `json:"classes"`
}

type loadWait struct {
	Class string  `json:"wait_class"`
	Event string  `json:"wait_event"`
	AAS   float64 `json:"aas"`
}

// loadEvent summarises the active session samples of the last minutes before
// a trigger: average active sessions by wait class, the top wait events, and
// the statements (by query_id) that were active most. It is evidence that
// stays with the incident when the sampled history expires.
func loadEvent(v live.View, now time.Time) *repository.IncidentEvent {
	var ticks int
	classes := map[string]int{}
	events := map[[2]string]int{}
	type skey struct {
		dbid, userid uint32
		qid          int64
	}
	stmts := map[skey]*loadStatement{}
	stmtClasses := map[skey]map[string]int{}
	for _, b := range v.Load {
		if b.At.Before(now.Add(-loadWindow)) {
			continue
		}
		ticks += b.Ticks
		for _, g := range b.Groups {
			classes[g.WaitClass] += g.Samples
			if g.WaitClass == observe.WaitIdleTx {
				continue
			}
			events[[2]string{g.WaitClass, g.WaitEvent}] += g.Samples
			k := skey{g.DBID, g.UserID, g.QueryID}
			if stmts[k] == nil {
				stmts[k] = &loadStatement{DBID: g.DBID, UserID: g.UserID, QueryID: g.QueryID, DatName: g.DatName, UserName: g.UserName}
				stmtClasses[k] = map[string]int{}
			}
			stmts[k].AAS += float64(g.Samples)
			stmtClasses[k][g.WaitClass] += g.Samples
		}
	}
	if ticks == 0 {
		return nil
	}
	n := float64(ticks)
	byClass := map[string]float64{}
	var active float64
	for c, s := range classes {
		byClass[c] = float64(s) / n
		if c != observe.WaitIdleTx {
			active += float64(s) / n
		}
	}
	var waits []loadWait
	for k, s := range events {
		waits = append(waits, loadWait{Class: k[0], Event: k[1], AAS: float64(s) / n})
	}
	slices.SortFunc(waits, func(a, b loadWait) int { return cmp.Compare(b.AAS, a.AAS) })
	if len(waits) > 8 {
		waits = waits[:8]
	}
	var top []loadStatement
	for k, s := range stmts {
		s.AAS /= n
		s.Classes = map[string]float64{}
		for c, x := range stmtClasses[k] {
			s.Classes[c] = float64(x) / n
		}
		top = append(top, *s)
	}
	slices.SortFunc(top, func(a, b loadStatement) int { return cmp.Compare(b.AAS, a.AAS) })
	if len(top) > 10 {
		top = top[:10]
	}
	payload, _ := json.Marshal(map[string]any{"window_seconds": loadWindow.Seconds(), "ticks": ticks, "active_aas": active,
		"classes": byClass, "waits": waits, "statements": top})
	return &repository.IncidentEvent{At: now, Kind: KindLoad, Payload: payload,
		Summary: fmt.Sprintf("Database load in the %s before the trigger: %.1f active sessions on average (from %d samples).", loadWindow, active, ticks)}
}
