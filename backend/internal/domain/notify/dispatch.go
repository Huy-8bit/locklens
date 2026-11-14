package notify

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/locklens/locklens/internal/lib/textsafe"
	"github.com/locklens/locklens/internal/repository"
)

// Sending. The recorder writes each incident change to the notification
// outbox, in the transaction of the change; the server running the
// notification job (one per cluster) claims the rows, processes their
// incidents, and removes them. A row claimed by a sender that stopped is
// claimed again after outboxStale. outboxBatch counts incidents.
const (
	outboxEvery   = 2 * time.Second
	outboxBatch   = 100
	outboxStale   = 2 * time.Minute
	reminderEvery = time.Minute
)

// Publish receives the recorder's incident events (incident.opened,
// incident.updated, incident.resolved). The changes are already in the
// outbox; this only wakes the sender when it runs on this server.
func (s *service) Publish(kind string, _ any) {
	switch kind {
	case "incident.opened", "incident.updated", "incident.resolved":
	default:
		return
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Run sends notifications until ctx ends: the incident changes in the
// outbox as they arrive, and every minute the open incidents, so due
// reminders, escalation tiers, and Alertmanager refreshes are sent. With
// several servers one runs it (a job); holder names it in outbox claims.
func (s *service) Run(ctx context.Context, holder string) {
	poll := time.NewTicker(outboxEvery)
	defer poll.Stop()
	remind := time.NewTicker(reminderEvery)
	defer remind.Stop()
	for {
		s.drainOutbox(ctx, holder)
		select {
		case <-ctx.Done():
			return
		case <-poll.C:
		case <-s.wake:
		case <-remind.C:
			s.remind(ctx)
		}
	}
}

// drainOutbox processes the outbox until it is empty.
func (s *service) drainOutbox(ctx context.Context, holder string) {
	for ctx.Err() == nil {
		items, err := s.store.ClaimNotifications(ctx, holder, outboxBatch, outboxStale)
		if err != nil {
			if ctx.Err() == nil {
				s.log.Warn("notifications: reading the outbox failed", "error", err.Error())
			}
			return
		}
		seen := map[int64]bool{}
		ids := make([]int64, 0, len(items))
		for _, it := range items {
			ids = append(ids, it.ID)
			if !seen[it.IncidentID] {
				seen[it.IncidentID] = true
				s.process(ctx, it.IncidentID)
			}
		}
		if ctx.Err() != nil {
			return // claimed rows are claimed again by the next sender
		}
		if err := s.store.DoneNotifications(ctx, ids, holder); err != nil {
			s.log.Warn("notifications: removing processed outbox rows failed", "error", err.Error())
			return
		}
		if len(seen) < outboxBatch {
			s.observeOutbox(ctx)
			return
		}
	}
}

// observeOutbox reports the outbox size and age.
func (s *service) observeOutbox(ctx context.Context) {
	if s.metrics == nil {
		return
	}
	n, oldest, err := s.store.PendingNotifications(ctx)
	if err != nil {
		return
	}
	s.metrics.OutboxPending.Set(float64(n))
	age := 0.0
	if oldest != nil {
		age = max(s.now().Sub(*oldest).Seconds(), 0)
	}
	s.metrics.OutboxAge.Set(age)
}

// remind processes the open incidents.
func (s *service) remind(ctx context.Context) {
	ins, err := s.store.OpenIncidents(ctx)
	if err != nil {
		s.log.Warn("notification reminders: list incidents failed", "error", err.Error())
		return
	}
	for _, in := range ins {
		if ctx.Err() != nil {
			return
		}
		s.process(ctx, in.ID)
	}
}

// process sends what each channel should receive for one incident now.
func (s *service) process(ctx context.Context, id int64) {
	in, err := s.store.GetIncident(ctx, id)
	if err != nil {
		if !errors.Is(err, repository.ErrNotFound) {
			s.log.Warn("notification: load incident failed", "incident_id", id, "error", err.Error())
		}
		return
	}
	// Imported incidents are history from elsewhere, not events here.
	if in.Source != "recorder" {
		return
	}
	channels, err := s.store.ListNotificationChannels(ctx)
	if err != nil || len(channels) == 0 {
		return
	}
	deliveries, err := s.store.ListDeliveries(ctx, &in.ID, 1000)
	if err != nil {
		s.log.Warn("notification: load deliveries failed", "incident_id", id, "error", err.Error())
		return
	}
	env := ""
	if in.TargetID != nil {
		if t, err := s.store.GetTarget(ctx, *in.TargetID); err == nil {
			env = t.Environment
		}
	}
	now := s.now().UTC()
	var observed []string
	var silence *repository.Silence
	var window *repository.MaintenanceWindow
	var silenceChecked bool
	for _, ch := range channels {
		var mine []repository.Delivery
		for _, d := range deliveries {
			if d.ChannelID == ch.ID {
				mine = append(mine, d)
			}
		}
		h := historyOf(mine)
		event := decide(in, env, ch, h, now)
		if event == "" && s.refreshDue(ch, in, h, now) {
			event = eventRefresh
		}
		if event == "" || h.skip(event, in.Severity, now) {
			continue
		}
		if !silenceChecked {
			silenceChecked = true
			if silence, err = s.store.ActiveSilence(ctx, in.TargetID, in.RuleID); err != nil {
				s.log.Warn("notification: check silences failed", "incident_id", id, "error", err.Error())
				return
			}
			windows, err := s.store.MaintenanceWindows(ctx)
			if err == nil {
				window, err = s.inMaintenance(ctx, windows, in, env, now)
			}
			if err != nil {
				s.log.Warn("notification: check maintenance windows failed", "incident_id", id, "error", err.Error())
				return
			}
		}
		if silence != nil || window != nil {
			// A silenced event is recorded once; reminders and refreshes
			// are simply skipped.
			if event != EventReminder && event != eventRefresh {
				var why *string
				if window != nil {
					msg := "maintenance window: " + window.Name
					why = &msg
				}
				s.record(ctx, ch.ID, &in.ID, event, in.Severity, "silenced", 0, why)
			}
			continue
		}
		if observed == nil {
			observed = s.observed(ctx, in.ID)
		}
		if event == eventRefresh {
			s.refresh(ctx, ch, in, buildMessage(EventOpened, in, env, observed, s.publicURL, now), now)
			continue
		}
		m := buildMessage(event, in, env, observed, s.publicURL, now)
		if ch.DelaySeconds > 0 && event == EventOpened {
			m.Note = fmt.Sprintf("not acknowledged for %s", (time.Duration(ch.DelaySeconds) * time.Second).String())
		}
		if s.deliver(ctx, ch, &in.ID, event, m).Status == "sent" && ch.Kind == KindAlertmanager {
			s.markRefreshed(ch.ID, in.ID, now)
		}
	}
}

// eventRefresh re-posts an open incident to Alertmanager, which would
// otherwise resolve it after its hold time; refreshes are not recorded as
// deliveries.
const eventRefresh = "refresh"

// alertmanagerRefresh is how often an open incident is re-posted.
const alertmanagerRefresh = 2 * time.Minute

// refreshDue reports whether an open incident Alertmanager was told about
// should be posted again.
func (s *service) refreshDue(ch repository.NotificationChannel, in repository.Incident, h history, now time.Time) bool {
	if ch.Kind != KindAlertmanager || in.State != "open" || !h.opened || h.resolved {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	last, ok := s.refreshed[[2]int64{ch.ID, in.ID}]
	if !ok {
		last = h.lastSent
	}
	return now.Sub(last) >= alertmanagerRefresh
}

func (s *service) markRefreshed(channelID, incidentID int64, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshed[[2]int64{channelID, incidentID}] = now
}

// refresh re-posts an open incident to an Alertmanager channel; only a
// failure is recorded.
func (s *service) refresh(ctx context.Context, ch repository.NotificationChannel, in repository.Incident, m Message, now time.Time) {
	d, err := s.destination(ch)
	if err == nil {
		sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		_, err = s.send.send(sctx, ch.Kind, d, m)
		cancel()
	}
	if err != nil {
		s.log.Warn("alertmanager refresh failed", "channel", ch.Name, "incident_id", in.ID, "error", err.Error())
		return
	}
	s.markRefreshed(ch.ID, in.ID, now)
}

// observed returns the observations of the incident's findings (facts, not
// causes; rules never put SQL text in them).
func (s *service) observed(ctx context.Context, incidentID int64) []string {
	fs, err := s.store.ListFindings(ctx, repository.FindingFilter{IncidentID: incidentID, Limit: 5})
	if err != nil {
		return []string{}
	}
	out := []string{}
	for _, f := range fs {
		out = append(out, f.Finding.Observed...)
	}
	return out
}

func (s *service) deliver(ctx context.Context, ch repository.NotificationChannel, incidentID *int64, event string, m Message) repository.Delivery {
	d, err := s.destination(ch)
	if err != nil {
		msg := err.Error()
		return s.record(ctx, ch.ID, incidentID, event, m.Severity, "failed", 0, &msg)
	}
	sctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	attempts, err := s.send.send(sctx, ch.Kind, d, m)
	cancel()
	if err != nil {
		msg, _ := textsafe.Truncate(textsafe.Terminal(err.Error()), 500)
		s.log.Warn("notification failed", "channel", ch.Name, "event", event, "attempts", attempts, "error", msg)
		return s.record(ctx, ch.ID, incidentID, event, m.Severity, "failed", attempts, &msg)
	}
	s.log.Info("notification sent", "channel", ch.Name, "event", event, "incident_id", m.IncidentID)
	return s.record(ctx, ch.ID, incidentID, event, m.Severity, "sent", attempts, nil)
}

func (s *service) record(ctx context.Context, channelID int64, incidentID *int64, event, severity, status string, attempts int, errMsg *string) repository.Delivery {
	sev := severity
	d := repository.Delivery{ChannelID: channelID, IncidentID: incidentID, Event: event, Severity: &sev, Status: status, Attempts: attempts, Error: errMsg, At: s.now().UTC()}
	if err := s.store.InsertDelivery(context.WithoutCancel(ctx), d); err != nil {
		s.log.Warn("notification: record delivery failed", "error", err.Error())
	}
	return d
}

// Test sends a test message through a channel now, enabled or not.
func (s *service) Test(ctx context.Context, id int64) (repository.Delivery, error) {
	ch, err := s.store.GetNotificationChannel(ctx, id)
	if err != nil {
		return repository.Delivery{}, err
	}
	now := s.now().UTC()
	m := Message{Event: EventTest, Title: "Test notification from LockLens", Severity: "info", State: "test", Instance: "LockLens",
		RuleID: "notifications.test", OpenedAt: now, Duration: "0s",
		Observed: []string{"This channel works. Incident notifications will look like this message."}}
	if s.publicURL != "" {
		m.URL = s.publicURL + "/settings"
	}
	return s.deliver(ctx, ch, nil, EventTest, m), nil
}

// Deliveries returns recent deliveries, of one incident when incidentID is set.
func (s *service) Deliveries(ctx context.Context, incidentID *int64, limit int) ([]repository.Delivery, error) {
	return s.store.ListDeliveries(ctx, incidentID, limit)
}

// SilenceInput creates a silence.
type SilenceInput struct {
	TargetID *int64  `json:"target_id"`
	RuleID   *string `json:"rule_id"`
	// Minutes from now; or EndsAt.
	Minutes int        `json:"minutes"`
	EndsAt  *time.Time `json:"ends_at"`
	Reason  string     `json:"reason"`
}

// CreateSilence validates and stores a silence starting now.
func (s *service) CreateSilence(ctx context.Context, in SilenceInput, by string) (repository.Silence, error) {
	now := s.now().UTC()
	fields := map[string]string{}
	end := now.Add(time.Duration(in.Minutes) * time.Minute)
	if in.EndsAt != nil {
		end = in.EndsAt.UTC()
	}
	if !end.After(now) || end.Sub(now) > 30*24*time.Hour {
		fields["ends_at"] = "between now and 30 days from now"
	}
	if len([]rune(in.Reason)) > 500 {
		fields["reason"] = "at most 500 characters"
	}
	if in.RuleID != nil && (*in.RuleID == "" || len(*in.RuleID) > 100) {
		fields["rule_id"] = "a rule ID, or null for every rule"
	}
	if in.TargetID != nil {
		if _, err := s.store.GetTarget(ctx, *in.TargetID); err != nil {
			fields["target_id"] = "unknown instance"
		}
	}
	if len(fields) > 0 {
		return repository.Silence{}, &ValidationError{Fields: fields}
	}
	return s.store.CreateSilence(ctx, repository.Silence{TargetID: in.TargetID, RuleID: in.RuleID, EndsAt: end,
		Reason: textsafe.Terminal(in.Reason), CreatedBy: by})
}

// Silences returns current silences (or all recent ones).
func (s *service) Silences(ctx context.Context, includeEnded bool) ([]repository.Silence, error) {
	return s.store.ListSilences(ctx, includeEnded, 200)
}

// EndSilence ends a silence now.
func (s *service) EndSilence(ctx context.Context, id int64) error {
	return s.store.EndSilence(ctx, id)
}
