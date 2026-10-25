//go:build integration

package postgres

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/demo/scenario"
	"github.com/locklens/locklens/internal/config"
	"github.com/locklens/locklens/internal/domain/model"
	"github.com/locklens/locklens/internal/domain/report"
)

// TestSessionEndingMidCollection terminates the blocker after LockLens has
// read the blocking relations but before it reads the other sources, using
// the collector's test hook. It documents what really happens: PostgreSQL
// caches pg_stat_activity for the rest of the transaction once it is first
// read (which happens inside the blocking query), so the ended session is
// still reported with its metadata as of that moment, the edge is kept, and
// the live pg_locks read no longer shows the waiter's request.
//
// Run through scripts/integration-test.sh, which provides the DSNs.
func TestSessionEndingMidCollection(t *testing.T) {
	adminDSN, demoDSN, observerDSN := os.Getenv("LOCKLENS_IT_ADMIN_DSN"), os.Getenv("LOCKLENS_IT_DEMO_DSN"), os.Getenv("LOCKLENS_IT_OBSERVER_DSN")
	if adminDSN == "" || demoDSN == "" || observerDSN == "" {
		t.Fatal("LOCKLENS_IT_ADMIN_DSN, LOCKLENS_IT_DEMO_DSN, and LOCKLENS_IT_OBSERVER_DSN must be set; run scripts/integration-test.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	s, err := scenario.RowLock(ctx, demoDSN, "it_session_ending_mid_collection")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	a, b := s.ByLabel("A").PID, s.ByLabel("B").PID

	adm, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer adm.Close(context.Background())

	cfg, red, err := config.PoolConfig(observerDSN, config.Options{ConnectTimeout: 5 * time.Second, QueryTimeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	c, err := Connect(ctx, cfg, red, model.Tool{Name: "locklens", Version: "it"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	testHookAfterBlocking = func() {
		if _, err := adm.Exec(ctx, "SELECT pg_terminate_backend($1, 5000)", a); err != nil {
			t.Errorf("terminate A: %v", err)
		}
		// Wait until B has acquired the row (its request disappears from
		// pg_locks), proving the server state changed mid-collection.
		deadline := time.Now().Add(10 * time.Second)
		for {
			var waiting bool
			if err := adm.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_locks WHERE pid = $1 AND NOT granted)", b).Scan(&waiting); err != nil {
				t.Errorf("poll pg_locks: %v", err)
				return
			}
			if !waiting {
				return
			}
			if time.Now().After(deadline) {
				t.Error("B still waiting after A was terminated")
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	defer func() { testHookAfterBlocking = nil }()

	snap, err := c.Collect(ctx, Options{QueryMaxLength: 200, MinTxAge: time.Minute})
	if err != nil {
		t.Fatalf("collection must survive a session ending mid-way: %v", err)
	}
	testHookAfterBlocking = nil

	r := report.Build(snap, time.Minute)
	data, err := json.Marshal(r)
	if err != nil || !json.Valid(data) {
		t.Fatalf("report does not encode: %v", err)
	}
	found := false
	for _, e := range snap.Edges {
		found = found || (e.Blocker == a && e.Waiter == b)
	}
	if !found {
		t.Fatalf("edge %d -> %d read before the termination must be kept: %+v", a, b, snap.Edges)
	}
	sa, ok := snap.SessionByPID(a)
	if !ok {
		t.Fatalf("terminated session %d missing; expected cached pg_stat_activity metadata", a)
	}
	if sa.State == nil || *sa.State != "idle in transaction" {
		t.Errorf("A should carry its state as of the cached activity snapshot, got %v", sa.State)
	}
	sb, ok := snap.SessionByPID(b)
	if !ok {
		t.Fatalf("waiter %d missing", b)
	}
	if len(sb.WaitingLocks) != 0 {
		t.Errorf("pg_locks is read live; B's request should be gone, got %+v", sb.WaitingLocks)
	}
	for _, n := range r.Analysis.Nodes {
		if n.ID == a && n.Kind != report.KindSession {
			t.Errorf("A kind = %s", n.Kind)
		}
	}
}
