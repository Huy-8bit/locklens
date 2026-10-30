package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/repository/queries"
)

// OptimizerRepository is the store of schema catalogs, optimizer recommendations, and the workload they are derived from.
type OptimizerRepository interface {
	// SaveCatalog replaces the stored catalog of a database of a target.
	SaveCatalog(ctx context.Context, targetID int64, c observe.Catalog) error
	// LatestCatalog returns the most recently collected catalog of a target (of
	// any of its databases).
	LatestCatalog(ctx context.Context, targetID int64) (observe.Catalog, error)
	// Catalogs returns the stored catalogs of a target, one per database, by
	// database name.
	Catalogs(ctx context.Context, targetID int64) ([]observe.Catalog, error)
	// PruneCatalogs deletes the catalogs of databases that are no longer
	// monitored.
	PruneCatalogs(ctx context.Context, targetID int64, keep []string) error
	// SyncRecommendations upserts the recommendations of one optimizer pass over
	// one database.
	// Content is refreshed while a recommendation is open or accepted (its
	// evidence changes); decisions and later states are kept. Open
	// recommendations that were not derived again are resolved ("no longer
	// applies"); a resolved one that comes back is open again.
	SyncRecommendations(ctx context.Context, targetID int64, datname string, now time.Time, recs []RecommendationInput) error
	// ListRecommendations returns recommendations, the largest benefit first.
	ListRecommendations(ctx context.Context, f RecommendationFilter) ([]Recommendation, error)
	// GetRecommendation returns one recommendation of a target.
	GetRecommendation(ctx context.Context, targetID, id int64) (Recommendation, error)
	// DecideRecommendation records a person's decision (accepted, dismissed, or
	// open again) with a note.
	DecideRecommendation(ctx context.Context, targetID, id int64, status, by, note string) (Recommendation, error)
	// MarkApplied records that the change a recommendation proposes was made
	// (detected in the catalog) and the statement time before it.
	MarkApplied(ctx context.Context, id int64, at time.Time, how string, beforeMS *float64) error
	// SetVerification records the statement time after the change and the
	// resulting status (verified, no_effect, regressed).
	SetVerification(ctx context.Context, id int64, status string, afterMS float64, at time.Time) error
	// SetWhatIf stores the result of a what-if check and the confidence it
	// gives.
	SetWhatIf(ctx context.Context, targetID, id int64, whatif, confidence any) error
	// StatementWorkload returns the statements with the most execution time
	// since a time (at most limit), with their texts when the target collects
	// them.
	StatementWorkload(ctx context.Context, targetID int64, since time.Time, limit int, includeText bool) ([]WorkloadStatement, error)
	// StatementMeanMS is the median, over five-minute buckets with at least
	// minCalls calls, of a statement's time per call in [from, to), with the
	// number of buckets; ok is false without such buckets.
	StatementMeanMS(ctx context.Context, targetID, identityID int64, from, to time.Time, minCalls int64) (float64, int, bool, error)
}

var _ OptimizerRepository = (*Store)(nil)

// SaveCatalog replaces the stored catalog of a database of a target.
func (s *Store) SaveCatalog(ctx context.Context, targetID int64, c observe.Catalog) error {
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, queries.SaveCatalog, targetID, c.DatName, c.At, raw)
	return s.red.Error(err)
}

// LatestCatalog returns the most recently collected catalog of a target (of
// any of its databases).
func (s *Store) LatestCatalog(ctx context.Context, targetID int64) (observe.Catalog, error) {
	var raw []byte
	var c observe.Catalog
	err := s.pool.QueryRow(ctx, queries.LatestCatalog, targetID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrNotFound
	}
	if err != nil {
		return c, s.red.Error(err)
	}
	return c, json.Unmarshal(raw, &c)
}

