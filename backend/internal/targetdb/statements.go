package targetdb

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/targetdb/queries"
)

// StatementRow is one pg_stat_statements entry read without its text.
type StatementRow struct {
	Key      observe.QueryKey
	DatName  string
	UserName string
	Counters observe.StatementCounters
}

// ReadStatementCounters reads the counters of every visible
// pg_stat_statements entry (schema: where the extension lives; major: the
// server major version, part of the identity). hidden counts the rows of
// other roles whose queryid is not visible.
func ReadStatementCounters(ctx context.Context, tx pgx.Tx, schema string, major int) (rows []StatementRow, hidden int, err error) {
	fn := pgx.Identifier{schema, "pg_stat_statements"}.Sanitize()
	res, err := tx.Query(ctx, queries.StatementCounters(fn))
	if err != nil {
		return nil, 0, err
	}
	var (
		userid, dbid uint32
		toplevel     bool
		queryid      *int64
		r            StatementRow
	)
	c := &r.Counters
	_, err = pgx.ForEachRow(res, []any{&userid, &dbid, &toplevel, &queryid, &r.DatName, &r.UserName,
		&c.Calls, &c.TotalMS, &c.Rows, &c.BlkHit, &c.BlkRead, &c.TempW, &c.WAL}, func() error {
		if queryid == nil {
			hidden++
			return nil
		}
		r.Key = observe.QueryKey{ServerMajor: major, DBID: dbid, UserID: userid, TopLevel: toplevel, QueryID: *queryid}
		rows = append(rows, r)
		return nil
	})
	return rows, hidden, err
}

// ReadStatementTexts reads the normalised texts of some queryids.
func ReadStatementTexts(ctx context.Context, tx pgx.Tx, schema string, major int, queryids []int64) (map[observe.QueryKey]string, error) {
	fn := pgx.Identifier{schema, "pg_stat_statements"}.Sanitize()
	rows, err := tx.Query(ctx, queries.StatementTexts(fn), queryids)
	if err != nil {
		return nil, err
	}
	out := map[observe.QueryKey]string{}
	var (
		userid, dbid uint32
		toplevel     bool
		queryid      int64
		text         *string
	)
	_, err = pgx.ForEachRow(rows, []any{&userid, &dbid, &toplevel, &queryid, &text}, func() error {
		if text != nil {
			out[observe.QueryKey{ServerMajor: major, DBID: dbid, UserID: userid, TopLevel: toplevel, QueryID: queryid}] = *text
		}
		return nil
	})
	return out, err
}

// ReadStatementsInfo reads the evictions (dealloc) and the last reset of
// pg_stat_statements.
func ReadStatementsInfo(ctx context.Context, tx pgx.Tx, schema string) (*int64, *time.Time, error) {
	view := pgx.Identifier{schema, "pg_stat_statements_info"}.Sanitize()
	var dealloc int64
	var reset time.Time
	if err := tx.QueryRow(ctx, queries.StatementsInfo(view)).Scan(&dealloc, &reset); err != nil {
		return nil, nil, err
	}
	return &dealloc, &reset, nil
}
