package investigate

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/locklens/locklens/internal/targetdb/queries"
)

// WhatIfStatement is the estimated cost of one statement without and with a
// hypothetical index.
type WhatIfStatement struct {
	SQL        string  `json:"-"`
	CostBefore float64 `json:"cost_before"`
	CostAfter  float64 `json:"cost_after"`
	UsesIndex  bool    `json:"uses_index"`
	Error      string  `json:"error,omitempty"`
}

// WhatIfIndex checks an index with HypoPG: it plans each statement, creates
// the index hypothetically (in this session's memory only, nothing is
// written to the catalog), plans again, and resets HypoPG. Statements with
// $n placeholders are planned as generic plans (PostgreSQL 16+). Nothing is
// executed. createIndex is a CREATE INDEX statement built by LockLens.
func WhatIfIndex(ctx context.Context, tx pgx.Tx, createIndex string, statements []string) ([]WhatIfStatement, error) {
	var version string
	if err := tx.QueryRow(ctx, queries.HypoPGVersion).Scan(&version); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fail("hypopg_missing", "The hypopg extension is not installed in this database. Its owner can install it (CREATE EXTENSION hypopg); LockLens never installs extensions.")
		}
		return nil, err
	}
	out := make([]WhatIfStatement, len(statements))
	for i, sql := range statements {
		out[i].SQL = sql
		before, err := explainSafely(ctx, tx, sql)
		if err != nil {
			out[i].Error = SafeError(err).Message
			continue
		}
		out[i].CostBefore = before.cost
	}
	var indexName string
	if err := tx.QueryRow(ctx, queries.HypoPGCreateIndex, createIndex).Scan(&indexName); err != nil {
		return nil, err
	}
	defer func() { _, _ = tx.Exec(context.WithoutCancel(ctx), queries.HypoPGReset) }()
	for i := range out {
		if out[i].Error != "" {
			continue
		}
		after, err := explainSafely(ctx, tx, out[i].SQL)
		if err != nil {
			out[i].Error = SafeError(err).Message
			continue
		}
		out[i].CostAfter = after.cost
		out[i].UsesIndex = strings.Contains(after.plan, indexName)
	}
	return out, nil
}

// explainSafely plans one statement under a savepoint, so a statement that
// fails (a dropped column, a missing privilege) does not abort the others.
func explainSafely(ctx context.Context, tx pgx.Tx, sql string) (explained, error) {
	if _, err := tx.Exec(ctx, queries.SavepointWhatIf); err != nil {
		return explained{}, err
	}
	e, err := explainCost(ctx, tx, sql)
	if err != nil {
		if _, rbErr := tx.Exec(ctx, queries.RollbackToSavepointWhatIf); rbErr != nil {
			return explained{}, rbErr
		}
		return explained{}, err
	}
	_, err = tx.Exec(ctx, queries.ReleaseSavepointWhatIf)
	return e, err
}

type explained struct {
	cost float64
	plan string
}

// explainCost plans a statement (a generic plan when it has placeholders)
// and returns its estimated total cost.
func explainCost(ctx context.Context, tx pgx.Tx, sql string) (explained, error) {
	var plan string
	var err error
	if strings.Contains(sql, "$1") {
		plan, err = ExplainGeneric(ctx, tx, sql)
	} else {
		sql = strings.TrimSpace(strings.TrimRight(strings.TrimSpace(sql), ";"))
		if strings.Contains(sql, ";") {
			return explained{}, fail("invalid_sql", "Only a single statement can be explained.")
		}
		var results []*pgconn.Result
		results, err = tx.Conn().PgConn().Exec(ctx, "EXPLAIN (FORMAT JSON) "+sql).ReadAll()
		if err == nil {
			if len(results) != 1 || len(results[0].Rows) != 1 {
				return explained{}, fail("invalid_result", "EXPLAIN returned an unexpected result.")
			}
			plan = string(results[0].Rows[0][0])
		}
	}
	if err != nil {
		return explained{}, err
	}
	var doc []struct {
		Plan struct {
			TotalCost float64 `json:"Total Cost"`
		} `json:"Plan"`
	}
	if err := json.Unmarshal([]byte(plan), &doc); err != nil || len(doc) == 0 {
		return explained{}, fail("invalid_result", "EXPLAIN returned an unexpected result.")
	}
	return explained{cost: doc[0].Plan.TotalCost, plan: plan}, nil
}
