package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	rules "github.com/locklens/locklens/internal/domain/finding"
	"github.com/locklens/locklens/internal/repository/queries"
)

// FindingRow is a stored finding.
type FindingRow struct {
	ID         int64         `json:"id"`
	TargetID   int64         `json:"target_id"`
	IncidentID *int64        `json:"incident_id"`
	FirstSeen  time.Time     `json:"first_seen"`
	LastSeen   time.Time     `json:"last_seen"`
	ResolvedAt *time.Time    `json:"resolved_at"`
	Finding    rules.Finding `json:"finding"`
}

// SyncFindings upserts the current findings of a target and resolves stored
// findings that are no longer reported. Returns the IDs of current findings
// keyed by rule and key.
func (s *Store) SyncFindings(ctx context.Context, targetID int64, now time.Time, fs []rules.Finding) (map[[2]string]int64, error) {
	ids := map[[2]string]int64{}
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var keepRules, keepKeys []string
		for _, f := range fs {
			observed, _ := json.Marshal(f.Observed)
			evidence, _ := json.Marshal(f.Evidence)
			poss, _ := json.Marshal(f.PossibleExplanations)
			next, _ := json.Marshal(f.NextChecks)
			var conf, metrics []byte
			if f.Confidence != nil {
				conf, _ = json.Marshal(f.Confidence)
			}
			if len(f.Metrics) > 0 {
				metrics, _ = json.Marshal(f.Metrics)
			}
			var id int64
			err := tx.QueryRow(ctx, queries.UpsertFinding, targetID, f.RuleID, f.RuleVersion, f.Key, string(f.Severity), f.Title, string(f.Status),
				f.ObservedFrom, now.UTC(), observed, evidence, f.Explanation, poss, next, f.Limitations, conf, metrics).Scan(&id)
			if err != nil {
				return err
			}
			ids[[2]string{f.RuleID, f.Key}] = id
			keepRules = append(keepRules, f.RuleID)
			keepKeys = append(keepKeys, f.Key)
		}
		_, err := tx.Exec(ctx, queries.ResolveMissingFindings,
			targetID, now.UTC(), keepRules, keepKeys)
		return err
	})
	return ids, s.red.Error(err)
}

// LinkFindingsToIncident records which incident a finding belongs to.
func (s *Store) LinkFindingsToIncident(ctx context.Context, incidentID int64, findingIDs []int64) error {
	if len(findingIDs) == 0 {
		return nil
	}
	_, err := s.pool.Exec(ctx, queries.LinkFindingsToIncident, incidentID, findingIDs)
	return s.red.Error(err)
}

// FindingFilter selects findings.
type FindingFilter struct {
	TargetID   int64
	IncidentID int64
	// Status: "current" (active and insufficient_data), "resolved", or "" for all.
	Status string
	Limit  int
}

// ListFindings returns findings, newest first.
func (s *Store) ListFindings(ctx context.Context, f FindingFilter) ([]FindingRow, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 200
	}
	rows, err := s.pool.Query(ctx, queries.ListFindings, f.TargetID, f.IncidentID, f.Status, f.Limit)
	if err != nil {
		return nil, s.red.Error(err)
	}
	out, err := pgx.CollectRows(rows, scanFinding)
	if out == nil {
		out = []FindingRow{}
	}
	return out, s.red.Error(err)
}

func scanFinding(r pgx.CollectableRow) (FindingRow, error) {
	var fr FindingRow
	var sev, status string
	var observed, evidence, poss, next, conf, metrics []byte
	err := r.Scan(&fr.ID, &fr.TargetID, &fr.IncidentID, &fr.Finding.RuleID, &fr.Finding.RuleVersion, &fr.Finding.Key, &sev,
		&fr.Finding.Title, &status, &fr.FirstSeen, &fr.LastSeen, &fr.ResolvedAt, &observed, &evidence,
		&fr.Finding.Explanation, &poss, &next, &fr.Finding.Limitations, &conf, &metrics)
	if err != nil {
		return fr, err
	}
	fr.Finding.Severity, fr.Finding.Status = rules.Severity(sev), rules.Status(status)
	fr.Finding.ObservedFrom, fr.Finding.ObservedTo = fr.FirstSeen, fr.LastSeen
	if err := errors.Join(json.Unmarshal(observed, &fr.Finding.Observed), json.Unmarshal(evidence, &fr.Finding.Evidence),
		json.Unmarshal(poss, &fr.Finding.PossibleExplanations), json.Unmarshal(next, &fr.Finding.NextChecks)); err != nil {
		return fr, err
	}
	if conf != nil {
		if err := json.Unmarshal(conf, &fr.Finding.Confidence); err != nil {
			return fr, err
		}
	}
	if metrics != nil {
		if err := json.Unmarshal(metrics, &fr.Finding.Metrics); err != nil {
			return fr, err
		}
	}
	return fr, nil
}
