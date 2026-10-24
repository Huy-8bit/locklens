// Package cli implements the locklens command-line interface.
//
// Output contract: reports go to stdout; diagnostics, logs, and errors go to
// stderr, so `--format json` output can always be piped into a JSON parser.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/locklens/locklens/internal/buildinfo"
	"github.com/locklens/locklens/internal/config"
	"github.com/locklens/locklens/internal/model"
	"github.com/locklens/locklens/internal/postgres"
)

// Exit codes. They are part of the documented interface.
const (
	ExitOK      = 0 // success, including when blocking was found
	ExitRuntime = 1 // connection, permission, collection, or I/O failure
	ExitUsage   = 2 // invalid flags, arguments, configuration, or input file
)

// errInterrupted is returned when SIGINT or SIGTERM stops a one-shot command
// (inspect, snapshot save) before it finished.
var errInterrupted = errors.New("interrupted")

// usageError marks errors caused by invalid input.
type usageError struct{ err error }

func (e *usageError) Error() string { return e.err.Error() }
func (e *usageError) Unwrap() error { return e.err }

func usagef(format string, args ...any) error {
	return &usageError{err: fmt.Errorf(format, args...)}
}

// app holds state shared by all commands.
type app struct {
	stdout, stderr io.Writer
	log            *slog.Logger

	dsn            string
	connectTimeout time.Duration
	queryTimeout   time.Duration
	logLevel       string

	// red is set once a DSN has been parsed, so errors printed at the end of
	// a run are redacted too.
	red *config.Redactor
	// isTTY reports whether stdout is a terminal; replaceable in tests.
	isTTY func(io.Writer) bool
}

// Main runs the CLI and returns the process exit code.
func Main(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	a := &app{stdout: stdout, stderr: stderr, isTTY: isTerminal}
	root := a.rootCommand()
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)

	err := root.ExecuteContext(ctx)
	if err == nil {
		return ExitOK
	}
	code := ExitRuntime
	var ue *usageError
	if errors.As(err, &ue) || isCobraUsageError(err) {
		code = ExitUsage
	}
	msg := a.red.String(err.Error())
	fmt.Fprintf(stderr, "locklens: error: %s\n", msg)
	if hint := postgres.Hint(err); hint != "" {
		fmt.Fprintf(stderr, "hint: %s\n", hint)
	}
	if code == ExitUsage && !errors.As(err, &ue) {
		fmt.Fprintln(stderr, "Run 'locklens --help' for usage.")
	}
	return code
}

// isCobraUsageError recognises argument errors produced inside cobra itself
// (unknown commands), which cobra does not expose as a typed error.
func isCobraUsageError(err error) bool {
	msg := err.Error()
	return strings.HasPrefix(msg, "unknown command") || strings.HasPrefix(msg, "unknown flag") ||
		strings.HasPrefix(msg, "unknown shorthand flag")
}

