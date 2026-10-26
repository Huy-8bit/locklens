// Package httpapi serves LockLens' REST API, server-sent events, and the
// health and metrics endpoints. The web UI is a separate image.
//
// All /api/v1 routes except login and session require an authenticated
// session; mutations also require the CSRF header. Responses never contain
// connection strings, passwords, or raw internal errors.
package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"github.com/locklens/locklens/internal/domain/auth"
	"github.com/locklens/locklens/internal/domain/collector"
	"github.com/locklens/locklens/internal/domain/model"
	"github.com/locklens/locklens/internal/domain/notify"
	"github.com/locklens/locklens/internal/domain/onboard"
	"github.com/locklens/locklens/internal/domain/settings"
	"github.com/locklens/locklens/internal/domain/targets"
	"github.com/locklens/locklens/internal/domain/workspace"
	"github.com/locklens/locklens/internal/lib/obs"
	"github.com/locklens/locklens/internal/repository"
)

// Limits on requests.
const (
	MaxBodyBytes   = 1 << 20
	MaxImportBytes = 10 << 20
	MaxRange       = 31 * 24 * time.Hour
	MaxPageSize    = 200
	maxPoints      = 400
)

// Deps are the server's collaborators.
type Deps struct {
	Workspace *workspace.Service
	Onboard   *onboard.Service
	Store     *repository.Store
	Auth      *auth.Service
	Targets   *targets.Service
	Manager   *collector.Manager
	Settings  *settings.Service
	Notify    *notify.Service
	Hub       *Hub
	Metrics   *obs.Metrics
	Log       *slog.Logger
	Tool      model.Tool
	// TrustedProxies are reverse proxies whose X-Forwarded-For is believed.
	TrustedProxies []netip.Prefix
	// TrustedProxyHosts are proxy host names, resolved every 30 seconds.
	TrustedProxyHosts []string
	// LookupHost resolves TrustedProxyHosts (net.DefaultResolver when nil).
	LookupHost func(ctx context.Context, host string) ([]string, error)
	// MetricsEnabled exposes /metrics.
	MetricsEnabled bool
	// DemoMode shows the lab guide in the UI.
	DemoMode bool
}

// Server is the HTTP handler.
type Server struct {
	d   Deps
	mux *http.ServeMux

	proxyMu       sync.Mutex
	proxyAddrs    map[netip.Addr]bool
	proxyResolved time.Time
}

