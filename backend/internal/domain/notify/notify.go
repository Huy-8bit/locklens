// Package notify sends incident notifications to webhooks, Slack, and email.
//
// Channels choose which incidents (minimum severity, environments) and which
// events (opened, escalated, resolved, reminders while unacknowledged) they
// receive. Decisions are made from the incident's current state and the
// channel's delivery history, so an event is never sent twice, also after a
// restart. Silences stop notifications for an instance, a rule, or both,
// without affecting incident recording.
//
// Messages carry the incident's title, severity, instance, rule, times, a few
// observations, and a link. They never carry SQL text, connection strings,
// or credentials. Destinations are sealed with the master key and are
// write-only through the API.
package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/mail"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/locklens/locklens/internal/domain/rules"
	"github.com/locklens/locklens/internal/lib/obs"
	"github.com/locklens/locklens/internal/lib/secret"
	"github.com/locklens/locklens/internal/repository"
)

// Channel kinds.
const (
	KindWebhook = "webhook"
	KindSlack   = "slack"
	KindEmail   = "email"
	// KindAlertmanager posts alerts to a Prometheus Alertmanager, which
	// routes them with its own configuration (PagerDuty, Opsgenie, ...).
	KindAlertmanager = "alertmanager"
)

// SMTP connection security.
const (
	TLSImplicit = "tls"
	TLSStart    = "starttls"
	TLSNone     = "none"
)

// Destination is where a channel sends. It is stored sealed and never
// returned by the API.
type Destination struct {
	// URL of a webhook or a Slack incoming webhook.
	URL string `json:"url,omitempty"`
	// Secret signs webhook bodies (HMAC-SHA256, X-LockLens-Signature).
	Secret string `json:"secret,omitempty"`
	// Token is sent to Alertmanager as a bearer token (optional).
	Token string `json:"token,omitempty"`

	SMTPHost string   `json:"smtp_host,omitempty"`
	SMTPPort int      `json:"smtp_port,omitempty"`
	TLS      string   `json:"tls,omitempty"`
	Username string   `json:"username,omitempty"`
	Password string   `json:"password,omitempty"`
	From     string   `json:"from,omitempty"`
	To       []string `json:"to,omitempty"`
}

// ChannelInput creates or changes a channel. A nil Destination on update
// keeps the stored one.
type ChannelInput struct {
	Name            string   `json:"name"`
	Kind            string   `json:"kind"`
	Enabled         bool     `json:"enabled"`
	MinSeverity     string   `json:"min_severity"`
	Environments    []string `json:"environments"`
	OnOpen          bool     `json:"on_open"`
	OnEscalate      bool     `json:"on_escalate"`
	OnResolve       bool     `json:"on_resolve"`
	ReminderMinutes int      `json:"reminder_minutes"`
	// RuleIDs and TargetIDs limit the channel (empty: all).
	RuleIDs   []string `json:"rule_ids"`
	TargetIDs []int64  `json:"target_ids"`
	// DelayMinutes > 0 makes the channel an escalation tier: it receives an
	// incident once it stayed open and unacknowledged this long.
	DelayMinutes int          `json:"delay_minutes"`
	Destination  *Destination `json:"destination"`
}

// ValidationError lists problems with an input, keyed by field.
type ValidationError struct {
	Fields map[string]string
}

func (e *ValidationError) Error() string {
	parts := make([]string, 0, len(e.Fields))
	for k, v := range e.Fields {
		parts = append(parts, k+": "+v)
	}
	return "invalid notification channel: " + strings.Join(parts, "; ")
}

// sealContext binds sealed destinations to their purpose.
var sealContext = []byte("locklens notification channel v1")

var envPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)

// Service sends incident notifications to channels, with reminders and silences.
type Service interface {
	// Publish receives the recorder's incident events (incident.opened,
	// incident.updated, incident.resolved): the changes are in the
	// notification outbox; this wakes the sender if it runs here. It never
	// blocks.
	Publish(kind string, payload any)
	// Run sends notifications until ctx ends: the incident changes in the
	// outbox as they arrive, and every minute the open incidents (reminders,
	// escalation tiers, Alertmanager refreshes). With several servers one
	// runs it; holder (the server ID) names it in outbox claims.
	Run(ctx context.Context, holder string)
	// Test sends a test message through a channel now, enabled or not.
	Test(ctx context.Context, id int64) (repository.Delivery, error)
	// Deliveries returns recent deliveries, of one incident when incidentID is set.
	Deliveries(ctx context.Context, incidentID *int64, limit int) ([]repository.Delivery, error)
	// CreateSilence validates and stores a silence starting now.
	CreateSilence(ctx context.Context, in SilenceInput, by string) (repository.Silence, error)
	// Silences returns current silences (or all recent ones).
	Silences(ctx context.Context, includeEnded bool) ([]repository.Silence, error)
	// EndSilence ends a silence now.
	EndSilence(ctx context.Context, id int64) error
	// Channels returns every channel.
	Channels(ctx context.Context) ([]repository.NotificationChannel, error)
	// SaveChannel validates and stores a channel; id 0 creates one.
	SaveChannel(ctx context.Context, id int64, in ChannelInput) (repository.NotificationChannel, error)
	// MaintenanceWindows returns every maintenance window.
	MaintenanceWindows(ctx context.Context) ([]repository.MaintenanceWindow, error)
	// SaveMaintenanceWindow creates (id 0) or replaces a maintenance window.
	SaveMaintenanceWindow(ctx context.Context, id int64, in WindowInput, by string) (repository.MaintenanceWindow, error)
	// DeleteMaintenanceWindow deletes a maintenance window.
	DeleteMaintenanceWindow(ctx context.Context, id int64) error
	// DeleteChannel removes a channel and its delivery history.
	DeleteChannel(ctx context.Context, id int64) error
}