// Catalogs returns the stored catalogs of a target, one per database, by
// database name.
func (s *Store) Catalogs(ctx context.Context, targetID int64) ([]observe.Catalog, error) {
	rows, err := s.pool.Query(ctx, queries.ListCatalogs, targetID)
	if err != nil {
		return nil, s.red.Error(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (observe.Catalog, error) {
		var raw []byte
		var c observe.Catalog
		if err := r.Scan(&raw); err != nil {
			return c, err
		}
		return c, json.Unmarshal(raw, &c)
	})
	return out, s.red.Error(err)
}

// PruneCatalogs deletes the catalogs of databases that are no longer
// monitored.
func (s *Store) PruneCatalogs(ctx context.Context, targetID int64, keep []string) error {
	if keep == nil {
		keep = []string{}
	}
	_, err := s.pool.Exec(ctx, queries.PruneCatalogs, targetID, keep)
	return s.red.Error(err)
}

// Recommendation is a stored optimizer recommendation with its life cycle.
type Recommendation struct {
	ID         int64           `json:"id"`
	TargetID   int64           `json:"target_id"`
	Database   string          `json:"database"`
	Kind       string          `json:"kind"`
	Key        string          `json:"key"`
	Status     string          `json:"status"`
	Severity   string          `json:"severity"`
	Title      string          `json:"title"`
	Summary    string          `json:"summary"`
	Details    json.RawMessage `json:"details"`
	DDL        *string         `json:"ddl"`
	Benefit    float64         `json:"benefit"`
	Confidence json.RawMessage `json:"confidence"`
	FirstSeen  time.Time       `json:"first_seen"`
	LastSeen   time.Time       `json:"last_seen"`
	DecidedBy  *string         `json:"decided_by"`
	DecidedAt  *time.Time      `json:"decided_at"`
	Note       string          `json:"note"`
	AppliedAt  *time.Time      `json:"applied_at"`
	AppliedHow *string         `json:"applied_how"`
	BeforeMS   *float64        `json:"before_ms"`
	AfterMS    *float64        `json:"after_ms"`
	VerifiedAt *time.Time      `json:"verified_at"`
	WhatIf     json.RawMessage `json:"whatif"`
	ResolvedAt *time.Time      `json:"resolved_at"`
}

// ErrRecommendationState means a decision is not possible in the
// recommendation's current state (for example it was already applied).
var ErrRecommendationState = errors.New("the recommendation is not in a state that allows this")

// Recommendation statuses.
const (
	RecOpen      = "open"
	RecAccepted  = "accepted"
	RecDismissed = "dismissed"
	RecApplied   = "applied"
	RecVerified  = "verified"
	RecNoEffect  = "no_effect"
	RecRegressed = "regressed"
	RecResolved  = "resolved"
)

// RecommendationInput is what the optimizer derives in one pass.
type RecommendationInput struct {
	Kind, Key, Severity, Title, Summary string
	Details                             any
	DDL                                 *string
	Benefit                             float64
	Confidence                          any
}

func scanRec(r pgx.CollectableRow) (Recommendation, error) {
	var x Recommendation
	err := r.Scan(&x.ID, &x.TargetID, &x.Database, &x.Kind, &x.Key, &x.Status, &x.Severity, &x.Title, &x.Summary, &x.Details, &x.DDL, &x.Benefit,
		&x.Confidence, &x.FirstSeen, &x.LastSeen, &x.DecidedBy, &x.DecidedAt, &x.Note, &x.AppliedAt, &x.AppliedHow,
		&x.BeforeMS, &x.AfterMS, &x.VerifiedAt, &x.WhatIf, &x.ResolvedAt)
	return x, err
}

// SyncRecommendations upserts the recommendations of one optimizer pass over
// one database.
// Content is refreshed while a recommendation is open or accepted (its
// evidence changes); decisions and later states are kept. Open
// recommendations that were not derived again are resolved ("no longer
// applies"); a resolved one that comes back is open again.
func (s *Store) SyncRecommendations(ctx context.Context, targetID int64, datname string, now time.Time, recs []RecommendationInput) error {
	return s.red.Error(pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		kinds, keys := make([]string, len(recs)), make([]string, len(recs))
		for i, r := range recs {
			details, err := json.Marshal(r.Details)
			if err != nil {
				return err
			}
			conf, err := json.Marshal(r.Confidence)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, queries.UpsertRecommendation,
				targetID, r.Kind, r.Key, r.Severity, r.Title, r.Summary, details, r.DDL, r.Benefit, conf, now.UTC(), datname); err != nil {
				return err
			}
			kinds[i], keys[i] = r.Kind, r.Key
		}
		_, err := tx.Exec(ctx, queries.ResolveMissingRecommendations, targetID, now.UTC(), kinds, keys, datname)
		return err
	}))
}

// RecommendationFilter selects recommendations.
type RecommendationFilter struct {
	TargetID int64
	// Statuses limits the statuses; empty means all.
	Statuses []string
	Kind     string
	// Database limits the database; nil means all.
	Database *string
	Limit    int
}

// ListRecommendations returns recommendations, the largest benefit first.
func (s *Store) ListRecommendations(ctx context.Context, f RecommendationFilter) ([]Recommendation, error) {
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 500
	}
	if f.Statuses == nil {
		f.Statuses = []string{} // a nil slice is NULL, which would match nothing
	}
	rows, err := s.pool.Query(ctx, queries.ListRecommendations, f.TargetID, f.Statuses, f.Kind, f.Limit, f.Database)
	if err != nil {
		return nil, s.red.Error(err)
	}
	out, err := pgx.CollectRows(rows, scanRec)
	if out == nil {
		out = []Recommendation{}
	}
	return out, s.red.Error(err)
}

