package notify

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/locklens/locklens/internal/repository"
)

func sev(s string) *string { return &s }

var t0 = time.Date(2026, 10, 25, 13, 0, 0, 0, time.UTC)

func channel() repository.NotificationChannel {
	return repository.NotificationChannel{ID: 1, Enabled: true, MinSeverity: "warning", OnOpen: true, OnEscalate: true, OnResolve: true, Environments: []string{}}
}

func TestDecide(t *testing.T) {
	open := repository.Incident{ID: 7, State: "open", Severity: "warning", OpenedAt: t0}
	sent := func(event, s string, at time.Time) repository.Delivery {
		return repository.Delivery{Event: event, Severity: sev(s), Status: "sent", At: at}
	}
	cases := []struct {
		name string
		in   repository.Incident
		env  string
		ch   func(*repository.NotificationChannel)
		ds   []repository.Delivery
		now  time.Time
		want string
	}{
		{"opens once", open, "", nil, nil, t0, EventOpened},
		{"already opened", open, "", nil, []repository.Delivery{sent(EventOpened, "warning", t0)}, t0.Add(time.Minute), ""},
		{"below the minimum severity", repository.Incident{State: "open", Severity: "info"}, "", nil, nil, t0, ""},
		{"escalation", repository.Incident{State: "open", Severity: "critical"}, "", nil, []repository.Delivery{sent(EventOpened, "warning", t0)}, t0, EventEscalated},
		{"escalation sent once", repository.Incident{State: "open", Severity: "critical"}, "", nil,
			[]repository.Delivery{sent(EventOpened, "warning", t0), sent(EventEscalated, "critical", t0.Add(time.Minute))}, t0.Add(2 * time.Minute), ""},
		{"opens when it reaches the minimum", repository.Incident{State: "open", Severity: "critical"}, "", func(c *repository.NotificationChannel) { c.MinSeverity = "critical" }, nil, t0, EventOpened},
		{"resolved after opened", repository.Incident{State: "resolved", Severity: "warning"}, "", nil, []repository.Delivery{sent(EventOpened, "warning", t0)}, t0, EventResolved},
		{"no resolved without opened", repository.Incident{State: "resolved", Severity: "warning"}, "", nil, nil, t0, ""},
		{"resolved once", repository.Incident{State: "resolved", Severity: "warning"}, "", nil,
			[]repository.Delivery{sent(EventOpened, "warning", t0), sent(EventResolved, "warning", t0.Add(time.Minute))}, t0.Add(2 * time.Minute), ""},
		{"reopened after resolved", open, "", nil,
			[]repository.Delivery{sent(EventOpened, "warning", t0), sent(EventResolved, "warning", t0.Add(time.Minute))}, t0.Add(2 * time.Minute), EventOpened},
		{"reminder when due", open, "", func(c *repository.NotificationChannel) { c.ReminderSeconds = 3600 },
			[]repository.Delivery{sent(EventOpened, "warning", t0)}, t0.Add(61 * time.Minute), EventReminder},
		{"reminder not yet due", open, "", func(c *repository.NotificationChannel) { c.ReminderSeconds = 3600 },
			[]repository.Delivery{sent(EventOpened, "warning", t0)}, t0.Add(59 * time.Minute), ""},
		{"no reminder once acknowledged", repository.Incident{State: "open", Severity: "warning", AcknowledgedAt: &t0}, "", func(c *repository.NotificationChannel) { c.ReminderSeconds = 3600 },
			[]repository.Delivery{sent(EventOpened, "warning", t0)}, t0.Add(2 * time.Hour), ""},
		{"other environment", open, "staging", func(c *repository.NotificationChannel) { c.Environments = []string{"production"} }, nil, t0, ""},
		{"matching environment", open, "production", func(c *repository.NotificationChannel) { c.Environments = []string{"production"} }, nil, t0, EventOpened},
		{"disabled channel", open, "", func(c *repository.NotificationChannel) { c.Enabled = false }, nil, t0, ""},
		{"opened event off", open, "", func(c *repository.NotificationChannel) { c.OnOpen = false }, nil, t0, ""},
		{"a failed open is tried again", open, "", nil, []repository.Delivery{{Event: EventOpened, Severity: sev("warning"), Status: "failed", At: t0}}, t0, EventOpened},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ch := channel()
			if c.ch != nil {
				c.ch(&ch)
			}
			if got := decide(c.in, c.env, ch, historyOf(c.ds), c.now); got != c.want {
				t.Fatalf("decide = %q, want %q", got, c.want)
			}
		})
	}
}

