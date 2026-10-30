package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/domain/auth"
	"github.com/locklens/locklens/internal/domain/baseline"
	"github.com/locklens/locklens/internal/domain/changes"
	"github.com/locklens/locklens/internal/domain/collector"
	"github.com/locklens/locklens/internal/domain/insight"
	"github.com/locklens/locklens/internal/domain/logs"
	"github.com/locklens/locklens/internal/domain/notify"
	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/domain/onboard"
	"github.com/locklens/locklens/internal/domain/optimizer"
	"github.com/locklens/locklens/internal/domain/rules"
	"github.com/locklens/locklens/internal/domain/settings"
	"github.com/locklens/locklens/internal/domain/targets"
	"github.com/locklens/locklens/internal/domain/tokens"
	"github.com/locklens/locklens/internal/domain/workspace"
	"github.com/locklens/locklens/internal/repository"
)

// The interfaces the HTTP layer needs from the rest of LockLens. app wires the
// implementations (the monitoring store and the domain services); tests can
// substitute fakes. Each lists only the methods the handlers call.

// Store is implemented by *repository.Store.
type Store interface {
	Timescale() repository.TimescaleStatus
	AcknowledgeIncident(ctx context.Context, id int64, by, note string) (repository.Incident, error)
	AuditWorkspace(ctx context.Context, id int64, actor, action, status string, duration time.Duration, rows int) error
	CommandBreakdown(ctx context.Context, targetID int64, from, to time.Time, step time.Duration, excludeUser string) ([]repository.CommandTotals, []repository.CommandBucket, []repository.CoverageBucket, error)
	CountPlans(ctx context.Context, targetID, queryID int64) (int, error)
	CountsByTarget(ctx context.Context) (map[int64]repository.TargetCounts, error)
	CreateIncident(ctx context.Context, in repository.Incident, events []repository.IncidentEvent) (int64, error)
	CreateUser(ctx context.Context, username, passwordHash, role string) (repository.User, error)
	DecideRecommendation(ctx context.Context, targetID, id int64, status, by, note string) (repository.Recommendation, error)
	DeletePlan(ctx context.Context, targetID, id int64) error
	DeleteTopologyLink(ctx context.Context, id int64) error
	DeleteUser(ctx context.Context, id int64) error
	GetActivitySnapshot(ctx context.Context, targetID, id int64) (repository.SnapshotMeta, json.RawMessage, error)
	GetIncident(ctx context.Context, id int64) (repository.Incident, error)
	GetPlan(ctx context.Context, targetID, id int64) (repository.PlanRow, error)
	GetRecommendation(ctx context.Context, targetID, id int64) (repository.Recommendation, error)
	GetTarget(ctx context.Context, id int64) (repository.Target, error)
	HistoryBounds(ctx context.Context, id int64) (map[string]any, error)
	IdentityKey(ctx context.Context, targetID, id int64) (observe.QueryKey, error)
	LatestActivitySnapshot(ctx context.Context, targetID int64) (repository.SnapshotMeta, json.RawMessage, error)
	LatestCatalog(ctx context.Context, targetID int64) (observe.Catalog, error)
	Catalogs(ctx context.Context, targetID int64) ([]observe.Catalog, error)
	LatestState(ctx context.Context, targetID int64, kind string) (time.Time, json.RawMessage, error)
	LatestTableHealth(ctx context.Context, targetID int64) (*observe.TableHealth, error)
	ListActivitySnapshots(ctx context.Context, targetID int64, from, to time.Time, onlyBlocking bool, limit int) ([]repository.SnapshotMeta, error)
	ListFindings(ctx context.Context, f repository.FindingFilter) ([]repository.FindingRow, error)
	ListGaps(ctx context.Context, targetID int64, from, to time.Time, limit int) ([]repository.Gap, error)
	ListIncidentEvents(ctx context.Context, id int64, afterID int64, limit int) ([]repository.IncidentEvent, error)
	ListIncidents(ctx context.Context, f repository.IncidentFilter) ([]repository.Incident, int, error)
	ListPlans(ctx context.Context, targetID, identityID int64) ([]repository.PlanRow, error)
	ListQueries(ctx context.Context, p repository.QueryListParams) ([]repository.QueryRow, int, error)
	ListRecommendations(ctx context.Context, f repository.RecommendationFilter) ([]repository.Recommendation, error)
	ListUsers(ctx context.Context) ([]repository.User, error)
	LoadRows(ctx context.Context, targetID int64, from, to time.Time, step time.Duration, dim repository.LoadDimension, f repository.LoadFilter) ([]repository.LoadRow, []repository.LoadTicks, error)
	Ping(ctx context.Context) error
	QueryBuckets(ctx context.Context, targetID, id int64, from, to time.Time, step time.Duration) ([]repository.QueryBucket, error)
	QueryIdentity(ctx context.Context, targetID, id int64, includeText bool) (repository.QueryRow, time.Time, time.Time, error)
	QueryRefs(ctx context.Context, targetID int64, keys []observe.QueryKey, includeText bool) ([]repository.QueryRef, error)
	QueryRefsByActivity(ctx context.Context, targetID int64, refs []repository.StatementRef, includeText bool) ([]repository.QueryRef, error)
	QuerySeries(ctx context.Context, targetID int64, metrics []string, from, to time.Time, step time.Duration) ([]repository.Series, error)
	QueryStep(from time.Time, step time.Duration) time.Duration
	SavePlan(ctx context.Context, targetID int64, p repository.PlanRow) (repository.PlanRow, error)
	SchemaVersion(ctx context.Context) (int, error)
	SeriesStep(from time.Time, step time.Duration) time.Duration
	SetTopologyLink(ctx context.Context, l repository.TopologyLink) error
	StatementCoverage(ctx context.Context, targetID int64, from, to time.Time) (repository.Coverage, error)
	TableSizes(ctx context.Context) ([]repository.StorageStats, error)
	TopologyLinks(ctx context.Context) ([]repository.TopologyLink, error)
	UpdateUser(ctx context.Context, id int64, c repository.UserChanges) (repository.User, error)
	UserByID(ctx context.Context, id int64) (repository.User, error)
	WindowTotals(ctx context.Context, targetID, id int64, from, to time.Time) (calls int64, totalMS float64, rows int64, err error)
	WorkspacePolicy(ctx context.Context, id int64) (repository.WorkspacePolicy, error)
}