var _ Service = (*service)(nil)

// service manages channels and silences and dispatches notifications.
type service struct {
	store     Repositories
	box       *secret.Box
	log       *slog.Logger
	publicURL string
	version   string
	send      sender
	now       func() time.Time

	metrics *obs.Metrics
	wake    chan struct{}
	mu      sync.Mutex
	// refreshed is when each open incident was last posted to each
	// Alertmanager channel (by channel and incident).
	refreshed map[[2]int64]time.Time
}

// New returns a service. publicURL is LockLens' address as users reach it
// (for links in messages); it may be empty. metrics may be nil.
func New(store Repositories, box *secret.Box, metrics *obs.Metrics, log *slog.Logger, publicURL, version string) Service {
	return &service{store: store, box: box, metrics: metrics, log: log, publicURL: strings.TrimRight(publicURL, "/"), version: version,
		send: newSender(version), now: time.Now, wake: make(chan struct{}, 1), refreshed: map[[2]int64]time.Time{}}
}

// Channels returns every channel.
func (s *service) Channels(ctx context.Context) ([]repository.NotificationChannel, error) {
	return s.store.ListNotificationChannels(ctx)
}

// SaveChannel validates and stores a channel; id 0 creates one.
func (s *service) SaveChannel(ctx context.Context, id int64, in ChannelInput) (repository.NotificationChannel, error) {
	in.Name = strings.TrimSpace(in.Name)
	fields := map[string]string{}
	if in.Name == "" || len([]rune(in.Name)) > 100 {
		fields["name"] = "between 1 and 100 characters"
	}
	var current repository.NotificationChannel
	if id != 0 {
		c, err := s.store.GetNotificationChannel(ctx, id)
		if err != nil {
			return c, err
		}
		current = c
		in.Kind = c.Kind
	}
	switch in.Kind {
	case KindWebhook, KindSlack, KindEmail:
	case KindAlertmanager:
		// Alertmanager keeps the alert state: it is told about every
		// change and refreshed while the incident is open, never reminded.
		in.OnOpen, in.OnEscalate, in.OnResolve, in.ReminderMinutes = true, true, true, 0
	default:
		fields["kind"] = "webhook, slack, email, or alertmanager"
	}
	for _, r := range in.RuleIDs {
		if !slices.Contains(rules.AllRules, r) {
			fields["rule_ids"] = "unknown rule " + r
		}
	}
	if len(in.TargetIDs) > 100 {
		fields["target_ids"] = "at most 100 instances"
	}
	if in.DelayMinutes != 0 && (in.DelayMinutes < 5 || in.DelayMinutes > 24*60) {
		fields["delay_minutes"] = "0 (notify at once) or between 5 minutes and 1 day"
	}
	if in.MinSeverity == "" {
		in.MinSeverity = "warning"
	}
	if rank(in.MinSeverity) < 0 {
		fields["min_severity"] = "info, warning, or critical"
	}
	envs := []string{}
	for _, e := range in.Environments {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if !envPattern.MatchString(e) {
			fields["environments"] = "lower-case letters, digits, '-' and '_'"
			break
		}
		envs = append(envs, e)
	}
	if len(envs) > 20 {
		fields["environments"] = "at most 20"
	}
	if in.ReminderMinutes != 0 && (in.ReminderMinutes < 10 || in.ReminderMinutes > 7*24*60) {
		fields["reminder_minutes"] = "0 (off) or between 10 minutes and 7 days"
	}
	if !in.OnOpen && !in.OnEscalate && !in.OnResolve && in.ReminderMinutes == 0 {
		fields["events"] = "choose at least one event"
	}
	if id == 0 && in.Destination == nil {
		fields["destination"] = "required"
	}
	var sealed []byte
	var display string
	if in.Destination != nil && len(fields) == 0 {
		d, problems := normalizeDestination(in.Kind, *in.Destination)
		for k, v := range problems {
			fields[k] = v
		}
		if len(problems) == 0 {
			raw, _ := json.Marshal(d)
			ct, err := s.box.Seal(raw, sealContext)
			if err != nil {
				return current, err
			}
			sealed, display = ct, describe(in.Kind, d)
		}
	}
	if len(fields) > 0 {
		return current, &ValidationError{Fields: fields}
	}
	c := repository.NotificationChannel{ID: id, Name: in.Name, Kind: in.Kind, Enabled: in.Enabled, MinSeverity: in.MinSeverity,
		Environments: envs, OnOpen: in.OnOpen, OnEscalate: in.OnEscalate, OnResolve: in.OnResolve, ReminderSeconds: in.ReminderMinutes * 60,
		RuleIDs: in.RuleIDs, TargetIDs: in.TargetIDs, DelaySeconds: in.DelayMinutes * 60,
		ConfigCiphertext: sealed, Display: display}
	out, err := s.store.SaveNotificationChannel(ctx, c)
	if errors.Is(err, repository.ErrConflict) {
		return out, &ValidationError{Fields: map[string]string{"name": "already used by another channel"}}
	}
	return out, err
}

