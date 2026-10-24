// Command locklens-lab runs incident lab scenarios against the LockLens lab
// databases (deploy/compose.yaml). It writes fake data and must never be
// pointed at a real database.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/locklens/locklens/demo/lab"
)

func usage() {
	fmt.Fprint(os.Stderr, `Usage:
  locklens-lab run <scenario> [--duration D]   run a scenario, then clean up
  locklens-lab status                          show lab sessions and objects
  locklens-lab stop                            stop every lab session and undo lab changes
  locklens-lab list                            list scenarios

Environment: LOCKLENS_LAB_DSN (data owner), LOCKLENS_LAB_ADMIN_DSN (superuser
on the lab primary), LOCKLENS_LAB_STANDBY_ADMIN_DSN (superuser on the lab
standby). All lab credentials are fake, local-only values.
`)
}

func main() { os.Exit(run()) }

func run() int {
	if len(os.Args) < 2 {
		usage()
		return 2
	}
	cfg := lab.Config{DSN: os.Getenv("LOCKLENS_LAB_DSN"), AdminDSN: os.Getenv("LOCKLENS_LAB_ADMIN_DSN"),
		StandbyAdminDSN: os.Getenv("LOCKLENS_LAB_STANDBY_ADMIN_DSN"), Out: os.Stdout}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	switch os.Args[1] {
	case "list":
		for _, s := range lab.Scenarios {
			fmt.Printf("%-20s %-8s %s\n", s.Name, s.Default, s.Description)
		}
		fmt.Printf("%-20s %-8s %s\n", "outage", "host", "stop the primary, then start it again (make lab-outage)")
		return 0
	case "status":
		return check(lab.Status(ctx, cfg))
	case "stop":
		return check(lab.Stop(context.WithoutCancel(ctx), cfg))
	case "run":
		fs := flag.NewFlagSet("run", flag.ContinueOnError)
		d := fs.Duration("duration", 0, "how long to keep the scenario running (default per scenario, max 1h)")
		if len(os.Args) < 3 {
			usage()
			return 2
		}
		sc, ok := lab.Find(os.Args[2])
		if !ok {
			fmt.Fprintf(os.Stderr, "unknown scenario %q (see locklens-lab list)\n", os.Args[2])
			return 2
		}
		if err := fs.Parse(os.Args[3:]); err != nil {
			return 2
		}
		dur := sc.Default
		if *d > 0 {
			dur = *d
		}
		if dur > time.Hour {
			fmt.Fprintln(os.Stderr, "--duration is limited to 1h")
			return 2
		}
		if cfg.DSN == "" || cfg.AdminDSN == "" {
			fmt.Fprintln(os.Stderr, "LOCKLENS_LAB_DSN and LOCKLENS_LAB_ADMIN_DSN are required")
			return 2
		}
		sctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		err := lab.Setup(sctx, cfg)
		cancel()
		if err != nil {
			return check(err)
		}
		fmt.Printf("scenario %s: %s (duration %s)\n", sc.Name, sc.Description, dur)
		err = sc.Run(ctx, cfg, dur)
		if ctx.Err() != nil {
			fmt.Println("interrupted; cleaned up")
			return 0
		}
		if err == nil {
			fmt.Printf("scenario %s finished and cleaned up\n", sc.Name)
		}
		return check(err)
	default:
		usage()
		return 2
	}
}

func check(err error) int {
	if err != nil {
		fmt.Fprintln(os.Stderr, "locklens-lab:", err)
		return 1
	}
	return 0
}