// WorkspaceService is implemented by *workspace.Service.
type WorkspaceService interface {
	Cancel(owner string, target int64, id string) bool
	Configure(ctx context.Context, id int64, p repository.WorkspacePolicy, dsn string) error
	Run(ctx context.Context, id int64, owner, action, requestID string, fn func(context.Context, pgx.Tx) (any, error)) (out any, err error)
}

// OnboardService is implemented by *onboard.Service.
type OnboardService interface {
	Add(ctx context.Context, req onboard.Request) (targets.View, onboard.Plan, error)
	Inspect(ctx context.Context, dsn string) (onboard.Plan, error)
}

// AuthService is implemented by *auth.Service.
type AuthService interface {
	Authenticate(r *http.Request) (auth.Session, error)
	Login(ctx context.Context, w http.ResponseWriter, clientKey, username, password string) (auth.Session, error)
	Logout(ctx context.Context, w http.ResponseWriter, sess auth.Session) error
}

// TargetService is implemented by *targets.Service.
type TargetService interface {
	Create(ctx context.Context, in targets.Input) (targets.View, error)
	Delete(ctx context.Context, id int64, purge, keepIncidents bool) error
	Get(ctx context.Context, id int64) (targets.View, error)
	List(ctx context.Context) ([]targets.View, error)
	Test(ctx context.Context, id int64, dsn string) (targets.TestResult, error)
	Update(ctx context.Context, id int64, in targets.Input) (targets.View, error)
}

