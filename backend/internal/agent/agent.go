// Package agent is the LockLens log agent (`locklens agent`). It runs on
// the database host, follows the PostgreSQL server log files, turns log
// messages into events (package logs: statements normalised, messages
// redacted), and ships them to the LockLens server with an API token of
// scope "ingest". When the server cannot be reached, batches are kept in a
// spool directory and sent later; read positions are kept so a restart
// continues where it stopped.
package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/locklens/locklens/internal/domain/logs"
	"github.com/locklens/locklens/internal/lib/pglog"
)

// Config configures the agent.
type Config struct {
	// Server is the LockLens server's URL (https, or http for a local one).
	Server string
	Token  string
	// Paths are log files, directories (every *.log, *.csv, *.json file in
	// them), or glob patterns.
	Paths []string
	// Format is auto (from the file name), jsonlog, csvlog, or stderr.
	Format string
	// Prefix is log_line_prefix, for stderr logs.
	Prefix string
	// StateDir keeps read positions and the spool.
	StateDir string
	// FromStart reads files that existed when the agent first started from
	// their beginning (by default from their end).
	FromStart bool
	// FlushEvery is the longest time an event waits before it is sent.
	FlushEvery time.Duration
	// MaxSpool bounds the spool; the oldest batches are dropped beyond it.
	MaxSpool int64
	// AllowHTTP allows a non-local http:// server (the token is then sent
	// unencrypted).
	AllowHTTP bool
	Hostname  string
	Version   string
	Client    *http.Client
	Log       *slog.Logger
}

// Agent follows logs and ships events.
type Agent struct {
	cfg    Config
	prefix *pglog.Prefix
	files  *files
	spool  *spool
	ship   *shipper
	// every is how often the files are read.
	every time.Duration
}

// New checks the configuration and opens the state directory.
func New(cfg Config) (*Agent, error) {
	u, err := url.Parse(cfg.Server)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, errors.New("server must be an http(s) URL such as https://locklens.example.com")
	}
	if u.Scheme == "http" && !cfg.AllowHTTP && !isLocal(u.Hostname()) {
		return nil, errors.New("the API token would be sent unencrypted: use https, or allow http explicitly")
	}
	if cfg.Token == "" {
		return nil, errors.New("an API token of scope ingest is required")
	}
	if len(cfg.Paths) == 0 {
		return nil, errors.New("at least one log file or directory is required")
	}
	switch cfg.Format {
	case "", "auto", pglog.FormatJSON, pglog.FormatCSV, pglog.FormatStderr:
	default:
		return nil, fmt.Errorf("unknown log format %q (auto, jsonlog, csvlog, stderr)", cfg.Format)
	}
	if cfg.Prefix == "" {
		cfg.Prefix = pglog.DefaultPrefix
	}
	prefix, err := pglog.CompilePrefix(cfg.Prefix)
	if err != nil {
		return nil, err
	}
	if cfg.FlushEvery <= 0 {
		cfg.FlushEvery = 5 * time.Second
	}
	if cfg.MaxSpool <= 0 {
		cfg.MaxSpool = 256 << 20
	}
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: 30 * time.Second}
	}
	if err := os.MkdirAll(filepath.Join(cfg.StateDir, "spool"), 0o700); err != nil {
		return nil, fmt.Errorf("state directory: %w", err)
	}
	f, err := openFiles(cfg, prefix)
	if err != nil {
		return nil, err
	}
	return &Agent{cfg: cfg, prefix: prefix, files: f,
		spool: &spool{dir: filepath.Join(cfg.StateDir, "spool"), max: cfg.MaxSpool, log: cfg.Log},
		ship:  &shipper{cfg: cfg}, every: time.Second}, nil
}

// Run follows the logs until ctx ends.
func (a *Agent) Run(ctx context.Context) error {
	a.cfg.Log.Info("log agent started", "server", a.cfg.Server, "paths", a.cfg.Paths, "state_dir", a.cfg.StateDir)
	poll := time.NewTicker(a.every)
	defer poll.Stop()
	var pending []logs.Event
	lastFlush := time.Now()
	retryAt := time.Time{}
	for {
		select {
		case <-ctx.Done():
			pending = append(pending, a.files.flushAll()...)
			a.deliver(context.WithoutCancel(ctx), pending)
			return a.files.save()
		case <-poll.C:
		}
		events, err := a.files.poll()
		if err != nil {
			a.cfg.Log.Warn("reading logs failed", "error", err.Error())
		}
		pending = append(pending, events...)
		if len(pending) >= logs.MaxBatch/2 || len(pending) > 0 && time.Since(lastFlush) >= a.cfg.FlushEvery {
			a.deliver(ctx, pending)
			pending, lastFlush = nil, time.Now()
		}
		// Spooled batches go out once the server answers again.
		if time.Now().After(retryAt) && a.spool.pending() {
			if err := a.spool.drain(ctx, a.ship); err != nil {
				retryAt = time.Now().Add(30 * time.Second)
			}
		}
	}
}

// deliver ships events, or spools them when the server cannot take them,
// and then keeps the read positions: an event is never lost to a restart.
func (a *Agent) deliver(ctx context.Context, events []logs.Event) {
	for _, b := range batches(a.cfg, a.files.sources(), events) {
		if a.spool.pending() {
			a.spool.add(b) // keep the order: older batches first
			continue
		}
		if err := a.ship.send(ctx, b); err != nil {
			if errors.Is(err, errRejected) {
				a.cfg.Log.Warn("the server rejected a batch; it is dropped", "error", err.Error())
				continue
			}
			a.cfg.Log.Warn("sending failed; spooling", "error", err.Error())
			a.spool.add(b)
		}
	}
	if err := a.files.save(); err != nil {
		a.cfg.Log.Warn("saving read positions failed", "error", err.Error())
	}
}

func isLocal(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}
