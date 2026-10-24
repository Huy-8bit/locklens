//go:build integration

package storage_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/finding"
	"github.com/locklens/locklens/internal/observe"
	"github.com/locklens/locklens/internal/storage"
)

// freshStore creates an empty database on the disposable test server (via
// LOCKLENS_IT_ADMIN_DSN) and opens it as a monitoring store.
func freshStore(t *testing.T) (*storage.Store, string) {
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
	name := fmt.Sprintf("store_it_%d", time.Now().UnixNano())
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), admin)
		if err == nil {
			_, _ = c.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
			c.Close(context.Background())
		}
	})
	u, _ := url.Parse(admin)
	u.Path = "/" + name
	s, err := storage.Open(ctx, u.String(), storage.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s, u.String()
}

func TestMigrationsConcurrentAndIdempotent(t *testing.T) {
	s, dsn := freshStore(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			other, err := storage.Open(ctx, dsn, storage.Options{})
			if err != nil {
				errs <- err
				return
			}
			defer other.Close()
			_, err = other.Migrate(ctx)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent migrate: %v", err)
		}
	}
	applied, err := s.Migrate(ctx)
	if err != nil || len(applied) != 0 {
		t.Fatalf("second migrate applied %v (%v)", applied, err)
	}
	v, err := s.SchemaVersion(ctx)
	if err != nil || v < 1 {
		t.Fatalf("schema version %d %v", v, err)
	}
}

