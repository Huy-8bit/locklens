package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/locklens/locklens/internal/api"
	"github.com/locklens/locklens/internal/auth"
	"github.com/locklens/locklens/internal/collector"
	"github.com/locklens/locklens/internal/incidents"
	"github.com/locklens/locklens/internal/model"
	"github.com/locklens/locklens/internal/obs"
	"github.com/locklens/locklens/internal/retention"
	"github.com/locklens/locklens/internal/secret"
	"github.com/locklens/locklens/internal/settings"
	"github.com/locklens/locklens/internal/storage"
	"github.com/locklens/locklens/internal/targets"
	"github.com/locklens/locklens/internal/webui"
)

// NewLogger builds the structured logger. Logs go to stderr.
func NewLogger(level, format string) *slog.Logger {
	var l slog.Level
	_ = l.UnmarshalText([]byte(level))
	opts := &slog.HandlerOptions{Level: l}
	if format == "json" {
		return slog.New(slog.NewJSONHandler(os.Stderr, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stderr, opts))
}

// OpenStore connects to the store and applies migrations.
func OpenStore(ctx context.Context, dsn string, log *slog.Logger) (*storage.Store, error) {
	octx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	store, err := storage.Open(octx, dsn, storage.Options{ConnectTimeout: 10 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("open monitoring store: %w", err)
	}
	applied, err := store.Migrate(octx)
	if err != nil {
		store.Close()
		return nil, fmt.Errorf("migrate monitoring store: %w", err)
	}
	if len(applied) > 0 {
		log.Info("monitoring store migrated", "applied_versions", applied)
	}
	return store, nil
}

// Run starts the server and blocks until ctx is cancelled, then shuts down
// gracefully within cfg.ShutdownTimeout.
func Run(ctx context.Context, cfg Config, version string, log *slog.Logger) error {
	key, err := secret.ParseKey(cfg.MasterKey)
	if err != nil {
		return fmt.Errorf("LOCKLENS_MASTER_KEY: %w", err)
	}
	box, err := secret.New(key)
	if err != nil {
		return err
	}
	store, err := OpenStore(ctx, cfg.StoreDSN, log)
	if err != nil {
		return err
	}
	defer store.Close()

	if err := bootstrapAdmin(ctx, store, cfg, log); err != nil {
		return err
	}

	metrics := obs.NewMetrics()
	hub := api.NewHub(100, func(n int) { metrics.SSEClients.Set(float64(n)) })
	st, err := settings.Load(ctx, store, cfg.DemoMode)
	if err != nil {
		return fmt.Errorf("load settings: %w", err)
	}
	writer := collector.NewWriter(store, cfg.WriteQueue, metrics, log)
	manager := collector.NewManager(store, box, writer, nil, hub, st, metrics, log,
		collector.Config{MaxConcurrent: cfg.MaxConcurrent, Version: version, StatementsInterval: statementsInterval(cfg)})
	recorder := incidents.NewRecorder(store, incidents.DefaultConfig(), st, metrics, hub, log)
	if err := recorder.Recover(ctx); err != nil {
		return fmt.Errorf("recover open incidents: %w", err)
	}
	manager.SetAnalyzer(recorder)
	tsvc := targets.NewService(store, box, manager, version)

	if cfg.BootstrapTargets != "" {
		if err := bootstrapTargets(ctx, tsvc, cfg.BootstrapTargets, log); err != nil {
			return err
		}
	}

	tool := model.Tool{Name: "locklens", Version: version}
	handler := api.New(api.Deps{Store: store, Auth: auth.NewService(store, cfg.SessionIdle, cfg.CookieSecure), Targets: tsvc,
		Manager: manager, Settings: st, Hub: hub, Metrics: metrics, Log: log, Tool: tool, UI: webui.FS(),
		MetricsEnabled: cfg.MetricsEnabled, DemoMode: cfg.DemoMode})

	srv := &http.Server{Addr: cfg.Listen, Handler: handler, ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout: 60 * time.Second, IdleTimeout: 120 * time.Second, MaxHeaderBytes: 64 << 10}
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.Listen, err)
	}

	bg, cancelBG := context.WithCancel(context.WithoutCancel(ctx))
	done := make(chan struct{}, 3)
	go func() { writer.Run(bg); done <- struct{}{} }()
	go func() { manager.Run(bg); done <- struct{}{} }()
	go func() {
		retention.Run(bg, store, storage.RetentionPolicy{History: cfg.HistoryRetention, Incidents: cfg.IncidentRetention,
			Findings: cfg.FindingRetention}, time.Hour, log)
		done <- struct{}{}
	}()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()
	log.Info("LockLens server started", "listen", ln.Addr().String(), "version", version, "ui_embedded", webui.FS() != nil,
		"cookie_secure", cfg.CookieSecure, "metrics", cfg.MetricsEnabled, "demo_mode", cfg.DemoMode)
	if !cfg.CookieSecure && !strings.HasPrefix(cfg.Listen, "127.") && !strings.HasPrefix(cfg.Listen, "localhost") {
		log.Warn("session cookies are not marked Secure; put LockLens behind TLS or set LOCKLENS_COOKIE_SECURE=true")
	}

	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-errCh:
	}
	log.Info("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	_ = srv.Shutdown(sctx)
	cancelBG()
	for range 3 {
		select {
		case <-done:
		case <-sctx.Done():
			log.Warn("background tasks did not stop within the shutdown timeout")
			return serveErr
		}
	}
	if errors.Is(serveErr, http.ErrServerClosed) {
		return nil
	}
	return serveErr
}

