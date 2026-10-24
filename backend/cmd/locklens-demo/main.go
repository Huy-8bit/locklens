// Command locklens-demo creates real lock contention in the local demo
// database, runs `locklens inspect` against it, and cleans up.
//
// It writes to the database through the demo_app role and must only be used
// against the disposable PostgreSQL started by deploy/compose.yaml.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/locklens/locklens/demo/scenario"
)

const (
	defaultDemoDSN     = "postgres://demo_app:demo-app-pw@127.0.0.1:55432/locklens_demo?sslmode=disable"
	defaultObserverDSN = "postgres://locklens_observer:observer-pw@127.0.0.1:55432/locklens_demo?sslmode=disable"
)

func main() {
	os.Exit(run())
}

func run() int {
	fs := flag.NewFlagSet("locklens-demo", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `Usage: locklens-demo [flags] <row-lock|chain|idle|all>

Creates real lock contention in the local demo database, runs
"locklens inspect" against it, then rolls everything back.

Scenarios:
  row-lock  A updates row 1 and keeps the transaction open; B blocks on it (A -> B)
  chain     A holds row 1; B holds row 2 and waits for row 1; C waits for row 2 (A -> B -> C)
  idle      A updates a row and stays idle in transaction
  all       run the three scenarios one after another

Flags:
`)
		fs.PrintDefaults()
	}
	demoDSN := fs.String("demo-dsn", envOr("LOCKLENS_DEMO_DSN", defaultDemoDSN), "DSN of the role that creates contention (env LOCKLENS_DEMO_DSN)")
	observerDSN := fs.String("observer-dsn", envOr("LOCKLENS_DSN", defaultObserverDSN), "DSN passed to locklens via LOCKLENS_DSN (env LOCKLENS_DSN)")
	bin := fs.String("locklens", "./bin/locklens", "path to the locklens binary")
	idleAge := fs.Duration("idle-age", 3*time.Second, "how long the idle-in-transaction session waits before inspection; also used as --min-tx-age")
	hold := fs.Bool("hold", false, "keep the scenario running after inspection until Ctrl+C (try `locklens watch` meanwhile)")
	format := fs.String("format", "table", "locklens output format: table or json")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}

	var names []string
	switch fs.Arg(0) {
	case "row-lock", "chain", "idle":
		names = []string{fs.Arg(0)}
	case "all":
		names = []string{"row-lock", "chain", "idle"}
	default:
		fmt.Fprintf(os.Stderr, "unknown scenario %q\n", fs.Arg(0))
		fs.Usage()
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	for _, name := range names {
		if err := runScenario(ctx, name, *demoDSN, *observerDSN, *bin, *format, *idleAge, *hold); err != nil {
			fmt.Fprintf(os.Stderr, "locklens-demo: %v\n", err)
			return 1
		}
		if ctx.Err() != nil {
			break
		}
	}
	return 0
}

func runScenario(ctx context.Context, name, demoDSN, observerDSN, bin, format string, idleAge time.Duration, hold bool) error {
	fmt.Printf("\n### Scenario: %s\n", name)
	var (
		s        *scenario.Scenario
		err      error
		expected string
	)
	table := "demo_accounts"
	switch name {
	case "row-lock":
		s, err = scenario.RowLock(ctx, demoDSN, table)
		if err == nil {
			expected = fmt.Sprintf("A (pid %d) -> B (pid %d)", s.ByLabel("A").PID, s.ByLabel("B").PID)
		}
	case "chain":
		s, err = scenario.Chain(ctx, demoDSN, table)
		if err == nil {
			expected = fmt.Sprintf("A (pid %d) -> B (pid %d) -> C (pid %d); A blocks 1 directly and 2 in total",
				s.ByLabel("A").PID, s.ByLabel("B").PID, s.ByLabel("C").PID)
		}
	case "idle":
		s, err = scenario.IdleInTransaction(ctx, demoDSN, table, idleAge)
		if err == nil {
			expected = fmt.Sprintf("A (pid %d) idle in transaction for at least %s", s.ByLabel("A").PID, idleAge)
		}
	}
	if err != nil {
		return fmt.Errorf("set up %s: %w", name, err)
	}
	defer func() {
		if cerr := s.Close(context.Background()); cerr != nil {
			fmt.Fprintf(os.Stderr, "locklens-demo: cleanup: %v\n", cerr)
		} else {
			fmt.Printf("Cleaned up scenario %s (all demo transactions rolled back).\n", name)
		}
	}()

	var labels []string
	for _, sess := range s.Sessions {
		labels = append(labels, fmt.Sprintf("%s=pid %d", sess.Label, sess.PID))
	}
	fmt.Printf("Sessions: %s\nExpected: %s\n\n", strings.Join(labels, ", "), expected)

	minTxAge := idleAge
	if name != "idle" {
		minTxAge = time.Second
	}
	// The DSN goes through the environment, not the command line, so it does
	// not show up in the process list.
	cmd := exec.CommandContext(ctx, bin, "inspect", "--format", format, "--min-tx-age", minTxAge.String())
	cmd.Env = append(os.Environ(), "LOCKLENS_DSN="+observerDSN)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	fmt.Printf("$ LOCKLENS_DSN=... %s\n", strings.Join(cmd.Args, " "))
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run locklens: %w", err)
	}

	if hold {
		fmt.Printf("Holding scenario %s. In another terminal run:\n  LOCKLENS_DSN='%s' %s watch --min-tx-age %s\nPress Ctrl+C to clean up.\n",
			name, redactPassword(observerDSN), bin, minTxAge)
		<-ctx.Done()
	}
	return nil
}

// redactPassword hides the password when echoing the observer DSN.
// The demo uses fake local credentials, but the habit matters.
func redactPassword(dsn string) string {
	at := strings.Index(dsn, "@")
	scheme := strings.Index(dsn, "://")
	if at < 0 || scheme < 0 {
		return dsn
	}
	userinfo := dsn[scheme+3 : at]
	if colon := strings.Index(userinfo, ":"); colon >= 0 {
		return dsn[:scheme+3] + userinfo[:colon] + ":<password>" + dsn[at:]
	}
	return dsn
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
