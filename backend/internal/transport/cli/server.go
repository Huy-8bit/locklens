package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	server "github.com/locklens/locklens/internal/app"
	"github.com/locklens/locklens/internal/config"
	"github.com/locklens/locklens/internal/domain/auth"
	"github.com/locklens/locklens/internal/domain/export"
	"github.com/locklens/locklens/internal/lib/buildinfo"
	"github.com/locklens/locklens/internal/lib/logger"
	"github.com/locklens/locklens/internal/lib/snapshot"
)

func (a *app) serverCommand() *cobra.Command {
	var listen string
	cmd := &cobra.Command{
		Use:   "server",
		Short: "Run the LockLens server: API, web UI, collectors, and incident recorder",
		Long: `Run the LockLens monitoring app.

Configuration comes from environment variables (see docs/configuration.md):
LOCKLENS_STORE_DSN and LOCKLENS_MASTER_KEY are required, and
LOCKLENS_ADMIN_PASSWORD (or LOCKLENS_ADMIN_PASSWORD_HASH) is required on the
first start. The server listens on 127.0.0.1:8080 by default.

Stops gracefully on SIGINT or SIGTERM.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			getenv := config.Getenv
			if listen != "" {
				getenv = func(k string) string {
					if k == "LOCKLENS_LISTEN" {
						return listen
					}
					return config.Getenv(k)
				}
			}
			cfg, err := config.LoadConfig(getenv)
			if err != nil {
				return &usageError{err: err}
			}
			ctx, stop := signalContext(cmd.Context())
			defer stop()
			log := logger.New(os.Stderr, logger.Level(cfg.LogLevel), cfg.LogFormat)
			return server.Run(ctx, cfg, buildinfo.Get().Version, log)
		},
	}
	cmd.Flags().StringVar(&listen, "listen", "", "listen address (overrides LOCKLENS_LISTEN)")
	return cmd
}

func (a *app) migrateCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "migrate",
		Short: "Apply monitoring store schema migrations and exit",
		Long:  "Connects to LOCKLENS_STORE_DSN and applies pending migrations. The server does this automatically at start.",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dsn := config.Getenv("LOCKLENS_STORE_DSN")
			if dsn == "" {
				return usagef("LOCKLENS_STORE_DSN is required")
			}
			ts, err := config.LoadStoreTimescale(config.Getenv)
			if err != nil {
				return usagef("%s", err)
			}
			ctx, stop := signalContext(cmd.Context())
			defer stop()
			store, err := server.OpenStore(ctx, dsn, &ts, a.log)
			if err != nil {
				return err
			}
			defer store.Close()
			v, err := store.SchemaVersion(ctx)
			if err != nil {
				return err
			}
			fmt.Fprintf(a.stdout, "Monitoring store schema version %d\n", v)
			return nil
		},
	}
}

func (a *app) adminCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "admin", Short: "Administrator utilities", Args: noArgs}
	cmd.AddCommand(&cobra.Command{
		Use:   "hash-password",
		Short: "Read a password from stdin and print its bcrypt hash",
		Long: `Read one line from standard input and print a bcrypt hash suitable for
LOCKLENS_ADMIN_PASSWORD_HASH, so the plain password need not be stored in the
server's environment. Example:

  printf '%s' 'a long admin password' | locklens admin hash-password`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			line, err := bufio.NewReader(os.Stdin).ReadString('\n')
			if err != nil && line == "" {
				return usagef("no password on standard input")
			}
			hash, err := auth.HashPassword(strings.TrimRight(line, "\r\n"))
			if err != nil {
				return &usageError{err: err}
			}
			fmt.Fprintln(a.stdout, hash)
			return nil
		},
	})
	return cmd
}

func (a *app) incidentCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "incident", Short: "Export incidents and render offline reports", Args: noArgs}
	var (
		id           int64
		format, out  string
		includeQuery bool
		force        bool
	)
	exp := &cobra.Command{
		Use:   "export --id N --out FILE",
		Short: "Export an incident from the monitoring store as JSON or standalone HTML",
		Long: `Read an incident directly from the monitoring store (LOCKLENS_STORE_DSN) and
write it as versioned JSON or as a standalone HTML report that opens without
a backend. Query text is excluded unless --include-query is given (and only
exists where the target collected it).`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if id <= 0 || out == "" {
				return usagef("--id and --out are required")
			}
			if format != "json" && format != "html" {
				return usagef("--format must be json or html")
			}
			if !force {
				if _, err := os.Lstat(out); err == nil {
					return usagef("%s already exists (use --force to overwrite)", out)
				}
			}
			dsn := config.Getenv("LOCKLENS_STORE_DSN")
			if dsn == "" {
				return usagef("LOCKLENS_STORE_DSN is required")
			}
			ctx, stop := signalContext(cmd.Context())
			defer stop()
			octx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			store, err := server.OpenStore(octx, dsn, nil, a.log)
			if err != nil {
				return err
			}
			defer store.Close()
			e, err := export.Build(octx, store, id, includeQuery, tool())
			if err != nil {
				return fmt.Errorf("export incident %d: %w", id, err)
			}
			return writeExport(out, format, e, force)
		},
	}
	exp.Flags().Int64Var(&id, "id", 0, "incident ID")
	exp.Flags().StringVar(&format, "format", "json", "json or html")
	exp.Flags().StringVarP(&out, "out", "o", "", "destination file")
	exp.Flags().BoolVar(&includeQuery, "include-query", false, "include query text collected by targets that opted in")
	exp.Flags().BoolVar(&force, "force", false, "overwrite the destination")

	var reportOut string
	var reportForce bool
	rep := &cobra.Command{
		Use:   "report FILE --out report.html",
		Short: "Render an exported incident JSON file as standalone HTML (offline)",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if reportOut == "" {
				return usagef("--out is required")
			}
			if !reportForce {
				if _, err := os.Lstat(reportOut); err == nil {
					return usagef("%s already exists (use --force to overwrite)", reportOut)
				}
			}
			f, err := os.Open(args[0])
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return usagef("incident file %s does not exist", args[0])
				}
				return err
			}
			defer f.Close()
			e, err := export.Decode(f)
			if err != nil {
				return &usageError{err: fmt.Errorf("%s: %w", args[0], err)}
			}
			return writeExport(reportOut, "html", e, reportForce)
		},
	}
	rep.Flags().StringVarP(&reportOut, "out", "o", "", "destination HTML file")
	rep.Flags().BoolVar(&reportForce, "force", false, "overwrite the destination")
	cmd.AddCommand(exp, rep)
	return cmd
}

// writeExport writes atomically (temporary file, then rename or link).
func writeExport(path, format string, e export.Incident, force bool) error {
	var buf strings.Builder
	var err error
	if format == "html" {
		err = export.WriteHTML(&buf, e)
	} else {
		err = export.WriteJSON(&buf, e)
	}
	if err != nil {
		return err
	}
	if err := snapshot.WriteFileAtomic(path, []byte(buf.String()), force); err != nil {
		if errors.Is(err, snapshot.ErrExists) {
			return &usageError{err: err}
		}
		return err
	}
	return nil
}