func migrated(t *testing.T) *storage.Store {
	t.Helper()
	s, _ := freshStore(t)
	if _, err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

func target(t *testing.T, s *storage.Store, name string) storage.Target {
	t.Helper()
	tg, err := s.CreateTarget(context.Background(), storage.Target{Name: name, DSNCiphertext: []byte{1, 2, 3}, DisplayHost: "h", DisplayPort: 5432,
		DisplayDatabase: "d", DisplayUser: "u", Enabled: true, IntervalSeconds: 10, MaxConnections: 2})
	if err != nil {
		t.Fatal(err)
	}
	return tg
}

func TestSessionsAndAdmin(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	id, err := s.UpsertAdmin(ctx, "admin", "$2a$12$hash")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertAdmin(ctx, "other", "$2a$12$x"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AdminByUsername(ctx, "admin"); err == nil {
		t.Fatal("only one admin may exist: the previous one must be removed")
	}
	id, _ = s.UpsertAdmin(ctx, "admin", "$2a$12$hash")
	h := []byte("0123456789abcdef0123456789abcdef")
	if err := s.CreateSession(ctx, h, id, "csrf", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	sess, err := s.SessionByHash(ctx, h, time.Hour)
	if err != nil || sess.Username != "admin" || sess.CSRFToken != "csrf" {
		t.Fatalf("session %+v %v", sess, err)
	}
	if err := s.CreateSession(ctx, []byte("expired-token-hash-000000000000"), id, "c", time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionByHash(ctx, []byte("expired-token-hash-000000000000"), time.Hour); err != storage.ErrNotFound {
		t.Fatalf("expired session must not authenticate: %v", err)
	}
}

func TestTargetsSoftDeleteAndPurge(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	a := target(t, s, "alpha")
	if _, err := s.CreateTarget(ctx, storage.Target{Name: "ALPHA", DSNCiphertext: []byte{1}, DisplayHost: "h", DisplayPort: 1, DisplayDatabase: "d", DisplayUser: "u", IntervalSeconds: 10, MaxConnections: 1}); err != storage.ErrNameTaken {
		t.Fatalf("case-insensitive duplicate names must be rejected: %v", err)
	}
	now := time.Now().UTC()
	if err := s.InsertPoints(ctx, a.ID, now, []observe.Point{{Metric: "m", Value: observe.F(1)}, {Metric: "unknown"}}); err != nil {
		t.Fatal(err)
	}
	inc, err := s.CreateIncident(ctx, storage.Incident{TargetID: &a.ID, TargetName: a.Name, RuleID: "r", RuleVersion: 1, Severity: "warning", Title: "t", State: "open", OpenedAt: now, LastSeenAt: now, Source: "recorder"},
		[]storage.IncidentEvent{{At: now, Kind: "trigger", Summary: "s", Payload: json.RawMessage(`{"x":1}`)}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SoftDeleteTarget(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetTarget(ctx, a.ID); err != storage.ErrNotFound {
		t.Fatal("soft-deleted target must be hidden")
	}
	series, err := s.QuerySeries(ctx, a.ID, []string{"m", "unknown"}, now.Add(-time.Minute), now.Add(time.Minute), 10*time.Second)
	if err != nil || len(series[0].Points) != 1 || series[1].Points[0].V != nil {
		t.Fatalf("history must survive a soft delete and unknown must stay NULL: %+v %v", series, err)
	}
	b := target(t, s, "alpha") // the name is free again
	_ = b
	if err := s.PurgeTarget(ctx, a.ID, true); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetIncident(ctx, inc); err != nil || got.TargetID != nil || got.TargetName != "alpha" {
		t.Fatalf("incident kept with name after purge: %+v %v", got, err)
	}
	series, _ = s.QuerySeries(ctx, a.ID, []string{"m"}, now.Add(-time.Minute), now.Add(time.Minute), 10*time.Second)
	if len(series[0].Points) != 0 {
		t.Fatal("purge must delete history")
	}
}

func TestQueryAggregationAndCoverage(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	tg := target(t, s, "q")
	cache := storage.IdentityCache{}
	key := observe.QueryKey{ServerMajor: 17, DBID: 5, UserID: 10, TopLevel: true, QueryID: -9193564501488909311}
	base := time.Now().UTC().Truncate(time.Second).Add(-30 * time.Minute)
	iv := 60.0
	text := "SELECT secret_literal"
	for i := 0; i < 20; i++ {
		ms := 1.0
		if i >= 10 {
			ms = 5.0 // slower per call in the second half
		}
		c := observe.StatementsCollection{At: base.Add(time.Duration(i) * time.Minute), Interval: &iv, Entries: 1,
			Deltas: []observe.QueryDelta{{Key: key, DatName: "app", UserName: "u", Text: &text, Calls: 100, TotalMS: 100 * ms, Rows: 100}}}
		if err := s.InsertStatements(ctx, tg.ID, c, false, cache); err != nil {
			t.Fatal(err)
		}
	}
	from, to := base.Add(9*time.Minute+30*time.Second), base.Add(19*time.Minute+30*time.Second)
	rows, total, err := s.ListQueries(ctx, storage.QueryListParams{TargetID: tg.ID, From: from, To: to, Compare: true, IncludeText: true, Sort: "mean_ratio", Desc: true, Limit: 10})
	if err != nil || total != 1 {
		t.Fatalf("rows %+v total %d err %v", rows, total, err)
	}
	r := rows[0]
	if r.QueryID != key.QueryID || r.Calls != 1000 || r.TotalMS != 5000 || *r.PrevCalls != 1000 || *r.PrevTotalMS != 1000 {
		t.Errorf("window sums wrong: %+v", r)
	}
	if r.Text != nil {
		t.Error("text must not be stored when the target did not opt in")
	}
	cov, err := s.StatementCoverage(ctx, tg.ID, from, to)
	if err != nil || cov.CoveredSeconds != 600 || cov.Collections != 10 {
		t.Errorf("coverage %+v %v", cov, err)
	}
	// Opting in stores text; opting out clears it.
	c := observe.StatementsCollection{At: base.Add(25 * time.Minute), Interval: &iv, Entries: 1,
		Deltas: []observe.QueryDelta{{Key: key, DatName: "app", UserName: "u", Text: &text, Calls: 1, TotalMS: 1}}}
	if err := s.InsertStatements(ctx, tg.ID, c, true, cache); err != nil {
		t.Fatal(err)
	}
	q, _, _, _ := s.QueryIdentity(ctx, tg.ID, r.IdentityID, true)
	if q.Text == nil || *q.Text != text {
		t.Fatal("opt-in text not stored")
	}
	if err := s.ClearQueryTexts(ctx, tg.ID); err != nil {
		t.Fatal(err)
	}
	q, _, _, _ = s.QueryIdentity(ctx, tg.ID, r.IdentityID, true)
	if q.Text != nil {
		t.Fatal("texts must be cleared")
	}
	// Exclusion of the monitoring user.
	rows, total, _ = s.ListQueries(ctx, storage.QueryListParams{TargetID: tg.ID, From: from, To: to, Sort: "total_time", Limit: 10, ExcludeUser: "u"})
	if total != 0 || len(rows) != 0 {
		t.Error("ExcludeUser must hide the user's statements")
	}
}

func TestRetentionInBatches(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	tg := target(t, s, "r")
	old := time.Now().UTC().Add(-10 * 24 * time.Hour)
	var pts []observe.Point
	for i := 0; i < 12000; i++ {
		pts = append(pts, observe.Point{Metric: "m", Label: fmt.Sprint(i), Value: observe.F(1)})
	}
	if err := s.InsertPoints(ctx, tg.ID, old, pts); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertPoints(ctx, tg.ID, time.Now().UTC(), pts[:5]); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	res, err := s.CreateIncident(ctx, storage.Incident{TargetID: &tg.ID, TargetName: "r", RuleID: "x", RuleVersion: 1, Severity: "info", Title: "old",
		State: "resolved", OpenedAt: old, LastSeenAt: old, ResolvedAt: &old, Source: "recorder"}, []storage.IncidentEvent{{At: old, Kind: "trigger", Summary: "s"}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.ApplyRetention(ctx, storage.RetentionPolicy{History: 7 * 24 * time.Hour, Incidents: 90 * 24 * time.Hour, Findings: 30 * 24 * time.Hour}, now)
	if err != nil {
		t.Fatal(err)
	}
	if out["metric_points"] != 12000 {
		t.Errorf("deleted %d points, want 12000 (in batches)", out["metric_points"])
	}
	if _, err := s.GetIncident(ctx, res); err != nil {
		t.Error("a 10-day-old incident is within the 90-day incident retention")
	}
	series, _ := s.QuerySeries(ctx, tg.ID, []string{"m"}, now.Add(-time.Hour), now.Add(time.Minute), time.Minute)
	if len(series) != 5 {
		t.Errorf("recent points must survive, got %d series", len(series))
	}
}

func TestFindingsSync(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	tg := target(t, s, "f")
	now := time.Now().UTC()
	f := finding.Finding{RuleID: "r", RuleVersion: 1, Key: "k", Severity: finding.Warning, Title: "t", Status: finding.Active, ObservedFrom: now,
		Observed: []string{"o"}, Evidence: []finding.Evidence{{Label: "l", Value: 1.5, Source: "s"}}, Explanation: "e", Limitations: "l"}
	if _, err := s.SyncFindings(ctx, tg.ID, now, []finding.Finding{f}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SyncFindings(ctx, tg.ID, now.Add(time.Second), []finding.Finding{f}); err != nil {
		t.Fatal(err)
	}
	cur, _ := s.ListFindings(ctx, storage.FindingFilter{TargetID: tg.ID, Status: "current"})
	if len(cur) != 1 || cur[0].Finding.Evidence[0].Value != 1.5 {
		t.Fatalf("upsert must keep one finding: %+v", cur)
	}
	if _, err := s.SyncFindings(ctx, tg.ID, now.Add(2*time.Second), nil); err != nil {
		t.Fatal(err)
	}
	res, _ := s.ListFindings(ctx, storage.FindingFilter{TargetID: tg.ID, Status: "resolved"})
	if len(res) != 1 || res[0].ResolvedAt == nil {
		t.Fatalf("missing findings must be resolved: %+v", res)
	}
}