func bootstrapAdmin(ctx context.Context, store *storage.Store, cfg Config, log *slog.Logger) error {
	hash := cfg.AdminPasswordHash
	if hash != "" && !auth.IsBcryptHash(hash) {
		return errors.New("LOCKLENS_ADMIN_PASSWORD_HASH is not a bcrypt hash (create one with: locklens admin hash-password)")
	}
	if cfg.AdminPassword != "" {
		existing, err := store.AdminByUsername(ctx, cfg.AdminUser)
		if err == nil && auth.VerifyPassword(existing.PasswordHash, cfg.AdminPassword) {
			hash = existing.PasswordHash
		} else {
			if hash, err = auth.HashPassword(cfg.AdminPassword); err != nil {
				return fmt.Errorf("LOCKLENS_ADMIN_PASSWORD: %w", err)
			}
		}
	}
	if hash == "" {
		if _, err := store.AdminByUsername(ctx, cfg.AdminUser); err == nil {
			return nil
		}
		return errors.New("no administrator configured: set LOCKLENS_ADMIN_PASSWORD (or LOCKLENS_ADMIN_PASSWORD_HASH) for the first start")
	}
	prev, prevErr := store.AdminByUsername(ctx, cfg.AdminUser)
	if _, err := store.UpsertAdmin(ctx, cfg.AdminUser, hash); err != nil {
		return fmt.Errorf("bootstrap admin: %w", err)
	}
	if prevErr == nil && prev.PasswordHash != hash {
		// A changed password logs everybody out.
		if err := store.DeleteAllSessions(ctx); err != nil {
			return err
		}
		log.Info("admin password changed; existing sessions were ended")
	}
	return nil
}

func bootstrapTargets(ctx context.Context, svc *targets.Service, path string, log *slog.Logger) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("LOCKLENS_BOOTSTRAP_TARGETS_FILE: %w", err)
	}
	defer f.Close()
	var entries []targets.BootstrapEntry
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&entries); err != nil {
		return fmt.Errorf("LOCKLENS_BOOTSTRAP_TARGETS_FILE: invalid JSON: %w", err)
	}
	n, err := svc.Bootstrap(ctx, entries, Getenv)
	if err != nil {
		return err
	}
	if n > 0 {
		log.Info("bootstrap targets created", "count", n)
	}
	return nil
}

// statementsInterval is 60 s in production; the lab samples pg_stat_statements
// every 15 s so the shorter demo windows have enough collections.
func statementsInterval(cfg Config) time.Duration {
	if cfg.DemoMode {
		return 15 * time.Second
	}
	return 60 * time.Second
}
