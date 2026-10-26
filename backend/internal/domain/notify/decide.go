package notify

import (
	"slices"
	"time"

	"github.com/locklens/locklens/internal/repository"
)

// Events a channel can receive.
const (
	EventOpened    = "opened"
	EventEscalated = "escalated"
	EventResolved  = "resolved"
	EventReminder  = "reminder"
	EventTest      = "test"
)

// retryFailedAfter is how long a failed event waits before it is tried again
// (incident updates arrive every minute or so while an incident is open).
const retryFailedAfter = 5 * time.Minute

// history is what one channel already received for one incident, in the
// current cycle (since the last resolution it was told about).
type history struct {
	opened      bool      // an "opened" was sent in this cycle
	maxSeverity string    // highest severity sent in this cycle
	lastSent    time.Time // last successful delivery of any event
	resolved    bool      // the cycle ended with a sent "resolved"
	// silenced and failed record events of this cycle that were not sent,
	// keyed by event and severity, with the time of the last attempt.
	silenced map[string]bool
	failed   map[string]time.Time
}

func eventKey(event, severity string) string { return event + "/" + severity }

// historyOf replays a channel's deliveries for one incident (any order).
func historyOf(ds []repository.Delivery) history {
	ds = slices.Clone(ds)
	slices.SortFunc(ds, func(a, b repository.Delivery) int {
		if c := a.At.Compare(b.At); c != 0 {
			return c
		}
		return int(a.ID - b.ID)
	})
	h := history{silenced: map[string]bool{}, failed: map[string]time.Time{}}
	for _, d := range ds {
		sev := ""
		if d.Severity != nil {
			sev = *d.Severity
		}
		switch d.Status {
		case "silenced":
			h.silenced[eventKey(d.Event, sev)] = true
			continue
		case "failed":
			h.failed[eventKey(d.Event, sev)] = d.At
			continue
		}
		h.lastSent = d.At
		switch d.Event {
		case EventOpened:
			if h.resolved || !h.opened {
				h = history{opened: true, maxSeverity: sev, lastSent: d.At, silenced: map[string]bool{}, failed: map[string]time.Time{}}
			}
		case EventEscalated:
			if rank(sev) > rank(h.maxSeverity) {
				h.maxSeverity = sev
			}
		case EventResolved:
			h.resolved = true
		}
	}
	return h
}

// decide returns the event a channel should receive for an incident now, or
// "" for none. env is the incident's instance environment.
func decide(in repository.Incident, env string, ch repository.NotificationChannel, h history, now time.Time) string {
	if !ch.Enabled || (len(ch.Environments) > 0 && !slices.Contains(ch.Environments, env)) {
		return ""
	}
	qualifies := rank(in.Severity) >= rank(ch.MinSeverity)
	cycleOpen := h.opened && !h.resolved
	switch in.State {
	case "open":
		switch {
		case !cycleOpen && qualifies && ch.OnOpen:
			return EventOpened
		case cycleOpen && rank(in.Severity) > rank(h.maxSeverity) && ch.OnEscalate:
			return EventEscalated
		case cycleOpen && in.AcknowledgedAt == nil && ch.ReminderSeconds > 0 &&
			now.Sub(h.lastSent) >= time.Duration(ch.ReminderSeconds)*time.Second:
			return EventReminder
		}
	case "resolved":
		if cycleOpen && ch.OnResolve {
			return EventResolved
		}
	}
	return ""
}

// skip reports whether an event was already handled in this cycle: silenced
// once (it is recorded, not repeated), or failed less than retryFailedAfter
// ago.
func (h history) skip(event, severity string, now time.Time) bool {
	k := eventKey(event, severity)
	if event != EventReminder && h.silenced[k] {
		return true
	}
	if at, ok := h.failed[k]; ok && now.Sub(at) < retryFailedAfter {
		return true
	}
	return false
}
