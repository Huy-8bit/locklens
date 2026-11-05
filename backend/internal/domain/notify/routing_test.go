package notify

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/repository"
)

func TestRoutingAndEscalationTiers(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	seven := int64(7)
	in := repository.Incident{ID: 1, TargetID: &seven, RuleID: "target.unreachable", Severity: "critical", State: "open", OpenedAt: now.Add(-10 * time.Minute)}
	ch := repository.NotificationChannel{Enabled: true, MinSeverity: "warning", OnOpen: true}
	h := historyOf(nil)
	if decide(in, "prod", ch, h, now) != EventOpened {
		t.Fatal("a channel without filters receives every incident")
	}
	ch.RuleIDs = []string{"blocking.long_chain"}
	if decide(in, "prod", ch, h, now) != "" {
		t.Fatal("another rule")
	}
	ch.RuleIDs, ch.TargetIDs = nil, []int64{8}
	if decide(in, "prod", ch, h, now) != "" {
		t.Fatal("another instance")
	}
	// An escalation tier waits until the incident stayed open and
	// unacknowledged for its delay.
	ch.TargetIDs, ch.DelaySeconds = nil, 15*60
	if decide(in, "prod", ch, h, now) != "" {
		t.Fatal("open for 10 minutes: the 15-minute tier waits")
	}
	if decide(in, "prod", ch, h, now.Add(6*time.Minute)) != EventOpened {
		t.Fatal("open for 16 minutes: the tier is told")
	}
	ack := now
	in.AcknowledgedAt = &ack
	if decide(in, "prod", ch, h, now.Add(6*time.Minute)) != "" {
		t.Fatal("an acknowledged incident does not escalate")
	}
}

func TestMaintenanceWindows(t *testing.T) {
	seven := int64(7)
	in := repository.Incident{TargetID: &seven, RuleID: "blocking.long_chain"}
	sun := time.Date(2026, 11, 8, 1, 30, 0, 0, time.UTC) // a Sunday
	weekly := repository.MaintenanceWindow{Enabled: true, Kind: repository.MaintenanceWeekly, Weekdays: []int{6}, StartTime: "23:00",
		DurationMinutes: 240, Timezone: "UTC", Environments: []string{"staging"}}
	// Saturday 23:00 for 4 hours still covers Sunday 01:30.
	if !covers(weekly, in, "staging") || !activeAt(weekly, sun, nil) {
		t.Fatal("weekly window running past midnight")
	}
	if activeAt(weekly, sun.Add(3*time.Hour), nil) || covers(weekly, in, "prod") {
		t.Fatal("after the window, or another environment")
	}
	after := repository.MaintenanceWindow{Enabled: true, Kind: repository.MaintenanceAfterDeploy, DurationMinutes: 30}
	deploys := []observe.ChangeEvent{{At: sun.Add(-20 * time.Minute), Kind: "deploy"}}
	if !activeAt(after, sun, deploys) || activeAt(after, sun.Add(15*time.Minute), deploys) {
		t.Fatal("30 minutes after a deploy")
	}
	in2 := WindowInput{Name: "x", Kind: repository.MaintenanceWeekly, Weekdays: []int{9}, StartTime: "25:00", DurationMinutes: 0, Timezone: "Mars/Base"}
	f := in2.validate()
	if f["weekdays"] == "" || f["start_time"] == "" || f["duration_minutes"] == "" || f["timezone"] == "" {
		t.Fatalf("validation %v", f)
	}
}

func TestAlertmanagerBody(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	m := Message{Event: EventOpened, IncidentID: 42, Title: "Instance cannot be reached", Severity: "critical", Instance: "orders-db",
		Environment: "prod", RuleID: "target.unreachable", OpenedAt: now.Add(-time.Minute), Observed: []string{"3 attempts failed"},
		URL: "https://ll.example.com/incidents/42"}
	var alerts []struct {
		Labels      map[string]string `json:"labels"`
		Annotations map[string]string `json:"annotations"`
		StartsAt    time.Time         `json:"startsAt"`
		EndsAt      time.Time         `json:"endsAt"`
	}
	if err := json.Unmarshal(m.alertmanagerBody(now), &alerts); err != nil || len(alerts) != 1 {
		t.Fatal(err)
	}
	a := alerts[0]
	if a.Labels["alertname"] != "target.unreachable" || a.Labels["instance"] != "orders-db" || a.Labels["environment"] != "prod" ||
		a.Labels["incident_id"] != "42" || a.Annotations["summary"] != m.Title || !a.EndsAt.Equal(now.Add(alertmanagerHold)) {
		t.Fatalf("firing %+v", a)
	}
	res := now
	m.ResolvedAt, m.Event = &res, EventResolved
	_ = json.Unmarshal(m.alertmanagerBody(now.Add(time.Hour)), &alerts)
	if !alerts[0].EndsAt.Equal(res) {
		t.Fatalf("resolved ends at its resolution: %v", alerts[0].EndsAt)
	}
}