// New builds the server and its routes.
func New(d Deps) *Server {
	s := &Server{d: d, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) routes() {
	m := s.mux
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	m.HandleFunc("GET /readyz", s.ready)
	if s.d.MetricsEnabled {
		m.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
			_ = s.d.Metrics.Registry.WriteText(w)
		})
	}

	m.HandleFunc("POST /api/v1/auth/login", s.login)
	m.HandleFunc("POST /api/v1/auth/logout", s.authed(s.logout))
	m.HandleFunc("GET /api/v1/session", s.authed(s.session))
	m.HandleFunc("PUT /api/v1/session/password", s.authed(s.changeOwnPassword))

	// Users (admin only).
	m.HandleFunc("GET /api/v1/users", s.allow(auth.PermManageUsers, s.listUsers))
	m.HandleFunc("POST /api/v1/users", s.allow(auth.PermManageUsers, s.createUser))
	m.HandleFunc("PUT /api/v1/users/{id}", s.allow(auth.PermManageUsers, s.updateUser))
	m.HandleFunc("DELETE /api/v1/users/{id}", s.allow(auth.PermManageUsers, s.deleteUser))

	// Reads need any role (every role can view); changes name their permission.

	m.HandleFunc("GET /api/v1/fleet", s.authed(s.fleet))
	m.HandleFunc("GET /api/v1/fleet/metrics", s.authed(s.fleetMetrics))
	m.HandleFunc("GET /api/v1/fleet/queries", s.authed(s.fleetQueries))
	m.HandleFunc("GET /api/v1/fleet/live", s.authed(s.fleetLive))
	m.HandleFunc("GET /api/v1/system", s.authed(s.system))
	m.HandleFunc("GET /api/v1/topology", s.authed(s.topology))
	m.HandleFunc("PUT /api/v1/topology", s.allow(auth.PermManageSettings, s.putTopology))
	m.HandleFunc("DELETE /api/v1/topology/{id}", s.allow(auth.PermManageSettings, s.deleteTopology))
	m.HandleFunc("GET /api/v1/targets/{id}/history-bounds", s.authed(s.historyBounds))
	m.HandleFunc("GET /api/v1/targets/{id}/workspace/policy", s.authed(s.workspacePolicy))
	m.HandleFunc("PUT /api/v1/targets/{id}/workspace/policy", s.allow(auth.PermManageTargets, s.putWorkspacePolicy))
	m.HandleFunc("POST /api/v1/targets/{id}/workspace/run", s.authed(s.workspaceRun))
	m.HandleFunc("POST /api/v1/targets/{id}/workspace/cancel", s.authed(s.workspaceCancel))
	m.HandleFunc("GET /api/v1/targets", s.authed(s.listTargets))
	m.HandleFunc("POST /api/v1/targets", s.allow(auth.PermManageTargets, s.createTarget))
	m.HandleFunc("POST /api/v1/targets/test", s.allow(auth.PermManageTargets, s.testNewTarget))
	m.HandleFunc("POST /api/v1/targets/inspect", s.allow(auth.PermManageTargets, s.inspectConnection))
	m.HandleFunc("POST /api/v1/targets/quick", s.allow(auth.PermManageTargets, s.quickAddTarget))
	m.HandleFunc("GET /api/v1/targets/{id}", s.authed(s.getTarget))
	m.HandleFunc("PUT /api/v1/targets/{id}", s.allow(auth.PermManageTargets, s.updateTarget))
	m.HandleFunc("DELETE /api/v1/targets/{id}", s.allow(auth.PermManageTargets, s.deleteTarget))
	m.HandleFunc("POST /api/v1/targets/{id}/test", s.allow(auth.PermManageTargets, s.testTarget))
	m.HandleFunc("GET /api/v1/targets/{id}/overview", s.authed(s.targetOverview))
	m.HandleFunc("GET /api/v1/targets/{id}/metrics", s.authed(s.targetMetrics))
	m.HandleFunc("GET /api/v1/targets/{id}/activity", s.authed(s.targetActivity))
	m.HandleFunc("GET /api/v1/targets/{id}/blocking/snapshots", s.authed(s.listSnapshots))
	m.HandleFunc("GET /api/v1/targets/{id}/blocking/snapshots/{sid}", s.authed(s.getSnapshot))
	m.HandleFunc("GET /api/v1/targets/{id}/load", s.authed(s.targetLoad))
	m.HandleFunc("GET /api/v1/targets/{id}/queries", s.authed(s.listQueries))
	m.HandleFunc("GET /api/v1/targets/{id}/queries/commands", s.authed(s.queryCommands))
	m.HandleFunc("GET /api/v1/targets/{id}/queries/{qid}", s.authed(s.getQuery))
	m.HandleFunc("GET /api/v1/targets/{id}/tables", s.authed(s.targetTables))
	m.HandleFunc("GET /api/v1/targets/{id}/replication", s.authed(s.targetReplication))
	m.HandleFunc("GET /api/v1/targets/{id}/host", s.authed(s.targetHost))
	m.HandleFunc("GET /api/v1/targets/{id}/findings", s.authed(s.targetFindings))
	m.HandleFunc("GET /api/v1/targets/{id}/gaps", s.authed(s.targetGaps))

	m.HandleFunc("GET /api/v1/incidents", s.authed(s.listIncidents))
	m.HandleFunc("POST /api/v1/incidents/import", s.allow(auth.PermManageSettings, s.importIncident))
	m.HandleFunc("GET /api/v1/incidents/{id}", s.authed(s.getIncident))
	m.HandleFunc("GET /api/v1/incidents/{id}/events", s.authed(s.incidentEvents))
	m.HandleFunc("POST /api/v1/incidents/{id}/acknowledge", s.allow(auth.PermAcknowledge, s.acknowledgeIncident))
	m.HandleFunc("GET /api/v1/incidents/{id}/export", s.authed(s.exportIncident))

	m.HandleFunc("POST /api/v1/snapshots/analyze", s.authed(s.analyzeSnapshot))
	m.HandleFunc("GET /api/v1/notifications/channels", s.allow(auth.PermManageSettings, s.listChannels))
	m.HandleFunc("POST /api/v1/notifications/channels", s.allow(auth.PermManageSettings, s.saveChannel))
	m.HandleFunc("PUT /api/v1/notifications/channels/{id}", s.allow(auth.PermManageSettings, s.saveChannel))
	m.HandleFunc("DELETE /api/v1/notifications/channels/{id}", s.allow(auth.PermManageSettings, s.deleteChannel))
	m.HandleFunc("POST /api/v1/notifications/channels/{id}/test", s.allow(auth.PermManageSettings, s.testChannel))
	m.HandleFunc("GET /api/v1/notifications/deliveries", s.authed(s.listDeliveries))
	m.HandleFunc("GET /api/v1/notifications/silences", s.authed(s.listSilences))
	m.HandleFunc("POST /api/v1/notifications/silences", s.allow(auth.PermAcknowledge, s.createSilence))
	m.HandleFunc("DELETE /api/v1/notifications/silences/{id}", s.allow(auth.PermAcknowledge, s.endSilence))
	m.HandleFunc("GET /api/v1/settings/rules", s.authed(s.getRuleSettings))
	m.HandleFunc("PUT /api/v1/settings/rules", s.allow(auth.PermManageSettings, s.putRuleSettings))
	m.HandleFunc("GET /api/v1/storage", s.authed(s.storageStats))
	m.HandleFunc("GET /api/v1/events", s.authed(func(w http.ResponseWriter, r *http.Request) { s.d.Hub.ServeHTTP(w, r) }))

	m.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, http.StatusNotFound, "not_found", "unknown API route")
	})
	m.HandleFunc("/", s.notAPI)
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := s.d.Store.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable", "reason": "monitoring store unreachable"})
		return
	}
	v, err := s.d.Store.SchemaVersion(ctx)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable", "reason": "schema version unreadable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ready", "schema_version": v})
}

// ui serves the embedded single-page app, falling back to index.html for
// client-side routes.
// notAPI answers every path outside /api/ and the operational endpoints. The
// web UI is a separate image (the frontend) that proxies /api/ here.
func (s *Server) notAPI(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, http.StatusNotFound, "not_found", "this is the LockLens backend: the API is under /api/; the web UI is served by the frontend")
}
