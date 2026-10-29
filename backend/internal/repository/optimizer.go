package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/domain/observe"
)

// SaveCatalog replaces the stored catalog of a target.
func (s *Store) SaveCatalog(ctx context.Context, targetID int64, c observe.Catalog) error {
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
INSERT INTO catalog_state (target_id, collected_at, catalog) VALUES ($1, $2, $3)
ON CONFLICT (target_id) DO UPDATE SET collected_at = EXCLUDED.collected_at, catalog = EXCLUDED.catalog`, targetID, c.At, raw)
	return s.red.Error(err)
}

// LatestCatalog returns the stored catalog of a target.
func (s *Store) LatestCatalog(ctx context.Context, targetID int64) (observe.Catalog, error) {
	var raw []byte
	var c observe.Catalog
	err := s.pool.QueryRow(ctx, `SELECT catalog FROM catalog_state WHERE target_id = $1`, targetID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrNotFound
	}
	if err != nil {
		return c, s.red.Error(err)
	}
	return c, json.Unmarshal(raw, &c)
}

// Recommendation is a stored optimizer recommendation with its life cycle.
type Recommendation struct {
	ID         int64           `json:"id"`
	TargetID   int64           `json:"target_id"`
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

const recColumns = `id, target_id, kind, rec_key, status, severity, title, summary, details, ddl, benefit, confidence,
first_seen, last_seen, decided_by, decided_at, note, applied_at, applied_how, before_ms, after_ms, verified_at, whatif, resolved_at`

func scanRec(r pgx.CollectableRow) (Recommendation, error) {
	var x Recommendation
	err := r.Scan(&x.ID, &x.TargetID, &x.Kind, &x.Key, &x.Status, &x.Severity, &x.Title, &x.Summary, &x.Details, &x.DDL, &x.Benefit,
		&x.Confidence, &x.FirstSeen, &x.LastSeen, &x.DecidedBy, &x.DecidedAt, &x.Note, &x.AppliedAt, &x.AppliedHow,
		&x.BeforeMS, &x.AfterMS, &x.VerifiedAt, &x.WhatIf, &x.ResolvedAt)
	return x, err
}

// SyncRecommendations upserts the recommendations of one optimizer pass.
// Content is refreshed while a recommendation is open or accepted (its
// evidence changes); decisions and later states are kept. Open
// recommendations that were not derived again are resolved ("no longer
// applies"); a resolved one that comes back is open again.
func (s *Store) SyncRecommendations(ctx context.Context, targetID int64, now time.Time, recs []RecommendationInput) error {
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
			if _, err := tx.Exec(ctx, `
INSERT INTO recommendations (target_id, kind, rec_key, severity, title, summary, details, ddl, benefit, confidence, first_seen, last_seen)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $11)
ON CONFLICT (target_id, kind, rec_key) DO UPDATE SET
    last_seen = EXCLUDED.last_seen,
    status = CASE WHEN recommendations.status = 'resolved' THEN 'open' ELSE recommendations.status END,
    resolved_at = CASE WHEN recommendations.status = 'resolved' THEN NULL ELSE recommendations.resolved_at END,
    severity = CASE WHEN recommendations.status IN ('open', 'accepted', 'resolved') THEN EXCLUDED.severity ELSE recommendations.severity END,
    title = CASE WHEN recommendations.status IN ('open', 'accepted', 'resolved') THEN EXCLUDED.title ELSE recommendations.title END,
    summary = CASE WHEN recommendations.status IN ('open', 'accepted', 'resolved') THEN EXCLUDED.summary ELSE recommendations.summary END,
    details = CASE WHEN recommendations.status IN ('open', 'accepted', 'resolved') THEN EXCLUDED.details ELSE recommendations.details END,
    ddl = CASE WHEN recommendations.status IN ('open', 'accepted', 'resolved') THEN EXCLUDED.ddl ELSE recommendations.ddl END,
    benefit = EXCLUDED.benefit,
    confidence = CASE WHEN recommendations.whatif IS NULL THEN EXCLUDED.confidence ELSE recommendations.confidence END`,
				targetID, r.Kind, r.Key, r.Severity, r.Title, r.Summary, details, r.DDL, r.Benefit, conf, now.UTC()); err != nil {
				return err
			}
			kinds[i], keys[i] = r.Kind, r.Key
		}
		_, err := tx.Exec(ctx, `