func (a *app) rootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "locklens",
		Short: "Investigate PostgreSQL lock contention",
		Long: `LockLens shows which PostgreSQL sessions are blocked, who blocks them,
where each blocking chain starts, and which transactions have been open too long.

LockLens only observes: it runs read-only queries and never cancels or
terminates sessions.

The connection string is taken from --dsn, or else from the LOCKLENS_DSN
environment variable. Prefer the environment variable: command-line arguments
are visible to other users in the process list.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			level, err := parseLevel(a.logLevel)
			if err != nil {
				return err
			}
			a.log = slog.New(slog.NewTextHandler(a.stderr, &slog.HandlerOptions{Level: level}))
			if a.connectTimeout <= 0 || a.queryTimeout <= 0 {
				return usagef("--connect-timeout and --query-timeout must be positive")
			}
			return nil
		},
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return &usageError{err: err} })

	pf := root.PersistentFlags()
	pf.StringVar(&a.dsn, "dsn", "", "PostgreSQL connection string (default: $LOCKLENS_DSN)")
	pf.DurationVar(&a.connectTimeout, "connect-timeout", 5*time.Second, "timeout for establishing a connection")
	pf.DurationVar(&a.queryTimeout, "query-timeout", 10*time.Second, "timeout for one collection (also sent as statement_timeout)")
	pf.StringVar(&a.logLevel, "log-level", "info", "stderr log level: debug, info, warn, or error")

	root.AddCommand(a.doctorCommand(), a.inspectCommand(), a.watchCommand(), a.snapshotCommand(), a.versionCommand(),
		a.serverCommand(), a.migrateCommand(), a.adminCommand(), a.incidentCommand())
	return root
}

func parseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, usagef("invalid --log-level %q (want debug, info, warn, or error)", s)
}

// exactArgs wraps cobra.ExactArgs so wrong argument counts exit with code 2.
func exactArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := cobra.ExactArgs(n)(cmd, args); err != nil {
			return &usageError{err: err}
		}
		return nil
	}
}

func noArgs(cmd *cobra.Command, args []string) error {
	if err := cobra.NoArgs(cmd, args); err != nil {
		return &usageError{err: err}
	}
	return nil
}

// reportFlags are shared by inspect, watch, and snapshot save.
type reportFlags struct {
	format         string
	minTxAge       time.Duration
	queryMaxLength int
	includeQuery   bool
}

func (f *reportFlags) register(cmd *cobra.Command, withFormat bool) {
	fl := cmd.Flags()
	if withFormat {
		fl.StringVar(&f.format, "format", "table", "output format: table or json")
	}
	fl.DurationVar(&f.minTxAge, "min-tx-age", time.Minute, "report transactions open at least this long")
	fl.IntVar(&f.queryMaxLength, "query-max-length", 200, "maximum characters of query text kept per session (with --include-query)")
	fl.BoolVar(&f.includeQuery, "include-query", false, "collect SQL query text (off by default: it may contain sensitive data)")
}

const maxQueryLength = 1 << 20

func (f *reportFlags) validate(withFormat bool) error {
	if withFormat {
		if err := validateFormat(f.format); err != nil {
			return err
		}
	}
	if f.minTxAge < 0 {
		return usagef("--min-tx-age must not be negative")
	}
	if f.queryMaxLength < 1 || f.queryMaxLength > maxQueryLength {
		return usagef("--query-max-length must be between 1 and %d", maxQueryLength)
	}
	return nil
}

func (f *reportFlags) options() postgres.Options {
	return postgres.Options{IncludeQuery: f.includeQuery, QueryMaxLength: f.queryMaxLength, MinTxAge: f.minTxAge}
}

func validateFormat(format string) error {
	switch format {
	case "table", "json":
		return nil
	}
	return usagef("invalid --format %q (want table or json)", format)
}

// poolConfig resolves and parses the DSN. Failures are usage errors.
func (a *app) poolConfig() (*config.Target, func(context.Context) (*postgres.Collector, error), error) {
	dsn, err := config.ResolveDSN(a.dsn)
	if err != nil {
		return nil, nil, &usageError{err: err}
	}
	cfg, red, err := config.PoolConfig(dsn, config.Options{ConnectTimeout: a.connectTimeout, QueryTimeout: a.queryTimeout})
	a.red = red
	if err != nil {
		return nil, nil, &usageError{err: err}
	}
	target := config.TargetOf(cfg)
	connect := func(ctx context.Context) (*postgres.Collector, error) {
		ctx, cancel := context.WithTimeout(ctx, a.connectTimeout+time.Second)
		defer cancel()
		// Each attempt needs a fresh copy: the pool takes ownership of the
		// config, and Connect installs hooks on it.
		c, err := postgres.Connect(ctx, cfg.Copy(), red, tool())
		if err != nil {
			return nil, fmt.Errorf("connect to %s: %w", target, err)
		}
		return c, nil
	}
	return &target, connect, nil
}

func tool() model.Tool {
	return model.Tool{Name: "locklens", Version: buildinfo.Get().Version}
}

// collectOnce connects, collects one snapshot, and disconnects.
func (a *app) collectOnce(ctx context.Context, opts postgres.Options) (*model.Snapshot, error) {
	_, connect, err := a.poolConfig()
	if err != nil {
		return nil, err
	}
	c, err := connect(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil, errInterrupted
		}
		return nil, err
	}
	defer c.Close()
	cctx, cancel := context.WithTimeout(ctx, a.queryTimeout)
	defer cancel()
	snap, err := c.Collect(cctx, opts)
	if err != nil {
		if ctx.Err() != nil {
			return nil, errInterrupted
		}
		return nil, fmt.Errorf("collect snapshot: %w", err)
	}
	return snap, nil
}
