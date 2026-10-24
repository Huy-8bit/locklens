package cli

import (
	"github.com/spf13/cobra"

	"github.com/locklens/locklens/internal/render"
	"github.com/locklens/locklens/internal/report"
)

func (a *app) inspectCommand() *cobra.Command {
	var f reportFlags
	cmd := &cobra.Command{
		Use:   "inspect",
		Short: "Collect and show blocking chains and long-running transactions once",
		Example: `  LOCKLENS_DSN='postgres://observer@localhost:5432/app' locklens inspect
  locklens inspect --format json | jq '.analysis.summary'
  locklens inspect --min-tx-age 30s --include-query --query-max-length 120`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := f.validate(true); err != nil {
				return err
			}
			ctx, stop := signalContext(cmd.Context())
			defer stop()
			snap, err := a.collectOnce(ctx, f.options())
			if err != nil {
				return err
			}
			return a.writeReport(report.Build(snap, f.minTxAge), f.format)
		},
	}
	f.register(cmd, true)
	return cmd
}

func (a *app) writeReport(r *report.Report, format string) error {
	if format == "json" {
		return render.JSON(a.stdout, r)
	}
	return render.Table(a.stdout, r)
}
