//go:build integration

package optimizer

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/repository"
)

func store(t *testing.T) *repository.Store {
	t.Helper()
	ctx := context.Background()
	admin := os.Getenv("LOCKLENS_IT_ADMIN_DSN")
	if admin == "" {
		t.Fatal("LOCKLENS_IT_ADMIN_DSN is required; run scripts/integration-test.sh")
	}
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	name := fmt.Sprintf("opt_it_%d", time.Now().UnixNano())
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

// TestLifecycleAppliedVerifiedRegressed applies two index recommendations
// (seen in the catalog) and judges them from the statements' history: one
// statement got faster, the other slower.
func TestLifecycleAppliedVerifiedRegressed(t *testing.T) {
	ctx := context.Background()
	s := store(t)
	tg, err := s.CreateTarget(ctx, repository.Target{Name: "opt", DSNCiphertext: []byte{1}, DisplayHost: "h", DisplayPort: 5432,
		DisplayDatabase: "app", DisplayUser: "u", Enabled: true, IntervalSeconds: 10, MaxConnections: 2})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Minute)
	applied := now.Add(-time.Hour)
	fast := observe.QueryKey{ServerMajor: 18, DBID: 1, UserID: 2, TopLevel: true, QueryID: 11}
	slow := observe.QueryKey{ServerMajor: 18, DBID: 1, UserID: 2, TopLevel: true, QueryID: 22}
	cache := repository.IdentityCache{}
	iv := 60.0
	// Three hours of history: before the change 10 ms and 2 ms per call,
	// after it 2 ms and 10 ms.
	for at := applied.Add(-2 * time.Hour); !at.After(now); at = at.Add(time.Minute) {
		fms, sms := 10.0, 2.0
		if at.After(applied) {
			fms, sms = 2.0, 10.0
		}
		c := observe.StatementsCollection{At: at, Interval: &iv, Entries: 2, Deltas: []observe.QueryDelta{
			{Key: fast, DatName: "app", UserName: "u", Calls: 100, TotalMS: 100 * fms, Rows: 100},
			{Key: slow, DatName: "app", UserName: "u", Calls: 100, TotalMS: 100 * sms, Rows: 100},
		}}
		if err := s.InsertStatements(ctx, tg.ID, c, false, cache); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RollupStatements(ctx, tg.ID, applied.Add(-3*time.Hour).Truncate(5*time.Minute), now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	refs, err := s.QueryRefs(ctx, tg.ID, []observe.QueryKey{fast, slow}, false)
	if err != nil || len(refs) != 2 {
		t.Fatalf("refs %+v %v", refs, err)
	}
	ids := map[int64]int64{}
	for _, r := range refs {
		ids[r.Key.QueryID] = r.IdentityID
	}
	recs := []repository.RecommendationInput{}
	for _, x := range []struct {
		col string
		qid int64
	}{{"a", 11}, {"b", 22}} {
		recs = append(recs, repository.RecommendationInput{Kind: KindMissingIndex, Key: "public.t(" + x.col + ")", Severity: "info", Title: "t", Summary: "s",
			Details:    map[string]any{"table": "public.t", "columns": []string{x.col}, "statements": []StatementRef{{IdentityID: ids[x.qid]}}},
			Confidence: Confidence{Level: "low", Reasons: []string{}}})
	}
	if err := s.SyncRecommendations(ctx, tg.ID, applied.Add(-2*time.Hour), recs); err != nil {
		t.Fatal(err)
	}
	// The catalog collected after the change has both indexes.
	cat := observe.Catalog{At: applied, DatName: "app", Tables: []observe.CatalogTable{{Schema: "public", Name: "t", RelTuples: 1e6}},
		Indexes: []observe.CatalogIndex{
			{Schema: "public", Table: "t", Name: "t_a", Method: "btree", Columns: []string{"a"}, KeyColumns: 1, Valid: true, Definition: "CREATE INDEX t_a ON public.t USING btree (a)"},
			{Schema: "public", Table: "t", Name: "t_b", Method: "btree", Columns: []string{"b"}, KeyColumns: 1, Valid: true},
		}}
	if err := s.SaveCatalog(ctx, tg.ID, cat); err != nil {
		t.Fatal(err)
	}
	svc := NewService(s, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	stored, _ := s.GetTarget(ctx, tg.ID)
	for range 2 { // the first pass records the change, the second judges it
		if _, err := svc.Refresh(ctx, stored, now); err != nil {
			t.Fatal(err)
		}
	}
	all, err := s.ListRecommendations(ctx, repository.RecommendationFilter{TargetID: tg.ID})
	if err != nil || len(all) != 2 {
		t.Fatalf("recommendations %+v %v", all, err)
	}
	for _, r := range all {
		if r.AppliedAt == nil || !r.AppliedAt.Equal(applied) || r.BeforeMS == nil || r.AfterMS == nil || r.AppliedHow == nil {
			t.Fatalf("lifecycle fields: %+v", r)
		}
		switch r.Key {
		case "public.t(a)":
			if r.Status != repository.RecVerified || *r.BeforeMS != 10 || *r.AfterMS != 2 {
				t.Errorf("faster statement: %s %v -> %v", r.Status, *r.BeforeMS, *r.AfterMS)
			}
		case "public.t(b)":
			if r.Status != repository.RecRegressed || *r.BeforeMS != 2 || *r.AfterMS != 10 {
				t.Errorf("slower statement: %s %v -> %v", r.Status, *r.BeforeMS, *r.AfterMS)
			}
		}
	}
	// A decision is no longer possible after the change was applied.
	if _, err := s.DecideRecommendation(ctx, tg.ID, all[0].ID, repository.RecDismissed, "x", ""); err != repository.ErrRecommendationState {
		t.Fatalf("decision after apply: %v", err)
	}
}
