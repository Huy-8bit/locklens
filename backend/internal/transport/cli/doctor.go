package cli

import (
	"errors"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/locklens/locklens/internal/lib/render"
	"github.com/locklens/locklens/internal/lib/textsafe"
	"github.com/locklens/locklens/internal/targetdb"
)

// errDoctorFailed signals exit code 1 after the checks were printed.
var errDoctorFailed = errors.New("one or more checks failed")

type doctorResult struct {
	Target string           `json:"target"`
	OK     bool             `json:"ok"`
	Checks []targetdb.Check `json:"checks"`
}

func (a *app) doctorCommand() *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check connectivity, server version, and observation privileges",
		Long: `Check that LockLens can observe the target server:

  - connection and authentication
  - PostgreSQL version (14 or later)
  - access to pg_stat_activity, pg_locks, pg_blocking_pids(), pg_prepared_xacts
  - whether other roles' session details and query text are visible
  - track_activities
  - a full test collection

Warnings mean reports will be less complete; failures exit with code 1.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := validateFormat(format); err != nil {
				return err
			}
			target, connect, err := a.poolConfig()
			if err != nil {
				return err
			}
			ctx, stop := signalContext(cmd.Context())
			defer stop()

			res := doctorResult{Target: target.String(), OK: true}
			started := time.Now()
			c, err := connect(ctx)
			if err != nil {
				res.Checks = append(res.Checks, targetdb.Check{Name: "connection", Status: targetdb.StatusFail,
					Detail: a.red.String(err.Error()), Hint: targetdb.Hint(err)})
			} else {
				defer c.Close()
				res.Checks = append(res.Checks, targetdb.Check{Name: "connection", Status: targetdb.StatusOK,
					Detail: fmt.Sprintf("connected in %d ms", time.Since(started).Milliseconds())})
				dctx, cancel := contextWithTimeout(ctx, a.queryTimeout)
				res.Checks = append(res.Checks, c.Diagnose(dctx, targetdb.Options{QueryMaxLength: 200, MinTxAge: time.Minute})...)
				cancel()
			}
			for _, ch := range res.Checks {
				if ch.Status == targetdb.StatusFail {
					res.OK = false
				}
			}
			if err := a.writeDoctor(res, format); err != nil {
				return err
			}
			if !res.OK {
				return errDoctorFailed
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "table", "output format: table or json")
	return cmd
}

func (a *app) writeDoctor(res doctorResult, format string) error {
	if format == "json" {
		return render.JSON(a.stdout, res)
	}
	fmt.Fprintf(a.stdout, "LockLens doctor\n  Target: %s\n\n", textsafe.Terminal(res.Target))
	tw := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  STATUS\tCHECK\tDETAIL")
	for _, ch := range res.Checks {
		fmt.Fprintf(tw, "  %s\t%s\t%s\n", strings.ToUpper(string(ch.Status)), ch.Name, textsafe.Terminal(ch.Detail))
		if ch.Hint != "" {
			fmt.Fprintf(tw, "  \t\thint: %s\n", textsafe.Terminal(ch.Hint))
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if res.OK {
		fmt.Fprintln(a.stdout, "\nResult: LockLens can observe this server (see warnings above, if any).")
	} else {
		fmt.Fprintln(a.stdout, "\nResult: some checks failed.")
	}
	return nil
}
