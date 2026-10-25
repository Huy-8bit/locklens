package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/locklens/locklens/internal/lib/buildinfo"
	"github.com/locklens/locklens/internal/lib/render"
)

// signalContext is cancelled on SIGINT (Ctrl+C) or SIGTERM. Commands treat
// the cancellation as a request to stop, not as an error to report.
func signalContext(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
}

func contextWithTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, d)
}

// isTerminal reports whether w is a character device such as a terminal.
// Pipes, regular files, and /dev/null-style redirections are not.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0 && fi.Name() != os.DevNull
}

func (a *app) versionCommand() *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print version, commit, and build time",
		Args:  noArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := validateFormat(format); err != nil {
				return err
			}
			info := buildinfo.Get()
			if format == "json" {
				return render.JSON(a.stdout, info)
			}
			orUnknown := func(s string) string {
				if s == "" {
					return "unknown"
				}
				return s
			}
			_, err := fmt.Fprintf(a.stdout, "locklens %s\n  commit: %s\n  built:  %s\n  go:     %s\n",
				info.Version, orUnknown(info.Commit), orUnknown(info.Date), info.GoVersion)
			return err
		},
	}
	cmd.Flags().StringVar(&format, "format", "table", "output format: table or json")
	return cmd
}
