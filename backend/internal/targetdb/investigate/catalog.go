// Catalog and Data: browsing the structure and rows of a monitored database.
package investigate

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/targetdb/queries"
)

func Catalog(ctx context.Context, tx pgx.Tx, kind, schema, relation string) (out any, err error) {
	defer func() {
		if r, ok := out.(Result); ok && len(r.Rows) > 200 {
			r.Rows = r.Rows[:200]
			r.Truncated = true
			out = r
		}
	}()
	switch kind {
	case "databases":
		return readResult(ctx, tx, queries.CatalogDatabases)
	case "schemas":
		return readResult(ctx, tx, queries.CatalogSchemas)
	case "relations":
		return readResult(ctx, tx, queries.CatalogRelations, schema)
	case "functions":
		return readResult(ctx, tx, queries.CatalogFunctions, schema)
	case "types":
		return readResult(ctx, tx, queries.CatalogTypes, schema)
	case "detail":
		oid, err := relationOID(ctx, tx, schema, relation)
		if err != nil {
			return nil, err
		}
		sections := []struct{ name, sql string }{
			{"columns", queries.DetailColumns},
			{"constraints", queries.DetailConstraints},
			{"indexes", queries.DetailIndexes},
			{"triggers", queries.DetailTriggers},
			{"partitions", queries.DetailPartitions},
		}
		out := map[string]Result{}
		for _, q := range sections {
			r, err := readResult(ctx, tx, q.sql, oid)
			if err != nil {
				return nil, err
			}
			if len(r.Rows) > 200 {
				r.Rows = r.Rows[:200]
				r.Truncated = true
			}
			out[q.name] = r
		}
		return out, nil
	}
	return nil, fail("invalid_request", "Unknown catalog section.")
}
func relationOID(ctx context.Context, tx pgx.Tx, schema, name string) (uint32, error) {
	if len(schema) == 0 || len(schema) > 63 || len(name) == 0 || len(name) > 63 || strings.HasPrefix(schema, "pg_") || schema == "information_schema" {
		return 0, fail("invalid_relation", "Select an application schema and relation.")
	}
	var oid uint32
	err := tx.QueryRow(ctx, queries.RelationOID, schema, name).Scan(&oid)
	return oid, err
}

type DataRequest struct {
	Schema       string   `json:"schema"`
	Relation     string   `json:"relation"`
	Columns      []string `json:"columns"`
	Sort         string   `json:"sort"`
	Desc         bool     `json:"desc"`
	FilterColumn string   `json:"filter_column"`
	FilterOp     string   `json:"filter_op"`
	FilterValue  string   `json:"filter_value"`
	Limit        int      `json:"limit"`
	Offset       int      `json:"offset"`
	After        *string  `json:"after"`
}

func Data(ctx context.Context, tx pgx.Tx, d DataRequest) (Result, error) {
	if d.Limit == 0 {
		d.Limit = 25
	}
	if d.Limit < 1 || d.Limit > 100 || d.Offset < 0 || d.Offset > 10000 || len(d.FilterValue) > 1024 || len(d.Columns) > 100 {
		return Result{}, fail("invalid_request", "Use 1–100 rows, offset at most 10000, at most 100 columns and a filter up to 1024 bytes.")
	}
	oid, err := relationOID(ctx, tx, d.Schema, d.Relation)
	if err != nil {
		return Result{}, err
	}
	rows, err := tx.Query(ctx, queries.DataColumns, oid)
	if err != nil {
		return Result{}, err
	}
	types := map[string]uint32{}
	all := []string{}
	pk := ""
	for rows.Next() {
		var name string
		var typ uint32
		var primary bool
		if err = rows.Scan(&name, &typ, &primary); err != nil {
			rows.Close()
			return Result{}, err
		}
		types[name] = typ
		all = append(all, name)
		if primary {
			pk = name
		}
	}
	rows.Close()
	if rows.Err() != nil {
		return Result{}, rows.Err()
	}
	if len(all) > 100 {
		return Result{}, fail("too_many_columns", "Relations with more than 100 columns are not supported by the bounded browser.")
	}
	if len(d.Columns) == 0 {
		d.Columns = all
	}
	if d.Sort == "" {
		d.Sort = pk
	}
	if d.Sort == "" && len(all) > 0 {
		d.Sort = all[0]
	}
	quote := func(s string) string { return pgx.Identifier{s}.Sanitize() }
	selected := []string{}
	keySelected := false
	for _, c := range d.Columns {
		if _, ok := types[c]; !ok {
			return Result{}, fail("invalid_column", "Unknown selected column.")
		}
		selected = append(selected, quote(c))
		if c == pk {
			keySelected = true
		}
	}
	if _, ok := types[d.Sort]; !ok {
		return Result{}, fail("invalid_column", "Unknown sort column.")
	}
	casts := map[uint32]string{20: "bigint", 21: "smallint", 23: "integer", 25: "text", 1043: "text", 2950: "uuid"}
	cast := casts[types[pk]]
	keyset := pk != "" && d.Sort == pk && keySelected && cast != ""
	where := []string{}
	args := []any{}
	arg := func(v any) string { args = append(args, v); return fmt.Sprintf("$%d", len(args)) }
	if d.FilterColumn != "" {
		if _, ok := types[d.FilterColumn]; !ok {
			return Result{}, fail("invalid_column", "Unknown filter column.")
		}
		c := quote(d.FilterColumn)
		switch d.FilterOp {
		case "eq":
			where = append(where, c+"::text = "+arg(d.FilterValue))
		case "contains":
			where = append(where, "strpos("+c+"::text,"+arg(d.FilterValue)+") > 0")
		case "null":
			where = append(where, c+" IS NULL")
		case "not_null":
			where = append(where, c+" IS NOT NULL")
		default:
			return Result{}, fail("invalid_filter", "Unsupported filter operator.")
		}
	}
	if d.After != nil {
		if !keyset || len(*d.After) > 1024 {
			return Result{}, fail("invalid_cursor", "Cursor requires the selected single-column primary key sort.")
		}
		op := ">"
		if d.Desc {
			op = "<"
		}
		where = append(where, quote(pk)+op+arg(*d.After)+"::"+cast)
	}
	query := "SELECT " + strings.Join(selected, ",") + " FROM " + pgx.Identifier{d.Schema, d.Relation}.Sanitize()
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY " + quote(d.Sort)
	if d.Desc {
		query += " DESC"
	}
	if !keyset && pk != "" && pk != d.Sort {
		query += "," + quote(pk)
	}
	query += " LIMIT " + arg(d.Limit+1)
	if !keyset {
		query += " OFFSET " + arg(d.Offset)
	}
	r, err := readResult(ctx, tx, query, args...)
	r.Pagination = "bounded offset; concurrent writes may change page membership"
	if keyset {
		r.Pagination = "primary-key cursor"
	}
	if len(r.Rows) > d.Limit {
		r.Rows = r.Rows[:d.Limit]
		r.Truncated = true
		if keyset && !r.CellTruncated {
			for i, c := range d.Columns {
				if c == pk {
					r.Next = r.Rows[len(r.Rows)-1][i]
				}
			}
		}
	}
	return r, err
}