func TestSkipSilencedOnceAndBackOffFailures(t *testing.T) {
	h := historyOf([]repository.Delivery{
		{Event: EventOpened, Severity: sev("warning"), Status: "silenced", At: t0},
		{Event: EventEscalated, Severity: sev("critical"), Status: "failed", At: t0},
	})
	if !h.skip(EventOpened, "warning", t0.Add(time.Hour)) {
		t.Fatal("a silenced event is recorded once, not again on every update")
	}
	if !h.skip(EventEscalated, "critical", t0.Add(time.Minute)) || h.skip(EventEscalated, "critical", t0.Add(retryFailedAfter)) {
		t.Fatal("a failed event waits retryFailedAfter before the next try")
	}
}

func TestMessageIsSafeText(t *testing.T) {
	res := "condition cleared"
	end := t0.Add(90 * time.Minute)
	in := repository.Incident{ID: 3, Title: "Blocked\r\nBcc: evil@example.com <b>", Severity: "critical", State: "resolved", TargetName: "orders-db",
		RuleID: "blocking.long_chain", OpenedAt: t0, ResolvedAt: &end, Resolution: &res}
	m := buildMessage(EventResolved, in, "production", []string{"one", "two", "three", "four"}, "https://ll.example.com", t0)
	if strings.ContainsAny(m.Title, "\r\n") || strings.ContainsAny(m.Subject(), "\r\n") {
		t.Fatalf("title must stay on one line: %q", m.Title)
	}
	if len(m.Observed) != 3 || m.URL != "https://ll.example.com/incidents/3" || m.Duration != "1h30m" {
		t.Fatalf("message = %+v", m)
	}
	var slack struct {
		Text   string `json:"text"`
		Blocks []struct {
			Text struct {
				Text string `json:"text"`
			} `json:"text"`
		} `json:"blocks"`
	}
	if err := json.Unmarshal(m.slackBody(), &slack); err != nil || !strings.Contains(slack.Text, "&lt;b&gt;") || !strings.Contains(slack.Blocks[0].Text.Text, "&lt;b&gt;") {
		t.Fatalf("slack body must escape markup: %s", m.slackBody())
	}
	if !strings.Contains(m.Text(), "Resolved:") || !strings.Contains(m.Text(), "not a confirmed cause") {
		t.Fatalf("text body:\n%s", m.Text())
	}
}

func testSender() *netSender {
	s := newSender("test")
	s.backoff = []time.Duration{time.Millisecond, time.Millisecond}
	return s
}

func TestWebhookSignsRetriesAndHidesTheURL(t *testing.T) {
	var calls atomic.Int32
	var gotSig, gotEvent string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		gotSig, gotEvent = r.Header.Get("X-LockLens-Signature"), r.Header.Get("X-LockLens-Event")
		gotBody, _ = io.ReadAll(r.Body)
	}))
	defer srv.Close()
	m := Message{Event: EventOpened, Title: "t", Severity: "warning", OpenedAt: t0}
	attempts, err := testSender().send(context.Background(), KindWebhook, Destination{URL: srv.URL + "/hook/secret-token", Secret: "k"}, m)
	if err != nil || attempts != 2 {
		t.Fatalf("attempts %d err %v", attempts, err)
	}
	mac := hmac.New(sha256.New, []byte("k"))
	mac.Write(gotBody)
	if gotSig != "sha256="+hex.EncodeToString(mac.Sum(nil)) || gotEvent != EventOpened {
		t.Fatalf("signature %q event %q", gotSig, gotEvent)
	}

	// A client error is not retried, and errors never contain the URL.
	gone := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) }))
	defer gone.Close()
	attempts, err = testSender().send(context.Background(), KindSlack, Destination{URL: gone.URL + "/services/T000/B000/XXXX"}, m)
	if err == nil || attempts != 1 || strings.Contains(err.Error(), "XXXX") {
		t.Fatalf("attempts %d err %v", attempts, err)
	}
	closed := gone.URL + "/services/T000/B000/YYYY"
	gone.Close()
	_, err = testSender().send(context.Background(), KindSlack, Destination{URL: closed}, m)
	if err == nil || strings.Contains(err.Error(), "YYYY") {
		t.Fatalf("connection errors must not contain the URL: %v", err)
	}
}

