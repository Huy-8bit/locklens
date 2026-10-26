package notify

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/locklens/locklens/internal/lib/render"
	"github.com/locklens/locklens/internal/lib/textsafe"
	"github.com/locklens/locklens/internal/repository"
)

// Message is what every channel kind renders. It holds no SQL text,
// connection string, or credential.
type Message struct {
	Event       string     `json:"event"`
	IncidentID  int64      `json:"incident_id,omitempty"`
	Title       string     `json:"title"`
	Severity    string     `json:"severity"`
	State       string     `json:"state"`
	Instance    string     `json:"instance"`
	Environment string     `json:"environment"`
	RuleID      string     `json:"rule_id"`
	OpenedAt    time.Time  `json:"opened_at"`
	ResolvedAt  *time.Time `json:"resolved_at,omitempty"`
	Resolution  string     `json:"resolution,omitempty"`
	Duration    string     `json:"duration"`
	Observed    []string   `json:"observed"`
	URL         string     `json:"url,omitempty"`
}

// clean makes a server- or user-provided string safe for every channel: one
// line, no control characters, bounded length.
func clean(s string, max int) string {
	s, _ = textsafe.Truncate(textsafe.Terminal(s), max)
	return s
}

func buildMessage(event string, in repository.Incident, env string, observed []string, publicURL string, now time.Time) Message {
	end := now
	if in.ResolvedAt != nil {
		end = *in.ResolvedAt
	}
	m := Message{Event: event, IncidentID: in.ID, Title: clean(in.Title, 200), Severity: in.Severity, State: in.State,
		Instance: clean(in.TargetName, 100), Environment: env, RuleID: in.RuleID, OpenedAt: in.OpenedAt.UTC(),
		ResolvedAt: in.ResolvedAt, Duration: render.Duration(end.Sub(in.OpenedAt).Seconds()), Observed: []string{}}
	if in.Resolution != nil {
		m.Resolution = *in.Resolution
	}
	for _, o := range observed {
		if len(m.Observed) == 3 {
			break
		}
		m.Observed = append(m.Observed, clean(o, 300))
	}
	if publicURL != "" && in.ID > 0 {
		m.URL = fmt.Sprintf("%s/incidents/%d", publicURL, in.ID)
	}
	return m
}

var eventWord = map[string]string{
	EventOpened:    "Opened",
	EventEscalated: "Escalated",
	EventResolved:  "Resolved",
	EventReminder:  "Still open",
	EventTest:      "Test",
}

// Subject is a one-line summary, used as the email subject and chat text.
func (m Message) Subject() string {
	return fmt.Sprintf("[%s] %s: %s (%s)", strings.ToUpper(m.Severity), eventWord[m.Event], m.Title, m.Instance)
}

// Text is the plain-text body (email).
func (m Message) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", m.Subject())
	fmt.Fprintf(&b, "Instance:    %s", m.Instance)
	if m.Environment != "" {
		fmt.Fprintf(&b, " (%s)", m.Environment)
	}
	fmt.Fprintf(&b, "\nSeverity:    %s\nRule:        %s\nOpened:      %s\n", m.Severity, m.RuleID, m.OpenedAt.Format(time.RFC3339))
	if m.ResolvedAt != nil {
		fmt.Fprintf(&b, "Resolved:    %s (%s)\n", m.ResolvedAt.UTC().Format(time.RFC3339), m.Resolution)
	}
	fmt.Fprintf(&b, "Duration:    %s\n", m.Duration)
	if len(m.Observed) > 0 {
		b.WriteString("\nObserved:\n")
		for _, o := range m.Observed {
			fmt.Fprintf(&b, "- %s\n", o)
		}
	}
	if m.URL != "" {
		fmt.Fprintf(&b, "\nOpen the incident: %s\n", m.URL)
	}
	b.WriteString("\nObservations are evidence, not a confirmed cause. Sent by LockLens.\n")
	return b.String()
}

// webhookBody is the JSON document POSTed to a webhook. Its shape is
// versioned; fields are only added.
func (m Message) webhookBody() []byte {
	raw, _ := json.Marshal(map[string]any{"version": 1, "source": "locklens", "event": m.Event, "incident": m,
		"sent_at": time.Now().UTC()})
	return raw
}

// slackEscape escapes the characters Slack's mrkdwn treats as markup.
func slackEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

var slackIcon = map[string]string{"critical": ":red_circle:", "warning": ":large_orange_circle:", "info": ":large_blue_circle:"}

// slackBody is a Slack incoming-webhook message.
func (m Message) slackBody() []byte {
	icon := slackIcon[m.Severity]
	if m.Event == EventResolved {
		icon = ":white_check_mark:"
	}
	head := fmt.Sprintf("%s *%s: %s*", icon, eventWord[m.Event], slackEscape(m.Title))
	if m.URL != "" {
		head = fmt.Sprintf("%s *%s: <%s|%s>*", icon, eventWord[m.Event], m.URL, slackEscape(m.Title))
	}
	ctx := fmt.Sprintf("*%s* · %s · `%s` · opened %s · %s", slackEscape(m.Instance), strings.ToUpper(m.Severity), slackEscape(m.RuleID),
		m.OpenedAt.Format("2006-01-02 15:04 MST"), m.Duration)
	if m.Environment != "" {
		ctx = fmt.Sprintf("*%s* (%s) · %s · `%s` · opened %s · %s", slackEscape(m.Instance), slackEscape(m.Environment), strings.ToUpper(m.Severity),
			slackEscape(m.RuleID), m.OpenedAt.Format("2006-01-02 15:04 MST"), m.Duration)
	}
	blocks := []map[string]any{
		{"type": "section", "text": map[string]string{"type": "mrkdwn", "text": head}},
		{"type": "context", "elements": []map[string]string{{"type": "mrkdwn", "text": ctx}}},
	}
	if len(m.Observed) > 0 {
		lines := make([]string, len(m.Observed))
		for i, o := range m.Observed {
			lines[i] = "• " + slackEscape(o)
		}
		blocks = append(blocks, map[string]any{"type": "section", "text": map[string]string{"type": "mrkdwn", "text": strings.Join(lines, "\n")}})
	}
	raw, _ := json.Marshal(map[string]any{"text": slackEscape(m.Subject()), "blocks": blocks})
	return raw
}