// GetRecommendation returns one recommendation of a target.
func (s *Store) GetRecommendation(ctx context.Context, targetID, id int64) (Recommendation, error) {
	rows, err := s.pool.Query(ctx, queries.GetRecommendation, targetID, id)
	if err != nil {
		return Recommendation{}, s.red.Error(err)
	}
	r, err := pgx.CollectExactlyOneRow(rows, scanRec)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, s.red.Error(err)
}

// DecideRecommendation records a person's decision (accepted, dismissed, or
// open again) with a note.
func (s *Store) DecideRecommendation(ctx context.Context, targetID, id int64, status, by, note string) (Recommendation, error) {
	tag, err := s.pool.Exec(ctx, queries.DecideRecommendation, targetID, id, status, by, note)
	if err != nil {
		return Recommendation{}, s.red.Error(err)
	}
	if tag.RowsAffected() == 0 {
		if _, err := s.GetRecommendation(ctx, targetID, id); err != nil {
			return Recommendation{}, err
		}
		return Recommendation{}, ErrRecommendationState
	}
	return s.GetRecommendation(ctx, targetID, id)
}

// MarkApplied records that the change a recommendation proposes was made
// (detected in the catalog) and the statement time before it.
func (s *Store) MarkApplied(ctx context.Context, id int64, at time.Time, how string, beforeMS *float64) error {
	_, err := s.pool.Exec(ctx, queries.MarkApplied, id, at.UTC(), how, beforeMS)
	return s.red.Error(err)
}

// SetVerification records the statement time after the change and the
// resulting status (verified, no_effect, regressed).
func (s *Store) SetVerification(ctx context.Context, id int64, status string, afterMS float64, at time.Time) error {
	_, err := s.pool.Exec(ctx, queries.SetVerification, id, status, afterMS, at.UTC())
	return s.red.Error(err)
}

// SetWhatIf stores the result of a what-if check and the confidence it
// gives.
func (s *Store) SetWhatIf(ctx context.Context, targetID, id int64, whatif, confidence any) error {
	w, err := json.Marshal(whatif)
	if err != nil {
		return err
	}
	c, err := json.Marshal(confidence)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, queries.SetWhatIf, targetID, id, w, c)
	return s.red.Error(err)
}

// WorkloadStatement is one statement's activity over a period, from the
// five-minute rollups.
type WorkloadStatement struct {
	IdentityID int64
	Key        observe.QueryKey
	DatName    string
	UserName   string
	Command    string
	Text       *string
	Calls      int64
	TotalMS    float64
	Rows       int64
	BlkRead    int64
	TempW      int64
	// Share is the statement's share of all statement execution time.
	Share float64
}

// StatementWorkload returns the statements with the most execution time
// since a time (at most limit), with their texts when the target collects
// them.
func (s *Store) StatementWorkload(ctx context.Context, targetID int64, since time.Time, limit int, includeText bool) ([]WorkloadStatement, error) {
	rows, err := s.pool.Query(ctx, queries.StatementWorkload, targetID, since.UTC(), limit, includeText)
	if err != nil {
		return nil, s.red.Error(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (WorkloadStatement, error) {
		var x WorkloadStatement
		var dbid, userid int64
		err := r.Scan(&x.IdentityID, &x.Key.ServerMajor, &dbid, &userid, &x.Key.TopLevel, &x.Key.QueryID, &x.DatName, &x.UserName, &x.Command,
			&x.Text, &x.Calls, &x.TotalMS, &x.Rows, &x.BlkRead, &x.TempW, &x.Share)
		x.Key.DBID, x.Key.UserID = uint32(dbid), uint32(userid)
		return x, err
	})
	return out, s.red.Error(err)
}

// StatementMeanMS is the median, over five-minute buckets with at least
// minCalls calls, of a statement's time per call in [from, to), with the
// number of buckets; ok is false without such buckets.
func (s *Store) StatementMeanMS(ctx context.Context, targetID, identityID int64, from, to time.Time, minCalls int64) (float64, int, bool, error) {
	var median *float64
	var n int
	err := s.pool.QueryRow(ctx, queries.StatementMeanMS,
		targetID, identityID, from.UTC(), to.UTC(), minCalls).Scan(&median, &n)
	if err != nil {
		return 0, 0, false, s.red.Error(err)
	}
	if median == nil {
		return 0, 0, false, nil
	}
	return *median, n, true, nil
}