UPDATE recommendations SET status = 'resolved', resolved_at = $2
WHERE target_id = $1 AND status = 'open'
  AND (kind, rec_key) NOT IN (SELECT k, r FROM unnest($3::text[], $4::text[]) AS u(k, r))`, targetID, now.UTC(), kinds, keys)
		return err
	}))
}

// RecommendationFilter selects recommendations.
type RecommendationFilter struct {
	TargetID int64
	// Statuses limits the statuses; empty means all.
	Statuses []string
	Kind     string
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
	rows, err := s.pool.Query(ctx, `SELECT `+recColumns+` FROM recommendations
WHERE ($1 = 0 OR target_id = $1) AND (cardinality($2::text[]) = 0 OR status = ANY ($2)) AND ($3 = '' OR kind = $3)
ORDER BY benefit DESC, id LIMIT $4`, f.TargetID, f.Statuses, f.Kind, f.Limit)
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
	rows, err := s.pool.Query(ctx, `SELECT `+recColumns+` FROM recommendations WHERE target_id = $1 AND id = $2`, targetID, id)
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
	tag, err := s.pool.Exec(ctx, `
UPDATE recommendations SET status = $3, decided_by = $4, decided_at = now(), note = $5
WHERE target_id = $1 AND id = $2 AND status IN ('open', 'accepted', 'dismissed')`, targetID, id, status, by, note)
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
	_, err := s.pool.Exec(ctx, `
UPDATE recommendations SET status = 'applied', applied_at = $2, applied_how = $3, before_ms = $4
WHERE id = $1 AND status IN ('open', 'accepted', 'dismissed', 'resolved')`, id, at.UTC(), how, beforeMS)
	return s.red.Error(err)
}

// SetVerification records the statement time after the change and the
// resulting status (verified, no_effect, regressed).
func (s *Store) SetVerification(ctx context.Context, id int64, status string, afterMS float64, at time.Time) error {
	_, err := s.pool.Exec(ctx, `
UPDATE recommendations SET status = $2, after_ms = $3, verified_at = $4
WHERE id = $1 AND status IN ('applied', 'verified', 'no_effect', 'regressed')`, id, status, afterMS, at.UTC())
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
	_, err = s.pool.Exec(ctx, `UPDATE recommendations SET whatif = $3, confidence = $4 WHERE target_id = $1 AND id = $2`, targetID, id, w, c)
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
	rows, err := s.pool.Query(ctx, `
WITH w AS (
 SELECT identity_id, sum(calls)::bigint AS calls, sum(total_exec_ms) AS ms, sum(rows)::bigint AS rows,
        sum(shared_blks_read)::bigint AS rd, sum(temp_blks_written)::bigint AS tw
 FROM query_rollups WHERE target_id = $1 AND ts >= $2 GROUP BY identity_id
), total AS (SELECT sum(ms) AS ms FROM w)
SELECT w.identity_id, q.server_major, q.dbid::bigint, q.userid::bigint, q.toplevel, q.queryid, q.datname, q.username, q.command,
       CASE WHEN $4 THEN q.query_text END, w.calls, w.ms, w.rows, w.rd, w.tw, coalesce(w.ms / NULLIF(total.ms, 0), 0)
FROM w JOIN query_identities q ON q.id = w.identity_id, total
ORDER BY w.ms DESC LIMIT $3`, targetID, since.UTC(), limit, includeText)
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
	err := s.pool.QueryRow(ctx, `
SELECT percentile_cont(0.5) WITHIN GROUP (ORDER BY total_exec_ms / calls), count(*)
FROM query_rollups WHERE target_id = $1 AND identity_id = $2 AND ts >= $3 AND ts < $4 AND calls >= $5`,
		targetID, identityID, from.UTC(), to.UTC(), minCalls).Scan(&median, &n)
	if err != nil {
		return 0, 0, false, s.red.Error(err)
	}
	if median == nil {
		return 0, 0, false, nil
	}
	return *median, n, true, nil
}

// LatestPlans returns the newest saved plan of each identity, with its body.
func (s *Store) LatestPlans(ctx context.Context, targetID int64, identityIDs []int64) (map[int64]PlanRow, error) {
	rows, err := s.pool.Query(ctx, `
SELECT DISTINCT ON (identity_id) id, identity_id, captured_at, captured_by, source, format, plan, fingerprint, total_cost, note
FROM query_plans WHERE target_id = $1 AND identity_id = ANY ($2::bigint[])
ORDER BY identity_id, captured_at DESC`, targetID, identityIDs)
	if err != nil {
		return nil, s.red.Error(err)
	}
	list, err := pgx.CollectRows(rows, scanPlan)
	if err != nil {
		return nil, s.red.Error(err)
	}
	out := make(map[int64]PlanRow, len(list))
	for _, p := range list {
		out[p.IdentityID] = p
	}
	return out, nil
}