// DeleteChannel removes a channel and its delivery history.
func (s *service) DeleteChannel(ctx context.Context, id int64) error {
	return s.store.DeleteNotificationChannel(ctx, id)
}

func (s *service) destination(c repository.NotificationChannel) (Destination, error) {
	var d Destination
	raw, err := s.box.Open(c.ConfigCiphertext, sealContext)
	if err != nil {
		return d, errors.New("cannot decrypt the channel's destination (was the master key changed?)")
	}
	err = json.Unmarshal(raw, &d)
	return d, err
}

// normalizeDestination checks a destination for its kind and keeps only the
// fields that kind uses.
func normalizeDestination(kind string, d Destination) (Destination, map[string]string) {
	fields := map[string]string{}
	switch kind {
	case KindWebhook, KindSlack, KindAlertmanager:
		u, err := url.Parse(strings.TrimSpace(d.URL))
		switch {
		case err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http"):
			fields["destination.url"] = "an http or https URL"
		case kind == KindSlack && u.Scheme != "https":
			fields["destination.url"] = "a Slack incoming webhook URL (https://hooks.slack.com/…)"
		case u.User != nil:
			fields["destination.url"] = "must not contain a user name or password"
		case len(d.URL) > 2048:
			fields["destination.url"] = "at most 2048 characters"
		}
		if len(d.Secret) > 256 {
			fields["destination.secret"] = "at most 256 characters"
		}
		out := Destination{URL: strings.TrimSpace(d.URL)}
		switch kind {
		case KindWebhook:
			out.Secret = d.Secret
		case KindAlertmanager:
			if len(d.Token) > 512 {
				fields["destination.token"] = "at most 512 characters"
			}
			out.Token = d.Token
		}
		return out, fields
	case KindEmail:
		out := Destination{SMTPHost: strings.TrimSpace(d.SMTPHost), SMTPPort: d.SMTPPort, TLS: d.TLS, Username: d.Username, Password: d.Password}
		if out.SMTPHost == "" || strings.ContainsAny(out.SMTPHost, " /:@") {
			fields["destination.smtp_host"] = "a host name or address"
		}
		if out.SMTPPort == 0 {
			out.SMTPPort = 587
		}
		if out.SMTPPort < 1 || out.SMTPPort > 65535 {
			fields["destination.smtp_port"] = "between 1 and 65535"
		}
		if out.TLS == "" {
			out.TLS = TLSStart
		}
		switch out.TLS {
		case TLSImplicit, TLSStart:
		case TLSNone:
			if out.Username != "" {
				fields["destination.tls"] = "a password is never sent without TLS; use starttls or tls"
			}
		default:
			fields["destination.tls"] = "tls, starttls, or none"
		}
		if a, err := mail.ParseAddress(d.From); err != nil {
			fields["destination.from"] = "an email address"
		} else {
			out.From = a.Address
		}
		for _, t := range d.To {
			if strings.TrimSpace(t) == "" {
				continue
			}
			a, err := mail.ParseAddress(t)
			if err != nil {
				fields["destination.to"] = fmt.Sprintf("%q is not an email address", t)
				break
			}
			out.To = append(out.To, a.Address)
		}
		if len(out.To) == 0 || len(out.To) > 20 {
			fields["destination.to"] = "between 1 and 20 recipients"
		}
		return out, fields
	}
	return d, fields
}

// describe is the non-secret summary of a destination shown in the UI: the
// host of a URL (never its path, which holds tokens), or the recipients.
func describe(kind string, d Destination) string {
	switch kind {
	case KindWebhook, KindSlack, KindAlertmanager:
		if u, err := url.Parse(d.URL); err == nil {
			return u.Scheme + "://" + u.Host
		}
	case KindEmail:
		return fmt.Sprintf("%s via %s", strings.Join(d.To, ", "), net.JoinHostPort(d.SMTPHost, fmt.Sprint(d.SMTPPort)))
	}
	return ""
}

func rank(sev string) int {
	switch sev {
	case "info":
		return 0
	case "warning":
		return 1
	case "critical":
		return 2
	}
	return -1
}
