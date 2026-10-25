package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// CheckStatus is the outcome of one doctor check.
type CheckStatus string

// Doctor check outcomes. A warning means LockLens works but its reports will
// be less complete; a failure means it cannot do its job.
const (
	StatusOK   CheckStatus = "ok"
	StatusWarn CheckStatus = "warn"
	StatusFail CheckStatus = "fail"
	StatusSkip CheckStatus = "skip"
)

// Check is one doctor result.
type Check struct {
	Name   string      `json:"name"`
	Status CheckStatus `json:"status"`
	Detail string      `json:"detail"`
	Hint   string      `json:"hint,omitempty"`
}

// accessProbes exercise each source LockLens reads. They are cheap and touch
// no user data.
var accessProbes = []struct {
	name  string
	query string
}{
	{"read pg_stat_activity", "SELECT count(*) FROM pg_catalog.pg_stat_activity"},
	{"read pg_locks", "SELECT count(*) FROM pg_catalog.pg_locks"},
	{"execute pg_blocking_pids()", "SELECT pg_catalog.pg_blocking_pids(pg_catalog.pg_backend_pid())"},
	{"read pg_prepared_xacts", "SELECT count(*) FROM pg_catalog.pg_prepared_xacts"},
}

// Diagnose runs the doctor checks after a successful connection. The
// connection check itself is performed by the caller, which owns Connect.
func (c *Collector) Diagnose(ctx context.Context, opts Options) []Check {
	var checks []Check
	fail := func(name string, err error) {
		checks = append(checks, Check{Name: name, Status: StatusFail, Detail: c.red.String(err.Error()), Hint: Hint(err)})
	}

	var srv serverInfo
	err := pgx.BeginTxFunc(ctx, c.pool, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		var err error
		srv, err = readServer(ctx, tx)
		return err
	})
	if err != nil {
		fail("server information", err)
		return checks
	}
	if srv.versionNum < MinServerVersionNum {
		checks = append(checks, Check{Name: "server version", Status: StatusFail,
			Detail: fmt.Sprintf("PostgreSQL %s; LockLens requires 14 or later", srv.version)})
		return checks
	}
	checks = append(checks, Check{Name: "server version", Status: StatusOK, Detail: "PostgreSQL " + srv.version})

	allAccess := true
	for _, p := range accessProbes {
		err := pgx.BeginTxFunc(ctx, c.pool, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, p.query)
			return err
		})
		if err != nil {
			allAccess = false
			fail(p.name, err)
			continue
		}
		checks = append(checks, Check{Name: p.name, Status: StatusOK, Detail: "allowed"})
	}

	if srv.readAllStats {
		checks = append(checks, Check{Name: "session visibility", Status: StatusOK,
			Detail: fmt.Sprintf("role %q can see details of all sessions (superuser or pg_read_all_stats)", c.target.User)})
	} else {
		checks = append(checks, Check{Name: "session visibility", Status: StatusWarn,
			Detail: fmt.Sprintf("role %q sees full details only for its own sessions; other roles' state, timings, and query text are hidden", c.target.User),
			Hint:   fmt.Sprintf("GRANT pg_read_all_stats TO %s; (or pg_monitor) for a complete report", quoteIdent(c.target.User))})
	}

	if srv.trackActivities {
		checks = append(checks, Check{Name: "track_activities", Status: StatusOK, Detail: "on"})
	} else {
		checks = append(checks, Check{Name: "track_activities", Status: StatusWarn,
			Detail: "off: session state, wait events, and query text are not reported by the server"})
	}

	if srv.readAllStats {
		checks = append(checks, Check{Name: "query text", Status: StatusOK,
			Detail: "visible for all sessions; LockLens only collects it with --include-query"})
	} else {
		checks = append(checks, Check{Name: "query text", Status: StatusWarn,
			Detail: "hidden for other roles' sessions even with --include-query"})
	}

	if !allAccess {
		checks = append(checks, Check{Name: "collect snapshot", Status: StatusSkip, Detail: "skipped because a required source is not readable"})
		return checks
	}
	snap, err := c.Collect(ctx, opts)
	if err != nil {
		fail("collect snapshot", err)
		return checks
	}
	blocked := map[int]bool{}
	for _, e := range snap.Edges {
		blocked[e.Waiter] = true
	}
	checks = append(checks, Check{Name: "collect snapshot", Status: StatusOK,
		Detail: fmt.Sprintf("%d ms; %d session(s) of interest, %d blocked", snap.Collection.DurationMS, len(snap.Sessions), len(blocked))})
	return checks
}

func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}
