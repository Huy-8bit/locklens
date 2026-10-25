//go:build integration

package onboard

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/secret"
	"github.com/locklens/locklens/internal/storage"
	"github.com/locklens/locklens/internal/targets"
	"github.com/locklens/locklens/internal/workspace"
)

type noReload struct{}

func (noReload) Reload() {}

func freshStore(t *testing.T, admin string) *storage.Store {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	name := fmt.Sprintf("onboard_store_%d", time.Now().UnixNano())
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
	s, err := storage.Open(ctx, u.String(), storage.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if _, err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return s
}

// standbyAdminDSN is the admin DSN pointed at the standby's address.
func standbyAdminDSN(t *testing.T, admin string) string {
	t.Helper()
	sb, err := pgx.ParseConfig(os.Getenv("LOCKLENS_IT_STANDBY_OBSERVER_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(admin)
	u.Host = fmt.Sprintf("%s:%d", sb.Host, sb.Port)
	return u.String()
}

func TestOneConnectionString(t *testing.T) {
	admin := os.Getenv("LOCKLENS_IT_ADMIN_DSN")
	if admin == "" {
		t.Fatal("LOCKLENS_IT_ADMIN_DSN is required; run scripts/integration-test.sh")
	}
	ctx := context.Background()
	store := freshStore(t, admin)
	box, _ := secret.New(make([]byte, 32))
	ws := workspace.New(store, box)
	svc := New(store, box, targets.NewService(store, box, noReload{}, "it"), ws)
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), admin)
		if err != nil {
			return
		}
		defer c.Close(context.Background())
		db := pgx.Identifier{c.Config().Database}.Sanitize()
		for _, r := range []string{MonitorRole, ReaderRole} {
			_, _ = c.Exec(context.Background(), "REVOKE CONNECT ON DATABASE "+db+" FROM "+r)
			_, _ = c.Exec(context.Background(), "DROP ROLE IF EXISTS "+r)
		}
	})

	// A superuser on the primary: LockLens proposes to create its roles.
	p, err := svc.Inspect(ctx, admin)
	if err != nil || !p.OK || p.Mode != ModeCreateRoles || !p.Superuser || len(p.Statements) != 6 {
		t.Fatalf("superuser plan %+v %v", p, err)
	}
	for _, st := range p.Statements {
		if strings.Contains(st, "PASSWORD '") && !strings.Contains(st, "••••") {
			t.Fatalf("plan must not show passwords: %s", st)
		}
	}
	if _, _, err := svc.Add(ctx, Request{DSN: admin, Name: "primary"}); err != ErrNeedsConfirmation {
		t.Fatalf("roles must not be created without confirmation: %v", err)
	}
	v, _, err := svc.Add(ctx, Request{DSN: admin, Name: "primary", Environment: "it", BrowseData: true, RunSQL: true, Confirm: true})
	if err != nil {
		t.Fatal(err)
	}
	if v.User != MonitorRole || v.Environment != "it" {
		t.Fatalf("target must use the monitoring role: %+v", v)
	}
	pol, err := store.WorkspacePolicy(ctx, v.ID)
	if err != nil || !pol.Metadata || !pol.Data || !pol.SQL || !pol.Configured {
		t.Fatalf("workspace policy %+v %v", pol, err)
	}
	// The Explorer runs as the reader role, which cannot signal backends.
	run := func(sql string) (workspace.Result, error) {
		out, err := ws.Run(ctx, v.ID, "session", "sql", fmt.Sprintf("it-%d", time.Now().UnixNano()), func(c context.Context, tx pgx.Tx) (any, error) {
			return workspace.Execute(c, tx, sql, "public", false)
		})
		r, _ := out.(workspace.Result)
		return r, err
	}
	r, err := run("SELECT current_user")
	if err != nil || len(r.Rows) != 1 || *r.Rows[0][0] != ReaderRole {
		t.Fatalf("workspace user: %+v %v", r, err)
	}
	if _, err := run("SELECT pg_terminate_backend(pg_backend_pid())"); err == nil {
		t.Fatal("the reader role must not be able to terminate sessions")
	}
	if _, err := run("SELECT pg_read_file('postgresql.conf')"); err == nil {
		t.Fatal("the reader role must not read server files")
	}

	// Adding the primary again reuses the stored roles and changes nothing.
	if p, err := svc.Inspect(ctx, admin); err != nil || p.Mode != ModeReuseRoles {
		t.Fatalf("second inspection %+v %v", p, err)
	}

	// The standby shares the roles through replication.
	sbDSN := standbyAdminDSN(t, admin)
	var sp Plan
	deadline := time.Now().Add(30 * time.Second)
	for {
		sp, err = svc.Inspect(ctx, sbDSN)
		if err == nil && sp.Mode == ModeReuseRoles || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Second)
	}
	if err != nil || !sp.IsStandby || sp.Mode != ModeReuseRoles {
		t.Fatalf("standby plan %+v %v", sp, err)
	}
	sv, _, err := svc.Add(ctx, Request{DSN: sbDSN, Name: "standby"})
	if err != nil || sv.User != MonitorRole {
		t.Fatalf("standby target %+v %v", sv, err)
	}

	// A role without administrative privileges is used as is.
	limited := os.Getenv("LOCKLENS_IT_LIMITED_DSN")
	lp, err := svc.Inspect(ctx, limited)
	if err != nil || lp.Mode != ModeDirect || !lp.WorkspaceAllowed || lp.SeesAllSessions {
		t.Fatalf("limited plan %+v %v", lp, err)
	}
	op, err := svc.Inspect(ctx, os.Getenv("LOCKLENS_IT_OBSERVER_DSN"))
	if err != nil || op.Mode != ModeDirect || !op.SeesAllSessions {
		t.Fatalf("observer plan %+v %v", op, err)
	}
	lv, _, err := svc.Add(ctx, Request{DSN: limited, Name: "limited", RunSQL: true})
	if err != nil {
		t.Fatal(err)
	}
	if lpol, _ := store.WorkspacePolicy(ctx, lv.ID); !lpol.Configured || !lpol.SQL || lpol.Data {
		t.Fatalf("direct workspace policy %+v", lpol)
	}

	// A wrong password is reported in the plan, without the password.
	bad, _ := url.Parse(limited)
	bad.User = url.UserPassword(bad.User.Username(), "cnry-wrong-password")
	bp, err := svc.Inspect(ctx, bad.String())
	if err != nil || bp.OK || bp.Error == nil || strings.Contains(*bp.Error, "cnry-wrong-password") {
		t.Fatalf("bad password plan %+v %v", bp, err)
	}
}
