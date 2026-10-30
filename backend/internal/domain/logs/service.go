package logs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/repository"
)

// Limits on what an agent sends.
const (
	MaxBatch     = 1000
	maxQuery     = 16 << 10
	maxMessage   = 4 << 10
	maxDetails   = 64 << 10
	maxPlan      = 256 << 10
	maxEventAge  = 7 * 24 * time.Hour
	maxClockSkew = 10 * time.Minute
)

// Batch is what an agent sends.
type Batch struct {
	Agent  AgentInfo `json:"agent"`
	Events []Event   `json:"events"`
}

// AgentInfo describes the agent.
type AgentInfo struct {
	Hostname string   `json:"hostname"`
	Version  string   `json:"version"`
	Sources  []string `json:"sources"`
}

// ErrInvalid is wrapped by validation errors.
var ErrInvalid = errors.New("invalid log batch")

// Validate checks a batch; events too old or in the future are rejected
// with the batch, since the agent's clock or reading is wrong.
func (b Batch) Validate(now time.Time) error {
	bad := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
	}
	if len(b.Events) > MaxBatch {
		return bad("at most %d events per batch", MaxBatch)
	}
	if len(b.Agent.Hostname) > 255 || len(b.Agent.Version) > 64 || len(b.Agent.Sources) > 50 {
		return bad("agent description too long")
	}
	for i, e := range b.Events {
		switch {
		case !slices.Contains(Kinds, e.Kind):
			return bad("event %d: unknown kind %q", i, e.Kind)
		case e.ID == "" || len(e.ID) > 64:
			return bad("event %d: id must be 1 to 64 characters", i)
		case e.At.Before(now.Add(-maxEventAge)) || e.At.After(now.Add(maxClockSkew)):
			return bad("event %d: time %s is not within the last 7 days", i, e.At.Format(time.RFC3339))
		case len(e.Query) > maxQuery || len(e.Message) > maxMessage:
			return bad("event %d: query or message too long", i)
		case e.DurationMS != nil && (*e.DurationMS < 0 || *e.DurationMS > 1e10):
			return bad("event %d: duration out of range", i)
		}
		limit := maxDetails
		if e.Kind == KindPlan {
			limit = maxPlan
		}
		if raw, err := json.Marshal(e.Details); err != nil || len(raw) > limit {
			return bad("event %d: details too large", i)
		}
	}
	return nil
}

// Service stores agents' events and summarises them.
type Service struct{ store Store }

// New returns the service.
func New(store Store) *Service { return &Service{store: store} }

// Ingest stores a batch (validated) sent with a token of a target; it
// returns how many events were new.
func (s *Service) Ingest(ctx context.Context, targetID, tokenID int64, b Batch) (int64, error) {
	return s.store.InsertLogEvents(ctx, targetID, tokenID, b.Agent.Hostname, b.Agent.Version, b.Agent.Sources, b.Events)
}

// Summary is what the logs of a target show for a period.
type Summary struct {
	From       time.Time                 `json:"from"`
	To         time.Time                 `json:"to"`
	Step       float64                   `json:"step_seconds"`
	Counts     []repository.LogCount     `json:"counts"`
	Statements []repository.LogStatement `json:"statements"`
	Agents     []repository.LogAgent     `json:"agents"`
	// Settings are the instance's logging settings (from the last reading
	// of its settings) with what each one makes the log contain.
	Settings []SettingCheck `json:"settings"`
}

// SettingCheck is one logging setting and whether it is set so the log has
// the events LockLens reads.
type SettingCheck struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	OK     bool   `json:"ok"`
	Advice string `json:"advice"`
}

// Summarize returns the summary of a period.
func (s *Service) Summarize(ctx context.Context, targetID int64, from, to time.Time, step time.Duration) (Summary, error) {
	sum := Summary{From: from, To: to, Step: step.Seconds()}
	var err error
	if sum.Counts, err = s.store.LogCounts(ctx, targetID, from, to, step); err != nil {
		return sum, err
	}
	if sum.Statements, err = s.store.LogStatements(ctx, targetID, from, to, 50); err != nil {
		return sum, err
	}
	if sum.Agents, err = s.store.LogAgents(ctx, targetID); err != nil {
		return sum, err
	}
	raw, err := s.store.ChangeState(ctx, targetID, "server")
	switch {
	case errors.Is(err, repository.ErrNotFound):
		sum.Settings = []SettingCheck{}
	case err != nil:
		return sum, err
	default:
		var st observe.ServerState
		if err := json.Unmarshal(raw, &st); err != nil {
			return sum, err
		}
		sum.Settings = CheckSettings(st.Settings)
	}
	return sum, nil
}

// Events returns stored events.
func (s *Service) Events(ctx context.Context, f repository.LogEventFilter) ([]Event, error) {
	return s.store.LogEvents(ctx, f)
}

// CheckSettings says which logging settings make the log contain the
// events LockLens reads. LockLens never changes them.
func CheckSettings(set map[string]observe.Setting) []SettingCheck {
	v := func(name string) (string, bool) {
		s, ok := set[name]
		return s.Value, ok
	}
	var out []SettingCheck
	add := func(name string, ok func(string) bool, advice string) {
		val, found := v(name)
		if !found {
			return
		}
		out = append(out, SettingCheck{Name: name, Value: val, OK: ok(val), Advice: advice})
	}
	on := func(s string) bool { return s == "on" }
	nonNegative := func(s string) bool { return s != "-1" && s != "" }
	add("logging_collector", on, "Writes the server log to files the agent can read (restart needed).")
	add("log_destination", func(s string) bool { return s != "" }, "jsonlog (PostgreSQL 15+) or csvlog are the most reliable to read; stderr needs log_line_prefix.")
	add("log_min_duration_statement", nonNegative, "Logs statements slower than this (ms); percentiles are of those executions only.")
	add("log_lock_waits", on, "Logs sessions waiting for a lock longer than deadlock_timeout.")
	add("log_autovacuum_min_duration", nonNegative, "Logs autovacuum and autoanalyze runs longer than this (ms).")
	add("log_temp_files", nonNegative, "Logs temporary files larger than this (kB).")
	add("log_checkpoints", on, "Logs each checkpoint with its duration.")
	add("auto_explain.log_min_duration", nonNegative, "With auto_explain loaded, logs the plans of statements slower than this (ms).")
	if _, ok := v("auto_explain.log_min_duration"); !ok {
		out = append(out, SettingCheck{Name: "auto_explain", Value: "not loaded", OK: false,
			Advice: "Add auto_explain to session_preload_libraries or shared_preload_libraries for real plans of slow statements."})
	}
	return out
}
