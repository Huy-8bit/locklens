package investigate

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/targetdb/queries"
)

func Execute(ctx context.Context, tx pgx.Tx, sql, schema string, explain bool) (result Result, err error) {
	started := time.Now()
	defer func() { result.DurationMS = time.Since(started).Milliseconds() }()
	if len(sql) == 0 || len(sql) > 32768 {
		return Result{}, fail("invalid_sql", "Statement must contain 1–32768 bytes.")
	}
	if len(schema) > 63 {
		return Result{}, fail("invalid_schema", "Schema name is too long.")
	}
	if schema == "" {
		schema = "public"
	}
	if _, err := tx.Exec(ctx, queries.SetSearchPath, "pg_catalog,"+pgx.Identifier{schema}.Sanitize()); err != nil {
		return Result{}, err
	}
	// Extended protocol accepts exactly one statement. DECLARE uses PostgreSQL's
	// query grammar, not a SELECT-prefix check, and cannot commit the transaction.
	sql = strings.TrimSpace(sql)
	if _, err := tx.Conn().PgConn().ExecParams(ctx, declaration+sql, nil, nil, nil, nil).Close(); err != nil {
		return Result{}, err
	}
	if explain {
		return readResult(ctx, tx, "EXPLAIN (FORMAT JSON) "+sql)
	}
	return readResult(ctx, tx, fmt.Sprintf("FETCH FORWARD %d FROM locklens_result", MaxRows+1))
}

// ExplainGeneric returns the estimated plan of a parameterised statement
// such as a pg_stat_statements text ($1, $2, ...) with EXPLAIN (FORMAT JSON,
// GENERIC_PLAN), PostgreSQL 16 or later. EXPLAIN without ANALYZE plans the
// statement without executing it, and the transaction is read-only. The
// simple query protocol is used because the placeholders have no values;
// a statement containing ';' is refused so only one statement is sent.
func ExplainGeneric(ctx context.Context, tx pgx.Tx, sql string) (string, error) {
	sql = strings.TrimSpace(strings.TrimRight(strings.TrimSpace(sql), ";"))
	if len(sql) == 0 || len(sql) > 32768 {
		return "", fail("invalid_sql", "Statement must contain 1–32768 bytes.")
	}
	if strings.Contains(sql, ";") {
		return "", fail("invalid_sql", "Only a single statement can be explained.")
	}
	results, err := tx.Conn().PgConn().Exec(ctx, "EXPLAIN (FORMAT JSON, GENERIC_PLAN) "+sql).ReadAll()
	if err != nil {
		return "", err
	}
	if len(results) != 1 || len(results[0].Rows) != 1 || len(results[0].Rows[0]) != 1 {
		return "", fail("invalid_result", "EXPLAIN returned an unexpected result.")
	}
	return string(results[0].Rows[0][0]), nil
}