// CollectorManager is implemented by *collector.Manager.
type CollectorManager interface {
	RunSoon(targetID int64, collector string) bool
	View(ctx context.Context, id int64) (collector.View, bool)
}

// SettingsService is implemented by *settings.Service.
type SettingsService interface {
	LongTransactionThreshold() time.Duration
	Thresholds() rules.Thresholds
	UpdateThresholds(ctx context.Context, th rules.Thresholds) error
}

// NotifyService is implemented by *notify.Service.
type NotifyService interface {
	Channels(ctx context.Context) ([]repository.NotificationChannel, error)
	CreateSilence(ctx context.Context, in notify.SilenceInput, by string) (repository.Silence, error)
	DeleteChannel(ctx context.Context, id int64) error
	Deliveries(ctx context.Context, incidentID *int64, limit int) ([]repository.Delivery, error)
	EndSilence(ctx context.Context, id int64) error
	SaveChannel(ctx context.Context, id int64, in notify.ChannelInput) (repository.NotificationChannel, error)
	Silences(ctx context.Context, includeEnded bool) ([]repository.Silence, error)
	Test(ctx context.Context, id int64) (repository.Delivery, error)
}

// BaselineService is implemented by *baseline.Service.
type BaselineService interface {
	Get(targetID int64) map[observe.QueryKey]baseline.Baseline
	Load(ctx context.Context, targetID int64, ids []int64, from, to time.Time, p baseline.Params) (map[int64]baseline.Baseline, error)
}

// InsightService is implemented by *insight.Service.
type InsightService interface {
	Investigate(ctx context.Context, targetID int64, subject string, queryID *int64, at time.Time) (insight.Investigation, error)
}

// OptimizerService is implemented by *optimizer.Service.
type OptimizerService interface {
	Refresh(ctx context.Context, t repository.Target, now time.Time) (int, error)
	WhatIf(ctx context.Context, t repository.Target, recID int64, owner, requestID string) (optimizer.WhatIfResult, error)
}

var (
	_ Store            = (*repository.Store)(nil)
	_ WorkspaceService = (*workspace.Service)(nil)
	_ OnboardService   = (*onboard.Service)(nil)
	_ AuthService      = (*auth.Service)(nil)
	_ TargetService    = (*targets.Service)(nil)
	_ CollectorManager = (*collector.Manager)(nil)
	_ SettingsService  = (*settings.Service)(nil)
	_ NotifyService    = (*notify.Service)(nil)
	_ BaselineService  = (*baseline.Service)(nil)
	_ InsightService   = (*insight.Service)(nil)
	_ OptimizerService = (*optimizer.Service)(nil)
)

// ChangeService is implemented by *changes.Service.
type ChangeService interface {
	Delete(ctx context.Context, id int64) (observe.ChangeEvent, error)
	List(ctx context.Context, f repository.ChangeFilter) ([]observe.ChangeEvent, error)
	Record(ctx context.Context, r changes.Report, source, by string, now time.Time) (observe.ChangeEvent, error)
}

// TokenService is implemented by *tokens.Service.
type TokenService interface {
	Authenticate(ctx context.Context, secret, scope string, targetID int64) (repository.APIToken, error)
	Create(ctx context.Context, in tokens.Input, by string) (repository.APIToken, string, error)
	List(ctx context.Context) ([]repository.APIToken, error)
	Revoke(ctx context.Context, id int64) error
}

// ClusterInfo is implemented by *cluster.Node.
type ClusterInfo interface {
	ID() string
	Servers() []repository.Server
}

// LogService is implemented by *logs.Service.
type LogService interface {
	Events(ctx context.Context, f repository.LogEventFilter) ([]observe.LogEvent, error)
	Ingest(ctx context.Context, targetID, tokenID int64, b logs.Batch) (int64, error)
	Summarize(ctx context.Context, targetID int64, from, to time.Time, step time.Duration) (logs.Summary, error)
}