func TestBlockedAddresses(t *testing.T) {
	attempts, err := testSender().send(context.Background(), KindWebhook, Destination{URL: "http://169.254.169.254/latest/meta-data"}, Message{})
	if err == nil || !strings.Contains(err.Error(), "not allowed") || attempts != 1 {
		t.Fatalf("attempts %d err %v", attempts, err)
	}
}

// fakeSMTP accepts one message without TLS or authentication.
func fakeSMTP(t *testing.T) (port int, got chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	got = make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		r, w := bufio.NewReader(c), bufio.NewWriter(c)
		say := func(s string) { _, _ = w.WriteString(s + "\r\n"); _ = w.Flush() }
		say("220 fake ESMTP")
		var data strings.Builder
		inData := false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			if inData {
				if line == ".\r\n" {
					inData = false
					got <- data.String()
					say("250 queued")
					continue
				}
				data.WriteString(line)
				continue
			}
			switch cmd := strings.ToUpper(strings.TrimSpace(line)); {
			case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
				say("250 fake")
			case strings.HasPrefix(cmd, "DATA"):
				inData = true
				say("354 go on")
			case strings.HasPrefix(cmd, "QUIT"):
				say("221 bye")
				return
			default:
				say("250 ok")
			}
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, got
}

func TestEmail(t *testing.T) {
	port, got := fakeSMTP(t)
	m := Message{Event: EventOpened, Title: "Connections are running out", Severity: "critical", Instance: "orders-db", RuleID: "connections.pressure", OpenedAt: t0, Duration: "5m 0s",
		Observed: []string{"Connection usage at 96%"}}
	d := Destination{SMTPHost: "127.0.0.1", SMTPPort: port, TLS: TLSNone, From: "locklens@example.com", To: []string{"ops@example.com"}}
	if _, err := testSender().send(context.Background(), KindEmail, d, m); err != nil {
		t.Fatal(err)
	}
	msg := <-got
	for _, want := range []string{"Subject: [CRITICAL] Opened: Connections are running out (orders-db)", "To: ops@example.com", "Connection usage at 96%", "Auto-Submitted: auto-generated"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message lacks %q:\n%s", want, msg)
		}
	}
}

func TestNormalizeDestination(t *testing.T) {
	bad := []struct {
		kind  string
		d     Destination
		field string
	}{
		{KindSlack, Destination{URL: "http://hooks.slack.com/services/x"}, "destination.url"},
		{KindWebhook, Destination{URL: "ftp://example.com"}, "destination.url"},
		{KindWebhook, Destination{URL: "https://user:pw@example.com/x"}, "destination.url"},
		{KindEmail, Destination{SMTPHost: "mail", TLS: TLSNone, Username: "u", From: "a@b.c", To: []string{"x@y.z"}}, "destination.tls"},
		{KindEmail, Destination{SMTPHost: "mail", From: "nobody", To: []string{"x@y.z"}}, "destination.from"},
		{KindEmail, Destination{SMTPHost: "mail", From: "a@b.c"}, "destination.to"},
	}
	for _, c := range bad {
		if _, f := normalizeDestination(c.kind, c.d); f[c.field] == "" {
			t.Errorf("%s %+v: expected a problem with %s, got %v", c.kind, c.d, c.field, f)
		}
	}
	d, f := normalizeDestination(KindEmail, Destination{SMTPHost: "mail.example.com", From: "LockLens <ll@example.com>", To: []string{"ops@example.com", " "}})
	if len(f) != 0 || d.SMTPPort != 587 || d.TLS != TLSStart || d.From != "ll@example.com" || len(d.To) != 1 {
		t.Fatalf("defaults: %+v %v", d, f)
	}
	if got := describe(KindSlack, Destination{URL: "https://hooks.slack.com/services/T/B/secret"}); got != "https://hooks.slack.com" {
		t.Fatalf("describe must not reveal the path: %q", got)
	}
	if got := describe(KindEmail, d); got != "ops@example.com via mail.example.com:"+strconv.Itoa(587) {
		t.Fatalf("describe email = %q", got)
	}
}
