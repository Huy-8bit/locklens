package notify

import (
	"context"
	"errors"
	"time"

	"github.com/locklens/locklens/internal/lib/textsafe"
	"github.com/locklens/locklens/internal/repository"
)

// Publish receives the recorder's incident events (incident.opened,
// incident.updated, incident.resolved) and queues the incident. It never
// blocks: an incident already queued is not queued twice, and when the queue
// is full the event is dropped (the next update or the reminder sweep
// catches up, because decisions use the incident's state).
func (s *Service) Publish(kind string, payload any) {
	switch kind {
	case "incident.opened", "incident.updated", "incident.resolved":
	default:
		return
	}
	p, ok := payload.(map[string]any)
	if !ok {
		return
	}
	id, ok := p["incident_id"].(int64)
	if !ok {
		return
	}
	s.enqueue(id)
}

// enqueue queues an incident for processing once.
func (s *Service) enqueue(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending[id] {
		return
	}
	select {
	case s.queue <- id:
		s.pending[id] = true
	default:
		s.log.Warn("notification queue full; an incident update was not queued", "incident_id", id)
	}
}

// Run processes queued incidents until ctx ends. Incidents are queued by
// the server that records them, so each change is sent once.
func (s *Service) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-s.queue:
			s.mu.Lock()
			delete(s.pending, id)
			s.mu.Unlock()
			s.process(ctx, id)
		}
	}
}

// RunReminders queues the open unacknowledged incidents every minute, so
// due reminders are sent, until ctx ends. With several servers, one runs it.
func (s *Service) RunReminders(ctx context.Context) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		ins, err := s.store.OpenUnacknowledgedIncidents(ctx)
		if err != nil {
			s.log.Warn("notification reminders: list incidents failed", "error", err.Error())
			continue
		}
		for _, in := range ins {
			s.enqueue(in.ID)
		}
	}
}

// process sends what each channel should receive for one incident now.
func (s *Service) process(ctx context.Context, id int64) {
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
		if event == "" || h.skip(event, in.Severity, now) {
			continue
		}
		if !silenceChecked {
			silenceChecked = true
			if silence, err = s.store.ActiveSilence(ctx, in.TargetID, in.RuleID); err != nil {
				s.log.Warn("notification: check silences failed", "incident_id", id, "error", err.Error())
				return
			}
		}
		if silence != nil {
			// A silenced event is recorded once; reminders are simply skipped.
			if event != EventReminder {
				s.record(ctx, ch.ID, &in.ID, event, in.Severity, "silenced", 0, nil)
			}
			continue
		}
		if observed == nil {
			observed = s.observed(ctx, in.ID)
		}
		s.deliver(ctx, ch, &in.ID, event, buildMessage(event, in, env, observed, s.publicURL, now))
	}
}

// observed returns the observations of the incident's findings (facts, not
// causes; rules never put SQL text in them).
func (s *Service) observed(ctx context.Context, incidentID int64) []string {
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

func (s *Service) deliver(ctx context.Context, ch repository.NotificationChannel, incidentID *int64, event string, m Message) repository.Delivery {
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

func (s *Service) record(ctx context.Context, channelID int64, incidentID *int64, event, severity, status string, attempts int, errMsg *string) repository.Delivery {
	sev := severity
	d := repository.Delivery{ChannelID: channelID, IncidentID: incidentID, Event: event, Severity: &sev, Status: status, Attempts: attempts, Error: errMsg, At: s.now().UTC()}
	if err := s.store.InsertDelivery(context.WithoutCancel(ctx), d); err != nil {
		s.log.Warn("notification: record delivery failed", "error", err.Error())
	}
	return d
}

// Test sends a test message through a channel now, enabled or not.
func (s *Service) Test(ctx context.Context, id int64) (repository.Delivery, error) {
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
func (s *Service) Deliveries(ctx context.Context, incidentID *int64, limit int) ([]repository.Delivery, error) {
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
func (s *Service) CreateSilence(ctx context.Context, in SilenceInput, by string) (repository.Silence, error) {
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
func (s *Service) Silences(ctx context.Context, includeEnded bool) ([]repository.Silence, error) {
	return s.store.ListSilences(ctx, includeEnded, 200)
}

// EndSilence ends a silence now.
func (s *Service) EndSilence(ctx context.Context, id int64) error {
	return s.store.EndSilence(ctx, id)
}
