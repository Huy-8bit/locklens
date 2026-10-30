// Package app wires and runs `locklens server`: the monitoring store,
// authentication, collectors, incident recorder, retention, and the HTTP API.
package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/locklens/locklens/internal/config"
	"github.com/locklens/locklens/internal/domain/auth"
	"github.com/locklens/locklens/internal/domain/baseline"
	"github.com/locklens/locklens/internal/domain/changes"
	"github.com/locklens/locklens/internal/domain/cluster"
	"github.com/locklens/locklens/internal/domain/collector"
	"github.com/locklens/locklens/internal/domain/events"
	"github.com/locklens/locklens/internal/domain/incidents"
	"github.com/locklens/locklens/internal/domain/insight"
	"github.com/locklens/locklens/internal/domain/logs"
	"github.com/locklens/locklens/internal/domain/model"
	"github.com/locklens/locklens/internal/domain/notify"
	"github.com/locklens/locklens/internal/domain/onboard"
	"github.com/locklens/locklens/internal/domain/optimizer"
	"github.com/locklens/locklens/internal/domain/retention"
	"github.com/locklens/locklens/internal/domain/rollup"
	"github.com/locklens/locklens/internal/domain/settings"
	"github.com/locklens/locklens/internal/domain/targets"
	"github.com/locklens/locklens/internal/domain/tokens"
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
	store, err := OpenStore(ctx, cfg.StoreDSN, &cfg.StoreTimescale, log)
	if err != nil {
		return err
	}
	defer store.Close()

	if err := bootstrapAdmin(ctx, store, cfg, log); err != nil {
		return err
	}

	store.SetRawRetention(cfg.HistoryRetention)
	metrics := obs.NewMetrics()
	// Events reach this server's browsers through the hub and, over the
	// cluster bus, the browsers of the other servers sharing the store.
	hub := events.NewHub(100, func(n int) { metrics.SSEClients.Set(float64(n)) })
	clusterRepos := cluster.Repositories{ClusterRepository: store}
	node := cluster.New(clusterRepos, serverID(cfg), hostname(), version, log)
	bus := cluster.NewBus(node, clusterRepos, hub, log)

	st, err := settings.Load(ctx, settings.Repositories{SettingRepository: store}, cfg.DemoMode,
		func() { bus.Send(messageSettingsChanged, nil) })
	if err != nil {
		return fmt.Errorf("load settings: %w", err)
	}
	notifier := notify.New(notify.Repositories{NotificationRepository: store, IncidentRepository: store, TargetRepository: store, FindingRepository: store},
		box, log, cfg.PublicURL, version)
	baselines := baseline.New(baseline.Repositories{RollupRepository: store, TargetRepository: store},
		func() baseline.Params { return insight.BaselineParams(st.Thresholds()) }, log)
	// Incident events go to the browsers and to notifications (sent by the
	// server that records the incident).
	recorder := incidents.NewRecorder(incidents.Repositories{IncidentRepository: store, FindingRepository: store},
		incidents.DefaultConfig(), st, baselines, metrics, events.Fanout{bus, notifier}, log)
	collectorRepos := collector.Repositories{ChangeRepository: store, ClusterRepository: store, CollectionRepository: store,
		LoadRepository: store, OptimizerRepository: store, TargetRepository: store}
	writer := collector.NewWriter(collectorRepos, cfg.WriteQueue, metrics, log)
	manager := collector.NewManager(collector.Deps{Store: collectorRepos, Box: box, Writer: writer, Recorder: recorder, Events: bus,
		Settings: st, Cluster: node, Bus: bus, Metrics: metrics, Log: log},
		collector.Config{MaxConcurrent: cfg.MaxConcurrent, Version: version, StatementsInterval: statementsInterval(cfg), LoadInterval: loadInterval(cfg),
			BurstDuration: burstDuration(cfg), BudgetPercent: cfg.CollectorBudgetPercent})
	tsvc := targets.New(targets.Repositories{CollectionRepository: store, TargetRepository: store}, box, manager, version)
	ws := workspace.New(workspace.Repositories{WorkspaceRepository: store, TargetRepository: store}, box)
	opt := optimizer.New(optimizer.Repositories{OptimizerRepository: store, PlanRepository: store, QueryRepository: store, TargetRepository: store},
		ws, bus, log)

	if cfg.BootstrapTargets != "" {
		if err := bootstrapTargets(ctx, tsvc, ws, bootstrapStore{TargetRepository: store, WorkspaceRepository: store}, cfg.BootstrapTargets, log); err != nil {
			return err
		}
	}

	// Requests from the other servers.
	bus.Handle(collector.MessageRunSoon, func(raw json.RawMessage) {
		var m struct {
			TargetID  int64  `json:"target_id"`
			Collector string `json:"collector"`
		}
		if json.Unmarshal(raw, &m) == nil {
			manager.RunSoonHere(m.TargetID, m.Collector)
		}
	})
	bus.Handle(messageSettingsChanged, func(json.RawMessage) {
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if err := st.Reload(rctx); err != nil {
			log.Warn("reloading settings changed on another server failed", "error", err.Error())
		}
	})

	handler := httpapi.New(httpapi.Deps{
		Store: httpapi.Repositories{AuthRepository: store, FindingRepository: store, HealthRepository: store, IncidentRepository: store,
			LoadRepository: store, MetricRollupRepository: store, OptimizerRepository: store, PlanRepository: store, QueryRepository: store,
			RetentionRepository: store, SeriesRepository: store, SnapshotRepository: store, TargetRepository: store, TimescaleRepository: store,
			TopologyRepository: store, WorkspaceRepository: store},
		Auth:      auth.New(auth.Repositories{AuthRepository: store}, cfg.SessionIdle, cfg.CookieSecure),
		Targets:   tsvc,
		Onboard:   onboard.New(onboard.Repositories{ManagedRoleRepository: store}, box, tsvc, ws),
		Workspace: ws,
		Manager:   manager,
		Settings:  st,
		Notify:    notifier,
		Baselines: baselines,
		Insight: insight.New(insight.Repositories{ChangeRepository: store, FindingRepository: store, LoadRepository: store,
			LogRepository: store, PlanRepository: store, SeriesRepository: store}, cfg.LoadSampling),
		Optimizer: opt,
		Changes:   changes.New(changes.Repositories{ChangeRepository: store}),
		Tokens:    tokens.New(tokens.Repositories{TokenRepository: store}),
		Logs:      logs.New(logs.Repositories{ChangeRepository: store, LogRepository: store}),
		Cluster:   node,
		Events:    bus,
		Stream:    hub,
		Metrics:   metrics, Log: log, Tool: model.Tool{Name: "locklens", Version: version},
		TrustedProxies: cfg.TrustedProxies, TrustedProxyHosts: cfg.TrustedProxyHosts,
		MetricsEnabled: cfg.MetricsEnabled, DemoMode: cfg.DemoMode,
	})

	srv := &http.Server{Addr: cfg.Listen, Handler: handler, ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout: 60 * time.Second, IdleTimeout: 120 * time.Second, MaxHeaderBytes: 64 << 10}
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.Listen, err)
	}

	bg, cancelBG := context.WithCancel(context.WithoutCancel(ctx))
	// Every server runs these.
	tasks := []func(context.Context){
		node.Run,
		bus.Run,
		writer.Run,
		notifier.Run,
		manager.Run,
	}
	// One server of a cluster runs each of these at a time.
	singletons := map[string]func(context.Context){
		"baselines": func(ctx context.Context) { baselines.Run(ctx, baselineRefresh(cfg)) },
		"rollups": func(ctx context.Context) {
			rollup.Run(ctx, rollup.Repositories{MetricRollupRepository: store, TargetRepository: store}, time.Minute, cfg.HistoryRetention, log)
		},
		"optimizer": func(ctx context.Context) { opt.Run(ctx, optimizerEvery(cfg)) },
		"retention": func(ctx context.Context) {
			retention.Run(ctx, retention.Repositories{RetentionRepository: store}, repository.RetentionPolicy{History: cfg.HistoryRetention, Incidents: cfg.IncidentRetention,
				Findings: cfg.FindingRetention, Rollups: cfg.BaselineRetention,
				MetricMinute: cfg.MinuteRollupRetention, MetricHour: cfg.HourRollupRetention}, time.Hour, log)
		},
		"notification-reminders": notifier.RunReminders,
	}
	for name, run := range singletons {
		tasks = append(tasks, func(ctx context.Context) { node.Singleton(ctx, name, run) })
	}
	done := make(chan struct{}, len(tasks))
	for _, run := range tasks {
		go func() { run(bg); done <- struct{}{} }()
	}

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
	for range tasks {
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

// statementsInterval is LOCKLENS_STATEMENTS_INTERVAL (15 s by default); the
// lab never samples pg_stat_statements less often than every 15 s so the
// shorter demo windows have enough collections.
func statementsInterval(cfg config.Config) time.Duration {
	if cfg.DemoMode {
		return min(cfg.StatementsInterval, 15*time.Second)
	}
	return cfg.StatementsInterval
}

// baselineRefresh is how often rollups and baselines are brought up to date.
func baselineRefresh(cfg config.Config) time.Duration {
	if cfg.DemoMode {
		return time.Minute
	}
	return 5 * time.Minute
}

// burstDuration is how long a target is collected faster during a problem,
// or 0 when adaptive sampling is off.
func burstDuration(cfg config.Config) time.Duration {
	if !cfg.AdaptiveSampling {
		return 0
	}
	return cfg.BurstDuration
}

// optimizerEvery is how often recommendations are derived (the catalog is
// collected every 15 minutes).
func optimizerEvery(cfg config.Config) time.Duration {
	if cfg.DemoMode {
		return 2 * time.Minute
	}
	return 15 * time.Minute
}

// loadInterval is the active session sampling interval, or 0 when sampling
// is turned off.
func loadInterval(cfg config.Config) time.Duration {
	if !cfg.LoadSampling {
		return 0
	}
	return cfg.LoadSampleInterval
}

// messageSettingsChanged tells the other servers to reload the settings.
const messageSettingsChanged = "settings.changed"

// serverID is LOCKLENS_SERVER_ID, or the host name with a random suffix.
func serverID(cfg config.Config) string {
	if cfg.ServerID != "" {
		return cfg.ServerID
	}
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hostname() + "-" + hex.EncodeToString(b)
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "locklens"
	}
	return h
}
