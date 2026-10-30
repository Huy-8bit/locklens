package optimizer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/domain/plans"
	"github.com/locklens/locklens/internal/domain/workspace"
	"github.com/locklens/locklens/internal/lib/sqlparse"
	"github.com/locklens/locklens/internal/repository"
	"github.com/locklens/locklens/internal/targetdb/investigate"
)

// Lifecycle settings.
const (
	// workloadWindow is the statement activity recommendations are based on.
	workloadWindow = 24 * time.Hour
	// maxStatements bounds the statements analysed per target and pass.
	maxStatements = 200
	// beforeWindow is the history a change is compared with.
	beforeWindow = 7 * 24 * time.Hour
	// minAfterPoints is how many five-minute buckets after a change are
	// needed before its effect is judged.
	minAfterPoints = 6
	// minCallsPerPoint: buckets with fewer calls are left out of the means.
	minCallsPerPoint = 5
	// improvedRatio and regressedRatio bound "no effect".
	improvedRatio  = 0.8
	regressedRatio = 1.2
)

// Service runs the optimizer over the stored catalog and workload.
type Service struct {
	store     Store
	workspace Workspace
	events    Events
	log       *slog.Logger
}

// NewService returns an optimizer service.
func NewService(store Store, ws Workspace, events Events, log *slog.Logger) *Service {
	return &Service{store: store, workspace: ws, events: events, log: log}
}

// Run analyses every target every interval until ctx is cancelled.
func (s *Service) Run(ctx context.Context, every time.Duration) {
	timer := time.NewTimer(2 * time.Minute)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		targets, err := s.store.ListTargets(ctx)
		if err != nil && ctx.Err() == nil {
			s.log.Warn("optimizer: list targets failed", "error", err.Error())
		}
		for _, t := range targets {
			if !t.Enabled || t.DeletedAt != nil {
				continue
			}
			if _, err := s.Refresh(ctx, t, time.Now().UTC()); err != nil && ctx.Err() == nil && !errors.Is(err, repository.ErrNotFound) {
				s.log.Warn("optimizer pass failed", "target_id", t.ID, "error", err.Error())
			}
		}
		timer.Reset(every)
	}
}

// Refresh runs one optimizer pass for a target, over the catalog of each of
// its databases: it records which recommendations were applied (from the
// catalog), measures applied ones, and stores the current recommendations.
// It returns how many it derived. ErrNotFound means no catalog was
// collected yet.
func (s *Service) Refresh(ctx context.Context, t repository.Target, now time.Time) (int, error) {
	cats, err := s.store.Catalogs(ctx, t.ID)
	if err != nil {
		return 0, err
	}
	if len(cats) == 0 {
		return 0, repository.ErrNotFound
	}
	stmts, err := s.store.StatementWorkload(ctx, t.ID, now.Add(-workloadWindow), maxStatements, t.QueryTextEnabled)
	if err != nil {
		return 0, err
	}
	ids := make([]int64, len(stmts))
	for i, st := range stmts {
		ids[i] = st.IdentityID
	}
	saved, err := s.store.LatestPlans(ctx, t.ID, ids)
	if err != nil {
		return 0, err
	}
	parsedPlans := map[int64]ParsedPlan{}
	for id, p := range saved {
		pp, err := plans.Parse(p.Plan)
		if err != nil || len(pp.Nodes) == 0 {
			continue
		}
		var rels []string
		for _, n := range pp.Nodes {
			if n.Relation != "" && !slices.Contains(rels, n.Relation) {
				rels = append(rels, n.Relation)
			}
		}
		parsedPlans[id] = ParsedPlan{TopRows: pp.Nodes[0].Rows, Relations: rels, Analyzed: pp.Analyzed}
	}
	derived := 0
	for _, cat := range cats {
		if err := s.lifecycle(ctx, t.ID, cat, now); err != nil {
			return 0, err
		}
		recs := Analyze(Input{Now: now, Catalog: cat, Statements: databaseStatements(stmts, cat.DatName, len(cats) == 1),
			Plans: parsedPlans, Parser: sqlparse.Available()})
		if err := s.store.SyncRecommendations(ctx, t.ID, cat.DatName, now, recs); err != nil {
			return 0, err
		}
		derived += len(recs)
	}
	return derived, s.resolveDropped(ctx, t.ID, cats, now)
}

