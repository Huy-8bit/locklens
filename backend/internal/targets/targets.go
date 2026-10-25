// Package targets manages monitored PostgreSQL targets: validation,
// encryption of connection strings, connection tests, and redacted views.
//
// A target's DSN is write-only for clients: it is accepted on create and
// update, encrypted with the master key, and never returned.
package targets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/collector"
	"github.com/locklens/locklens/internal/config"
	"github.com/locklens/locklens/internal/hostmetrics"
	"github.com/locklens/locklens/internal/model"
	"github.com/locklens/locklens/internal/observe"
	"github.com/locklens/locklens/internal/postgres"
	"github.com/locklens/locklens/internal/secret"
	"github.com/locklens/locklens/internal/storage"
)

// Input is a create or update request. DSN may be empty on update to keep
// the stored one.
type Input struct {
	Name            string `json:"name"`
	Description     string `json:"description"`
	DSN             string `json:"dsn"`
	Enabled         *bool  `json:"enabled"`
	IntervalSeconds int    `json:"interval_seconds"`
	// QueryTextEnabled defaults to true on create and keeps the stored value
	// on update when omitted.
	QueryTextEnabled *bool   `json:"query_text_enabled"`
	MaxConnections   int     `json:"max_connections"`
	NodeExporterURL  *string `json:"node_exporter_url"`
}

// ValidationError lists problems with an input, keyed by field.
type ValidationError struct {
	Fields map[string]string
}

func (e *ValidationError) Error() string {
	parts := make([]string, 0, len(e.Fields))
	for k, v := range e.Fields {
		parts = append(parts, k+": "+v)
	}
	return "invalid target: " + strings.Join(parts, "; ")
}

