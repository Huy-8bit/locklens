//go:build integration

package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/lib/obs"
	"github.com/locklens/locklens/internal/lib/secret"
	"github.com/locklens/locklens/internal/repository"
)

func store(t *testing.T) *repository.Store {
	t.Helper()
	admin := os.Getenv("LOCKLENS_IT_ADMIN_DSN")
	if admin == "" {
		t.Fatal("LOCKLENS_IT_ADMIN_DSN is required; run scripts/integration-test.sh")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	name := fmt.Sprintf("notify_it_%d", time.Now().UnixNano())
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if c, err := pgx.Connect(context.Background(), admin); err == nil {
			_, _ = c.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
			c.Close(context.Background())
		}
	})
	u, _ := url.Parse(admin)
	u.Path = "/" + name
	s, err := repository.Open(ctx, u.String(), repository.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if _, err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestNotificationFlow(t *testing.T) {
	ctx := context.Background()
	st := store(t)
	box, _ := secret.New([]byte(strings.Repeat("k", 32)))
	svc := newService(st, box)
	svc.send.(*netSender).backoff = nil

	var mu sync.Mutex
	var got []map[string]any
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		got = append(got, body)
		mu.Unlock()
	}))
	defer hook.Close()
	events := func() []string {
		mu.Lock()
		defer mu.Unlock()
		var out []string
		for _, b := range got {
			out = append(out, fmt.Sprint(b["event"]))
		}
		return out
	}

	ch, err := svc.SaveChannel(ctx, 0, ChannelInput{Name: "ops", Kind: KindWebhook, Enabled: true, MinSeverity: "warning", OnOpen: true, OnEscalate: true,
		OnResolve: true, Destination: &Destination{URL: hook.URL + "/hook?token=abc", Secret: "s"}})
	if err != nil {
		t.Fatal(err)
	}
	if ch.Display != hook.URL || len(ch.ConfigCiphertext) == 0 {
		t.Fatalf("channel = %+v", ch)
	}
	raw, _ := json.Marshal(ch)
	if strings.Contains(string(raw), "token=abc") || strings.Contains(string(raw), "config_ciphertext") {
		t.Fatalf("the destination must not be serialised: %s", raw)
	}
	// Updating without a destination keeps it.
	if ch, err = svc.SaveChannel(ctx, ch.ID, ChannelInput{Name: "ops", Enabled: true, MinSeverity: "warning", OnOpen: true, OnEscalate: true, OnResolve: true}); err != nil || ch.Display != hook.URL {
		t.Fatalf("update: %+v %v", ch, err)
	}

	tg, err := st.CreateTarget(ctx, repository.Target{Name: "orders", DSNCiphertext: []byte{1}, DisplayHost: "h", DisplayPort: 5432, DisplayDatabase: "d",
		DisplayUser: "u", Enabled: true, IntervalSeconds: 10, MaxConnections: 2, Environment: "production"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	id, err := st.CreateIncident(ctx, repository.Incident{TargetID: &tg.ID, TargetName: tg.Name, RuleID: "blocking.long_chain", RuleVersion: 1, Severity: "warning",
		Title: "Blocking chain", State: "open", OpenedAt: now, LastSeenAt: now, TriggerConfig: json.RawMessage(`{}`), Source: "recorder"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	svc.process(ctx, id)
	svc.process(ctx, id) // nothing new: sent once
	if e := events(); len(e) != 1 || e[0] != EventOpened {
		t.Fatalf("events = %v", e)
	}

	// Escalation while a silence covers the rule: recorded once, not sent.
	if err := st.UpdateIncidentState(ctx, id, "open", nil, nil, "critical", "Blocking chain", false); err != nil {
		t.Fatal(err)
	}
	rule := "blocking.long_chain"
	sil, err := svc.CreateSilence(ctx, SilenceInput{TargetID: &tg.ID, RuleID: &rule, Minutes: 60, Reason: "maintenance"}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	svc.process(ctx, id)
	svc.process(ctx, id)
	ds, _ := st.ListDeliveries(ctx, &id, 10)
	silenced := 0
	for _, d := range ds {
		if d.Status == "silenced" {
			silenced++
		}
	}
	if len(events()) != 1 || silenced != 1 {
		t.Fatalf("events %v, silenced deliveries %d", events(), silenced)
	}

	// Resolution after the silence ends is sent.
	if err := svc.EndSilence(ctx, sil.ID); err != nil {
		t.Fatal(err)
	}
	res, end := "condition cleared", time.Now().UTC()
	if err := st.UpdateIncidentState(ctx, id, "resolved", &end, &res, "critical", "Blocking chain", false); err != nil {
		t.Fatal(err)
	}
	svc.process(ctx, id)
	if e := events(); len(e) != 2 || e[1] != EventResolved {
		t.Fatalf("events = %v", e)
	}
	mu.Lock()
	inc := got[1]["incident"].(map[string]any)
	mu.Unlock()
	if inc["url"] != fmt.Sprintf("https://ll.example.com/incidents/%d", id) || inc["environment"] != "production" {
		t.Fatalf("incident payload = %v", inc)
	}

	// A test message is recorded without an incident.
	d, err := svc.Test(ctx, ch.ID)
	if err != nil || d.Status != "sent" || d.IncidentID != nil {
		t.Fatalf("test delivery = %+v, %v", d, err)
	}
	all, _ := svc.Deliveries(ctx, nil, 10)
	if len(all) != 4 || all[0].ChannelName != "ops" {
		t.Fatalf("deliveries = %+v", all)
	}

	// Names are unique.
	if _, err := svc.SaveChannel(ctx, 0, ChannelInput{Name: "ops", Kind: KindSlack, Enabled: true, OnOpen: true, Destination: &Destination{URL: "https://hooks.slack.com/x"}}); err == nil {
		t.Fatal("duplicate name accepted")
	}
}

func newService(st *repository.Store, box *secret.Box) *service {
	svc := New(Repositories{NotificationRepository: st, IncidentRepository: st, TargetRepository: st, FindingRepository: st,
		ChangeRepository: st, MaintenanceRepository: st, OutboxRepository: st}, box, obs.NewMetrics(),
		slog.New(slog.NewTextHandler(io.Discard, nil)), "https://ll.example.com", "test").(*service)
	svc.send.(*netSender).backoff = nil
	return svc
}

// Two senders (a notification job handed over between servers, briefly
// overlapping) drain one outbox with several changes per incident: every
// incident is notified once.
func TestOutboxIsSentOnce(t *testing.T) {
	ctx := context.Background()
	st := store(t)
	box, _ := secret.New([]byte(strings.Repeat("k", 32)))
	a, b := newService(st, box), newService(st, box)

	var mu sync.Mutex
	sent := map[string]int{}
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		inc, _ := body["incident"].(map[string]any)
		mu.Lock()
		sent[fmt.Sprint(body["event"], "/", inc["incident_id"])]++
		mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}))
	defer hook.Close()
	if _, err := a.SaveChannel(ctx, 0, ChannelInput{Name: "ops", Kind: KindWebhook, Enabled: true, MinSeverity: "warning", OnOpen: true,
		OnResolve: true, Destination: &Destination{URL: hook.URL}}); err != nil {
		t.Fatal(err)
	}
	tg, err := st.CreateTarget(ctx, repository.Target{Name: "orders", DSNCiphertext: []byte{1}, DisplayHost: "h", DisplayPort: 5432, DisplayDatabase: "d",
		DisplayUser: "u", Enabled: true, IntervalSeconds: 10, MaxConnections: 2})
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for i := range 20 {
		now := time.Now().UTC()
		id, err := st.CreateIncident(ctx, repository.Incident{TargetID: &tg.ID, TargetName: tg.Name, RuleID: "blocking.long_chain", RuleVersion: 1,
			Severity: "warning", Title: fmt.Sprintf("chain %d", i), State: "open", OpenedAt: now, LastSeenAt: now, TriggerConfig: json.RawMessage(`{}`), Source: "recorder"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
		for _, kind := range []string{"incident.opened", "incident.updated", "incident.updated"} {
			if err := st.EnqueueNotification(ctx, id, kind); err != nil {
				t.Fatal(err)
			}
		}
	}
	var wg sync.WaitGroup
	wg.Go(func() { a.drainOutbox(ctx, "a") })
	wg.Go(func() { b.drainOutbox(ctx, "b") })
	wg.Wait()
	for _, id := range ids {
		if n := sent[fmt.Sprintf("%s/%d", EventOpened, id)]; n != 1 {
			t.Fatalf("incident %d opened sent %d times (%v)", id, n, sent)
		}
	}
	if n, _, _ := st.PendingNotifications(ctx); n != 0 {
		t.Fatalf("%d outbox rows left", n)
	}
	// Resolutions arrive later through the outbox: sent once too.
	end, res := time.Now().UTC(), "condition cleared"
	for _, id := range ids[:5] {
		if err := st.UpdateIncidentState(ctx, id, "resolved", &end, &res, "warning", "x", false); err != nil {
			t.Fatal(err)
		}
		_ = st.EnqueueNotification(ctx, id, "incident.resolved")
		_ = st.EnqueueNotification(ctx, id, "incident.resolved")
	}
	wg.Go(func() { a.drainOutbox(ctx, "a") })
	wg.Go(func() { b.drainOutbox(ctx, "b") })
	wg.Wait()
	for _, id := range ids[:5] {
		if n := sent[fmt.Sprintf("%s/%d", EventResolved, id)]; n != 1 {
			t.Fatalf("incident %d resolved sent %d times", id, n)
		}
	}
}
