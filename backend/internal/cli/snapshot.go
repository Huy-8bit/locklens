package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/locklens/locklens/internal/report"
	"github.com/locklens/locklens/internal/snapshot"
)

func (a *app) snapshotCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "snapshot",
		Short: "Save a snapshot to a file or show a saved snapshot offline",
		Args:  noArgs,
	}
	cmd.AddCommand(a.snapshotSaveCommand(), a.snapshotShowCommand())
	return cmd
}

func (a *app) snapshotSaveCommand() *cobra.Command {
	var (
		f     reportFlags
		out   string
		force bool
	)
	cmd := &cobra.Command{
		Use:   "save --out FILE",
		Short: "Collect one snapshot and write it as JSON",
		Long: `Collect one snapshot and write it to --out as JSON (schema version 1).

The file never contains the connection string or credentials. Query text is
stored only with --include-query. An existing file is not replaced unless
--force is given. The file is written atomically with mode 0600.`,
		Example: `  locklens snapshot save --out incident.json
  locklens snapshot save --out incident.json --include-query --force`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := f.validate(false); err != nil {
				return err
			}
			if out == "" {
				return usagef("--out is required")
			}
			// Fail before connecting; Save re-checks atomically.
			if !force {
				if _, err := os.Lstat(out); err == nil {
					return usagef("%s already exists (use --force to overwrite)", out)
				}
			}
			ctx, stop := signalContext(cmd.Context())
			defer stop()
			snap, err := a.collectOnce(ctx, f.options())
			if err != nil {
				return err
			}
			if err := snapshot.Save(out, snap, force); err != nil {
				if errors.Is(err, snapshot.ErrExists) {
					return &usageError{err: err}
				}
				return fmt.Errorf("save snapshot: %w", err)
			}
			fmt.Fprintf(a.stdout, "Snapshot saved to %s (%d sessions, %d blocking edges, collected %s)\n",
				out, len(snap.Sessions), len(snap.Edges), snap.CollectedAt.Format(time.RFC3339))
			return nil
		},
	}
	f.register(cmd, false)
	cmd.Flags().StringVarP(&out, "out", "o", "", "destination file (required)")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite the destination if it exists")
	return cmd
}

func (a *app) snapshotShowCommand() *cobra.Command {
	var (
		format   string
		minTxAge time.Duration
	)
	cmd := &cobra.Command{
		Use:   "show FILE",
		Short: "Render a saved snapshot without connecting to a database",
		Long: `Render a saved snapshot exactly like inspect, without a database connection.

The long-transaction threshold defaults to the one recorded in the file;
--min-tx-age overrides it. A lower threshold cannot reveal transactions that
were not recorded: the file holds client backends with an open transaction and
every session involved in blocking at collection time.`,
		Example: `  locklens snapshot show incident.json
  locklens snapshot show incident.json --format json | jq '.analysis.components'`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateFormat(format); err != nil {
				return err
			}
			if minTxAge < 0 {
				return usagef("--min-tx-age must not be negative")
			}
			if fi, err := os.Stat(args[0]); err == nil && fi.IsDir() {
				return usagef("%s is a directory, not a snapshot file", args[0])
			}
			snap, err := snapshot.Load(args[0])
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return usagef("snapshot file %s does not exist", args[0])
				}
				var inv *snapshot.InvalidError
				var unsup *snapshot.UnsupportedVersionError
				if errors.As(err, &inv) || errors.As(err, &unsup) {
					return &usageError{err: err}
				}
				return fmt.Errorf("read snapshot: %w", err)
			}
			threshold := time.Duration(snap.Collection.MinTxAgeSeconds * float64(time.Second))
			if cmd.Flags().Changed("min-tx-age") {
				threshold = minTxAge
			}
			return a.writeReport(report.Build(snap, threshold), format)
		},
	}
	cmd.Flags().StringVar(&format, "format", "table", "output format: table or json")
	cmd.Flags().DurationVar(&minTxAge, "min-tx-age", 0, "override the long-transaction threshold stored in the snapshot")
	return cmd
}
