package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/locklens/locklens/internal/domain/report"
	"github.com/locklens/locklens/internal/lib/render"
	"github.com/locklens/locklens/internal/targetdb"
)

// Terminal control used only when stdout is a TTY and the format is table:
// move the cursor home and clear the screen before each frame.
const clearScreen = "\x1b[H\x1b[2J"

// minInterval prevents accidental tight polling; every collection calls
// pg_blocking_pids(), which briefly takes the lock manager's locks.
const minInterval = 500 * time.Millisecond

// maxBackoff caps the delay between retries after consecutive failures.
const maxBackoff = 30 * time.Second

func (a *app) watchCommand() *cobra.Command {
	var (
		f        reportFlags
		interval time.Duration
	)
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Refresh the report periodically until interrupted",
		Long: `Collect and display a report every --interval until Ctrl+C.

Collections never overlap: the next one starts only after the previous one
finished, as soon as the interval since its start has elapsed. A failed
collection is reported on stderr and retried with exponential backoff
(capped at 30s); authentication, permission, and unsupported-version errors
stop the command.

With --format json, each line of stdout is one complete report (NDJSON).
Screen clearing is used only when stdout is a terminal and the format is table.`,
		Example: `  locklens watch
  locklens watch --interval 5s --min-tx-age 30s
  locklens watch --format json > incident.ndjson`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := f.validate(true); err != nil {
				return err
			}
			if interval < minInterval {
				return usagef("--interval must be at least %s", minInterval)
			}
			ctx, stop := signalContext(cmd.Context())
			defer stop()
			return a.watch(ctx, f, interval)
		},
	}
	f.register(cmd, true)
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "time between the starts of two collections")
	return cmd
}

func (a *app) watch(ctx context.Context, f reportFlags, interval time.Duration) error {
	_, connect, err := a.poolConfig()
	if err != nil {
		return err
	}
	tty := f.format == "table" && a.isTTY(a.stdout)

	var collector *targetdb.Collector
	defer func() {
		if collector != nil {
			collector.Close()
		}
	}()

	failures := 0
	frame := 0
	for {
		started := time.Now()
		err := func() error {
			if collector == nil {
				c, err := connect(ctx)
				if err != nil {
					return err
				}
				collector = c
			}
			cctx, cancel := context.WithTimeout(ctx, a.queryTimeout)
			defer cancel()
			snap, err := collector.Collect(cctx, f.options())
			if err != nil {
				return fmt.Errorf("collect snapshot: %w", err)
			}
			frame++
			return a.writeFrame(report.Build(snap, f.minTxAge), f.format, tty, frame, interval)
		}()

		// Ctrl+C or SIGTERM: whatever failed was caused by the cancellation,
		// so exit quietly with success.
		if ctx.Err() != nil {
			return nil
		}

		delay := interval - time.Since(started)
		if err != nil {
			if targetdb.IsFatal(err) {
				return err
			}
			failures++
			delay = backoff(interval, failures)
			a.log.Error("collection failed; retrying", "attempt", failures, "retry_in", delay.String(),
				"error", a.red.String(err.Error()), "hint", targetdb.Hint(err))
		} else {
			if failures > 0 {
				a.log.Info("collection recovered", "after_failures", failures)
			}
			failures = 0
		}

		timer := time.NewTimer(max(delay, 0))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

// backoff returns interval·2^(failures-1), capped at max(maxBackoff, interval).
func backoff(interval time.Duration, failures int) time.Duration {
	limit := max(maxBackoff, interval)
	d := interval
	for i := 1; i < failures && d < limit; i++ {
		d *= 2
	}
	return min(d, limit)
}

func (a *app) writeFrame(r *report.Report, format string, tty bool, frame int, interval time.Duration) error {
	if format == "json" {
		return render.NDJSONLine(a.stdout, r)
	}
	if tty {
		if _, err := fmt.Fprint(a.stdout, clearScreen); err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "locklens watch: every %s, press Ctrl+C to exit\n\n", interval)
	} else {
		if frame > 1 {
			fmt.Fprintln(a.stdout)
		}
		fmt.Fprintf(a.stdout, "=== frame %d ===\n", frame)
	}
	return render.Table(a.stdout, r)
}
