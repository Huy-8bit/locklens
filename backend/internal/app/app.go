// Package app wires and runs `locklens server`: the monitoring store,
// authentication, collectors, incident recorder, retention, and the HTTP API.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/locklens/locklens/internal/config"
	"github.com/locklens/locklens/internal/domain/auth"
	"github.com/locklens/locklens/internal/domain/collector"
	"github.com/locklens/locklens/internal/domain/incidents"
	"github.com/locklens/locklens/internal/domain/model"
	"github.com/locklens/locklens/internal/domain/onboard"
	"github.com/locklens/locklens/internal/domain/retention"
	"github.com/locklens/locklens/internal/domain/settings"
	"github.com/locklens/locklens/internal/domain/targets"
	"github.com/locklens/locklens/internal/domain/workspace"
	"github.com/locklens/locklens/internal/lib/obs"
	"github.com/locklens/locklens/internal/lib/secret"
	"github.com/locklens/locklens/internal/repository"
	"github.com/locklens/locklens/internal/transport/httpapi"
)

// Run starts the server and blocks until ctx is cancelled, then shuts down
// gracefully within cfg.ShutdownTimeout.
func Run(ctx context.Context, cfg config.Config, version string, log *slog.Logger) error {
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
	hub := httpapi.NewHub(100, func(n int) { metrics.SSEClients.Set(float64(n)) })
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
	ws := workspace.New(store, box)

	if cfg.BootstrapTargets != "" {
		if err := bootstrapTargets(ctx, tsvc, ws, store, cfg.BootstrapTargets, log); err != nil {
			return err
		}
	}

	tool := model.Tool{Name: "locklens", Version: version}
	handler := httpapi.New(httpapi.Deps{Workspace: ws, Store: store, Auth: auth.NewService(store, cfg.SessionIdle, cfg.CookieSecure), Targets: tsvc,
		Onboard: onboard.New(store, box, tsvc, ws),
		Manager: manager, Settings: st, Hub: hub, Metrics: metrics, Log: log, Tool: tool, TrustedProxies: cfg.TrustedProxies, TrustedProxyHosts: cfg.TrustedProxyHosts,
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
		retention.Run(bg, store, repository.RetentionPolicy{History: cfg.HistoryRetention, Incidents: cfg.IncidentRetention,
			Findings: cfg.FindingRetention}, time.Hour, log)
		done <- struct{}{}
	}()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()
	log.Info("LockLens server started", "listen", ln.Addr().String(), "version", version, "trusted_proxies", len(cfg.TrustedProxies)+len(cfg.TrustedProxyHosts),
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

// statementsInterval is 60 s in production; the lab samples pg_stat_statements
// every 15 s so the shorter demo windows have enough collections.
func statementsInterval(cfg config.Config) time.Duration {
	if cfg.DemoMode {
		return 15 * time.Second
	}
	return 60 * time.Second
}
