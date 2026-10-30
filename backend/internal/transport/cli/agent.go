package cli

import (
	"errors"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/locklens/locklens/internal/agent"
	"github.com/locklens/locklens/internal/config"
	"github.com/locklens/locklens/internal/lib/buildinfo"
	"github.com/locklens/locklens/internal/lib/logger"
)

func (a *app) agentCommand() *cobra.Command {
	var cfg agent.Config
	var tokenFile string
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Follow PostgreSQL server logs and ship events to a LockLens server",
		Long: `Run the LockLens log agent on the database host.

It follows the PostgreSQL log files and sends slow statements, auto_explain
plans, lock waits, deadlocks, autovacuum runs, temporary files, checkpoints,
and errors to the LockLens server. Statement texts are normalised (constants
replaced by $1, $2, ...) and error messages redacted before they leave the
host. It never connects to PostgreSQL.

The API token (scope "ingest", created in Settings → API tokens for one
instance) comes from $LOCKLENS_AGENT_TOKEN or --token-file, never from a
flag, so it does not show in the process list.

Stops gracefully on SIGINT or SIGTERM.`,
		Example: `  LOCKLENS_AGENT_TOKEN=llk_... locklens agent --server https://locklens.example.com \
    --log /var/lib/postgresql/data/log --state-dir /var/lib/locklens-agent`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg.Token = strings.TrimSpace(config.Getenv("LOCKLENS_AGENT_TOKEN"))
			if tokenFile != "" {
				raw, err := os.ReadFile(tokenFile)
				if err != nil {
					return usagef("--token-file: %v", err)
				}
				cfg.Token = strings.TrimSpace(string(raw))
			}
			if cfg.Token == "" {
				return &usageError{err: errors.New("set $LOCKLENS_AGENT_TOKEN or --token-file")}
			}
			if cfg.Server == "" {
				cfg.Server = config.Getenv("LOCKLENS_SERVER_URL")
			}
			level, err := parseLevel(a.logLevel)
			if err != nil {
				return &usageError{err: err}
			}
			cfg.Log = logger.New(os.Stderr, level, "text")
			cfg.Version = buildinfo.Get().Version
			if cfg.Hostname == "" {
				cfg.Hostname, _ = os.Hostname()
			}
			ag, err := agent.New(cfg)
			if err != nil {
				return &usageError{err: err}
			}
			ctx, stop := signalContext(cmd.Context())
			defer stop()
			return ag.Run(ctx)
		},
	}
	f := cmd.Flags()
	f.StringVar(&cfg.Server, "server", "", "LockLens server URL (default: $LOCKLENS_SERVER_URL)")
	f.StringVar(&tokenFile, "token-file", "", "file holding the API token (default: $LOCKLENS_AGENT_TOKEN)")
	f.StringSliceVar(&cfg.Paths, "log", nil, "log file, directory, or glob pattern (repeatable)")
	f.StringVar(&cfg.Format, "format", "auto", "log format: auto (from the file name), jsonlog, csvlog, or stderr")
	f.StringVar(&cfg.Prefix, "log-line-prefix", "%m [%p] ", "the server's log_line_prefix, for stderr logs")
	f.StringVar(&cfg.StateDir, "state-dir", "locklens-agent", "directory for read positions and the spool")
	f.BoolVar(&cfg.FromStart, "from-start", false, "on the first start, read existing log files from their beginning")
	f.DurationVar(&cfg.FlushEvery, "flush-interval", 5*time.Second, "longest time an event waits before it is sent")
	f.Int64Var(&cfg.MaxSpool, "max-spool-bytes", 256<<20, "spool size limit while the server is unreachable")
	f.BoolVar(&cfg.AllowHTTP, "allow-http", false, "allow an http:// server that is not local (sends the token unencrypted)")
	f.StringVar(&cfg.Hostname, "hostname", "", "name reported to the server (default: the host name)")
	return cmd
}