// databaseStatements are the statements that ran in a database; with a
// single catalog, statements of an unknown database count too.
func databaseStatements(stmts []repository.WorkloadStatement, datname string, only bool) []repository.WorkloadStatement {
	var out []repository.WorkloadStatement
	for _, st := range stmts {
		if st.DatName == datname || only && st.DatName == "" {
			out = append(out, st)
		}
	}
	return out
}

// resolveDropped resolves the open recommendations of databases that no
// longer have a catalog (they left the target's scope).
func (s *Service) resolveDropped(ctx context.Context, targetID int64, cats []observe.Catalog, now time.Time) error {
	open, err := s.store.ListRecommendations(ctx, repository.RecommendationFilter{TargetID: targetID, Statuses: []string{repository.RecOpen}})
	if err != nil {
		return err
	}
	done := map[string]bool{}
	for _, c := range cats {
		done[c.DatName] = true
	}
	for _, r := range open {
		if !done[r.Database] {
			done[r.Database] = true
			if err := s.store.SyncRecommendations(ctx, targetID, r.Database, now, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

// lifecycle detects applied recommendations of a database in its catalog
// and judges the effect of applied ones on their statements.
func (s *Service) lifecycle(ctx context.Context, targetID int64, cat observe.Catalog, now time.Time) error {
	recs, err := s.store.ListRecommendations(ctx, repository.RecommendationFilter{TargetID: targetID, Database: &cat.DatName,
		Statuses: []string{repository.RecOpen, repository.RecAccepted, repository.RecDismissed, repository.RecApplied,
			repository.RecVerified, repository.RecNoEffect, repository.RecRegressed}})
	if err != nil {
		return err
	}
	ci := newCatalogIndex(cat)
	for _, r := range recs {
		switch r.Status {
		case repository.RecOpen, repository.RecAccepted, repository.RecDismissed:
			how, ok := appliedHow(r, ci)
			if !ok {
				continue
			}
			var before *float64
			if id, ok := firstStatement(r); ok {
				if v, _, ok, err := s.store.StatementMeanMS(ctx, targetID, id, cat.At.Add(-beforeWindow), cat.At, minCallsPerPoint); err != nil {
					return err
				} else if ok {
					before = &v
				}
			}
			if err := s.store.MarkApplied(ctx, r.ID, cat.At, how, before); err != nil {
				return err
			}
			s.publish(targetID, r.ID, repository.RecApplied)
		default:
			if err := s.verify(ctx, targetID, r, now); err != nil {
				return err
			}
		}
	}
	return nil
}

// appliedHow says whether the catalog shows the change a recommendation
// proposes, and how.
func appliedHow(r repository.Recommendation, c catalogIndex) (string, bool) {
	var d struct {
		Table   string   `json:"table"`
		Columns []string `json:"columns"`
		Index   string   `json:"index"`
	}
	if json.Unmarshal(r.Details, &d) != nil {
		return "", false
	}
	switch r.Kind {
	case KindMissingIndex, KindFKIndex:
		for _, ix := range c.indexes[d.Table] {
			if !ix.Valid || len(ix.Columns) == 0 || len(d.Columns) == 0 {
				continue
			}
			if r.Kind == KindFKIndex && fkIndexed([]observe.CatalogIndex{ix}, d.Columns) || r.Kind == KindMissingIndex && ix.Columns[0] == d.Columns[0] {
				return fmt.Sprintf("index %s was created: %s", ix.Qualified(), ix.Definition), true
			}
		}
	case KindUnusedIndex, KindDuplicateIndex:
		for _, ix := range c.cat.Indexes {
			if ix.Qualified() == d.Index {
				return "", false
			}
		}
		return fmt.Sprintf("index %s was dropped", d.Index), true
	case KindInvalidIndex:
		for _, ix := range c.cat.Indexes {
			if ix.Qualified() == d.Index {
				if ix.Valid {
					return fmt.Sprintf("index %s is valid again", d.Index), true
				}
				return "", false
			}
		}
		return fmt.Sprintf("index %s was dropped", d.Index), true
	case KindStaleStats:
		if t, ok := c.tables[d.Table]; ok {
			last := t.LastAnalyze
			if t.LastAutoAnalyze != nil && (last == nil || t.LastAutoAnalyze.After(*last)) {
				last = t.LastAutoAnalyze
			}
			if last != nil && last.After(r.FirstSeen) {
				return fmt.Sprintf("%s was analysed at %s", d.Table, last.UTC().Format(time.RFC3339)), true
			}
		}
	}
	return "", false
}

// firstStatement is the busiest statement a recommendation is about.
func firstStatement(r repository.Recommendation) (int64, bool) {
	var d struct {
		Statements []StatementRef `json:"statements"`
		Statement  *StatementRef  `json:"statement"`
	}
	if json.Unmarshal(r.Details, &d) != nil {
		return 0, false
	}
	if len(d.Statements) > 0 {
		return d.Statements[0].IdentityID, true
	}
	if d.Statement != nil {
		return d.Statement.IdentityID, true
	}
	return 0, false
}

// StatementIDs are the identities of the statements a recommendation names.
func StatementIDs(details json.RawMessage) []int64 {
	var d struct {
		Statements []StatementRef `json:"statements"`
		Statement  *StatementRef  `json:"statement"`
	}
	if json.Unmarshal(details, &d) != nil {
		return nil
	}
	var out []int64
	for _, st := range d.Statements {
		out = append(out, st.IdentityID)
	}
	if d.Statement != nil {
		out = append(out, d.Statement.IdentityID)
	}
	return out
}

// verify compares a statement's time per call after a change with before:
// verified (at least 20% faster), regressed (at least 20% slower), or no
// effect. It keeps watching, so a later slowdown turns a verified change
// into a regression.
func (s *Service) verify(ctx context.Context, targetID int64, r repository.Recommendation, now time.Time) error {
	if r.AppliedAt == nil || r.BeforeMS == nil || *r.BeforeMS <= 0 {
		return nil
	}
	id, ok := firstStatement(r)
	if !ok {
		return nil
	}
	// A settling period after the change, then the last day at most.
	from := r.AppliedAt.Add(5 * time.Minute)
	if now.Sub(from) > 24*time.Hour {
		from = now.Add(-24 * time.Hour)
	}
	after, n, ok, err := s.store.StatementMeanMS(ctx, targetID, id, from, now, minCallsPerPoint)
	if err != nil || !ok || n < minAfterPoints {
		return err
	}
	status := repository.RecNoEffect
	switch ratio := after / *r.BeforeMS; {
	case ratio <= improvedRatio:
		status = repository.RecVerified
	case ratio >= regressedRatio:
		status = repository.RecRegressed
	}
	if err := s.store.SetVerification(ctx, r.ID, status, after, now); err != nil {
		return err
	}
	if status != r.Status {
		s.publish(targetID, r.ID, status)
		if status == repository.RecRegressed {
			s.log.Warn("optimizer: statement slower after an applied recommendation", "target_id", targetID, "recommendation_id", r.ID,
				"before_ms", *r.BeforeMS, "after_ms", after)
		}
	}
	return nil
}

func (s *Service) publish(targetID, id int64, status string) {
	if s.events != nil {
		s.events.Publish("recommendation.updated", map[string]any{"target_id": targetID, "recommendation_id": id, "status": status})
	}
}

// WhatIfResult is the outcome of a what-if check of an index
// recommendation.
type WhatIfResult struct {
	At         time.Time                     `json:"at"`
	Index      string                        `json:"index"`
	Statements []WhatIfStatementResult       `json:"statements"`
	Summary    string                        `json:"summary"`
	Raw        []investigate.WhatIfStatement `json:"-"`
}

// WhatIfStatementResult is one statement of a what-if check.
type WhatIfStatementResult struct {
	IdentityID int64   `json:"identity_id"`
	QueryID    string  `json:"queryid"`
	CostBefore float64 `json:"cost_before"`
	CostAfter  float64 `json:"cost_after"`
	UsesIndex  bool    `json:"uses_index"`
	Error      string  `json:"error,omitempty"`
}

// ErrNotIndex means the recommendation proposes no new index.
var ErrNotIndex = errors.New("only index recommendations can be checked with a hypothetical index")

// WhatIf checks an index recommendation with a hypothetical index (HypoPG)
// through the investigation workspace, stores the result, and grades the
// recommendation's confidence by it. ctx carries the actor
// (workspace.WithActor); owner is the session that may cancel it.
func (s *Service) WhatIf(ctx context.Context, t repository.Target, recID int64, owner, requestID string) (WhatIfResult, error) {
	r, err := s.store.GetRecommendation(ctx, t.ID, recID)
	if err != nil {
		return WhatIfResult{}, err
	}
	if r.Kind != KindMissingIndex && r.Kind != KindFKIndex {
		return WhatIfResult{}, ErrNotIndex
	}
	// The workspace connects to the target's own database only.
	if r.Database != "" && r.Database != t.DisplayDatabase {
		return WhatIfResult{}, &workspace.Error{Code: "unsupported", Message: fmt.Sprintf(
			"What-if checks run on the connected database (%s); this recommendation is for database %s.", t.DisplayDatabase, r.Database)}
	}
	var d struct {
		Table      string         `json:"table"`
		Columns    []string       `json:"columns"`
		Statements []StatementRef `json:"statements"`
	}
	if err := json.Unmarshal(r.Details, &d); err != nil {
		return WhatIfResult{}, err
	}
	schema, name, _ := strings.Cut(d.Table, ".")
	create := fmt.Sprintf("CREATE INDEX ON %s (%s)", quoteQualified(schema, name), quoteList(d.Columns))
	// The statements' full texts (excerpts in details may be shortened).
	var sqls []string
	var refs []StatementRef
	for _, st := range d.Statements {
		q, _, _, err := s.store.QueryIdentity(ctx, t.ID, st.IdentityID, true)
		if err != nil || q.Text == nil {
			continue
		}
		sqls = append(sqls, *q.Text)
		refs = append(refs, st)
	}
	if len(sqls) == 0 {
		return WhatIfResult{}, &workspace.Error{Code: "unsupported", Message: "The statements' texts are not collected for this instance (query text collection is off), so they cannot be planned."}
	}
	out, err := s.workspace.Run(ctx, t.ID, owner, "whatif", requestID, func(ctx context.Context, tx pgx.Tx) (any, error) {
		return investigate.WhatIfIndex(ctx, tx, create, sqls)
	})
	if err != nil {
		return WhatIfResult{}, err
	}
	raw := out.([]investigate.WhatIfStatement)
	res := WhatIfResult{At: time.Now().UTC(), Index: create, Raw: raw}
	used, better, failed := 0, 0, 0
	for i, w := range raw {
		res.Statements = append(res.Statements, WhatIfStatementResult{IdentityID: refs[i].IdentityID, QueryID: refs[i].QueryID,
			CostBefore: w.CostBefore, CostAfter: w.CostAfter, UsesIndex: w.UsesIndex, Error: w.Error})
		switch {
		case w.Error != "":
			failed++
		case w.UsesIndex:
			used++
			if w.CostBefore > 0 && w.CostAfter/w.CostBefore <= 0.5 {
				better++
			}
		}
	}
	conf := Confidence{Level: "low"}
	switch {
	case failed == len(raw):
		res.Summary = "The statements could not be planned (generic plans of statements with parameters need PostgreSQL 16 or later)."
		conf.Reasons = []string{res.Summary}
		var old Confidence
		if json.Unmarshal(r.Confidence, &old) == nil && old.Level != "" {
			conf.Level = old.Level
		}
	case used == 0:
		res.Summary = "The planner would not use this index for the analysed statements."
		conf.Reasons = []string{"checked with a hypothetical index (HypoPG): " + res.Summary}
	case better == used && used == len(raw)-failed:
		res.Summary = fmt.Sprintf("The planner would use this index for %d statement(s), at half the estimated cost or less.", used)
		conf.Level = "high"
		conf.Reasons = []string{"checked with a hypothetical index (HypoPG): " + res.Summary}
	default:
		res.Summary = fmt.Sprintf("The planner would use this index for %d of %d statement(s); the estimated saving is moderate.", used, len(raw)-failed)
		conf.Level = "medium"
		conf.Reasons = []string{"checked with a hypothetical index (HypoPG): " + res.Summary}
	}
	conf.Reasons = append(conf.Reasons, "estimated costs are the planner's, not measured run times")
	if err := s.store.SetWhatIf(ctx, t.ID, r.ID, res, conf); err != nil {
		return WhatIfResult{}, err
	}
	s.publish(t.ID, r.ID, r.Status)
	return res, nil
}