// View is the redacted representation returned to clients.
type View struct {
	ID               int64      `json:"id"`
	Name             string     `json:"name"`
	Description      string     `json:"description"`
	Host             string     `json:"host"`
	Port             int        `json:"port"`
	Database         string     `json:"database"`
	User             string     `json:"user"`
	Enabled          bool       `json:"enabled"`
	IntervalSeconds  int        `json:"interval_seconds"`
	QueryTextEnabled bool       `json:"query_text_enabled"`
	MaxConnections   int        `json:"max_connections"`
	NodeExporterURL  *string    `json:"node_exporter_url"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	Status           StatusView `json:"status"`
}

// StatusView is the monitoring status of a target.
type StatusView struct {
	State            string                               `json:"state"`
	ServerVersion    *string                              `json:"server_version"`
	IsStandby        *bool                                `json:"is_standby"`
	Capabilities     *observe.Capabilities                `json:"capabilities"`
	Collectors       map[string]collector.CollectorStatus `json:"collectors"`
	LastAttemptAt    *time.Time                           `json:"last_attempt_at"`
	LastSuccessAt    *time.Time                           `json:"last_success_at"`
	LastError        *string                              `json:"last_error"`
	LastErrorAt      *time.Time                           `json:"last_error_at"`
	FreshnessSeconds *float64                             `json:"freshness_seconds"`
}

// Reloader is notified when targets change.
type Reloader interface{ Reload() }

// Service implements target management.
type Service struct {
	store   *storage.Store
	box     *secret.Box
	reload  Reloader
	version string
}

// NewService returns a service.
func NewService(store *storage.Store, box *secret.Box, reload Reloader, version string) *Service {
	return &Service{store: store, box: box, reload: reload, version: version}
}

func validName(s string) bool {
	if s == "" || utf8.RuneCountInString(s) > 100 {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func (s *Service) validate(in *Input, requireDSN bool) (config.Target, error) {
	fields := map[string]string{}
	in.Name = strings.TrimSpace(in.Name)
	if !validName(in.Name) {
		fields["name"] = "required, at most 100 characters, no control characters"
	}
	if utf8.RuneCountInString(in.Description) > 1000 {
		fields["description"] = "at most 1000 characters"
	}
	if in.IntervalSeconds == 0 {
		in.IntervalSeconds = 10
	}
	if in.IntervalSeconds < 5 || in.IntervalSeconds > 3600 {
		fields["interval_seconds"] = "must be between 5 and 3600"
	}
	if in.MaxConnections == 0 {
		in.MaxConnections = 2
	}
	if in.MaxConnections < 1 || in.MaxConnections > 5 {
		fields["max_connections"] = "must be between 1 and 5"
	}
	if in.NodeExporterURL != nil {
		u := strings.TrimSpace(*in.NodeExporterURL)
		if u == "" {
			in.NodeExporterURL = nil
		} else if err := hostmetrics.ValidateURL(u); err != nil {
			fields["node_exporter_url"] = err.Error()
		} else {
			in.NodeExporterURL = &u
		}
	}
	var tgt config.Target
	if in.DSN == "" {
		if requireDSN {
			fields["dsn"] = "required"
		}
	} else {
		cfg, _, err := config.PoolConfig(in.DSN, config.Options{})
		if err != nil {
			fields["dsn"] = err.Error()
		} else {
			tgt = config.TargetOf(cfg)
		}
	}
	if len(fields) > 0 {
		return tgt, &ValidationError{Fields: fields}
	}
	return tgt, nil
}

// Create validates, encrypts, and stores a new target.
func (s *Service) Create(ctx context.Context, in Input) (View, error) {
	tgt, err := s.validate(&in, true)
	if err != nil {
		return View{}, err
	}
	ct, err := s.box.Seal([]byte(in.DSN), []byte(collector.DSNAdditionalData))
	if err != nil {
		return View{}, err
	}
	enabled := in.Enabled == nil || *in.Enabled
	t, err := s.store.CreateTarget(ctx, storage.Target{Name: in.Name, Description: in.Description, DSNCiphertext: ct,
		DisplayHost: tgt.Host, DisplayPort: int(tgt.Port), DisplayDatabase: tgt.Database, DisplayUser: tgt.User,
		Enabled: enabled, IntervalSeconds: in.IntervalSeconds, QueryTextEnabled: in.QueryTextEnabled == nil || *in.QueryTextEnabled,
		MaxConnections: in.MaxConnections, NodeExporterURL: in.NodeExporterURL})
	if err != nil {
		return View{}, err
	}
	s.reload.Reload()
	return s.Get(ctx, t.ID)
}

// Update replaces a target's settings. An empty DSN keeps the stored one.
// Switching query-text collection off deletes stored statement texts.
func (s *Service) Update(ctx context.Context, id int64, in Input) (View, error) {
	cur, err := s.store.GetTarget(ctx, id)
	if err != nil {
		return View{}, err
	}
	tgt, err := s.validate(&in, false)
	if err != nil {
		return View{}, err
	}
	t := cur
	t.Name, t.Description, t.IntervalSeconds = in.Name, in.Description, in.IntervalSeconds
	t.MaxConnections, t.NodeExporterURL = in.MaxConnections, in.NodeExporterURL
	if in.QueryTextEnabled != nil {
		t.QueryTextEnabled = *in.QueryTextEnabled
	}
	if in.Enabled != nil {
		t.Enabled = *in.Enabled
	}
	if in.DSN != "" {
		ct, err := s.box.Seal([]byte(in.DSN), []byte(collector.DSNAdditionalData))
		if err != nil {
			return View{}, err
		}
		t.DSNCiphertext = ct
		t.DisplayHost, t.DisplayPort, t.DisplayDatabase, t.DisplayUser = tgt.Host, int(tgt.Port), tgt.Database, tgt.User
	}
	if _, err := s.store.UpdateTarget(ctx, t); err != nil {
		return View{}, err
	}
	if cur.QueryTextEnabled && !t.QueryTextEnabled {
		if err := s.store.ClearQueryTexts(ctx, id); err != nil {
			return View{}, err
		}
	}
	if !t.Enabled {
		st, err := s.store.GetTargetStatus(ctx, id)
		if err == nil {
			st.State = collector.StateDisabled
			_ = s.store.SaveTargetStatus(ctx, st)
		}
	}
	s.reload.Reload()
	return s.Get(ctx, id)
}

// Delete soft-deletes a target, or purges it and its history when purge is
// true (incidents are kept unless keepIncidents is false).
func (s *Service) Delete(ctx context.Context, id int64, purge, keepIncidents bool) error {
	var err error
	if purge {
		err = s.store.PurgeTarget(ctx, id, keepIncidents)
	} else {
		err = s.store.SoftDeleteTarget(ctx, id)
	}
	if err == nil {
		s.reload.Reload()
	}
	return err
}

// Get returns one target's view.
func (s *Service) Get(ctx context.Context, id int64) (View, error) {
	t, err := s.store.GetTarget(ctx, id)
	if err != nil {
		return View{}, err
	}
	st, err := s.store.GetTargetStatus(ctx, id)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return View{}, err
	}
	return toView(t, st), nil
}

// List returns all targets.
func (s *Service) List(ctx context.Context) ([]View, error) {
	ts, err := s.store.ListTargets(ctx)
	if err != nil {
		return nil, err
	}
	sts, err := s.store.ListTargetStatuses(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]View, 0, len(ts))
	for _, t := range ts {
		out = append(out, toView(t, sts[t.ID]))
	}
	return out, nil
}

func toView(t storage.Target, st storage.TargetStatus) View {
	v := View{ID: t.ID, Name: t.Name, Description: t.Description, Host: t.DisplayHost, Port: t.DisplayPort,
		Database: t.DisplayDatabase, User: t.DisplayUser, Enabled: t.Enabled, IntervalSeconds: t.IntervalSeconds,
		QueryTextEnabled: t.QueryTextEnabled, MaxConnections: t.MaxConnections, NodeExporterURL: t.NodeExporterURL,
		CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt}
	v.Status.State = st.State
	if v.Status.State == "" {
		v.Status.State = collector.StatePending
	}
	if !t.Enabled {
		v.Status.State = collector.StateDisabled
	}
	v.Status.ServerVersion, v.Status.IsStandby = st.ServerVersion, st.IsStandby
	v.Status.LastAttemptAt, v.Status.LastSuccessAt = st.LastAttemptAt, st.LastSuccessAt
	v.Status.LastError, v.Status.LastErrorAt = st.LastError, st.LastErrorAt
	if len(st.Capabilities) > 2 {
		var c observe.Capabilities
		if json.Unmarshal(st.Capabilities, &c) == nil {
			v.Status.Capabilities = &c
		}
	}
	v.Status.Collectors = map[string]collector.CollectorStatus{}
	if len(st.Collectors) > 2 {
		_ = json.Unmarshal(st.Collectors, &v.Status.Collectors)
	}
	if st.LastSuccessAt != nil {
		f := time.Since(*st.LastSuccessAt).Seconds()
		v.Status.FreshnessSeconds = &f
	}
	return v
}

// TestResult is the outcome of a connection test.
type TestResult struct {
	OK           bool                  `json:"ok"`
	DurationMS   int64                 `json:"duration_ms"`
	Target       string                `json:"target"`
	Capabilities *observe.Capabilities `json:"capabilities"`
	Checks       []postgres.Check      `json:"checks"`
	Error        *string               `json:"error"`
	Hint         *string               `json:"hint"`
}

// Test connects with dsn (or the stored DSN of target id when dsn is empty),
// detects capabilities, and runs the doctor checks. Nothing is stored.
func (s *Service) Test(ctx context.Context, id int64, dsn string) (TestResult, error) {
	if dsn == "" {
		t, err := s.store.GetTarget(ctx, id)
		if err != nil {
			return TestResult{}, err
		}
		plain, err := s.box.Open(t.DSNCiphertext, []byte(collector.DSNAdditionalData))
		if err != nil {
			return TestResult{}, err
		}
		dsn = string(plain)
	}
	cfg, red, err := config.PoolConfig(dsn, config.Options{ConnectTimeout: 5 * time.Second, QueryTimeout: 15 * time.Second})
	if err != nil {
		return TestResult{}, &ValidationError{Fields: map[string]string{"dsn": err.Error()}}
	}
	cfg.MaxConns = 1
	res := TestResult{Target: config.TargetOf(cfg).String(), Checks: []postgres.Check{}}
	start := time.Now()
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	c, err := postgres.Connect(cctx, cfg, red, model.Tool{Name: "locklens", Version: s.version})
	if err != nil {
		msg := strings.Join(strings.Fields(red.String(err.Error())), " ")
		res.Error = &msg
		if h := postgres.Hint(err); h != "" {
			res.Hint = &h
		}
		res.DurationMS = time.Since(start).Milliseconds()
		return res, nil
	}
	defer c.Close()
	var caps observe.Capabilities
	if err := c.ReadOnly(cctx, func(ctx context.Context, tx pgx.Tx, _ []int32) error {
		var err error
		caps, err = collector.DetectCapabilities(ctx, tx)
		return err
	}); err != nil {
		msg := strings.Join(strings.Fields(err.Error()), " ")
		res.Error = &msg
		res.DurationMS = time.Since(start).Milliseconds()
		return res, nil
	}
	res.Capabilities = &caps
	res.Checks = c.Diagnose(cctx, postgres.Options{QueryMaxLength: 200, MinTxAge: time.Minute})
	res.OK = true
	for _, ch := range res.Checks {
		if ch.Status == postgres.StatusFail {
			res.OK = false
		}
	}
	res.DurationMS = time.Since(start).Milliseconds()
	return res, nil
}

// BootstrapEntry is one target in a bootstrap file.
type BootstrapEntry struct {
	Input
	// DSNEnv names an environment variable holding the DSN, so bootstrap
	// files need not contain credentials.
	DSNEnv string `json:"dsn_env"`
	// Workspace optionally enables catalog and table-data browsing with a
	// separate, least-privileged role (configured by the server only while
	// the target has no workspace configuration yet).
	Workspace *BootstrapWorkspace `json:"workspace"`
}

// BootstrapWorkspace is the investigation access of a bootstrap target.
type BootstrapWorkspace struct {
	Metadata bool   `json:"metadata"`
	Data     bool   `json:"data"`
	DSNEnv   string `json:"dsn_env"`
}

// Bootstrap creates targets that do not exist yet (matched by name). It is
// meant for demos and automated setups; existing targets are not changed.
func (s *Service) Bootstrap(ctx context.Context, entries []BootstrapEntry, getenv func(string) string) (int, error) {
	created := 0
	for _, e := range entries {
		if _, err := s.store.GetTargetByName(ctx, e.Name); err == nil {
			continue
		} else if !errors.Is(err, storage.ErrNotFound) {
			return created, err
		}
		in := e.Input
		if e.DSNEnv != "" {
			in.DSN = getenv(e.DSNEnv)
		}
		if in.DSN == "" {
			return created, fmt.Errorf("bootstrap target %q: no DSN (set %s)", e.Name, e.DSNEnv)
		}
		if _, err := s.Create(ctx, in); err != nil {
			return created, fmt.Errorf("bootstrap target %q: %w", e.Name, err)
		}
		created++
	}
	return created, nil
}
