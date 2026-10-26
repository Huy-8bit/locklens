//go:build integration

package repository_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/domain/finding"
	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/repository"
)

// freshStore creates an empty database on the disposable test server (via
// LOCKLENS_IT_ADMIN_DSN) and opens it as a monitoring store.
func freshStore(t *testing.T) (*repository.Store, string) {
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
	s, err := repository.Open(ctx, u.String(), repository.Options{})
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
			other, err := repository.Open(ctx, dsn, repository.Options{})
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

func migrated(t *testing.T) *repository.Store {
	t.Helper()
	s, _ := freshStore(t)
	if _, err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

func target(t *testing.T, s *repository.Store, name string) repository.Target {
	t.Helper()
	tg, err := s.CreateTarget(context.Background(), repository.Target{Name: name, DSNCiphertext: []byte{1, 2, 3}, DisplayHost: "h", DisplayPort: 5432,
		DisplayDatabase: "d", DisplayUser: "u", Enabled: true, IntervalSeconds: 10, MaxConnections: 2})
	if err != nil {
		t.Fatal(err)
	}
	return tg
}

func TestSessionsAndAdmin(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	id, changed, err := s.UpsertManagedAdmin(ctx, "admin", "$2a$12$hash")
	if err != nil || changed {
		t.Fatalf("first admin: %v changed=%v", err, changed)
	}
	viewer, err := s.CreateUser(ctx, "Dana", "$2a$12$v", "viewer")
	if err != nil || viewer.Role != "viewer" || viewer.Managed {
		t.Fatalf("create user %+v %v", viewer, err)
	}
	if _, err := s.CreateUser(ctx, "dana", "$2a$12$v", "viewer"); err != repository.ErrConflict {
		t.Fatalf("case-insensitive duplicate user names must conflict: %v", err)
	}
	// A new environment administrator replaces the previous one, but other
	// users stay.
	if _, _, err := s.UpsertManagedAdmin(ctx, "other", "$2a$12$x"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UserByUsername(ctx, "admin"); err != repository.ErrNotFound {
		t.Fatalf("the previous environment administrator must be removed: %v", err)
	}
	if u, err := s.UserByUsername(ctx, "DANA"); err != nil || u.ID != viewer.ID {
		t.Fatalf("other users must stay: %+v %v", u, err)
	}
	id, _, _ = s.UpsertManagedAdmin(ctx, "admin", "$2a$12$hash")
	if _, changed, _ := s.UpsertManagedAdmin(ctx, "admin", "$2a$12$new"); !changed {
		t.Fatal("a new password must be reported as changed")
	}
	h := []byte("0123456789abcdef0123456789abcdef")
	if err := s.CreateSession(ctx, h, id, "csrf", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	sess, err := s.SessionByHash(ctx, h, time.Hour)
	if err != nil || sess.Username != "admin" || sess.CSRFToken != "csrf" || sess.Role != "admin" || !sess.Managed {
		t.Fatalf("session %+v %v", sess, err)
	}
	if err := s.CreateSession(ctx, []byte("expired-token-hash-000000000000"), id, "c", time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionByHash(ctx, []byte("expired-token-hash-000000000000"), time.Hour); err != repository.ErrNotFound {
		t.Fatalf("expired session must not authenticate: %v", err)
	}

	// Role changes apply to the next request; disabling ends the sessions.
	vh := []byte("viewer-token-hash-00000000000000")
	if err := s.CreateSession(ctx, vh, viewer.ID, "c", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	op := "operator"
	if _, err := s.UpdateUser(ctx, viewer.ID, repository.UserChanges{Role: &op}); err != nil {
		t.Fatal(err)
	}
	if vs, err := s.SessionByHash(ctx, vh, time.Hour); err != nil || vs.Role != "operator" {
		t.Fatalf("role change must apply to the session: %+v %v", vs, err)
	}
	yes := true
	if _, err := s.UpdateUser(ctx, viewer.ID, repository.UserChanges{Disabled: &yes}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionByHash(ctx, vh, time.Hour); err != repository.ErrNotFound {
		t.Fatalf("a disabled user's session must end: %v", err)
	}
	if err := s.DeleteUser(ctx, viewer.ID); err != nil {
		t.Fatal(err)
	}
	if users, err := s.ListUsers(ctx); err != nil || len(users) != 1 || !users[0].Managed {
		t.Fatalf("users after delete %+v %v", users, err)
	}
}

func TestTargetsSoftDeleteAndPurge(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	a := target(t, s, "alpha")
	if _, err := s.CreateTarget(ctx, repository.Target{Name: "ALPHA", DSNCiphertext: []byte{1}, DisplayHost: "h", DisplayPort: 1, DisplayDatabase: "d", DisplayUser: "u", IntervalSeconds: 10, MaxConnections: 1}); err != repository.ErrNameTaken {
		t.Fatalf("case-insensitive duplicate names must be rejected: %v", err)
	}
	now := time.Now().UTC()
	if err := s.InsertPoints(ctx, a.ID, now, []observe.Point{{Metric: "m", Value: observe.F(1)}, {Metric: "unknown"}}); err != nil {
		t.Fatal(err)
	}
	inc, err := s.CreateIncident(ctx, repository.Incident{TargetID: &a.ID, TargetName: a.Name, RuleID: "r", RuleVersion: 1, Severity: "warning", Title: "t", State: "open", OpenedAt: now, LastSeenAt: now, Source: "recorder"},
		[]repository.IncidentEvent{{At: now, Kind: "trigger", Summary: "s", Payload: json.RawMessage(`{"x":1}`)}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SoftDeleteTarget(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetTarget(ctx, a.ID); err != repository.ErrNotFound {
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
	cache := repository.IdentityCache{}
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
	rows, total, err := s.ListQueries(ctx, repository.QueryListParams{TargetID: tg.ID, From: from, To: to, Compare: true, IncludeText: true, Sort: "mean_ratio", Desc: true, Limit: 10})
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
	rows, total, _ = s.ListQueries(ctx, repository.QueryListParams{TargetID: tg.ID, From: from, To: to, Sort: "total_time", Limit: 10, ExcludeUser: "u"})
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
	res, err := s.CreateIncident(ctx, repository.Incident{TargetID: &tg.ID, TargetName: "r", RuleID: "x", RuleVersion: 1, Severity: "info", Title: "old",
		State: "resolved", OpenedAt: old, LastSeenAt: old, ResolvedAt: &old, Source: "recorder"}, []repository.IncidentEvent{{At: old, Kind: "trigger", Summary: "s"}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.ApplyRetention(ctx, repository.RetentionPolicy{History: 7 * 24 * time.Hour, Incidents: 90 * 24 * time.Hour, Findings: 30 * 24 * time.Hour}, now)
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
	cur, _ := s.ListFindings(ctx, repository.FindingFilter{TargetID: tg.ID, Status: "current"})
	if len(cur) != 1 || cur[0].Finding.Evidence[0].Value != 1.5 {
		t.Fatalf("upsert must keep one finding: %+v", cur)
	}
	if _, err := s.SyncFindings(ctx, tg.ID, now.Add(2*time.Second), nil); err != nil {
		t.Fatal(err)
	}
	res, _ := s.ListFindings(ctx, repository.FindingFilter{TargetID: tg.ID, Status: "resolved"})
	if len(res) != 1 || res[0].ResolvedAt == nil {
		t.Fatalf("missing findings must be resolved: %+v", res)
	}
}

func TestSeriesAggregationAndGaps(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	a := target(t, s, "series-alpha")
	b := target(t, s, "series-beta")
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for idx, seconds := range []int{10, 30} {
		v := float64(idx*20 + 10)
		pts := []observe.Point{
			{Metric: "xact.commit.per_s", Value: observe.F(v), IntervalSeconds: observe.F(float64(10 + idx*10))},
			{Metric: "sessions.active", Value: observe.F(v)},
			{Metric: "deadlocks.delta", Value: observe.F(v)},
			{Metric: "db.total_size_bytes", Value: observe.F(40 - v)},
			{Metric: "partial", Value: observe.F(v)},
			{Metric: "legacy.per_s", Value: observe.F(v)},
		}
		if idx == 1 {
			pts[4].Value = nil
		}
		if err := s.InsertPoints(ctx, a.ID, from.Add(time.Duration(seconds)*time.Second), pts); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.InsertPoints(ctx, b.ID, from.Add(10*time.Second), []observe.Point{{Metric: "sessions.active", Value: observe.F(999)}}); err != nil {
		t.Fatal(err)
	}
	names := []string{"xact.commit.per_s", "sessions.active", "deadlocks.delta", "db.total_size_bytes", "partial", "legacy.per_s", "absent"}
	series, err := s.QuerySeries(ctx, a.ID, names, from, from.Add(time.Minute), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	want := []float64{70.0 / 3, 30, 40, 10}
	for i, v := range want {
		if len(series[i].Points) != 1 || series[i].Points[0].V == nil || math.Abs(*series[i].Points[0].V-v) > 1e-9 {
			t.Fatalf("%s: %+v", names[i], series[i])
		}
	}
	if series[4].Points[0].V != nil || series[5].Points[0].V != nil || len(series[6].Points) != 0 {
		t.Fatalf("unknown/legacy/absent values fabricated: %+v", series)
	}
	if err := s.InsertGap(ctx, a.ID, "activity", from.Add(15*time.Second), from.Add(20*time.Second), "test interruption"); err != nil {
		t.Fatal(err)
	}
	series, err = s.QuerySeries(ctx, a.ID, []string{"sessions.active", "deadlocks.delta"}, from, from.Add(time.Minute), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !series[0].Points[0].Gap || series[0].Points[0].V != nil || series[1].Points[0].V == nil {
		t.Fatalf("collector gap isolation broken: %+v", series)
	}
	points := make([]observe.Point, repository.MaxSeriesPoints+1)
	for i := range points {
		points[i] = observe.Point{Metric: "fanout", Label: fmt.Sprint(i), Value: observe.F(1)}
	}
	if err := s.InsertPoints(ctx, a.ID, from.Add(time.Second), points); err != nil {
		t.Fatal(err)
	}
	if _, err := s.QuerySeries(ctx, a.ID, []string{"fanout"}, from, from.Add(time.Minute), time.Minute); err != repository.ErrSeriesLimit {
		t.Fatalf("unbounded labelled response: %v", err)
	}
	// Strict (from,to] bounds, exact-edge samples and cancelled work.
	series, err = s.QuerySeries(ctx, a.ID, []string{"deadlocks.delta"}, from.Add(10*time.Second), from.Add(30*time.Second), 20*time.Second)
	if err != nil || len(series[0].Points) != 1 || *series[0].Points[0].V != 30 || series[0].Points[0].T.Before(from.Add(10*time.Second)) {
		t.Fatalf("range boundary: %+v %v", series, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.QuerySeries(cancelled, a.ID, names, from, from.Add(time.Minute), time.Second); err == nil {
		t.Fatal("cancelled query succeeded")
	}
}

func TestLoadBucketsAndStatementResolution(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	tg := target(t, s, "load")
	key := observe.QueryKey{ServerMajor: 17, DBID: 5, UserID: 10, TopLevel: true, QueryID: -42}
	text := "SELECT slow FROM t WHERE id = $1"
	iv := 60.0
	if err := s.InsertStatements(ctx, tg.ID, observe.StatementsCollection{At: time.Now().UTC(), Interval: &iv, Entries: 1,
		Deltas: []observe.QueryDelta{{Key: key, DatName: "app", UserName: "u", Text: &text, Calls: 1, TotalMS: 1}}}, true, repository.IdentityCache{}); err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Minute).Add(-10 * time.Minute)
	for i := 0; i < 4; i++ {
		b := observe.LoadBucket{At: base.Add(time.Duration(i) * 15 * time.Second), Ticks: 5, IntervalSeconds: 3, Groups: []observe.LoadGroup{
			{WaitClass: "Lock", WaitEvent: "transactionid", BackendType: "client backend", DBID: 5, DatName: "app", UserID: 10, UserName: "u", Application: "api", QueryID: -42, Samples: 10},
			{WaitClass: "CPU", BackendType: "client backend", DBID: 5, DatName: "app", UserID: 10, UserName: "u", Application: "api", Samples: 5},
		}}
		if err := s.InsertLoadBucket(ctx, tg.ID, b); err != nil {
			t.Fatal(err)
		}
		// A retried write of the same bucket is ignored.
		if err := s.InsertLoadBucket(ctx, tg.ID, b); err != nil {
			t.Fatal(err)
		}
	}
	from, to := base, base.Add(time.Minute)
	rows, ticks, err := s.LoadRows(ctx, tg.ID, from, to, time.Minute, repository.LoadByQuery, repository.LoadFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ticks) != 1 || ticks[0].Ticks != 20 {
		t.Fatalf("ticks = %+v", ticks)
	}
	var lock int64
	for _, r := range rows {
		if r.WaitClass == "Lock" {
			lock = r.Samples
			if len(r.Key) != 5 || r.Key[0] != "5" || r.Key[1] != "10" || r.Key[2] != "-42" || r.Key[3] != "app" {
				t.Fatalf("key = %v", r.Key)
			}
		}
		if r.WaitClass == "CPU" && r.Key[2] != "0" {
			t.Fatalf("missing query id must read as 0: %v", r.Key)
		}
	}
	if lock != 40 {
		t.Fatalf("lock samples = %d, want 40 (duplicates ignored)", lock)
	}
	qid := int64(-42)
	rows, _, err = s.LoadRows(ctx, tg.ID, from, to, time.Minute, repository.LoadByEvent, repository.LoadFilter{QueryID: &qid})
	if err != nil || len(rows) != 1 || rows[0].Key[0] != "transactionid" {
		t.Fatalf("filtered rows = %+v, %v", rows, err)
	}
	refs, err := s.QueryRefsByActivity(ctx, tg.ID, []repository.StatementRef{{DBID: 5, UserID: 10, QueryID: -42}, {DBID: 5, UserID: 10, QueryID: 99}}, true)
	if err != nil || len(refs) != 1 || refs[0].Text == nil || *refs[0].Text != text {
		t.Fatalf("refs = %+v, %v", refs, err)
	}
	if refs, _ = s.QueryRefsByActivity(ctx, tg.ID, []repository.StatementRef{{DBID: 5, UserID: 10, QueryID: -42}}, false); refs[0].Text != nil {
		t.Fatal("text must be left out when not requested")
	}
}
