//go:build integration

package repository_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/domain/baseline"
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
	return freshStoreOn(t, admin)
}

// freshStoreOn creates an empty store database on the server of admin.
func freshStoreOn(t *testing.T, admin string) (*repository.Store, string) {
	t.Helper()
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

func TestTimescaleHypertablesCompressionAndRetention(t *testing.T) {
	admin := os.Getenv("LOCKLENS_IT_TIMESCALE_DSN")
	if admin == "" {
		t.Skip("LOCKLENS_IT_TIMESCALE_DSN not set")
	}
	ctx := context.Background()
	s, _ := freshStoreOn(t, admin)
	if _, err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	tg := target(t, s, "ts")
	now := time.Now().UTC().Truncate(time.Second)
	// Ten days of points, written before the conversion: they must move into chunks.
	iv := 3600.0
	for h := 0; h < 240; h++ {
		at := now.Add(-time.Duration(h) * time.Hour)
		if err := s.InsertPoints(ctx, tg.ID, at, []observe.Point{{Metric: "xact.commit.per_s", Value: observe.F(float64(h)), IntervalSeconds: &iv}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.InsertActivitySnapshot(ctx, tg.ID, now.Add(-time.Hour), 1, 1, 0, []byte(`{"schema_version":1}`)); err != nil {
		t.Fatal(err)
	}
	snaps, err := s.ListActivitySnapshots(ctx, tg.ID, now.Add(-2*time.Hour), now, false, 10)
	if err != nil || len(snaps) != 1 {
		t.Fatalf("snapshots %v %v", snaps, err)
	}

	st, err := s.SetupTimescale(ctx, repository.TimescaleAuto, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Enabled || !st.Compression || st.License != "timescale" || len(st.Hypertables) != 14 {
		t.Fatalf("status = %+v", st)
	}
	// Idempotent: a restart changes nothing.
	if st2, err := s.SetupTimescale(ctx, repository.TimescaleAuto, time.Hour); err != nil || len(st2.Hypertables) != 14 {
		t.Fatalf("second setup: %+v %v", st2, err)
	}
	var n int
	if err := s.Pool().QueryRow(ctx, "SELECT count(*) FROM metric_points").Scan(&n); err != nil || n != 240 {
		t.Fatalf("rows after conversion = %d, %v", n, err)
	}
	// Snapshots keep their ids (the primary key now includes ts).
	if _, raw, err := s.GetActivitySnapshot(ctx, tg.ID, snaps[0].ID); err != nil || len(raw) == 0 {
		t.Fatalf("snapshot by id: %v", err)
	}

	// Compress the old chunks now (the policy would do it in the background),
	// then write into one of them again and read it back.
	// The compression policy job may compress a chunk at the same moment;
	// that chunk is then already compressed, which is fine (duplicate_object).
	if _, err := s.Pool().Exec(ctx, `DO $$
DECLARE c regclass;
BEGIN
  FOR c IN SELECT show_chunks('metric_points', older_than => now() - interval '2 days') LOOP
    BEGIN
      PERFORM compress_chunk(c, if_not_compressed => true);
    EXCEPTION WHEN duplicate_object THEN NULL;
    END;
  END LOOP;
END $$`); err != nil {
		t.Fatal(err)
	}
	old := now.Add(-100 * time.Hour)
	if err := s.InsertPoints(ctx, tg.ID, old, []observe.Point{{Metric: "xact.commit.per_s", Value: observe.F(-1), IntervalSeconds: &iv}}); err != nil {
		t.Fatalf("a duplicate in a compressed chunk must be ignored: %v", err)
	}
	series, err := s.QuerySeries(ctx, tg.ID, []string{"xact.commit.per_s"}, now.Add(-200*time.Hour), now, 24*time.Hour)
	if err != nil || len(series) != 1 || len(series[0].Points) < 8 {
		t.Fatalf("series over compressed chunks: %+v %v", series, err)
	}
	sizes, err := s.TableSizes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var mp repository.StorageStats
	for _, x := range sizes {
		if x.Table == "metric_points" {
			mp = x
		}
	}
	if mp.Bytes == 0 || mp.CompressedBefore == nil || mp.CompressedAfter == nil || *mp.CompressedAfter >= *mp.CompressedBefore {
		t.Fatalf("metric_points size = %+v", mp)
	}

	// Retention drops whole chunks older than the cutoff.
	res, err := s.ApplyRetention(ctx, repository.RetentionPolicy{History: 5 * 24 * time.Hour, Incidents: 90 * 24 * time.Hour, Findings: 30 * 24 * time.Hour}, now)
	if err != nil {
		t.Fatal(err)
	}
	if res["metric_points (chunks)"] < 3 {
		t.Fatalf("retention = %v", res)
	}
	var oldest time.Time
	if err := s.Pool().QueryRow(ctx, "SELECT min(ts) FROM metric_points").Scan(&oldest); err != nil {
		t.Fatal(err)
	}
	if oldest.Before(now.Add(-6 * 24 * time.Hour)) {
		t.Fatalf("oldest point %s is older than the cutoff plus one chunk", oldest)
	}
}

func TestTimescaleModesOnPlainPostgreSQL(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	st, err := s.SetupTimescale(ctx, repository.TimescaleAuto, time.Hour)
	if err != nil || st.Enabled || st.Reason == "" {
		t.Fatalf("auto on plain PostgreSQL: %+v %v", st, err)
	}
	if _, err := s.SetupTimescale(ctx, repository.TimescaleOn, time.Hour); err == nil {
		t.Fatal("on must fail without TimescaleDB")
	}
	// Retention still works with batched deletes.
	if _, err := s.ApplyRetention(ctx, repository.RetentionPolicy{History: time.Hour, Incidents: time.Hour * 24, Findings: time.Hour}, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestRollupsBaselinesFindingsAndPlans(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	tg := target(t, s, "rb")
	cache := repository.IdentityCache{}
	key := observe.QueryKey{ServerMajor: 17, DBID: 5, UserID: 10, TopLevel: true, QueryID: 77}
	base := time.Now().UTC().Truncate(time.Hour).Add(-4 * time.Hour)
	iv := 60.0
	// Three hours of one-minute collections: 60 calls a minute at 2 ms.
	for i := 1; i <= 180; i++ {
		c := observe.StatementsCollection{At: base.Add(time.Duration(i) * time.Minute), Interval: &iv, Entries: 1,
			Deltas: []observe.QueryDelta{{Key: key, DatName: "app", UserName: "u", Calls: 60, TotalMS: 120, Rows: 60, BlkRead: 600}}}
		if i > 120 && i <= 130 {
			c.Deltas = nil // ten quiet minutes: covered, no calls
		}
		if err := s.InsertStatements(ctx, tg.ID, c, false, cache); err != nil {
			t.Fatal(err)
		}
	}
	end := base.Add(4 * time.Hour)
	for range 2 { // idempotent
		if err := s.RollupStatements(ctx, tg.ID, base, end); err != nil {
			t.Fatal(err)
		}
	}
	if wm, ok, err := s.RollupWatermark(ctx, tg.ID); err != nil || !ok || !wm.Equal(base.Add(3*time.Hour-5*time.Minute)) {
		t.Fatalf("watermark %v %v %v", wm, ok, err)
	}
	active, err := s.ActiveStatements(ctx, tg.ID, base, 10)
	if err != nil || len(active) != 1 || active[0].Key != key {
		t.Fatalf("active %+v %v", active, err)
	}
	points, cov, err := s.BaselinePoints(ctx, tg.ID, []int64{active[0].IdentityID}, base, end, time.Hour)
	if err != nil || len(points) != 3 || len(cov) != 3 {
		t.Fatalf("points %+v cov %+v %v", points, cov, err)
	}
	if points[0].Calls != 3600 || points[0].TotalMS != 7200 || cov[0].Covered != 3600 || points[2].Calls != 3000 {
		t.Errorf("point sums: %+v %+v", points, cov)
	}
	svc := baseline.New(baseline.Repositories{RollupRepository: s, TargetRepository: s}, nil, nil)
	byID, err := svc.Load(ctx, tg.ID, []int64{active[0].IdentityID}, base, end, baseline.Params{Point: time.Hour, MinCalls: 10})
	if err != nil {
		t.Fatal(err)
	}
	b := byID[active[0].IdentityID]
	// The empty fourth hour has no coverage: unknown, not zero calls.
	if b.All.Points != 3 || b.All.Stats[baseline.MeanMS].Median != 2 || b.All.Stats[baseline.ReadPerCall].Median != 10 {
		t.Fatalf("baseline %+v", b.All)
	}
	if cps := b.All.Stats[baseline.CallsPerSec]; cps.Median != 1 || cps.N != 3 {
		t.Fatalf("calls/s %+v", cps)
	}

	// Confidence and metrics survive storage.
	now := time.Now().UTC()
	f := finding.Finding{RuleID: "queries.mean_regression", RuleVersion: 2, Key: "q:17:5:10:true:77", Severity: finding.Warning, Title: "t",
		Status: finding.Active, ObservedFrom: now, Observed: []string{}, Evidence: []finding.Evidence{},
		Confidence: &finding.Confidence{Level: finding.ConfidenceHigh, Reasons: []string{"r"}}, Metrics: map[string]float64{"ratio": 4.5}}
	if _, err := s.SyncFindings(ctx, tg.ID, now, []finding.Finding{f}); err != nil {
		t.Fatal(err)
	}
	cur, err := s.ListFindings(ctx, repository.FindingFilter{TargetID: tg.ID, Status: "current"})
	if err != nil || len(cur) != 1 || cur[0].Finding.Confidence == nil || cur[0].Finding.Confidence.Level != "high" || cur[0].Finding.Metrics["ratio"] != 4.5 {
		t.Fatalf("stored finding %+v %v", cur, err)
	}

	// Plans.
	cost := 12.5
	p, err := s.SavePlan(ctx, tg.ID, repository.PlanRow{IdentityID: active[0].IdentityID, CapturedBy: "admin", Source: "manual", Format: "json",
		Plan: `[{"Plan":{"Node Type":"Seq Scan"}}]`, Fingerprint: "abc", TotalCost: &cost})
	if err != nil || p.ID == 0 {
		t.Fatalf("save %+v %v", p, err)
	}
	if _, err := s.SavePlan(ctx, tg.ID, repository.PlanRow{IdentityID: 999999, CapturedBy: "a", Source: "manual", Format: "text", Plan: "x", Fingerprint: "f"}); err != repository.ErrNotFound {
		t.Fatalf("unknown identity: %v", err)
	}
	other := target(t, s, "other")
	if _, err := s.GetPlan(ctx, other.ID, p.ID); err != repository.ErrNotFound {
		t.Fatal("a plan must not be readable through another target")
	}
	list, _ := s.ListPlans(ctx, tg.ID, active[0].IdentityID)
	if len(list) != 1 || list[0].Plan != "" || *list[0].TotalCost != 12.5 {
		t.Fatalf("list %+v", list)
	}
	if got, err := s.GetPlan(ctx, tg.ID, p.ID); err != nil || got.Plan == "" {
		t.Fatalf("get %+v %v", got, err)
	}
	if n, err := s.CountPlans(ctx, tg.ID, 77); err != nil || n != 1 {
		t.Fatalf("count %d %v", n, err)
	}
	// Retention keeps identities that rollups or plans refer to.
	if _, err := s.ApplyRetention(ctx, repository.RetentionPolicy{History: time.Minute, Incidents: 24 * time.Hour, Findings: 24 * time.Hour, Rollups: 30 * 24 * time.Hour}, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountPlans(ctx, tg.ID, 77); n != 1 {
		t.Fatal("retention removed a plan or its identity")
	}
	if err := s.DeletePlan(ctx, tg.ID, p.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeletePlan(ctx, tg.ID, p.ID); err != repository.ErrNotFound {
		t.Fatalf("second delete: %v", err)
	}
}

// TestMetricRollupsMatchRawPoints checks that series read from rollups (fully
// rolled up, partly rolled up, and not at all) equal those computed from raw
// points, including unknown samples.
func TestMetricRollupsMatchRawPoints(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	tg := target(t, s, "mr")
	base := time.Now().UTC().Truncate(time.Hour).Add(-3 * time.Hour)
	iv := 10.0
	for i := 1; i <= 2*360; i++ { // two hours, every 10 s
		at := base.Add(time.Duration(i) * 10 * time.Second)
		pts := []observe.Point{
			{Metric: "sessions.active", Value: observe.F(float64(i % 7))},
			{Metric: "xact.commit.per_s", IntervalSeconds: &iv, Value: observe.F(float64(i%11) * 1.5)},
			{Metric: "deadlocks.delta", Value: observe.F(float64(i % 2))},
			{Metric: "db.size_bytes", Label: "app", Value: observe.F(float64(1000 + i))},
		}
		if i == 400 { // one unknown sample: its buckets are unknown
			pts[0].Value = nil
		}
		if err := s.InsertPoints(ctx, tg.ID, at, pts); err != nil {
			t.Fatal(err)
		}
	}
	names := []string{"sessions.active", "xact.commit.per_s", "deadlocks.delta", "db.size_bytes"}
	from, to := base, base.Add(2*time.Hour)
	read := func() []repository.Series {
		t.Helper()
		out, err := s.QuerySeries(ctx, tg.ID, names, from, to, 15*time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	rawSeries := read() // no rollups yet: computed from raw points
	if len(rawSeries) != 4 || len(rawSeries[0].Points) != 8 {
		t.Fatalf("raw series: %+v", rawSeries)
	}
	same := func(what string, got []repository.Series) {
		t.Helper()
		for i := range rawSeries {
			if len(got[i].Points) != len(rawSeries[i].Points) {
				t.Fatalf("%s %s: %d points, want %d", what, names[i], len(got[i].Points), len(rawSeries[i].Points))
			}
			for j, p := range got[i].Points {
				w := rawSeries[i].Points[j]
				if !p.T.Equal(w.T) || p.Samples != w.Samples || p.Known != w.Known || (p.V == nil) != (w.V == nil) ||
					(p.V != nil && math.Abs(*p.V-*w.V) > 1e-9) {
					t.Fatalf("%s %s bucket %d: %+v, want %+v", what, names[i], j, p, w)
				}
			}
		}
	}
	// Partly rolled up: rollups for the first hour, raw points after.
	if err := s.RollupMetrics(ctx, tg.ID, base, base.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	same("partial", read())
	if err := s.RollupMetrics(ctx, tg.ID, base, base.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	same("full", read())
	// The bucket with the unknown sample stays unknown; others are known.
	unknown := 0
	for _, p := range rawSeries[0].Points {
		if p.V == nil {
			unknown++
		}
	}
	if unknown != 1 {
		t.Fatalf("unknown buckets = %d", unknown)
	}
	// Hourly steps read the hourly rollups.
	hourly, err := s.QuerySeries(ctx, tg.ID, []string{"deadlocks.delta"}, from, to, 3*time.Hour)
	if err != nil || len(hourly[0].Points) != 1 || *hourly[0].Points[0].V != 360 {
		t.Fatalf("hourly: %+v %v", hourly, err)
	}
	// Retention removes old minute rollups but keeps hourly ones longer.
	if _, err := s.ApplyRetention(ctx, repository.RetentionPolicy{History: time.Hour, Incidents: 24 * time.Hour, Findings: time.Hour,
		Rollups: time.Hour, MetricMinute: time.Hour, MetricHour: 30 * 24 * time.Hour}, time.Now().Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	hourly, err = s.QuerySeries(ctx, tg.ID, []string{"deadlocks.delta"}, from, to, 3*time.Hour)
	if err != nil || len(hourly[0].Points) != 1 {
		t.Fatalf("hourly after minute retention: %+v %v", hourly, err)
	}
}

func TestSeveralDatabasesOfOneTarget(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	tg := target(t, s, "dbs")
	at := time.Now().UTC().Truncate(time.Second)
	i64 := func(v int64) *int64 { return &v }
	// The connected database reads cluster-wide data; another one only its
	// tables. Both have a table with OID 16384.
	main := observe.TableHealth{At: at, DatName: "app", Databases: []observe.DatabaseAge{{DatName: "app"}, {DatName: "reports"}},
		Settings: map[string]string{"autovacuum_freeze_max_age": "200000000"},
		Tables:   []observe.TableStat{{RelID: 16384, DatName: "app", Schema: "public", Name: "orders", TotalBytes: i64(10)}},
		Indexes:  []observe.IndexStat{{IndexRelID: 16390, RelID: 16384, DatName: "app", Schema: "public", Table: "orders", Name: "orders_pkey", Bytes: i64(5)}}}
	other := observe.TableHealth{At: at.Add(time.Millisecond), DatName: "reports",
		Tables:  []observe.TableStat{{RelID: 16384, DatName: "reports", Schema: "public", Name: "daily", TotalBytes: i64(20)}},
		Indexes: []observe.IndexStat{{IndexRelID: 16390, RelID: 16384, DatName: "reports", Schema: "public", Table: "daily", Name: "daily_pkey", Bytes: i64(7)}}}
	for _, h := range []observe.TableHealth{main, other} {
		if err := s.InsertTableHealth(ctx, tg.ID, h); err != nil {
			t.Fatal(err)
		}
	}
	h, err := s.LatestTableHealth(ctx, tg.ID)
	if err != nil {
		t.Fatal(err)
	}
	if h.DatName != "app" || len(h.Databases) != 2 || h.Settings["autovacuum_freeze_max_age"] == "" {
		t.Fatalf("cluster-wide data should come from the connected database: %+v", h)
	}
	if len(h.Tables) != 2 || h.Tables[0].DatName != "reports" || h.Tables[1].DatName != "app" {
		t.Fatalf("tables of both databases, largest first: %+v", h.Tables)
	}
	if len(h.Indexes) != 2 || h.Indexes[0].DatName != "reports" || h.Indexes[1].DatName != "app" {
		t.Fatalf("indexes of both databases: %+v", h.Indexes)
	}

	// One catalog per database; pruning drops those that left the scope.
	for _, c := range []observe.Catalog{{At: at, DatName: "app"}, {At: at.Add(time.Second), DatName: "reports"}} {
		if err := s.SaveCatalog(ctx, tg.ID, c); err != nil {
			t.Fatal(err)
		}
	}
	if cats, err := s.Catalogs(ctx, tg.ID); err != nil || len(cats) != 2 || cats[0].DatName != "app" {
		t.Fatalf("catalogs %+v %v", cats, err)
	}
	if c, err := s.LatestCatalog(ctx, tg.ID); err != nil || c.DatName != "reports" {
		t.Fatalf("latest catalog %+v %v", c, err)
	}

	// The same recommendation key in two databases is two recommendations,
	// and a pass over one database leaves the other's alone.
	rec := repository.RecommendationInput{Kind: "missing_index", Key: "public.t(a)", Severity: "warning", Title: "x", Summary: "y",
		Details: map[string]any{}, Benefit: 1}
	for _, db := range []string{"app", "reports"} {
		if err := s.SyncRecommendations(ctx, tg.ID, db, at, []repository.RecommendationInput{rec}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SyncRecommendations(ctx, tg.ID, "app", at.Add(time.Minute), nil); err != nil {
		t.Fatal(err)
	}
	open, err := s.ListRecommendations(ctx, repository.RecommendationFilter{TargetID: tg.ID, Statuses: []string{repository.RecOpen}})
	if err != nil || len(open) != 1 || open[0].Database != "reports" {
		t.Fatalf("open recommendations %+v %v", open, err)
	}
	app := "app"
	if resolved, err := s.ListRecommendations(ctx, repository.RecommendationFilter{TargetID: tg.ID, Database: &app}); err != nil ||
		len(resolved) != 1 || resolved[0].Status != repository.RecResolved {
		t.Fatalf("app recommendations %+v %v", resolved, err)
	}

	if err := s.PruneCatalogs(ctx, tg.ID, []string{"app"}); err != nil {
		t.Fatal(err)
	}
	if cats, err := s.Catalogs(ctx, tg.ID); err != nil || len(cats) != 1 || cats[0].DatName != "app" {
		t.Fatalf("pruned catalogs %+v %v", cats, err)
	}
}

func TestChangesAndTokens(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	tg := target(t, s, "chg")
	other := target(t, s, "chg2")
	at := time.Now().UTC().Truncate(time.Second)
	events := []observe.ChangeEvent{
		{TargetID: &tg.ID, At: at.Add(-time.Hour), Kind: "settings", Source: "detected", Title: "work_mem changed",
			Details: map[string]any{"changes": []map[string]any{{"name": "work_mem"}}}},
		{At: at.Add(-30 * time.Minute), Kind: "deploy", Source: "api", Title: "api v2", CreatedBy: "token:ci"},
		{TargetID: &other.ID, At: at.Add(-10 * time.Minute), Kind: "restart", Source: "detected", Title: "restarted"},
	}
	if err := s.InsertChanges(ctx, events); err != nil || events[0].ID == 0 {
		t.Fatalf("insert %v %+v", err, events)
	}
	// One instance's changes include those for every instance.
	got, err := s.ListChanges(ctx, repository.ChangeFilter{From: at.Add(-2 * time.Hour), To: at, TargetIDs: []int64{tg.ID}})
	if err != nil || len(got) != 2 || got[0].Kind != "deploy" || got[0].TargetID != nil || got[1].Details["changes"] == nil {
		t.Fatalf("list %+v %v", got, err)
	}
	if got, err := s.ListChanges(ctx, repository.ChangeFilter{From: at.Add(-2 * time.Hour), To: at, Kinds: []string{"restart"}}); err != nil || len(got) != 1 {
		t.Fatalf("by kind %+v %v", got, err)
	}
	// Detected changes cannot be deleted; reported ones can.
	if _, err := s.DeleteChange(ctx, events[0].ID); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("delete detected: %v", err)
	}
	if e, err := s.DeleteChange(ctx, events[1].ID); err != nil || e.Title != "api v2" {
		t.Fatalf("delete reported: %+v %v", e, err)
	}

	if _, err := s.ChangeState(ctx, tg.ID, "server"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("no state yet: %v", err)
	}
	for _, v := range []string{`{"a":1}`, `{"a":2}`} {
		if err := s.SaveChangeState(ctx, tg.ID, "server", at, []byte(v)); err != nil {
			t.Fatal(err)
		}
	}
	if raw, err := s.ChangeState(ctx, tg.ID, "server"); err != nil || string(raw) != `{"a": 2}` {
		t.Fatalf("state %s %v", raw, err)
	}

	hash := []byte("0123456789abcdef0123456789abcdef")
	tok, err := s.CreateToken(ctx, "ci", repository.TokenEvents, nil, hash, "llk_abcd", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.TokenByHash(ctx, hash); err != nil || got.ID != tok.ID || got.Name != "ci" {
		t.Fatalf("by hash %+v %v", got, err)
	}
	if err := s.RevokeToken(ctx, tok.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TokenByHash(ctx, hash); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("revoked: %v", err)
	}
	if err := s.RevokeToken(ctx, tok.ID); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("revoke twice: %v", err)
	}
}

func TestClusterLeasesLiveStateAndBus(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	tg := target(t, s, "cl")
	for _, id := range []string{"a", "b"} {
		if err := s.Heartbeat(ctx, repository.Server{ID: id, Hostname: "h", Version: "v", StartedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	if sv, err := s.Servers(ctx, time.Minute); err != nil || len(sv) != 2 || !sv[0].Alive {
		t.Fatalf("servers %+v %v", sv, err)
	}
	// One holder at a time; the holder renews; an expired lease is taken.
	if ok, _, err := s.HoldLease(ctx, "target:1", "a", time.Minute); err != nil || !ok {
		t.Fatalf("a takes: %v %v", ok, err)
	}
	if ok, _, err := s.HoldLease(ctx, "target:1", "b", time.Minute); err != nil || ok {
		t.Fatalf("b must wait: %v %v", ok, err)
	}
	first, second := time.Time{}, time.Time{}
	_, first, _ = s.HoldLease(ctx, "target:1", "a", time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	if ok, at, err := s.HoldLease(ctx, "target:1", "b", time.Minute); err != nil || !ok || !at.After(first) {
		t.Fatalf("b takes the expired lease: %v %v %v", ok, at, err)
	}
	_, second, _ = s.HoldLease(ctx, "target:1", "b", time.Minute)
	if ls, err := s.Leases(ctx, "target:"); err != nil || len(ls) != 1 || ls[0].Holder != "b" || !ls[0].Live || !ls[0].AcquiredAt.Equal(second) {
		t.Fatalf("leases %+v %v", ls, err)
	}
	if err := s.ReleaseLease(ctx, "target:1", "a"); err != nil { // not a's: no effect
		t.Fatal(err)
	}
	if err := s.ReleaseLease(ctx, "target:1", "b"); err != nil {
		t.Fatal(err)
	}
	if ls, _ := s.Leases(ctx, "target:"); len(ls) != 0 {
		t.Fatalf("released: %+v", ls)
	}

	if err := s.SaveLiveState(ctx, tg.ID, "a", time.Now(), []byte(`{"Connected":true}`)); err != nil {
		t.Fatal(err)
	}
	if sid, _, raw, err := s.LiveState(ctx, tg.ID); err != nil || sid != "a" || !strings.Contains(string(raw), "Connected") {
		t.Fatalf("live state %s %s %v", sid, raw, err)
	}

	// A message sent on the bus channel reaches a listener.
	lctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	got := make(chan string, 1)
	go func() {
		_ = s.Listen(lctx, "locklens_bus", func(p string) {
			select {
			case got <- p:
			default:
			}
		})
	}()
	deadline := time.After(8 * time.Second)
	for {
		if err := s.Notify(ctx, "locklens_bus", `{"kind":"x"}`); err != nil {
			t.Fatal(err)
		}
		select {
		case p := <-got:
			if p != `{"kind":"x"}` {
				t.Fatalf("payload %q", p)
			}
			return
		case <-time.After(200 * time.Millisecond): // the listener may not be subscribed yet
		case <-deadline:
			t.Fatal("no message received")
		}
	}
}

func TestLogEvents(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	tg := target(t, s, "logs")
	tok, err := s.CreateToken(ctx, "agent", repository.TokenIngest, &tg.ID, []byte("logs-token-hash-0123456789abcdef"), "llk_x", "admin")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Millisecond).Add(-time.Hour)
	f := func(v float64) *float64 { return &v }
	qid := int64(77)
	var events []observe.LogEvent
	for i := range 100 {
		events = append(events, observe.LogEvent{ID: fmt.Sprintf("s%d", i), At: at.Add(time.Duration(i) * time.Second), Kind: "slow_query",
			Severity: "LOG", DurationMS: f(float64(i + 1)), QueryID: &qid, Fingerprint: "fp1", Query: "select $1", Message: "duration"})
	}
	// The same execution in auto_explain is not added to the statement's count.
	events = append(events, observe.LogEvent{ID: "p1", At: at, Kind: "plan", Severity: "LOG", DurationMS: f(100), Fingerprint: "fp1",
		Query: "select $1", Message: "auto_explain plan", Details: map[string]any{"plan": "Seq Scan on t"}},
		observe.LogEvent{ID: "p2", At: at, Kind: "plan", Severity: "LOG", DurationMS: f(9), Fingerprint: "fp2", Query: "select 2", Message: "plan"},
		observe.LogEvent{ID: "d1", At: at.Add(time.Minute), Kind: "deadlock", Severity: "ERROR", SQLState: "40P01", Message: "deadlock detected",
			Details: map[string]any{"cycle": []any{}}})
	n, err := s.InsertLogEvents(ctx, tg.ID, tok.ID, "db1", "v1", []string{"/log/postgresql.log"}, events)
	if err != nil || n != int64(len(events)) {
		t.Fatalf("insert %d %v", n, err)
	}
	// A batch sent again is stored once.
	if n, err := s.InsertLogEvents(ctx, tg.ID, tok.ID, "db1", "v1", nil, events[:10]); err != nil || n != 0 {
		t.Fatalf("again %d %v", n, err)
	}
	stmts, err := s.LogStatements(ctx, tg.ID, at.Add(-time.Minute), at.Add(time.Hour), 10)
	if err != nil || len(stmts) != 2 {
		t.Fatalf("statements %+v %v", stmts, err)
	}
	st := stmts[0]
	if st.Fingerprint != "fp1" || st.Source != "slow_query" || st.Count != 100 || st.Plans != 1 || math.Abs(st.P95MS-95.05) > 0.01 || st.MaxMS != 100 || *st.QueryID != 77 {
		t.Fatalf("fp1 %+v", st)
	}
	if stmts[1].Source != "plan" || stmts[1].Count != 1 {
		t.Fatalf("fp2 %+v", stmts[1])
	}
	ev, err := s.LogEvents(ctx, repository.LogEventFilter{TargetID: tg.ID, From: at.Add(-time.Minute), To: at.Add(time.Hour), Kinds: []string{"deadlock", "plan"}})
	if err != nil || len(ev) != 3 || ev[0].Kind != "deadlock" || ev[0].SQLState != "40P01" {
		t.Fatalf("events %+v %v", ev, err)
	}
	counts, err := s.LogCounts(ctx, tg.ID, at.Add(-time.Minute), at.Add(time.Hour), 10*time.Minute)
	if err != nil || len(counts) == 0 {
		t.Fatalf("counts %+v %v", counts, err)
	}
	agents, err := s.LogAgents(ctx, tg.ID)
	if err != nil || len(agents) != 1 || agents[0].Received != int64(len(events)) || agents[0].Hostname != "db1" || len(agents[0].Sources) != 0 {
		t.Fatalf("agents %+v %v", agents, err)
	}
}
